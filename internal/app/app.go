// Package app holds the brainstorm CLI's core logic: locating the home dir,
// scaffolding embedded assets, generating/prompting for configuration values,
// and shelling out to `docker compose`.
package app

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	assets "github.com/NosFabrica/brainstorm_one_click_deployment"
)

const (
	composeFile = "docker-compose.yml"
	envFile     = ".env"
)

// Home returns the brainstorm home directory, honoring the BRAINSTORM_HOME
// override. The default is ~/.brainstorm, except when Docker is the confined
// snap package — which cannot read hidden dot-directories — where it falls back
// to the non-hidden ~/brainstorm so the stack actually works.
func Home() (string, error) {
	if h := os.Getenv("BRAINSTORM_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if DockerIsSnap() {
		return filepath.Join(u, "brainstorm"), nil
	}
	return filepath.Join(u, ".brainstorm"), nil
}

// DockerIsSnap reports whether the docker CLI is the AppArmor-confined snap
// build. Snap Docker cannot access hidden dot-directories or arbitrary host
// paths, which breaks a hidden home dir and its bind mounts.
func DockerIsSnap() bool {
	p, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	candidates := []string{p}
	if target, err := os.Readlink(p); err == nil {
		candidates = append(candidates, target)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		candidates = append(candidates, resolved)
	}
	for _, c := range candidates {
		if strings.Contains(c, "snap") {
			return true
		}
	}
	return false
}

// Kind classifies how a configuration value is obtained.
type Kind int

const (
	// Generated values are random secrets, created once and never re-asked.
	Generated Kind = iota
	// Prompted values are asked of the user on first run (or with --reconfigure).
	Prompted
	// Fixed values are written verbatim so the running config is self-documenting.
	Fixed
)

// Var describes one entry in the generated .env file.
type Var struct {
	Key      string
	Kind     Kind
	Prompt   string
	Default  string // value offered when prompting (internet mode)
	Local    string // value used automatically in local mode (no prompt)
	GenBytes int    // for Generated: random bytes, hex-encoded (so 32 -> 64 chars)
}

// Vars is the full configuration surface. Anything not listed here stays
// hardcoded in docker-compose.yml (internal hostnames, ports, etc.).
var Vars = []Var{
	// DEPLOY_ENVIRONMENT is set from the local/internet choice in Configure;
	// this entry is just a fallback so the key is always present.
	{Key: "DEPLOY_ENVIRONMENT", Kind: Fixed, Default: "PROD"},

	{Key: "AUTH_SECRET_KEY", Kind: Generated, GenBytes: 32},
	{Key: "SQL_ADMIN_PASSWORD", Kind: Generated, GenBytes: 24},
	{Key: "SQL_ADMIN_SECRET_KEY", Kind: Generated, GenBytes: 32},
	{Key: "NEO4J_PASSWORD", Kind: Generated, GenBytes: 24},

	{Key: "FRONTEND_URL", Kind: Prompted, Prompt: "Public URL of the Brainstorm UI", Default: "https://brainstorm.nosfabrica.com", Local: "http://localhost:3000"},
	{Key: "PUBLIC_BASE_URL", Kind: Prompted, Prompt: "Public URL of the Brainstorm API server", Default: "https://brainstormserver.nosfabrica.com", Local: "http://localhost:8000"},
	{Key: "VITE_API_URL", Kind: Prompted, Prompt: "UI: API URL the browser calls", Default: "https://brainstormserver.nosfabrica.com", Local: "http://localhost:8000"},
	{Key: "VITE_NIP85_RELAY_URL", Kind: Prompted, Prompt: "UI: NIP-85 relay websocket URL", Default: "wss://nip85.nosfabrica.com", Local: "ws://localhost:7778"},
	{Key: "NIP85_RELAY_PUBLIC_URL", Kind: Prompted, Prompt: "Public NIP-85 relay URL (published in events)", Default: "wss://nip85.nosfabrica.com", Local: "ws://localhost:7778"},
	{Key: "NOSTR_TRANSFER_FROM_RELAY", Kind: Prompted, Prompt: "Relay to sync the web-of-trust from", Default: "wss://wot.grapevine.network", Local: "wss://wot.grapevine.network"},
	{Key: "ADMIN_ENABLED", Kind: Prompted, Prompt: "Enable admin endpoints? (true/false)", Default: "false", Local: "false"},
	{Key: "ADMIN_WHITELISTED_PUBKEYS", Kind: Prompted, Prompt: "Admin whitelisted pubkeys (comma-separated, blank for none)", Default: "", Local: ""},
}

