#!/usr/bin/env bash
set -euo pipefail

# ── Colors & helpers ─────────────────────────────────────────────────────────
BOLD='\033[1m'
DIM='\033[2m'
CYAN='\033[1;36m'
GREEN='\033[1;32m'
YELLOW='\033[1;33m'
RED='\033[1;31m'
RESET='\033[0m'

banner() {
    echo ""
    echo -e "${CYAN}╔══════════════════════════════════════════════════════════╗${RESET}"
    echo -e "${CYAN}║${RESET}  ${BOLD}⚡ Brainstorm One-Click Deployment Configurator ⚡${RESET}      ${CYAN}║${RESET}"
    echo -e "${CYAN}╚══════════════════════════════════════════════════════════╝${RESET}"
    echo ""
}

section() {
    echo ""
    echo -e "${GREEN}── $1 ──${RESET}"
    echo ""
}

prompt() {
    local var_name="$1"
    local message="$2"
    local default="$3"
    local result

    if [ -n "$default" ]; then
        echo -ne "  ${BOLD}${message}${RESET} ${DIM}[${default}]${RESET}: "
        read -r result
        result="${result:-$default}"
    else
        echo -ne "  ${BOLD}${message}${RESET}: "
        read -r result
        while [ -z "$result" ]; do
            echo -ne "  ${RED}Required.${RESET} ${BOLD}${message}${RESET}: "
            read -r result
        done
    fi

    eval "$var_name=\"$result\""
}

prompt_yesno() {
    local var_name="$1"
    local message="$2"
    local default="$3"
    local result

    echo -ne "  ${BOLD}${message}${RESET} ${DIM}[${default}]${RESET}: "
    read -r result
    result="${result:-$default}"

    case "$result" in
        [Yy]*) eval "$var_name=true" ;;
        *)     eval "$var_name=false" ;;
    esac
}

generate_password() {
    openssl rand -base64 32 | tr -d '/+=' | head -c 32
}

# ── Load previous configuration ──────────────────────────────────────────────
load_previous_config() {
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    if [ -f "${SCRIPT_DIR}/.env" ]; then
        echo -e "${DIM}Loading previous configuration...${RESET}"
        # Source the .env file to get previous values
        set -a
        source "${SCRIPT_DIR}/.env"
        set +a
        
        # Set previous values for prompts
        PREV_UI_DOMAIN="${UI_DOMAIN:-}"
        PREV_API_DOMAIN="${API_DOMAIN:-}"
        PREV_GRAPERANK_WORKERS="${GRAPERANK_WORKERS:-}"
        PREV_FULL_SYNC="${FULL_SYNC:-}"
        PREV_SYNC_FROM_RELAY="${SYNC_FROM_RELAY:-}"
        PREV_PUBLISH_RELAY_URL="${PUBLISH_RELAY_URL:-}"
        PREV_ADMIN_ENABLED="${ADMIN_ENABLED:-}"
        PREV_ADMIN_WHITELISTED_PUBKEYS="${ADMIN_WHITELISTED_PUBKEYS:-}"
        
        # Keep passwords if they exist
        PREV_PG_PASSWORD="${PG_PASSWORD:-}"
        PREV_NEO4J_PASSWORD="${NEO4J_PASSWORD:-}"
        PREV_AUTH_SECRET="${AUTH_SECRET:-}"
        PREV_SQL_ADMIN_SECRET="${SQL_ADMIN_SECRET:-}"
        
        # Extract memory settings from existing docker-compose.yml if it exists
        if [ -f "${SCRIPT_DIR}/docker-compose.yml" ]; then
            # Extract -Xmx value from JAVA_OPTS
            PREV_JAVA_XMX=$(grep -A 10 "JAVA_OPTS:" "${SCRIPT_DIR}/docker-compose.yml" | grep -m1 "Xmx" | sed 's/.*-Xmx\([^ ]*\).*/\1/')
            PREV_JAVA_XMS=$(grep -A 10 "JAVA_OPTS:" "${SCRIPT_DIR}/docker-compose.yml" | grep -m1 "Xms" | sed 's/.*-Xms\([^ ]*\).*/\1/')
            
            # Extract container memory limit
            PREV_CONTAINER_MEMORY=$(grep -A 5 "brainstorm-graperank" "${SCRIPT_DIR}/docker-compose.yml" | grep -A 20 "deploy:" | grep -m1 "memory:" | awk '{print $2}')
            PREV_CONTAINER_MEMORY_RESERVATION=$(grep -A 5 "brainstorm-graperank" "${SCRIPT_DIR}/docker-compose.yml" | grep -A 20 "reservations:" | grep -m1 "memory:" | awk '{print $2}')
        fi
        
        return 0
    else
        return 1
    fi
}

