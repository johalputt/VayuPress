#!/bin/bash
# =============================================================================
#  deploy-vayupress.sh — VayuPress Production Deployment (v1.7.0)
# =============================================================================
#
#  Deploys the multi-package Go module architecture introduced in P14.
#  Supports fresh installs and zero-downtime upgrades on Ubuntu 24.04 LTS.
#
#  Stack:
#    Go 1.23+       — binary built from source
#    SQLite3        — primary database (CGO_ENABLED=1)
#    Nginx          — reverse proxy + TLS termination
#    Certbot        — Let's Encrypt HTTPS (optional)
#    Meilisearch    — full-text search
#    UFW            — host firewall
#    Fail2ban       — brute-force protection
#    Systemd        — process supervision
#
#  REQUIREMENTS
#  ────────────────────────────────────────────────────────────────────────────
#  OS     : Ubuntu 24.04 LTS (fresh or existing — idempotent)
#  RAM    : 8 GB minimum, 12 GB recommended
#  CPU    : 4 vCPU minimum
#  Disk   : 50 GB minimum NVMe
#  Access : Root or sudo
#  Network: Outbound HTTPS (GitHub, Go module proxy, Meilisearch CDN)
#
#  USAGE
#  ────────────────────────────────────────────────────────────────────────────
#    sudo ./deploy-vayupress.sh                # fresh install
#    sudo ./deploy-vayupress.sh --upgrade      # upgrade, preserves data
#    sudo ./deploy-vayupress.sh --dry-run      # validate only, no changes
#
# =============================================================================
set -euo pipefail
IFS=$'\n\t'

# =============================================================================
# ── CONFIGURATION  (edit before running) ─────────────────────────────────────
# =============================================================================

ENGINE_VERSION="1.7.0"

REPO_URL="https://github.com/johalputt/vayupress.git"
REPO_BRANCH="main"

DOMAIN="vayupress.com"
EMAIL="admin@vayupress.com"
WORKER_COUNT=4

# Directories
INSTALL_DIR="/opt/vayupress"
DATA_DIR="/var/lib/vayupress"
LOG_DIR="/var/log/vayupress"
CACHE_DIR="/var/cache/vayupress"
STATIC_DIR="/var/lib/vayupress/static"
BACKUP_DIR="/var/backups/vayupress"

# VayuPress runtime config (written to /etc/vayupress/env)
API_KEY=""                    # set a strong random value: openssl rand -hex 32
DB_PATH="${DATA_DIR}/vayupress.db"
QUEUE_HARD_LIMIT=1000
PLUGIN_MAX_CONCURRENT=8
PLUGIN_TIMEOUT_MS=2000
WAL_SIZE_THRESHOLD_MB=64
MAINTENANCE_MODE=false

# Backup & storage governance
BACKUP_RETAIN_DAYS=30         # days to keep database backups before pruning
STORAGE_QUOTA_GB=200          # alert threshold in GB for data directory

# Meilisearch
MEILI_DIR="/var/lib/meilisearch"
MEILI_MASTER_KEY=""           # set a strong random value: openssl rand -hex 32

# Go toolchain — minimum acceptable major.minor (patch is irrelevant)
GO_MIN_MAJOR=1
GO_MIN_MINOR=22   # VayuPress requires Go 1.22+; any patch release is fine

# =============================================================================
# ── HELPERS ───────────────────────────────────────────────────────────────────
# =============================================================================

DRY_RUN=false
UPGRADE=false

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    --upgrade) UPGRADE=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
die()  { echo -e "${RED}❌ $*${NC}" >&2; exit 1; }

run() {
  if $DRY_RUN; then
    echo -e "${YELLOW}[dry-run]${NC} $*"
  else
    "$@"
  fi
}

require_root() {
  if [[ $EUID -ne 0 ]]; then
    die "This script must be run as root (use sudo)."
  fi
}

# =============================================================================
# ── BANNER ────────────────────────────────────────────────────────────────────
# =============================================================================