func genSecret(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LoadEnv parses a KEY=VALUE .env file. A missing file yields an empty map.
func LoadEnv(path string) (map[string]string, error) {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = v
	}
	return m, sc.Err()
}

// SaveEnv writes the map back as a sorted .env file with 0600 perms (it holds
// secrets).
func SaveEnv(path string, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Generated by `brainstorm`. Holds secrets — keep this file safe.\n")
	b.WriteString("# Re-run `brainstorm config` to change prompted values, then `brainstorm restart`.\n\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Configure ensures every Var has a value in <home>/.env. Generated secrets are
// created exactly once; prompted values are asked for when missing, or always
// when reconfigure is true. Returns whether this was a first-time setup.
func Configure(home string, reconfigure bool) (firstRun bool, err error) {
	envPath := filepath.Join(home, envFile)
	m, err := LoadEnv(envPath)
	if err != nil {
		return false, err
	}
	firstRun = len(m) == 0
	in := bufio.NewReader(os.Stdin)

	if firstRun {
		fmt.Println("First run — generating secrets and collecting settings.")
		fmt.Println()
	}

	// Fixed defaults and one-time secret generation.
	for _, v := range Vars {
		switch v.Kind {
		case Fixed:
			if _, ok := m[v.Key]; !ok {
				m[v.Key] = v.Default
			}
		case Generated:
			if cur, ok := m[v.Key]; !ok || cur == "" {
				s, gerr := genSecret(v.GenBytes)
				if gerr != nil {
					return firstRun, gerr
				}
				m[v.Key] = s
				fmt.Printf("  generated %s\n", v.Key)
			}
		}
	}

	// Collect the manual values when any are missing, or when reconfiguring.
	collect := reconfigure
	for _, v := range Vars {
		if v.Kind == Prompted {
			if _, ok := m[v.Key]; !ok {
				collect = true
				break
			}
		}
	}

	if collect {
		local := promptMode(in)
		if local {
			m["DEPLOY_ENVIRONMENT"] = "DEV"
			fmt.Println("\nLocal mode — using localhost values, no further questions.")
		} else {
			m["DEPLOY_ENVIRONMENT"] = "PROD"
			fmt.Println("\nInternet mode — enter your public values (blank keeps the default):")
		}
		for _, v := range Vars {
			if v.Kind != Prompted {
				continue
			}
			cur, exists := m[v.Key]
			if exists && !reconfigure {
				continue
			}
			if local {
				m[v.Key] = v.Local
				continue
			}
			def := v.Default
			if exists {
				def = cur
			}
			m[v.Key] = prompt(in, v.Prompt, def)
		}
	}

	fmt.Println()
	return firstRun, SaveEnv(envPath, m)
}

// promptMode asks whether this is a local experiment or an internet-facing
// deployment. Returns true for local.
func promptMode(in *bufio.Reader) bool {
	for {
		fmt.Print("Are you running Brainstorm locally to experiment, or exposing it to the internet? (local/internet) [local]: ")
		line, _ := in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "local", "l":
			return true
		case "internet", "i", "prod", "production", "public":
			return false
		default:
			fmt.Println("  please answer 'local' or 'internet'.")
		}
	}
}

func prompt(in *bufio.Reader, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// Scaffold writes the embedded assets into home. The compose file is owned by
// the CLI and always refreshed so renames/updates in a new binary take effect;
// the user-editable configs (relay confs, vespa-app) are written only when
// missing, unless force is true. Stateful paths (.env, secrets/, named volumes)
// are never embedded, so they're never touched here.
func Scaffold(home string, force bool) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	// brainstorm-server bind-mounts ./secrets read-write for key rotation.
	if err := os.MkdirAll(filepath.Join(home, "secrets"), 0o755); err != nil {
		return err
	}

	return fs.WalkDir(assets.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		dst := filepath.Join(home, p)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		// The compose file is CLI-managed (all user config lives in .env), so
		// always keep it in sync with the binary. Everything else is preserved
		// once present so host edits survive.
		cliOwned := p == composeFile
		if !force && !cliOwned {
			if _, statErr := os.Stat(dst); statErr == nil {
				return nil // keep what the user has
			}
		}
		data, err := assets.Files.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

// CheckDocker verifies that the docker CLI is installed and its daemon is
// reachable, returning an actionable error otherwise.
func CheckDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("Docker is not installed (or not on your PATH).\n" +
			"Install Docker Engine, then run brainstorm again:\n" +
			"  https://docs.docker.com/engine/install/\n" +
			"  Ubuntu quick install: curl -fsSL https://get.docker.com | sudo sh")
	}
	// `docker compose version` exercises both the CLI and the compose plugin
	// without needing the daemon, then `docker info` confirms the daemon is up.
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		return fmt.Errorf("the Docker Compose plugin is missing.\n" +
			"Install it (it ships with Docker Engine): https://docs.docker.com/compose/install/")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return fmt.Errorf("Docker is installed but its daemon isn't reachable.\n" +
			"Start it and make sure you can run docker without sudo:\n" +
			"  sudo systemctl start docker\n" +
			"  sudo usermod -aG docker $USER   # then log out/in (or run: newgrp docker)")
	}
	return nil
}

