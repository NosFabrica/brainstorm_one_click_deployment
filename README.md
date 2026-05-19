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
docker logs neofry --tail 100

# Check current healthcheck status
docker inspect --format='{{.State.Health.Status}}' neofry

# Check autoheal activity
docker logs autoheal --tail 50
```

#### Manual restart (fallback)

If autoheal doesn't recover the container or you want to force a restart sooner:

```bash
# Connect to the remote production server (replace with values given by admin)
ssh <user>@<server-address>

# Restart neofry
docker restart neofry
```