echo -e "${CYAN}"
cat <<'BANNER'
 ██╗   ██╗ █████╗ ██╗   ██╗██╗   ██╗██████╗ ██████╗ ███████╗███████╗███████╗
 ██║   ██║██╔══██╗╚██╗ ██╔╝██║   ██║██╔══██╗██╔══██╗██╔════╝██╔════╝██╔════╝
 ██║   ██║███████║ ╚████╔╝ ██║   ██║██████╔╝██████╔╝█████╗  ███████╗███████╗
 ╚██╗ ██╔╝██╔══██║  ╚██╔╝  ██║   ██║██╔═══╝ ██╔══██╗██╔══╝  ╚════██║╚════██║
  ╚████╔╝ ██║  ██║   ██║   ╚██████╔╝██║     ██║  ██║███████╗███████║███████║
   ╚═══╝  ╚═╝  ╚═╝   ╚═╝    ╚═════╝ ╚═╝     ╚═╝  ╚═╝╚══════╝╚══════╝╚══════╝
BANNER
echo -e "${NC}"
echo "  VayuPress ${ENGINE_VERSION} — Publish at the Speed of Wind"
echo "  Domain: ${DOMAIN} | Branch: ${REPO_BRANCH}"
$DRY_RUN && warn "DRY-RUN MODE — no changes will be made"
$UPGRADE  && info "UPGRADE MODE — data and config will be preserved"
echo ""

# =============================================================================
# ── PRE-FLIGHT ────────────────────────────────────────────────────────────────
# =============================================================================

require_root

# Secrets: auto-generate strong random values when unset so a fresh install
# never silently runs with an empty/guessable key. Generated values are written
# to /etc/vayupress/env (mode 600) below, so they persist across reruns. On an
# upgrade with an existing env file we never regenerate — the old keys win.
gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

if [[ -z "$API_KEY" ]]; then
  API_KEY="${API_KEY:-$(gen_secret)}"
  warn "API_KEY was unset — generated a random one. It will be saved to /etc/vayupress/env (chmod 600)."
fi
if [[ -z "$MEILI_MASTER_KEY" ]]; then
  MEILI_MASTER_KEY="${MEILI_MASTER_KEY:-$(gen_secret)}"
  warn "MEILI_MASTER_KEY was unset — generated a random one. It will be saved to /etc/vayupress/env (chmod 600)."
fi

info "Pre-flight checks passed."

# =============================================================================
# ── SYSTEM PACKAGES ───────────────────────────────────────────────────────────
# =============================================================================

info "Installing system packages..."
run apt-get update -qq
run apt-get install -y -qq \
  build-essential git curl wget \
  nginx sqlite3 \
  certbot python3-certbot-nginx \
  fail2ban ufw \
  ca-certificates gnupg
ok "System packages installed."

# =============================================================================
# ── GO TOOLCHAIN ──────────────────────────────────────────────────────────────
# =============================================================================

# ── Go toolchain install ──────────────────────────────────────────────────────
#
# Strategy (patch-version-agnostic, no hardcoded hashes):
#   1. If `go` is already installed and meets the minimum major.minor, keep it.
#   2. Otherwise fetch the latest stable release + its SHA256 from the official
#      go.dev/dl JSON API — the same place Go publishes authoritative checksums.
#   3. Verify the downloaded tarball against that API-sourced SHA256.
#   4. Fallback: if the API is unreachable and no acceptable Go is installed,
#      abort with a clear message rather than installing an unverified binary.
#
# No SHA256 is ever hardcoded; no patch version is ever pinned.

_go_meets_minimum() {
  # Returns 0 (true) if the installed go version >= GO_MIN_MAJOR.GO_MIN_MINOR
  local ver
  ver=$(go version 2>/dev/null | awk '{print $3}') || return 1
  ver="${ver#go}"   # "1.24.3"
  local major minor
  major=$(echo "$ver" | cut -d. -f1)
  minor=$(echo "$ver" | cut -d. -f2)
  [[ "$major" -gt "$GO_MIN_MAJOR" ]] && return 0
  [[ "$major" -eq "$GO_MIN_MAJOR" && "$minor" -ge "$GO_MIN_MINOR" ]] && return 0
  return 1
}

_go_fetch_release_json() {
  # Fetch and cache the go.dev release JSON once per script run
  if [[ -z "${_GO_RELEASE_JSON:-}" ]]; then
    _GO_RELEASE_JSON=$(curl -fsSL --max-time 15 \
      "https://go.dev/dl/?mode=json" 2>/dev/null || true)
  fi
  echo "$_GO_RELEASE_JSON"
}

_go_latest_version() {
  # Returns the latest stable version string without the "go" prefix, e.g. "1.24.3"
  _go_fetch_release_json \
    | python3 -c "
import json,sys
data = sys.stdin.read().strip()
if not data: sys.exit(1)
releases = json.loads(data)
for r in releases:
    if r.get('stable'):
        print(r['version'].lstrip('go'))
        sys.exit(0)
sys.exit(1)
" 2>/dev/null || true
}