// hiddenServices are one-shot/sidecar services excluded from `status`, keyed by
// base name (the "brainstorm-" prefix is stripped before lookup).
var hiddenServices = map[string]bool{
	"autoheal":     true,
	"vespa-deploy": true,
}

func isHidden(service string) bool {
	return hiddenServices[strings.TrimPrefix(service, "brainstorm-")]
}

// ServiceStatus is one row of `brainstorm status`.
type ServiceStatus struct {
	Name    string
	Running bool
}

// Status returns the up/down state of each user-facing service (sidecars
// excluded), regardless of whether its container exists yet.
func Status(home string) ([]ServiceStatus, error) {
	if err := CheckDocker(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(home, composeFile)); err != nil {
		return nil, fmt.Errorf("brainstorm is not set up yet — run `brainstorm start` first (home: %s)", home)
	}

	services, err := composeServices(home)
	if err != nil {
		return nil, err
	}
	running, err := composeRunning(home)
	if err != nil {
		return nil, err
	}

	out := make([]ServiceStatus, 0, len(services))
	for _, s := range services {
		if isHidden(s) {
			continue
		}
		out = append(out, ServiceStatus{Name: s, Running: running[s]})
	}
	return out, nil
}

// composeServices lists every service defined in the compose file.
func composeServices(home string) ([]string, error) {
	data, err := composeOutput(home, "config", "--services")
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			services = append(services, line)
		}
	}
	sort.Strings(services)
	return services, nil
}

// composeRunning maps service name -> whether its container is running.
func composeRunning(home string) (map[string]bool, error) {
	data, err := composeOutput(home, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	type psLine struct {
		Service string `json:"Service"`
		State   string `json:"State"`
	}

	m := map[string]bool{}
	record := func(p psLine) {
		if p.Service != "" {
			m[p.Service] = p.State == "running"
		}
	}

	// Newer compose emits one JSON object per line; older emits a JSON array.
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var arr []psLine
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, err
		}
		for _, p := range arr {
			record(p)
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var p psLine
			if err := json.Unmarshal([]byte(line), &p); err != nil {
				return nil, err
			}
			record(p)
		}
	}
	return m, nil
}