# ── Detect system RAM ───────────────────────────────────────────────────────
detect_ram() {
    local total_kb
    total_kb=$(grep MemTotal /proc/meminfo | awk '{print $2}')
    echo $(( total_kb / 1024 / 1024 ))  # GB
}

calc_neo4j_memory() {
    local total_ram_gb=$1

    # Reserve ~4GB for OS + other services, give rest to neo4j
    local available=$(( total_ram_gb - 4 ))
    if [ "$available" -lt 2 ]; then
        available=2
    fi

    # Split: ~40% heap, ~60% pagecache
    local heap=$(( available * 40 / 100 ))
    local pagecache=$(( available * 60 / 100 ))

    # Minimum 1G each
    [ "$heap" -lt 1 ] && heap=1
    [ "$pagecache" -lt 1 ] && pagecache=1

    NEO4J_HEAP="${heap}G"
    NEO4J_PAGECACHE="${pagecache}G"
}

# ── Main ─────────────────────────────────────────────────────────────────────
banner

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load previous configuration if it exists
if load_previous_config; then
    echo -e "  ${GREEN}✓${RESET} Found previous configuration"
    echo ""
else
    # Set empty defaults for first run
    PREV_UI_DOMAIN=""
    PREV_API_DOMAIN=""
    PREV_GRAPERANK_WORKERS=""
    PREV_FULL_SYNC=""
    PREV_SYNC_FROM_RELAY=""
    PREV_PUBLISH_RELAY_URL=""
    PREV_ADMIN_ENABLED=""
    PREV_ADMIN_WHITELISTED_PUBKEYS=""
    PREV_JAVA_XMX=""
    PREV_JAVA_XMS=""
    PREV_CONTAINER_MEMORY=""
    PREV_CONTAINER_MEMORY_RESERVATION=""
fi

# Set memory defaults (use previous values or fallback to defaults)
JAVA_XMX="${PREV_JAVA_XMX:-3584m}"
JAVA_XMS="${PREV_JAVA_XMS:-2g}"
CONTAINER_MEMORY="${PREV_CONTAINER_MEMORY:-4g}"
CONTAINER_MEMORY_RESERVATION="${PREV_CONTAINER_MEMORY_RESERVATION:-2g}"

# ── DNS Names ────────────────────────────────────────────────────────────────
section "1/6  DNS Configuration"
echo -e "  ${DIM}These are the public domain names users will access.${RESET}"
echo -e "  ${DIM}Make sure DNS A records point to this server's IP.${RESET}"
echo ""
prompt UI_DOMAIN   "Brainstorm UI domain"  "${PREV_UI_DOMAIN:-brainstorm.example.com}"
prompt API_DOMAIN  "Brainstorm API domain" "${PREV_API_DOMAIN:-brainstorm-api.example.com}"

# ── RAM Detection & Memory Allocation ───────────────────────────────────────
section "2/6  System Resources"
TOTAL_RAM_GB=$(detect_ram)
echo -e "  ${BOLD}Detected RAM:${RESET} ${TOTAL_RAM_GB}GB"
calc_neo4j_memory "$TOTAL_RAM_GB"
echo -e "  ${BOLD}Neo4j heap:${RESET}   ${NEO4J_HEAP}"
echo -e "  ${BOLD}Neo4j page$:${RESET}  ${NEO4J_PAGECACHE}"

# Calculate memory limits for other services
# Allocate: Neo4j gets calculated above, Redis ~512MB-2GB, Postgres ~512MB-2GB, rest for apps
REDIS_MEMORY="512m"
POSTGRES_MEMORY="1g"
if [ "$TOTAL_RAM_GB" -ge 16 ]; then
    REDIS_MEMORY="2g"
    POSTGRES_MEMORY="2g"