_go_sha256_for() {
  # Returns the official SHA256 for go<ver>.linux-amd64.tar.gz
  local ver="$1" target="go${1}.linux-amd64.tar.gz"
  _go_fetch_release_json \
    | python3 -c "
import json,sys
target, data = sys.argv[1], sys.stdin.read().strip()
if not data: sys.exit(1)
for r in json.loads(data):
    for f in r.get('files',[]):
        if f.get('filename') == target:
            print(f.get('sha256',''))
            sys.exit(0)
sys.exit(1)
" "$target" 2>/dev/null || true
}

# ── Decision: install or skip ─────────────────────────────────────────────────
if _go_meets_minimum; then
  INSTALLED_VER=$(go version | awk '{print $3}')
  ok "Go ${INSTALLED_VER} already satisfies >= ${GO_MIN_MAJOR}.${GO_MIN_MINOR} — skipping install."
else
  info "No suitable Go found. Resolving latest stable release from go.dev..."

  GO_VERSION=$(_go_latest_version)
  if [[ -z "$GO_VERSION" ]]; then
    die "Could not reach go.dev/dl to resolve the latest Go version. \
Check network connectivity and retry."
  fi
  info "Will install Go ${GO_VERSION}."

  GO_TARBALL="go${GO_VERSION}.linux-amd64.tar.gz"
  GO_URL="https://go.dev/dl/${GO_TARBALL}"

  info "Fetching authoritative SHA256 for ${GO_TARBALL} from go.dev..."
  GO_SHA256=$(_go_sha256_for "$GO_VERSION")
  if [[ -z "$GO_SHA256" ]]; then
    die "go.dev returned no SHA256 for ${GO_TARBALL}. \
The version may not yet be indexed — retry in a few minutes."
  fi

  info "Downloading ${GO_URL}..."
  run curl -fsSL --max-time 120 -o "/tmp/${GO_TARBALL}" "${GO_URL}"

  ACTUAL_SHA=$(sha256sum "/tmp/${GO_TARBALL}" | awk '{print $1}')
  if [[ "$ACTUAL_SHA" != "$GO_SHA256" ]]; then
    rm -f "/tmp/${GO_TARBALL}"
    die "SHA256 mismatch for ${GO_TARBALL}:
  expected (go.dev): ${GO_SHA256}
  actual   (disk):   ${ACTUAL_SHA}
The file may be corrupt or tampered — aborting."
  fi

  run rm -rf /usr/local/go
  run tar -C /usr/local -xzf "/tmp/${GO_TARBALL}"
  rm -f "/tmp/${GO_TARBALL}"
  export PATH="/usr/local/go/bin:${PATH}"
  ok "Go ${GO_VERSION} installed and verified."
fi

export PATH="/usr/local/go/bin:${PATH}"

# Persist Go PATH for all future shell sessions (sudo and non-sudo).
# This ensures manual commands like `go build` after deployment find the
# right Go binary rather than a stale system-package version.
if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
  echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
  chmod 644 /etc/profile.d/go.sh
fi

# =============================================================================
# ── DIRECTORY LAYOUT ─────────────────────────────────────────────────────────
# =============================================================================

info "Creating directory layout..."
run mkdir -p "${INSTALL_DIR}" "${DATA_DIR}" "${LOG_DIR}" "${CACHE_DIR}" "${STATIC_DIR}" "${BACKUP_DIR}"
run mkdir -p /etc/vayupress
ok "Directories created."

# =============================================================================
# ── CLONE / PULL SOURCE ───────────────────────────────────────────────────────
# =============================================================================

if [[ -d "${INSTALL_DIR}/.git" ]]; then
  info "Updating existing repository..."
  run git -C "${INSTALL_DIR}" fetch origin
  run git -C "${INSTALL_DIR}" checkout "${REPO_BRANCH}"
  run git -C "${INSTALL_DIR}" pull --ff-only origin "${REPO_BRANCH}"
  ok "Repository updated."
else
  info "Cloning repository..."
  run git clone --branch "${REPO_BRANCH}" --depth 1 "${REPO_URL}" "${INSTALL_DIR}"
  ok "Repository cloned."
fi

# =============================================================================
# ── BUILD ─────────────────────────────────────────────────────────────────────
# =============================================================================

