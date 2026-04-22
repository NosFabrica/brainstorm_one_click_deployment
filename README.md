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
```

#### Restarting neofry

```bash
# Connect to the remote production server (replace with values given by admin)
ssh <user>@<server-address>

# Restart neofry
docker restart neofry
```