elif [ "$TOTAL_RAM_GB" -ge 8 ]; then
    REDIS_MEMORY="1g"
    POSTGRES_MEMORY="1g"
fi

echo -e "  ${BOLD}Redis limit:${RESET}  ${REDIS_MEMORY}"
echo -e "  ${BOLD}Postgres:${RESET}     ${POSTGRES_MEMORY}"

if [ "$TOTAL_RAM_GB" -lt 8 ]; then
    echo ""
    echo -e "  ${YELLOW}Warning: <8GB RAM detected. Performance may be limited.${RESET}"
fi

# ── Worker Configuration ───────────────────────────────────────────────────
section "3/6  Worker Configuration"
echo -e "  ${DIM}Graperank workers process trust attestation calculations.${RESET}"
echo -e "  ${DIM}More workers = faster processing, but uses more CPU/memory.${RESET}"
echo -e "  ${DIM}Recommended: 1 worker per 2 CPU cores (max 4 for most setups).${RESET}"
echo ""
prompt GRAPERANK_WORKERS "Number of graperank workers" "${PREV_GRAPERANK_WORKERS:-2}"

# Validate worker count is a positive integer
if ! [[ "$GRAPERANK_WORKERS" =~ ^[1-9][0-9]*$ ]]; then
    echo -e "  ${YELLOW}Invalid worker count, defaulting to 2${RESET}"
    GRAPERANK_WORKERS=2
fi

echo -e "  ${BOLD}Workers:${RESET} ${GRAPERANK_WORKERS}"

# ── Full Sync ────────────────────────────────────────────────────────────────
section "4/6  Initial Relay Sync"
echo -e "  ${DIM}Full sync downloads the social graph from a source relay.${RESET}"
echo -e "  ${DIM}This can use significant disk space (~50-100GB) and take hours.${RESET}"
echo ""
# Convert boolean to y/n for default
if [ "${PREV_FULL_SYNC}" = "true" ]; then
    FULL_SYNC_DEFAULT="y"
elif [ "${PREV_FULL_SYNC}" = "false" ]; then
    FULL_SYNC_DEFAULT="n"
else
    FULL_SYNC_DEFAULT="y"
fi
prompt_yesno FULL_SYNC "Enable full sync on first startup?" "${FULL_SYNC_DEFAULT}"

# ── Relay Configuration ──────────────────────────────────────────────────────
section "5/6  Relay Configuration"
echo -e "  ${DIM}Configure which relays to sync from and publish to.${RESET}"
echo -e "  ${DIM}Press enter to accept defaults (NosFabrica relays).${RESET}"
echo ""
prompt SYNC_FROM_RELAY   "Sync from relay (source)"           "${PREV_SYNC_FROM_RELAY:-wss://wot.grapevine.network}"
prompt PUBLISH_RELAY_URL "TA publish relay (public wss URL)"  "${PREV_PUBLISH_RELAY_URL:-wss://nip85.nosfabrica.com}"

# ── Admin Configuration ──────────────────────────────────────────────────────
echo ""
echo -e "  ${DIM}Admin panel allows management of the Brainstorm instance.${RESET}"
echo -e "  ${DIM}Whitelist pubkeys (comma-separated hex) to grant admin access.${RESET}"
echo ""
# Convert boolean to y/n for default
if [ "${PREV_ADMIN_ENABLED}" = "true" ]; then
    ADMIN_ENABLED_DEFAULT="y"
elif [ "${PREV_ADMIN_ENABLED}" = "false" ]; then
    ADMIN_ENABLED_DEFAULT="n"
else
    ADMIN_ENABLED_DEFAULT="n"
fi
prompt_yesno ADMIN_ENABLED "Enable admin panel?" "${ADMIN_ENABLED_DEFAULT}"

if [ "${ADMIN_ENABLED}" = "true" ]; then
    prompt ADMIN_WHITELISTED_PUBKEYS "Admin whitelisted pubkeys (comma-separated)" "${PREV_ADMIN_WHITELISTED_PUBKEYS:-}"