info "Building VayuPress binary..."
run bash -c "cd '${INSTALL_DIR}' && \
  CGO_ENABLED=1 go build \
    -ldflags='-s -w -X main.Version=${ENGINE_VERSION}' \
    -o /usr/local/bin/vayupress \
    ./cmd/vayupress/"
ok "Binary built: /usr/local/bin/vayupress"

# =============================================================================
# ── RUNTIME CONFIGURATION ─────────────────────────────────────────────────────
# =============================================================================

info "Writing runtime environment..."
if $UPGRADE && [[ -f /etc/vayupress/env ]]; then
  warn "Upgrade: preserving existing /etc/vayupress/env — review for new fields."
else
  cat > /etc/vayupress/env <<ENV
DOMAIN=${DOMAIN}
PORT=8080
DB_PATH=${DB_PATH}
CACHE_DIR=${CACHE_DIR}
STATIC_DIR=${STATIC_DIR}
LOG_DIR=${LOG_DIR}
API_KEY=${API_KEY}
WORKER_COUNT=${WORKER_COUNT}
QUEUE_HARD_LIMIT=${QUEUE_HARD_LIMIT}
PLUGIN_MAX_CONCURRENT=${PLUGIN_MAX_CONCURRENT}
PLUGIN_TIMEOUT_MS=${PLUGIN_TIMEOUT_MS}
WAL_SIZE_THRESHOLD_MB=${WAL_SIZE_THRESHOLD_MB}
MAINTENANCE_MODE=${MAINTENANCE_MODE}
MEILISEARCH_URL=http://127.0.0.1:7700
MEILISEARCH_KEY=${MEILI_MASTER_KEY}
ENV
  run chmod 600 /etc/vayupress/env
  ok "Runtime config written to /etc/vayupress/env"
fi

# =============================================================================
# ── SYSTEMD SERVICE ───────────────────────────────────────────────────────────
# =============================================================================

info "Writing systemd service..."
cat > /etc/systemd/system/vayupress.service <<SYSTEMD
[Unit]
Description=VayuPress CMS Engine ${ENGINE_VERSION}
Documentation=https://github.com/johalputt/vayupress
After=network.target meilisearch.service
Wants=meilisearch.service

[Service]
Type=simple
User=www-data
Group=www-data
EnvironmentFile=/etc/vayupress/env
ExecStart=/usr/local/bin/vayupress
WorkingDirectory=${DATA_DIR}
Restart=always
RestartSec=5s
TimeoutStopSec=90
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
ReadWritePaths=${DATA_DIR} ${LOG_DIR} ${CACHE_DIR} ${STATIC_DIR} ${BACKUP_DIR}
StandardOutput=append:${LOG_DIR}/vayupress.log
StandardError=append:${LOG_DIR}/vayupress.error.log

[Install]
WantedBy=multi-user.target
SYSTEMD

run systemctl daemon-reload
ok "Systemd service written."

# =============================================================================
# ── MEILISEARCH ───────────────────────────────────────────────────────────────
# =============================================================================

if ! command -v meilisearch &>/dev/null; then
  info "Installing Meilisearch (MUSL static build — works on Ubuntu 20.04 / GLIBC 2.31)..."
  # The default install.meilisearch.com binary is dynamically linked and requires
  # GLIBC 2.32+, which Ubuntu 20.04 does not provide. The x86_64-linux-musl build
  # is statically linked and has no GLIBC dependency at all.
  MEILI_VER=$(curl -fsSL "https://api.github.com/repos/meilisearch/meilisearch/releases/latest" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])")
  MEILI_URL="https://github.com/meilisearch/meilisearch/releases/download/${MEILI_VER}/meilisearch-linux-amd64-musl"
  info "Downloading Meilisearch ${MEILI_VER} (musl)..."
  run curl -fsSL -o /tmp/meilisearch "${MEILI_URL}"
  run chmod +x /tmp/meilisearch
  run mv /tmp/meilisearch /usr/local/bin/meilisearch
fi

run useradd -r -s /bin/false meilisearch 2>/dev/null || true
run mkdir -p "${MEILI_DIR}/data" /var/log/meilisearch
run chown -R meilisearch:meilisearch "${MEILI_DIR}" /var/log/meilisearch

cat > /etc/systemd/system/meilisearch.service <<MEILI_SVC
[Unit]
Description=Meilisearch Search Engine
After=network.target

