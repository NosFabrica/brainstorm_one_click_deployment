# Brainstorm One-Click Deployment

Deploy Brainstorm with an interactive configuration wizard that automatically configures and builds the necessary services from source.

## Quick Start

### Clone this repository

```bash
git clone https://github.com/nosfabrica/brainstorm_one_click_deployment.git
cd brainstorm_one_click_deployment
```

### 1. Install Docker (if not already installed)

For Debian or Ubuntu systems:

```bash
./install_docker.sh
```

### 2. Run the Configuration Wizard

```bash
./configure.sh
```

The wizard will interactively prompt you for:
- **DNS domains** for UI and API
- **System resources** (auto-detected RAM, Neo4j memory allocation)
- **Initial sync** preferences (enable/disable full relay sync)
- **Relay configuration** (sync source and publish targets)
- **Secure passwords** (auto-generated for all services)

### 3. Start the Services

After configuration completes:

```bash
docker compose up -d
```

### 4. Monitor Deployment

```bash
docker compose logs -f
```

The wizard generates:
- `docker-compose.yml` - Service orchestration with your settings
- `Caddyfile` - Reverse proxy with automatic HTTPS
- `.env` - Secure passwords

## What Gets Deployed

All services are built automatically from GitHub repositories:
- **brainstorm-server** - FastAPI backend
- **brainstorm-ui** - Frontend
- **brainstorm-graperank** - GrapeRank algorithm service
- **postgres** - Database
- **redis** - Message queue
- **neo4j** - Graph database
- **neofry** - Nostr relay (Neo4J)
- **strfry** - Nostr relay (LMDB)
- **caddy** - Reverse proxy with automatic Let's Encrypt SSL

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