// composeOutput runs a docker compose subcommand and returns its stdout.
func composeOutput(home string, args ...string) ([]byte, error) {
	full := append([]string{
		"compose",
		"-f", filepath.Join(home, composeFile),
		"--env-file", filepath.Join(home, envFile),
	}, args...)

	cmd := exec.Command("docker", full...)
	cmd.Dir = home
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s", msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// serverHealthURL is brainstorm-server's health endpoint as published on the
// host (compose maps container :8000 to host :8000).
const serverHealthURL = "http://localhost:8000/health"

// ServerReady reports whether brainstorm-server's /health endpoint returns 200.
func ServerReady() bool {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(serverHealthURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// UIURL returns the configured public URL of the Brainstorm UI (FRONTEND_URL).
func UIURL(home string) string {
	m, _ := LoadEnv(filepath.Join(home, envFile))
	if u := m["FRONTEND_URL"]; u != "" {
		return u
	}
	return "http://localhost:3000"
}

// ReferenceRelay returns the relay to sync events from (NOSTR_TRANSFER_FROM_RELAY).
func ReferenceRelay(home string) string {
	m, _ := LoadEnv(filepath.Join(home, envFile))
	if r := m["NOSTR_TRANSFER_FROM_RELAY"]; r != "" {
		return r
	}
	return "wss://wot.grapevine.network"
}

// syncKinds are the event kinds pulled by `brainstorm sync`.
var syncKinds = []int{0, 3, 10000, 1984}

// Sync runs a strfry negentropy sync inside neofry, pulling syncKinds down from
// the reference relay.
func Sync(home string) error {
	relay := ReferenceRelay(home)

	nums := make([]string, len(syncKinds))
	for i, k := range syncKinds {
		nums[i] = strconv.Itoa(k)
	}
	filter := fmt.Sprintf(`{"kinds":[%s]}`, strings.Join(nums, ","))

	fmt.Printf("syncing kinds %s down from %s into neofry...\n", strings.Join(nums, ", "), relay)
	return Compose(home, "exec", "-T", "brainstorm-neofry",
		"strfry", "--config=/etc/strfry.conf", "sync", relay,
		"--filter", filter, "--dir", "down")
}

// relayServices maps the user-facing relay name to its compose service.
var relayServices = map[string]string{
	"neofry": "brainstorm-neofry",
	"nip85":  "brainstorm-strfry-nip85",
}

// RelayTargets lists the valid relay names for `nostr-query`.
func RelayTargets() []string { return []string{"neofry", "nip85"} }

// QueueDepths reports how much work is waiting in Redis.
type QueueDepths struct {
	Events    int // strfry:event list — nostr events pending processing
	Graperank int // message_queue list — graperank requests pending processing
}

// QueueStatus reads the pending-work list lengths from Redis.
func QueueStatus(home string) (QueueDepths, error) {
	if err := CheckDocker(); err != nil {
		return QueueDepths{}, err
	}
	events, err := redisLLen(home, "strfry:events")
	if err != nil {
		return QueueDepths{}, err
	}
	graperank, err := redisLLen(home, "message_queue")
	if err != nil {
		return QueueDepths{}, err
	}
	return QueueDepths{Events: events, Graperank: graperank}, nil
}

// redisLLen returns the length of a Redis list (0 if the key doesn't exist).
func redisLLen(home, key string) (int, error) {
	data, err := composeOutput(home, "exec", "-T", "brainstorm-redis", "redis-cli", "LLEN", key)
	if err != nil {
		return 0, fmt.Errorf("can't query redis (is brainstorm-redis up?)")
	}
	s := strings.TrimSpace(string(data))
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("unexpected redis reply for %s: %q", key, s)
	}
	return n, nil
}

// NostrQuery runs a strfry filter scan against the chosen relay's stored events
// and streams the matching events (JSON, one per line) to stdout.
func NostrQuery(home, target, query string) error {
	service, ok := relayServices[strings.ToLower(strings.TrimSpace(target))]
	if !ok {
		return fmt.Errorf("unknown relay %q — use \"neofry\" or \"nip85\"", target)
	}
	return Compose(home, "exec", "-T", service,
		"strfry", "--config=/etc/strfry.conf", "scan", query)
}

// RedisKeyCount returns the total number of keys in Redis (DBSIZE).
func RedisKeyCount(home string) (int64, error) {
	data, err := composeOutput(home, "exec", "-T", "brainstorm-redis", "redis-cli", "DBSIZE")
	if err != nil {
		return 0, fmt.Errorf("redis unavailable")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected redis reply: %q", strings.TrimSpace(string(data)))
	}
	return n, nil
}

// Neo4jStats holds the node/relationship counts shown in status.
type Neo4jStats struct {
	Users   int64
	Follows int64
	Mutes   int64
	Reports int64
}

func neo4jPassword(home string) string {
	m, _ := LoadEnv(filepath.Join(home, envFile))
	if p := m["NEO4J_PASSWORD"]; p != "" {
		return p
	}
	return "password"
}

// Neo4jCounts returns the NostrUser node count and the FOLLOWS/MUTES/REPORTS
// relationship counts.
func Neo4jCounts(home string) (Neo4jStats, error) {
	const query = "MATCH (u:NostrUser) WITH count(u) AS users " +
		"CALL {MATCH ()-[r:FOLLOWS]->() RETURN count(r) AS follows} " +
		"CALL {MATCH ()-[r:MUTES]->() RETURN count(r) AS mutes} " +
		"CALL {MATCH ()-[r:REPORTS]->() RETURN count(r) AS reports} " +
		"RETURN users, follows, mutes, reports;"

	data, err := composeOutput(home, "exec", "-T", "brainstorm-neo4j",
		"cypher-shell", "-u", "neo4j", "-p", neo4jPassword(home), "--format", "plain", query)
	if err != nil {
		return Neo4jStats{}, fmt.Errorf("neo4j unavailable")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	parts := strings.Split(lines[len(lines)-1], ",") // last line: "users, follows, mutes, reports"
	if len(parts) != 4 {
		return Neo4jStats{}, fmt.Errorf("unexpected neo4j reply")
	}
	atoi := func(s string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return n }
	return Neo4jStats{atoi(parts[0]), atoi(parts[1]), atoi(parts[2]), atoi(parts[3])}, nil
}

// VespaDocCount returns the number of documents Vespa is serving.
func VespaDocCount() (int64, error) {
	q := url.Values{"yql": {"select * from doc where true"}, "hits": {"0"}}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:8080/search/?" + q.Encode())
	if err != nil {
		return 0, fmt.Errorf("vespa unavailable")
	}
	defer resp.Body.Close()
	var r struct {
		Root struct {
			Fields struct {
				TotalCount int64 `json:"totalCount"`
			} `json:"fields"`
		} `json:"root"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, fmt.Errorf("unexpected vespa reply")
	}
	return r.Root.Fields.TotalCount, nil
}

// ResourceUsage is the RAM and disk footprint of one service (-1 = unknown).
type ResourceUsage struct {
	Name      string
	RAMBytes  int64
	DiskBytes int64
}

// resourceServices is the set of data services whose footprint status reports.
var resourceServices = []struct{ name, container, dataPath string }{
	{"redis", "brainstorm-redis", "/data"},
	{"postgres", "brainstorm-postgres", "/var/lib/postgresql/data"},
	{"neo4j", "brainstorm-neo4j", "/data"},
	{"vespa", "brainstorm-vespa", "/opt/vespa/var"},
}

// ResourceUsages reports each data service's RAM (from docker stats) and disk
// (du of its data dir).
func ResourceUsages(home string) ([]ResourceUsage, error) {
	if err := CheckDocker(); err != nil {
		return nil, err
	}

	// One docker stats call covers RAM for every container.
	ram := map[string]int64{}
	args := []string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}"}
	for _, s := range resourceServices {
		args = append(args, s.container)
	}
	if out, err := dockerOutput(args...); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.SplitN(line, "\t", 2)
			if len(f) != 2 {
				continue
			}
			used := strings.SplitN(f[1], "/", 2)[0] // "399.1MiB / 62.65GiB" -> "399.1MiB"
			ram[strings.TrimSpace(f[0])] = parseHumanBytes(strings.TrimSpace(used))
		}
	}

	out := make([]ResourceUsage, 0, len(resourceServices))
	for _, s := range resourceServices {
		u := ResourceUsage{Name: s.name, RAMBytes: -1, DiskBytes: -1}
		if b, ok := ram[s.container]; ok {
			u.RAMBytes = b
		}
		// du -sk is portable (KB); multiply to bytes.
		if data, err := composeOutput(home, "exec", "-T", s.container, "du", "-sk", s.dataPath); err == nil {
			if f := strings.Fields(strings.TrimSpace(string(data))); len(f) >= 1 {
				if kb, perr := strconv.ParseInt(f[0], 10, 64); perr == nil {
					u.DiskBytes = kb * 1024
				}
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// dockerOutput runs a raw `docker` command (not compose) and returns stdout.
func dockerOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s", msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// parseHumanBytes converts docker's "399.1MiB"/"4.997GiB" style values to bytes
// (-1 if unparseable).
func parseHumanBytes(s string) int64 {
	s = strings.TrimSpace(s)
	for _, u := range []struct {
		suffix string
		mul    float64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	} {
		if strings.HasSuffix(s, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			if err != nil {
				return -1
			}
			return int64(v * u.mul)
		}
	}
	return -1
}

// expectedTransferKinds are the nostr event kinds whose relay transfer must be
// completed for the deployment to be considered healthy.
var expectedTransferKinds = []int{0, 3, 1984, 10000}

// KindStatus is the relay-transfer state of one event kind.
type KindStatus struct {
	Kind      int
	Exists    bool
	Completed bool
}

// TransferStatus queries the brainstorm_nostr_relay_transfer table in Postgres
// for each expected kind, reporting whether its row exists and is completed.
// Returns an error if Postgres can't be queried (not running, not migrated).
func TransferStatus(home string) ([]KindStatus, error) {
	if err := CheckDocker(); err != nil {
		return nil, err
	}
	const query = "SELECT kind, completed FROM brainstorm_nostr_relay_transfer " +
		"WHERE kind IN (0, 3, 1984, 10000)"
	data, err := composeOutput(home, "exec", "-T", "brainstorm-postgres",
		"psql", "-U", "postgres", "-d", "brainstorm-database", "-tAc", query)
	if err != nil {
		return nil, fmt.Errorf("can't query postgres (is brainstorm-postgres up and migrated?)")
	}

	completed := map[int]bool{}
	seen := map[int]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		k, convErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		if convErr != nil {
			continue
		}
		seen[k] = true
		completed[k] = strings.TrimSpace(parts[1]) == "t"
	}

	out := make([]KindStatus, 0, len(expectedTransferKinds))
	for _, k := range expectedTransferKinds {
		out = append(out, KindStatus{Kind: k, Exists: seen[k], Completed: completed[k]})
	}
	return out, nil
}

// Recommended/minimum host resources for the full stack. Neo4j alone is
// configured for ~12 GB (4 GB heap + 8 GB page cache), plus Vespa/Postgres/etc.
const (
	recommendedRAMGB  = 16
	minimumRAMGB      = 12
	recommendedCPUs   = 4
	recommendedDiskGB = 20
)

// SpecReport summarizes the host's resources against what the stack needs.
type SpecReport struct {
	RAMGB    int // 0 if undetectable
	CPUs     int
	DiskGB   int // free disk on /, -1 if undetectable
	Warnings []string
	OK       bool
}

// CheckSpecs inspects host RAM, CPUs, and free disk and flags shortfalls.
func CheckSpecs() SpecReport {
	r := SpecReport{RAMGB: totalRAMGB(), CPUs: runtime.NumCPU(), DiskGB: freeDiskGB("/")}

	switch {
	case r.RAMGB == 0:
		r.Warnings = append(r.Warnings, "couldn't detect RAM; the stack needs ~16 GB (Neo4j alone uses 12 GB).")
	case r.RAMGB < minimumRAMGB:
		r.Warnings = append(r.Warnings, fmt.Sprintf("RAM is %d GB — Neo4j alone is set to ~12 GB (4 GB heap + 8 GB page cache); the stack will likely OOM. Lower the NEO4J_server_memory_* values in docker-compose.yml or add RAM.", r.RAMGB))
	case r.RAMGB < recommendedRAMGB:
		r.Warnings = append(r.Warnings, fmt.Sprintf("RAM is %d GB — below the recommended %d GB; it may run but stay tight.", r.RAMGB, recommendedRAMGB))
	}
	if r.CPUs < recommendedCPUs {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d CPU cores — %d+ recommended.", r.CPUs, recommendedCPUs))
	}
	if r.DiskGB >= 0 && r.DiskGB < recommendedDiskGB {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d GB free disk on / — images + data want ~%d GB.", r.DiskGB, recommendedDiskGB))
	}
	r.OK = len(r.Warnings) == 0
	return r
}

func totalRAMGB() int {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				if f := strings.Fields(line); len(f) >= 2 {
					kb, _ := strconv.ParseInt(f[1], 10, 64)
					return int(kb / 1024 / 1024)
				}
			}
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			b, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
			return int(b / (1024 * 1024 * 1024))
		}
	}
	return 0
}

func freeDiskGB(path string) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int(uint64(st.Bavail) * uint64(st.Bsize) / (1024 * 1024 * 1024))
}

// Compose runs `docker compose` against the brainstorm home, wiring in the
// compose file and the generated env file. Output is streamed to the terminal.
func Compose(home string, args ...string) error {
	if err := CheckDocker(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(home, composeFile)); err != nil {
		return fmt.Errorf("brainstorm is not set up yet — run `brainstorm start` first (home: %s)", home)
	}

	full := append([]string{
		"compose",
		"-f", filepath.Join(home, composeFile),
		"--env-file", filepath.Join(home, envFile),
	}, args...)

	cmd := exec.Command("docker", full...)
	cmd.Dir = home // relative volume mounts (./secrets, ./vespa-app) resolve here
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