[Service]
User=meilisearch
Group=meilisearch
ExecStart=/usr/local/bin/meilisearch \
    --db-path ${MEILI_DIR}/data \
    --env production \
    --master-key ${MEILI_MASTER_KEY} \
    --http-addr 127.0.0.1:7700
Restart=always
RestartSec=5s
NoNewPrivileges=yes
PrivateTmp=yes
ReadWritePaths=${MEILI_DIR} /var/log/meilisearch
StandardOutput=append:/var/log/meilisearch/meilisearch.log
StandardError=append:/var/log/meilisearch/meilisearch.log

[Install]
WantedBy=multi-user.target
MEILI_SVC

run systemctl daemon-reload
run systemctl enable meilisearch
run systemctl restart meilisearch
ok "Meilisearch configured and started."

# =============================================================================
# ── FILE PERMISSIONS ──────────────────────────────────────────────────────────
# =============================================================================

run chown -R www-data:www-data "${DATA_DIR}" "${LOG_DIR}" "${CACHE_DIR}" "${STATIC_DIR}" "${BACKUP_DIR}"
ok "File permissions set."

# =============================================================================
# ── NGINX ─────────────────────────────────────────────────────────────────────
# =============================================================================

info "Writing Nginx config..."
# Rate limiting zones (rateLimit): general API and admin write endpoints
cat > /etc/nginx/conf.d/vayupress-ratelimit.conf <<RATELIMIT
limit_req_zone \$binary_remote_addr zone=vayupress_api:10m rate=30r/m;
limit_req_zone \$binary_remote_addr zone=vayupress_write:10m rate=10r/m;
limit_req_zone \$binary_remote_addr zone=vayupress_admin:10m rate=5r/m;
RATELIMIT

cat > /etc/nginx/sites-available/vayupress <<NGINX
server {
    listen 80;
    server_name ${DOMAIN} www.${DOMAIN};

    # ── Security headers (P9) ──────────────────────────────────────────────
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none';" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    add_header Permissions-Policy "camera=(), microphone=(), geolocation=(), payment=()" always;

    # ── CSRF token pass-through (enforced in application layer) ───────────
    # Application sets X-CSRF-Token on responses; clients must echo it.
    # See internal/auth — CSRFTokenMiddleware validates the csrf_token cookie.
    proxy_pass_header X-CSRF-Token;

    # ── Rate limiting (P9) ─────────────────────────────────────────────────
    location /api/v1/ {
        limit_req zone=vayupress_api burst=20 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 60s;
    }

    location /admin {
        limit_req zone=vayupress_admin burst=5 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 60s;
        proxy_connect_timeout 10s;
    }

    location /static/ {
        proxy_pass http://127.0.0.1:8080;
        add_header Cache-Control "public, immutable, max-age=31536000";
    }

    location /health {
        proxy_pass http://127.0.0.1:8080;
        proxy_buffering off;
    }

    client_max_body_size 50M;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript;
}
NGINX

run ln -sf /etc/nginx/sites-available/vayupress /etc/nginx/sites-enabled/vayupress
run rm -f /etc/nginx/sites-enabled/default
run nginx -t
run systemctl enable nginx
run systemctl reload nginx
ok "Nginx configured."

# =============================================================================
# ── TLS (CERTBOT) ─────────────────────────────────────────────────────────────
# =============================================================================

if [[ -n "$DOMAIN" && "$DOMAIN" != "localhost" ]]; then
  if [[ ! -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]]; then
    info "Obtaining Let's Encrypt certificate for ${DOMAIN}..."
    run certbot --nginx -d "${DOMAIN}" -d "www.${DOMAIN}" \
      --email "${EMAIL}" --agree-tos --non-interactive --redirect || \
      warn "Certbot failed — HTTP only. Run certbot manually after DNS propagates."
  else
    ok "TLS certificate already exists for ${DOMAIN}."
  fi
fi

# =============================================================================
# ── FIREWALL ─────────────────────────────────────────────────────────────────
# =============================================================================

info "Configuring UFW firewall..."
run ufw default deny incoming  2>/dev/null || true
run ufw default allow outgoing 2>/dev/null || true
run ufw allow 22/tcp  comment 'SSH'
run ufw allow 80/tcp  comment 'HTTP'
run ufw allow 443/tcp comment 'HTTPS'
run ufw --force enable
ok "Firewall configured."

# =============================================================================
# ── FAIL2BAN ─────────────────────────────────────────────────────────────────
# =============================================================================