else
    ADMIN_WHITELISTED_PUBKEYS=""
fi

# ── Generate Passwords ───────────────────────────────────────────────────────
section "6/6  Generating Secure Passwords"

# Reuse existing passwords if available, otherwise generate new ones
if [ -n "${PREV_PG_PASSWORD}" ]; then
    PG_PASSWORD="${PREV_PG_PASSWORD}"
    echo -e "  ${GREEN}✓${RESET} Reusing existing PostgreSQL password"
else
    PG_PASSWORD=$(generate_password)
    echo -e "  ${GREEN}✓${RESET} Generated new PostgreSQL password"
fi

if [ -n "${PREV_NEO4J_PASSWORD}" ]; then
    NEO4J_PASSWORD="${PREV_NEO4J_PASSWORD}"
    echo -e "  ${GREEN}✓${RESET} Reusing existing Neo4j password"
else
    NEO4J_PASSWORD=$(generate_password)
    echo -e "  ${GREEN}✓${RESET} Generated new Neo4j password"
fi

if [ -n "${PREV_AUTH_SECRET}" ]; then
    AUTH_SECRET="${PREV_AUTH_SECRET}"
    echo -e "  ${GREEN}✓${RESET} Reusing existing auth secret"
else
    AUTH_SECRET=$(generate_password)
    echo -e "  ${GREEN}✓${RESET} Generated new auth secret"
fi

if [ -n "${PREV_SQL_ADMIN_SECRET}" ]; then
    SQL_ADMIN_SECRET="${PREV_SQL_ADMIN_SECRET}"
    echo -e "  ${GREEN}✓${RESET} Reusing existing SQL admin secret"
else
    SQL_ADMIN_SECRET=$(generate_password)
    echo -e "  ${GREEN}✓${RESET} Generated new SQL admin secret"
fi

# ── Generate docker-compose.yml ──────────────────────────────────────────────
section "Generating configuration files..."

cat > "${SCRIPT_DIR}/docker-compose.yml" <<EOF
services:

  brainstorm-server:
    build:
      context: https://github.com/NosFabrica/brainstorm_server.git
      dockerfile: Dockerfile
    container_name: brainstorm-server
    ports:
      - "8000:8000"
    volumes:
      - ./secrets:/run/secrets:rw
    environment:
      deploy_environment: PRODUCTION
      auth_algorithm: HS256
      auth_secret_key: ${AUTH_SECRET}
      auth_access_token_expire_minutes: 60
      sql_admin_username: postgres
      sql_admin_password: ${PG_PASSWORD}
      sql_admin_secret_key: ${SQL_ADMIN_SECRET}
      db_url: postgresql+asyncpg://postgres:${PG_PASSWORD}@postgres:5432/brainstorm-database
      redis_host: redis_strfry
      redis_port: 6379
      neo4j_db_url: bolt://neo4j:7687
      neo4j_db_username: neo4j
      neo4j_db_password: ${NEO4J_PASSWORD}
      nostr_transfer_from_relay: ${SYNC_FROM_RELAY}
      nostr_transfer_to_relay: ws://neofry:7777
      nostr_upload_ta_events_relay: ws://strfry:7777
      nostr_upload_ta_events_relay_public_url: ${PUBLISH_RELAY_URL}
      cutoff_of_valid_graperank_scores: 0.02
      perform_nostr_full_sync: ${FULL_SYNC}
      frontend_url: https://${UI_DOMAIN}
      admin_enabled: ${ADMIN_ENABLED}
      admin_whitelisted_pubkeys: "${ADMIN_WHITELISTED_PUBKEYS}"
      PYTHONUNBUFFERED: "1"
      PYTHONDONTWRITEBYTECODE: "1"
      UVICORN_WORKERS: "4"
      UVICORN_BACKLOG: "2048"
      UVICORN_LIMIT_CONCURRENCY: "1000"
      UVICORN_TIMEOUT_KEEP_ALIVE: "5"
    depends_on:
      - postgres
      - redis_strfry
      - neo4j
      - neofry
      - strfry
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 2g
        reservations:
          memory: 512m

  brainstorm-ui:
    build:
      context: https://github.com/NosFabrica/Brainstorm-UI.git
      dockerfile: Dockerfile
      args:
        VITE_API_URL: https://${API_DOMAIN}
    container_name: brainstorm-ui
    ports:
      - "3000:3000"
    depends_on:
      - brainstorm-server
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 1g
        reservations:
          memory: 256m

