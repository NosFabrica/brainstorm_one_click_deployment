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

# ── DNS Names ────────────────────────────────────────────────────────────────
section "1/5  DNS Configuration"
echo -e "  ${DIM}These are the public domain names users will access.${RESET}"
echo -e "  ${DIM}Make sure DNS A records point to this server's IP.${RESET}"
echo ""
prompt UI_DOMAIN   "Brainstorm UI domain"  "brainstorm.example.com"
prompt API_DOMAIN  "Brainstorm API domain" "brainstorm-api.example.com"

# ── RAM Detection ────────────────────────────────────────────────────────────
section "2/5  System Resources"
TOTAL_RAM_GB=$(detect_ram)
echo -e "  ${BOLD}Detected RAM:${RESET} ${TOTAL_RAM_GB}GB"
calc_neo4j_memory "$TOTAL_RAM_GB"
echo -e "  ${BOLD}Neo4j heap:${RESET}   ${NEO4J_HEAP}"
echo -e "  ${BOLD}Neo4j page$:${RESET}  ${NEO4J_PAGECACHE}"

if [ "$TOTAL_RAM_GB" -lt 8 ]; then
    echo ""
    echo -e "  ${YELLOW}Warning: <8GB RAM detected. Performance may be limited.${RESET}"
fi

# ── Full Sync ────────────────────────────────────────────────────────────────
section "3/5  Initial Relay Sync"
echo -e "  ${DIM}Full sync downloads the social graph from a source relay.${RESET}"
echo -e "  ${DIM}This can use significant disk space (~50-100GB) and take hours.${RESET}"
echo ""
prompt_yesno FULL_SYNC "Enable full sync on first startup?" "y"

# ── Relay Configuration ──────────────────────────────────────────────────────
section "4/5  Relay Configuration"
echo -e "  ${DIM}Configure which relays to sync from and publish to.${RESET}"
echo -e "  ${DIM}Press enter to accept defaults (NosFabrica relays).${RESET}"
echo ""
prompt SYNC_FROM_RELAY   "Sync from relay (source)"           "wss://wot.grapevine.network"
prompt PUBLISH_RELAY_URL "TA publish relay (public wss URL)"  "wss://nip85.nosfabrica.com"

# ── Generate Passwords ───────────────────────────────────────────────────────
section "5/5  Generating Secure Passwords"
PG_PASSWORD=$(generate_password)
NEO4J_PASSWORD=$(generate_password)
AUTH_SECRET=$(generate_password)
SQL_ADMIN_SECRET=$(generate_password)

echo -e "  ${BOLD}PostgreSQL password:${RESET}  ${DIM}${PG_PASSWORD:0:8}...${RESET}"
echo -e "  ${BOLD}Neo4j password:${RESET}       ${DIM}${NEO4J_PASSWORD:0:8}...${RESET}"
echo -e "  ${BOLD}Auth secret:${RESET}          ${DIM}${AUTH_SECRET:0:8}...${RESET}"
echo -e "  ${BOLD}SQL admin secret:${RESET}     ${DIM}${SQL_ADMIN_SECRET:0:8}...${RESET}"

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
      admin_enabled: false
      admin_whitelisted_pubkeys: ""
    depends_on:
      - postgres
      - redis_strfry
      - neo4j
      - neofry
      - strfry
    restart: unless-stopped

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

  brainstorm-graperank:
    build:
      context: https://github.com/NosFabrica/brainstorm_graperank_algorithm.git
      dockerfile: Dockerfile
    container_name: brainstorm-graperank
    environment:
      REDIS_HOST: redis_strfry
      REDIS_PORT: 6379
      NEO4J_URL: neo4j://neo4j:7687
      NEO4J_USERNAME: neo4j
      NEO4J_PASSWORD: ${NEO4J_PASSWORD}
    depends_on:
      - redis_strfry
      - neo4j
    restart: unless-stopped

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

  redis_strfry:
    image: redis:7
    container_name: redis
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    restart: unless-stopped

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
PG_PASSWORD=${PG_PASSWORD}
NEO4J_PASSWORD=${NEO4J_PASSWORD}
AUTH_SECRET=${AUTH_SECRET}
SQL_ADMIN_SECRET=${SQL_ADMIN_SECRET}
UI_DOMAIN=${UI_DOMAIN}
API_DOMAIN=${API_DOMAIN}
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
echo -e "  ${BOLD}Neo4j:${RESET}     heap=${NEO4J_HEAP}  pagecache=${NEO4J_PAGECACHE}"
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