cat > /etc/fail2ban/jail.d/vayupress.conf <<F2B
[nginx-http-auth]
enabled  = true
port     = http,https
logpath  = /var/log/nginx/error.log
maxretry = 5
bantime  = 3600

[nginx-limit-req]
enabled  = true
port     = http,https
logpath  = /var/log/nginx/error.log
maxretry = 10
bantime  = 600
F2B

run systemctl enable fail2ban
run systemctl restart fail2ban
ok "Fail2ban configured."

# =============================================================================
# ── LOGROTATE ─────────────────────────────────────────────────────────────────
# =============================================================================

cat > /etc/logrotate.d/vayupress <<LOGROTATE
${LOG_DIR}/*.log {
    daily
    rotate 30
    compress
    delaycompress
    missingok
    notifempty
}
LOGROTATE
ok "Log rotation configured."

# =============================================================================
# ── DATABASE BACKUP & INTEGRITY ───────────────────────────────────────────────
# =============================================================================

run mkdir -p "${BACKUP_DIR}"

if $UPGRADE && [[ -f "${DB_PATH}" ]]; then
  info "Running SQLite integrity check before upgrade..."
  INTEGRITY=$(sqlite3 "${DB_PATH}" "PRAGMA integrity_check;" 2>&1 || true)
  if [[ "$INTEGRITY" != "ok" ]]; then
    warn "PRAGMA integrity_check returned: ${INTEGRITY}"
    warn "Database may be corrupt — backup preserved, proceeding with caution."
  else
    ok "PRAGMA integrity_check: ok"
  fi

  BACKUP_FILE="${BACKUP_DIR}/vayupress-$(date +%Y%m%d-%H%M%S).db"
  info "Backing up database to ${BACKUP_FILE}..."
  run sqlite3 "${DB_PATH}" ".backup '${BACKUP_FILE}'"
  ok "Database backup complete: ${BACKUP_FILE}"

  # Prune backups older than BACKUP_RETAIN_DAYS
  info "Pruning backups older than ${BACKUP_RETAIN_DAYS} days..."
  find "${BACKUP_DIR}" -name "*.db" -mtime "+${BACKUP_RETAIN_DAYS}" -delete 2>/dev/null || true
  ok "Backup retention enforced (${BACKUP_RETAIN_DAYS} days)."
fi

# Storage quota advisory check
USED_GB=$(du -sg "${DATA_DIR}" 2>/dev/null | awk '{print $1}' || echo 0)
if [[ "${USED_GB}" -gt "${STORAGE_QUOTA_GB}" ]]; then
  warn "Storage usage ${USED_GB}GB exceeds quota ${STORAGE_QUOTA_GB}GB — consider archiving old data."
else
  ok "Storage usage: ${USED_GB}GB / ${STORAGE_QUOTA_GB}GB quota."
fi

# =============================================================================
# ── START / RESTART VAYUPRESS ─────────────────────────────────────────────────
# =============================================================================

info "Starting VayuPress service..."
run systemctl enable vayupress

if $UPGRADE; then
  run systemctl restart vayupress
  ok "VayuPress restarted (upgrade)."
else
  run systemctl start vayupress
  ok "VayuPress started (fresh install)."
fi

info "Waiting for health check..."
for i in $(seq 1 20); do
  if curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1; then
    ok "Health check passed after ${i}s."
    break
  fi
  sleep 1
  if [[ $i -eq 20 ]]; then
    warn "Health check did not respond in 20s. Check: journalctl -u vayupress -n 50"
  fi
done

# =============================================================================
# ── RESULT ────────────────────────────────────────────────────────────────────
# =============================================================================

echo ""
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  VayuPress ${ENGINE_VERSION} deployed successfully!${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
echo ""
echo "  URL:    http://${DOMAIN}:8080 (HTTPS if certbot succeeded)"
echo "  Health: curl http://127.0.0.1:8080/health"
echo "  Logs:   journalctl -u vayupress -f"
echo "  Config: /etc/vayupress/env"
echo "  Data:   ${DATA_DIR}"
echo ""
echo "  Next steps:"
echo "    1. Verify health:  curl -s http://127.0.0.1:8080/health | python3 -m json.tool"
echo "    2. Configure DNS to point ${DOMAIN} at this server's IP"
echo "    3. Re-run with no args after DNS propagates (certbot will obtain TLS)"
echo "    4. For upgrades: sudo ./deploy-vayupress.sh --upgrade"
echo ""