EOF

# Generate graperank workers based on GRAPERANK_WORKERS count
for i in $(seq 1 $GRAPERANK_WORKERS); do
  cat >> "${SCRIPT_DIR}/docker-compose.yml" <<EOF
  brainstorm-graperank-worker-${i}:
    build:
      context: https://github.com/NosFabrica/brainstorm_graperank_algorithm.git
      dockerfile: Dockerfile
    container_name: brainstorm-graperank-worker-${i}
    environment:
      REDIS_HOST: redis_strfry
      REDIS_PORT: 6379
      NEO4J_URL: neo4j://neo4j:7687
      NEO4J_USERNAME: neo4j
      NEO4J_PASSWORD: ${NEO4J_PASSWORD}
      JAVA_OPTS: >-
        -Xms${JAVA_XMS}
        -Xmx${JAVA_XMX}
        -XX:+UseG1GC
        -XX:MaxGCPauseMillis=200
        -XX:+UseStringDeduplication
        -XX:+ParallelRefProcEnabled
        -XX:MaxMetaspaceSize=256m
        -Djava.net.preferIPv4Stack=true
    depends_on:
      - redis_strfry
      - neo4j
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: ${CONTAINER_MEMORY}
        reservations:
          memory: ${CONTAINER_MEMORY_RESERVATION}

EOF
done

cat >> "${SCRIPT_DIR}/docker-compose.yml" <<EOF

  postgres:
    image: postgres:15
    container_name: postgres
    restart: always
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${PG_PASSWORD}
      POSTGRES_DB: brainstorm-database
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5
    deploy:
      resources:
        limits:
          memory: ${POSTGRES_MEMORY}
        reservations:
          memory: 256m
    shm_size: 256m

  redis_strfry:
    image: redis:7
    container_name: redis
    command: redis-server --maxmemory ${REDIS_MEMORY} --maxmemory-policy allkeys-lru
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: ${REDIS_MEMORY}
        reservations:
          memory: 128m

  neo4j:
    image: neo4j:5
    container_name: neo4j
    restart: unless-stopped
    ports:
      - "7474:7474"
      - "7688:7687"
    environment:
      NEO4J_AUTH: neo4j/${NEO4J_PASSWORD}
      NEO4J_server_memory_heap_initial__size: ${NEO4J_HEAP}
      NEO4J_server_memory_heap_max__size: ${NEO4J_HEAP}
      NEO4J_server_memory_pagecache_size: ${NEO4J_PAGECACHE}
      NEO4J_server_bolt_telemetry_enabled: "false"
    volumes:
      - neo4j_data:/data
      - neo4j_logs:/logs
      - neo4j_import:/import
    ulimits:
      nofile:
        soft: 1048576
        hard: 1048576
    deploy:
      resources:
        limits:
          cpus: '4.0'
        reservations:
          cpus: '1.0'

  neofry:
    image: ghcr.io/nosfabrica/neofry:latest
    container_name: neofry
    ports:
      - "7777:7777"
    environment:
      ROUTER: /etc/strfry-router.conf
    volumes:
      - neofry-db:/app/strfry-db
      - ./neofry.conf:/etc/strfry.conf
    depends_on:
      - redis_strfry
    restart: always
    stop_grace_period: 2m
    ulimits:
      nofile:
        soft: 1000000
        hard: 1000000
    deploy:
      resources:
        limits:
          memory: 2g
        reservations:
          memory: 512m

  strfry:
    image: ghcr.io/hoytech/strfry:latest
    container_name: strfry
    ports:
      - "7778:7777"
    volumes:
      - strfry-db:/app/strfry-db
      - ./strfry.conf:/etc/strfry.conf
    restart: unless-stopped
    ulimits:
      nofile:
        soft: 1000000
        hard: 1000000
    deploy:
      resources:
        limits:
          memory: 2g
        reservations:
          memory: 512m

  caddy:
    image: caddy:2
    container_name: caddy
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    depends_on:
      - brainstorm-server
      - brainstorm-ui
      - neofry
      - strfry
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 512m
        reservations:
          memory: 128m

