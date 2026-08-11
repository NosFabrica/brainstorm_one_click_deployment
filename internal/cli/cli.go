// Package cli wires the brainstorm subcommands onto Cobra.
package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/NosFabrica/brainstorm_one_click_deployment/internal/app"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X .../internal/cli.Version=...".
var Version = "dev"

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func root() *cobra.Command {
	c := &cobra.Command{
		Use:   "brainstorm",
		Short: "Run the Brainstorm stack with one command",
		Long: "brainstorm wraps docker compose to run the full Brainstorm stack.\n" +
			"State lives in ~/.brainstorm (override with $BRAINSTORM_HOME).",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.AddCommand(
		startCmd(),
		stopCmd(),
		restartCmd(),
		statusCmd(),
		syncCmd(),
		queueCmd(),
		nostrQueryCmd(),
		logsCmd(),
		updateCmd(),
		configCmd(),
		destroyCmd(),
	)
	return c
}

// withHome adapts a home-taking function into a cobra RunE that resolves the
// brainstorm home first.
func withHome(fn func(home string) error) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		home, err := app.Home()
		if err != nil {
			return err
		}
		return fn(home)
	}
}

func startCmd() *cobra.Command {
	var reconfigure, refreshAssets bool
	c := &cobra.Command{
		Use:   "start",
		Short: "Set up config if needed, then bring the stack up",
		RunE: func(*cobra.Command, []string) error {
			if err := app.CheckDocker(); err != nil {
				return err
			}
			home, err := app.Home()
			if err != nil {
				return err
			}
			fmt.Printf("brainstorm home: %s\n", home)
			if app.DockerIsSnap() && os.Getenv("BRAINSTORM_HOME") == "" {
				fmt.Println("note: detected snap Docker (can't read hidden dirs) — using a non-hidden home.")
				fmt.Println("      for production, prefer Docker Engine from https://get.docker.com")
			}
			fmt.Println()

			printSpecs()
			fmt.Println()

			if err := app.Scaffold(home, refreshAssets); err != nil {
				return err
			}
			if _, err := app.Configure(home, reconfigure); err != nil {
				return err
			}
			fmt.Println("starting services...")
			fmt.Println("first run pulls images, then waits for Postgres health and the Vespa app")
			fmt.Println("deploy before brainstorm-server starts — expect a few minutes. Docker's")
			fmt.Println("\"... N more\" line is normal progress; watch it with:")
			fmt.Println("    brainstorm logs -f brainstorm-vespa-deploy")
			fmt.Println()
			if err := app.Compose(home, "up", "-d", "--remove-orphans"); err != nil {
				return err
			}
			printReadiness(home)
			fmt.Println("\n(re-check anytime with `brainstorm status`, or tail logs with `brainstorm logs -f`)")
			return nil
		},
	}
	c.Flags().BoolVar(&reconfigure, "reconfigure", false, "re-prompt for manually-set values")
	c.Flags().BoolVar(&refreshAssets, "refresh-assets", false, "overwrite compose/config files from the embedded copy")
	return c
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the stack (containers stopped, data kept)",
		RunE:  withHome(func(home string) error { return app.Compose(home, "stop") }),
	}
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart services",
		RunE:  withHome(func(home string) error { return app.Compose(home, "restart") }),
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which services are on (green ✓) or off (red ✗)",
		RunE: withHome(func(home string) error {
			printSpecs()
			fmt.Println()

			statuses, err := app.Status(home)
			if err != nil {
				return err
			}
			fmt.Println("services:")
			for _, s := range statuses {
				mark := colorize("✗", colorRed)
				if s.Running {
					mark = colorize("✓", colorGreen)
				}
				fmt.Printf("  %s  %s\n", mark, s.Name)
			}

			fmt.Println("\nrelay transfer (brainstorm_nostr_relay_transfer):")
			kinds, terr := app.TransferStatus(home)
			if terr != nil {
				fmt.Printf("  unavailable — %v\n", terr)
				return nil
			}
			for _, k := range kinds {
				mark := colorize("○", colorGray) // waiting to start: row not present yet
				label := "waiting to start"
				switch {
				case k.Exists && k.Completed:
					mark = colorize("✓", colorGreen)
					label = "completed"
				case k.Exists:
					mark = colorize("◐", colorYellow) // in progress: row present but not completed
					label = "in progress"
				}
				fmt.Printf("  %s  kind %-4d %s\n", mark, k.Kind, label)
			}

			printDataCounts(home)
			printResourceUsage(home)
			printReadiness(home)
			return nil
		}),
	}
}

