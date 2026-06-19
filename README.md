# Brainstorm deployment

## Quick start with the `brainstorm` CLI (recommended)

A small Go CLI (`brainstorm`, modeled after [nigiri](https://github.com/vulpemventures/nigiri))
wraps `docker compose` so you can run the whole stack with one command. It pulls
the prebuilt images from `ghcr.io/nosfabrica/*`, so no local image builds are
needed.

```bash
# install the prebuilt binary (needs Docker; no Go required)
curl -fsSL https://raw.githubusercontent.com/NosFabrica/brainstorm_one_click_deployment/main/install.sh | bash
# ...or build from source (needs Go >= 1.22): make install

brainstorm start      # first run: generates secrets, asks local/internet, brings the stack up
brainstorm status
brainstorm logs -f
brainstorm stop
```

On first `start` the CLI scaffolds `~/.brainstorm/` (compose file, relay configs,
vespa app, `secrets/`), generates the secret values (`auth_secret_key`, db/neo4j
passwords) once, then asks whether this is a **local** experiment or an
**internet**-facing deployment:

- **local** — uses `localhost` URLs and `deploy_environment=DEV`, no further questions.
- **internet** — sets `deploy_environment=PROD` and prompts for the public values
  (`VITE_API_URL`, `frontend_url`, `public_base_url`, relay URLs).

Everything is stored in `~/.brainstorm/.env`. Re-run the questions with
`brainstorm config`. Override the home dir with `$BRAINSTORM_HOME`.

Commands: `start`, `stop`, `restart`, `status`, `logs [svc]`, `update` (pull
latest images), `config`, `destroy` (delete all data volumes).

> **Ubuntu / snap Docker:** the Canonical **snap** build of Docker runs under
> AppArmor confinement and cannot read hidden directories like `~/.brainstorm`
> (you'll see `open .../.env: permission denied`). Use Docker Engine from the
> official apt repo instead:
> ```bash
> sudo snap remove docker
> curl -fsSL https://get.docker.com | sudo sh
> sudo usermod -aG docker $USER && newgrp docker
> ```

---

## Manual deployment (build images yourself)

to deploy, you need to first create 2 docker images.

clone the brainstorm_server repo, and on the main directory, run:

```bash
docker build -t brainstorm-server-service .
```

clone the brainstorm_graperank repo, and on the main directory, run:

```bash
docker build -t brainstorm-graperank-service .
```

clone the BrainstormUI repo, and on the main directory, run:

```bash
docker build -t brainstorm-ui-service --build-arg VITE_API_URL=https://brainstormserver.nosfabrica.com VITE_NIP85_RELAY_URL=wss://nip85.nosfabrica.com .
```

now, on this repo's main directory, run:

```bash
docker compose up
```

if you want it to keep running, run:

```bash
docker compose up -d
```

## Local development

for local dev, use the local override which builds UI and neofry from source and sets localhost URLs:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build
```

this requires the brainstorm_server, brainstorm_graperank, and Brainstorm-UI repos cloned as siblings.

---

## Router configuration

The neofry router config lives at `strfry-router.conf` in this repo and is bind-mounted into the container at `/etc/strfry-router.conf`. Edit it on the host — strfry's `file_change_monitor` picks up changes in-process, no restart or rebuild needed.

The file overlays the default baked into the neofry image. Removing the bind mount falls back to the image default.

### Autoheal

If the router loses its upstream connection, the autoheal sidecar restarts neofry automatically within ~3-4 minutes (healthcheck: 60s interval, 3 retries, then 30s autoheal poll). The healthcheck verifies both the local relay (HTTP probe on `:7777`) and the `strfry router` subprocess.

---

## Troubleshooting

### Relay is no longer syncing

This can happen if we lose connection to the `friend` relay. In which case, we must manually restart the `neofry` container in the docker (name can differ in production and instead of `neofry` an actual image id can be used).

#### How to tell if the router is working

Check the neofry logs for these three signs:

1. **`neo4j_inserted`** — should appear continuously. If you stop seeing it, the router likely needs a restart.
2. **`Loading router config file`** — confirms the router has loaded its configuration successfully.
3. **`friends: Connected`** — the router must be connected to the client's relay. If this is missing, sync will not work.

```bash
# Check neofry logs
docker logs brainstorm-neofry --tail 100

# Check current healthcheck status
docker inspect --format='{{.State.Health.Status}}' brainstorm-neofry

# Check autoheal activity
docker logs brainstorm-autoheal --tail 50
```

#### Manual restart (fallback)

If autoheal doesn't recover the container or you want to force a restart sooner:

```bash
# Connect to the remote production server (replace with values given by admin)
ssh <user>@<server-address>

# Restart neofry
docker restart brainstorm-neofry
```