volumes:
  postgres_data:
  redis_data:
  neo4j_data:
  neo4j_logs:
  neo4j_import:
  neofry-db:
  strfry-db:
  caddy_data:
EOF

echo -e "  ${GREEN}✓${RESET} docker-compose.yml"

# ── Generate Caddyfile ───────────────────────────────────────────────────────
cat > "${SCRIPT_DIR}/Caddyfile" <<EOF
# Brainstorm UI
${UI_DOMAIN} {
    reverse_proxy brainstorm-ui:3000
}

# Brainstorm API
${API_DOMAIN} {
    reverse_proxy brainstorm-server:8000
}
EOF

echo -e "  ${GREEN}✓${RESET} Caddyfile"

# ── Save passwords to .env (gitignored) ─────────────────────────────────────
cat > "${SCRIPT_DIR}/.env" <<EOF
# Generated by configure.sh — DO NOT COMMIT
# Passwords
PG_PASSWORD=${PG_PASSWORD}
NEO4J_PASSWORD=${NEO4J_PASSWORD}
AUTH_SECRET=${AUTH_SECRET}
SQL_ADMIN_SECRET=${SQL_ADMIN_SECRET}

# Domain Configuration
UI_DOMAIN=${UI_DOMAIN}
API_DOMAIN=${API_DOMAIN}

# Worker Configuration
GRAPERANK_WORKERS=${GRAPERANK_WORKERS}

# Sync Configuration
FULL_SYNC=${FULL_SYNC}
SYNC_FROM_RELAY=${SYNC_FROM_RELAY}
PUBLISH_RELAY_URL=${PUBLISH_RELAY_URL}

# Admin Configuration
ADMIN_ENABLED=${ADMIN_ENABLED}
ADMIN_WHITELISTED_PUBKEYS=${ADMIN_WHITELISTED_PUBKEYS}
EOF

echo -e "  ${GREEN}✓${RESET} .env (passwords saved)"

# ── Ensure .env is gitignored ────────────────────────────────────────────────
if ! grep -q "^\.env$" "${SCRIPT_DIR}/.gitignore" 2>/dev/null; then
    echo ".env" >> "${SCRIPT_DIR}/.gitignore"
    echo -e "  ${GREEN}✓${RESET} Added .env to .gitignore"
fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════════════════════╗${RESET}"
echo -e "${CYAN}║${RESET}  ${BOLD}Configuration complete!${RESET}                                 ${CYAN}║${RESET}"
echo -e "${CYAN}╚══════════════════════════════════════════════════════════╝${RESET}"
echo ""
echo -e "  ${BOLD}UI:${RESET}        https://${UI_DOMAIN}"
echo -e "  ${BOLD}API:${RESET}       https://${API_DOMAIN}"
echo -e "  ${BOLD}Workers:${RESET}   ${GRAPERANK_WORKERS} graperank workers"
echo ""
echo -e "  ${BOLD}Memory Allocation:${RESET}"
echo -e "    Neo4j:     heap=${NEO4J_HEAP}  pagecache=${NEO4J_PAGECACHE}"
echo -e "    Redis:     ${REDIS_MEMORY}"
echo -e "    Postgres:  ${POSTGRES_MEMORY}"
echo ""
echo -e "  ${BOLD}Full sync:${RESET} ${FULL_SYNC}"
echo -e "  ${BOLD}Sync from:${RESET} ${SYNC_FROM_RELAY}"
echo ""
echo -e "  ${BOLD}Next steps:${RESET}"
echo -e "    1. Ensure DNS A records point to this server"
echo -e "    2. Run: ${CYAN}docker compose up -d${RESET}"
echo -e "    3. Monitor: ${CYAN}docker compose logs -f${RESET}"
echo ""
echo -e "  ${DIM}Passwords saved to .env — keep this file safe!${RESET}"
echo ""