// printDataCounts shows record counts across Redis, Neo4j, and Vespa.
func printDataCounts(home string) {
	fmt.Println("\ndata:")
	if n, err := app.RedisKeyCount(home); err != nil {
		fmt.Printf("  %-18s %s\n", "redis keys:", "unavailable")
	} else {
		fmt.Printf("  %-18s %s\n", "redis keys:", commafy(n))
	}
	if c, err := app.Neo4jCounts(home); err != nil {
		fmt.Printf("  %-18s %s\n", "neo4j:", "unavailable")
	} else {
		fmt.Printf("  %-18s %s\n", "neo4j NostrUsers:", commafy(c.Users))
		fmt.Printf("  %-18s %s\n", "neo4j FOLLOWS:", commafy(c.Follows))
		fmt.Printf("  %-18s %s\n", "neo4j MUTES:", commafy(c.Mutes))
		fmt.Printf("  %-18s %s\n", "neo4j REPORTS:", commafy(c.Reports))
	}
	if n, err := app.VespaDocCount(); err != nil {
		fmt.Printf("  %-18s %s\n", "vespa documents:", "unavailable")
	} else {
		fmt.Printf("  %-18s %s\n", "vespa documents:", commafy(n))
	}
}

// printResourceUsage shows per-service RAM/disk and the combined total.
func printResourceUsage(home string) {
	usages, err := app.ResourceUsages(home)
	if err != nil {
		fmt.Printf("\nresource usage: unavailable (%v)\n", err)
		return
	}
	fmt.Println("\nresource usage (RAM / disk):")
	var ramTotal, diskTotal int64
	for _, u := range usages {
		fmt.Printf("  %-10s %9s / %9s\n", u.Name, humanBytes(u.RAMBytes), humanBytes(u.DiskBytes))
		if u.RAMBytes > 0 {
			ramTotal += u.RAMBytes
		}
		if u.DiskBytes > 0 {
			diskTotal += u.DiskBytes
		}
	}
	fmt.Printf("  %-10s %9s / %9s\n", "total", humanBytes(ramTotal), humanBytes(diskTotal))
}

// commafy formats an integer with thousands separators.
func commafy(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// humanBytes renders a byte count in IEC units ("?" if unknown).
func humanBytes(b int64) string {
	if b < 0 {
		return "?"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	val := float64(b)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", val, units[i])
}

// printReadiness shows whether brainstorm-server is serving, and where to reach
// the UI. Shared by `start` and `status`.
func printReadiness(home string) {
	fmt.Println()
	if app.ServerReady() {
		fmt.Println(colorize(fmt.Sprintf("Your Brainstorm is ready! Access it on %s", app.UIURL(home)), colorGreen))
	} else {
		fmt.Println("Your Brainstorm is still getting ready — check again in a moment.")
	}
}

func syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Pull events (kinds 0, 3, 10000, 1984) from the reference relay into neofry",
		RunE:  withHome(app.Sync),
	}
}

func queueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "queue",
		Short: "Show how many items are waiting to be processed",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := app.Home()
			if err != nil {
				return err
			}
			q, err := app.QueueStatus(home)
			if err != nil {
				return err
			}
			fmt.Printf("how many nostr events (0, 3, 1984 and 10000) are waiting to process: %d\n", q.Events)
			fmt.Printf("how many graperank requests are waiting to process: %d\n", q.Graperank)
			return nil
		},
	}
}

func nostrQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nostr-query <neofry|nip85> <filter>",
		Short: "Query a relay's stored events with a nostr filter and print the response",
		Example: "  brainstorm nostr-query neofry '{\"kinds\":[0],\"limit\":5}'\n" +
			"  brainstorm nostr-query nip85 '{\"kinds\":[10040]}'",
		Args:      cobra.ExactArgs(2),
		ValidArgs: app.RelayTargets(),
		RunE: func(_ *cobra.Command, args []string) error {
			home, err := app.Home()
			if err != nil {
				return err
			}
			return app.NostrQuery(home, args[0], args[1])
		},
	}
}

// printSpecs reports whether the host meets the stack's resource needs.
func printSpecs() {
	r := app.CheckSpecs()

	ram := "RAM unknown"
	if r.RAMGB > 0 {
		ram = fmt.Sprintf("%d GB RAM", r.RAMGB)
	}
	parts := []string{ram, fmt.Sprintf("%d CPUs", r.CPUs)}
	if r.DiskGB >= 0 {
		parts = append(parts, fmt.Sprintf("%d GB free disk", r.DiskGB))
	}
	specs := strings.Join(parts, ", ")

	if r.OK {
		fmt.Println(colorize(fmt.Sprintf("✓ machine specs look sufficient (%s)", specs), colorGreen))
		return
	}
	fmt.Println(colorize(fmt.Sprintf("⚠ machine may be underspecced (%s):", specs), colorYellow))
	for _, w := range r.Warnings {
		fmt.Printf("    - %s\n", w)
	}
}

const (
	colorGreen  = "32"
	colorRed    = "31"
	colorYellow = "33"
	colorGray   = "90"
)

// colorize wraps s in an ANSI color unless output isn't a terminal or NO_COLOR
// is set.
func colorize(s, code string) string {
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func logsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs [service...]",
		Short: "Show logs, optionally for specific services",
		RunE: func(_ *cobra.Command, args []string) error {
			home, err := app.Home()
			if err != nil {
				return err
			}
			cargs := []string{"logs", "--tail", "200"}
			if follow {
				cargs = append(cargs, "-f")
			}
			return app.Compose(home, append(cargs, args...)...)
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return c
}

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Pull the latest images and recreate containers",
		RunE: withHome(func(home string) error {
			if err := app.Compose(home, "pull"); err != nil {
				return err
			}
			return app.Compose(home, "up", "-d")
		}),
	}
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Re-prompt for manually-set values (run `restart` to apply)",
		RunE: withHome(func(home string) error {
			if err := app.Scaffold(home, false); err != nil {
				return err
			}
			_, err := app.Configure(home, true)
			if err == nil {
				fmt.Println("saved. Run `brainstorm restart` (or `start`) to apply.")
			}
			return err
		}),
	}
}

func destroyCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "destroy",
		Short: "Stop the stack and delete ALL data volumes",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := app.Home()
			if err != nil {
				return err
			}
			if !yes {
				fmt.Print("This deletes ALL Brainstorm data (postgres, neo4j, vespa, relays).\nType 'yes' to continue: ")
				var ans string
				fmt.Scanln(&ans)
				if ans != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}
			if err := app.Compose(home, "down", "-v"); err != nil {
				return err
			}

			// Offer to wipe the configuration too. Skipped under -y so scripted
			// runs never silently delete the only copy of the secrets.
			if yes {
				return nil
			}
			fmt.Printf("\nAlso delete your configuration at %s?\n", home)
			fmt.Print("This removes .env (generated secrets) and the secrets/ key — unrecoverable. Type 'yes' to delete: ")
			var ans string
			fmt.Scanln(&ans)
			if ans == "yes" {
				if err := os.RemoveAll(home); err != nil {
					return err
				}
				fmt.Println("configuration deleted. Next `brainstorm start` will set up fresh.")
			} else {
				fmt.Println("configuration kept.")
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}
