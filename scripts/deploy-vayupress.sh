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
#  Network: Outbound HTTPS (GitHub, Go module proxy)
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

# Domain, contact email and API key are taken from the environment when set, so
# the whole install is one command with no file editing:
#   sudo DOMAIN=example.com EMAIL=you@example.com bash scripts/deploy-vayupress.sh
# If DOMAIN/EMAIL are left at the defaults and a terminal is attached, the script
# prompts for them. A strong API key is generated automatically when unset.
DOMAIN="${DOMAIN:-vayupress.com}"
EMAIL="${EMAIL:-admin@vayupress.com}"
WORKER_COUNT="${WORKER_COUNT:-4}"

# Directories
INSTALL_DIR="/opt/vayupress"
DATA_DIR="/var/lib/vayupress"
LOG_DIR="/var/log/vayupress"
CACHE_DIR="/var/cache/vayupress"
STATIC_DIR="/var/lib/vayupress/static"
BACKUP_DIR="/var/backups/vayupress"

# VayuPress runtime config (written to /etc/vayupress/env)
API_KEY="${API_KEY:-}"        # auto-generated below when empty (openssl rand -hex 32)
DB_PATH="${DATA_DIR}/vayupress.db"
QUEUE_HARD_LIMIT=1000
PLUGIN_MAX_CONCURRENT=8
PLUGIN_TIMEOUT_MS=2000
WAL_SIZE_THRESHOLD_MB=64
MAINTENANCE_MODE=false

# Backup & storage governance
BACKUP_RETAIN_DAYS=30         # days to keep database backups before pruning
STORAGE_QUOTA_GB=200          # alert threshold in GB for data directory

# Search is built in (VayuFind, ADR-0101) — no external search service.

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

# One-command friendliness: if the domain/email are still the placeholder values
# and we have an interactive terminal, ask for them. Non-interactive callers must
# pass DOMAIN=/EMAIL= in the environment (documented above).
if [[ "$DRY_RUN" != true && -t 0 ]]; then
  if [[ "$DOMAIN" == "vayupress.com" ]]; then
    read -rp "Your domain (e.g. example.com), or leave blank for localhost: " _d
    [[ -n "$_d" ]] && DOMAIN="$_d"
    [[ -z "$_d" ]] && DOMAIN="localhost"
  fi
  if [[ "$EMAIL" == "admin@vayupress.com" && "$DOMAIN" != "localhost" ]]; then
    read -rp "Contact email for Let's Encrypt (e.g. you@${DOMAIN}): " _e
    [[ -n "$_e" ]] && EMAIL="$_e"
  fi
fi
# Safety: refuse to install for the placeholder domain when we couldn't ask
# (e.g. `curl … | bash` with no DOMAIN=). Better to stop with instructions than
# silently deploy + request a certificate for someone else's domain.
if [[ "$DRY_RUN" != true && "$DOMAIN" == "vayupress.com" && ! -t 0 ]]; then
  die "No domain provided. Re-run with your domain, e.g.:
  curl -sSL <url> | sudo DOMAIN=example.com EMAIL=you@example.com bash
Or download it and run interactively so it can prompt:
  curl -sSLo install.sh <url> && sudo bash install.sh"
fi
# Recompute values derived from DOMAIN after any prompt/env override.
[[ "$EMAIL" == "admin@vayupress.com" && "$DOMAIN" != "localhost" ]] && EMAIL="admin@${DOMAIN}"
DB_PATH="${DATA_DIR}/vayupress.db"
# Generate a strong API key automatically when the operator didn't supply one, so
# there is no manual "set API_KEY" step.
if [[ -z "$API_KEY" ]]; then
  API_KEY="$(openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi

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
After=network.target

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
# Let the non-root service bind the privileged mail ports (25/110/143/465/587/
# 993/995) so VayuMail works without a proxy. Ambient caps apply even with
# NoNewPrivileges. This is the only elevated capability granted.
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
ReadWritePaths=${DATA_DIR} ${LOG_DIR} ${CACHE_DIR} ${STATIC_DIR} ${BACKUP_DIR}
StandardOutput=append:${LOG_DIR}/vayupress.log
StandardError=append:${LOG_DIR}/vayupress.error.log

[Install]
WantedBy=multi-user.target
SYSTEMD

run systemctl daemon-reload
ok "Systemd service written."

# =============================================================================
# ── FILE PERMISSIONS ──────────────────────────────────────────────────────────
# =============================================================================

run chown -R www-data:www-data "${DATA_DIR}" "${LOG_DIR}" "${CACHE_DIR}" "${BACKUP_DIR}"
# STATIC_DIR ownership is set after static files are copied below
ok "File permissions set."

# =============================================================================
# ── STATIC ASSETS ─────────────────────────────────────────────────────────────
# =============================================================================

info "Copying static assets to ${STATIC_DIR}..."
run mkdir -p "${STATIC_DIR}/css" "${STATIC_DIR}/js" "${STATIC_DIR}/fonts" "${STATIC_DIR}/img"
if [[ -d "${INSTALL_DIR}/static" ]]; then
  run cp -r "${INSTALL_DIR}/static/." "${STATIC_DIR}/"
  ok "Static assets copied."
else
  warn "No static/ directory found in ${INSTALL_DIR} — skipping asset copy."
fi
run chown -R www-data:www-data "${STATIC_DIR}"

# =============================================================================
# ── NGINX ─────────────────────────────────────────────────────────────────────
# =============================================================================

info "Writing Nginx config..."
# Rate limiting zones — must live in http{} context (conf.d/ is included there).
# The site config references these zones; they MUST be defined here before the
# site config is parsed, otherwise nginx will return 500/403 on every request.
cat > /etc/nginx/conf.d/vayupress-ratelimit.conf <<RATELIMIT
limit_req_zone \$binary_remote_addr zone=vayupress_api:10m   rate=30r/m;
limit_req_zone \$binary_remote_addr zone=vayupress_write:10m rate=10r/m;
limit_req_zone \$binary_remote_addr zone=vayupress_admin:10m rate=5r/m;
RATELIMIT

# HTTP server: ACME challenge (for certbot webroot renewals) + redirect to HTTPS.
# VayuPress proxies everything so certbot --nginx mode cannot serve the ACME
# challenge itself. We serve /.well-known/acme-challenge/ from the filesystem
# at ${CACHE_DIR} instead, then redirect everything else to HTTPS.
cat > /etc/nginx/sites-available/vayupress <<NGINX
# ── HTTP → HTTPS redirect ──────────────────────────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN} www.${DOMAIN};

    # Certbot webroot: served from filesystem, not proxied to VayuPress.
    # Required both for initial certificate issuance and for renewals.
    location ^~ /.well-known/acme-challenge/ {
        root ${CACHE_DIR};
        default_type text/plain;
        try_files \$uri =404;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}

# ── HTTPS main server ──────────────────────────────────────────────────────────
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name ${DOMAIN} www.${DOMAIN};

    # TLS — populated by certbot after first run
    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 1d;
    ssl_stapling        on;
    ssl_stapling_verify on;

    # Security headers (Prompt 9 / P9 compliance):
    # Content-Security-Policy is set per-request by VayuPress with a per-request nonce;
    # do NOT set a static CSP here — it would conflict with the nonce and break scripts.
    # X-XSS-Protection is a legacy IE header; included for defence-in-depth on older clients.
    # Rate limiting (rateLimit) is enforced via limit_req in the API and admin locations below.
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options    "nosniff" always;
    add_header X-Frame-Options           "SAMEORIGIN" always;
    add_header X-XSS-Protection          "1; mode=block" always;
    add_header Referrer-Policy           "strict-origin-when-cross-origin" always;
    add_header Permissions-Policy        "camera=(), microphone=(), geolocation=(), payment=()" always;
    # Content-Security-Policy: injected by VayuPress middleware with per-request nonce

    proxy_pass_header X-CSRF-Token;

    client_max_body_size 50M;
    proxy_read_timeout    60s;
    proxy_send_timeout    30s;
    proxy_connect_timeout 10s;

    gzip on;
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 6;
    gzip_types text/plain text/css text/xml text/javascript
               application/javascript application/json application/xml+rss;

    # ACME challenge (for renewals after TLS is live)
    location ^~ /.well-known/acme-challenge/ {
        root ${CACHE_DIR};
        default_type text/plain;
        try_files \$uri =404;
    }

    # WebSocket (VayuOS live monitoring)
    location /os/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade    \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host       \$host;
        proxy_read_timeout 3600s;
    }

    # Health check — no rate limit, no access log
    location /health {
        proxy_pass http://127.0.0.1:8080;
        proxy_buffering off;
        access_log off;
    }

    # API rate limiting
    location /api/v1/ {
        limit_req zone=vayupress_api burst=20 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }

    # Admin write rate limiting
    location /os/api/ {
        limit_req zone=vayupress_admin burst=5 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_pass_header X-CSRF-Token;
    }

    # Everything else → VayuPress
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_pass_header X-CSRF-Token;
    }

    access_log /var/log/nginx/vayupress-access.log;
    error_log  /var/log/nginx/vayupress-error.log warn;
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
    # Use webroot mode, NOT --nginx: VayuPress proxies everything to port 8080,
    # so certbot's --nginx plugin cannot serve the ACME challenge itself.
    # The nginx config already serves /.well-known/acme-challenge/ from CACHE_DIR.
    # We need nginx running first (HTTP-only config above), then certbot, then
    # nginx will pick up the ssl_certificate paths on the next reload.
    run mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
    run chown -R www-data:www-data "${CACHE_DIR}"

    # Reload nginx to activate the HTTP config (which has the ACME block)
    run nginx -t
    run systemctl reload nginx

    # One certificate covering the site AND the mail host (mail.<domain>), so
    # VayuMail is trusted by strict mobile clients (the Gmail app, K-9). The mail
    # SAN is best-effort: if mail.<domain> DNS isn't set yet, retry without it so
    # the web cert still succeeds.
    run certbot certonly --webroot \
      -w "${CACHE_DIR}" \
      -d "${DOMAIN}" -d "www.${DOMAIN}" -d "mail.${DOMAIN}" \
      --email "${EMAIL}" --agree-tos --non-interactive || \
    run certbot certonly --webroot \
      -w "${CACHE_DIR}" \
      -d "${DOMAIN}" -d "www.${DOMAIN}" \
      --email "${EMAIL}" --agree-tos --non-interactive || \
      warn "Certbot failed — site will run HTTP only until cert is obtained.
  After DNS propagates, re-run: sudo certbot certonly --webroot -w ${CACHE_DIR} -d ${DOMAIN} -d www.${DOMAIN} -d mail.${DOMAIN} --email ${EMAIL} --agree-tos --non-interactive
  Then: sudo nginx -t && sudo systemctl reload nginx"
  else
    ok "TLS certificate already exists for ${DOMAIN}."
  fi

  # Make the certificate readable by the non-root mail service and keep it fresh:
  # copy it to a service-owned directory now, and install a certbot deploy hook so
  # every renewal re-copies + restarts VayuPress. VayuMail auto-discovers this
  # path, so IMAP/POP3/SMTP present a trusted cert with no extra configuration.
  if [[ -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]]; then
    run mkdir -p "${DATA_DIR}/mailcert"
    run install -m 0644 "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" "${DATA_DIR}/mailcert/fullchain.pem"
    run install -m 0640 "/etc/letsencrypt/live/${DOMAIN}/privkey.pem"   "${DATA_DIR}/mailcert/privkey.pem"
    run chown -R www-data:www-data "${DATA_DIR}/mailcert"
    run mkdir -p /etc/letsencrypt/renewal-hooks/deploy
    cat > /etc/letsencrypt/renewal-hooks/deploy/vayupress-mailcert.sh <<HOOK
#!/usr/bin/env bash
# Re-copy the renewed certificate to the VayuMail-readable location and restart.
set -e
install -m 0644 "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" "${DATA_DIR}/mailcert/fullchain.pem"
install -m 0640 "/etc/letsencrypt/live/${DOMAIN}/privkey.pem"   "${DATA_DIR}/mailcert/privkey.pem"
chown -R www-data:www-data "${DATA_DIR}/mailcert"
systemctl try-restart vayupress 2>/dev/null || true
HOOK
    run chmod +x /etc/letsencrypt/renewal-hooks/deploy/vayupress-mailcert.sh
    ok "Mail certificate installed for mail.${DOMAIN} (auto-renews)."
  fi
fi

run nginx -t
run systemctl reload nginx
ok "Nginx reloaded."

# =============================================================================
# ── FIREWALL ─────────────────────────────────────────────────────────────────
# =============================================================================

info "Configuring UFW firewall..."
run ufw default deny incoming  2>/dev/null || true
run ufw default allow outgoing 2>/dev/null || true
run ufw allow 22/tcp  comment 'SSH'
run ufw allow 80/tcp  comment 'HTTP'
run ufw allow 443/tcp comment 'HTTPS'
# VayuMail ports so mail apps (and other servers) can reach the built-in mail
# server: SMTP(25), submission(465/587), IMAP(143/993), POP3(110/995).
for p in 25 110 143 465 587 993 995; do
  run ufw allow "${p}/tcp" comment 'VayuMail'
done
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
echo "  Admin:  https://${DOMAIN}/os/login"
echo "  Health: curl http://127.0.0.1:8080/health"
echo "  Logs:   journalctl -u vayupress -f"
echo "  Config: /etc/vayupress/env"
echo "  Data:   ${DATA_DIR}"
echo ""

# Surface the auto-created administrator so the operator can sign in immediately.
# On first login VayuPress forces a password change. Everything else is done from
# the VayuOS web console — no more terminal needed.
CRED_FILE="${DATA_DIR}/initial-admin.txt"
if [[ -f "$CRED_FILE" ]]; then
  echo -e "${CYAN}  ── Sign in to VayuOS ─────────────────────────────────────${NC}"
  sed 's/^/  /' "$CRED_FILE"
  echo "  (You'll be asked to set a new password on first login.)"
  echo ""
elif ! $UPGRADE; then
  echo "  Admin credentials will be written to ${CRED_FILE} on first start"
  echo "  and printed in the log: journalctl -u vayupress | grep -A4 'Default admin'"
  echo ""
fi

echo "  From here, do EVERYTHING from the web console at https://${DOMAIN}/os —"
echo "  posts, themes, mail accounts, updates, backups. No terminal required."
echo ""
echo "  DNS to set (A records → this server's IP):  ${DOMAIN}, www.${DOMAIN}, mail.${DOMAIN}"
echo "  Upgrades later: one click in VayuOS → Update & Backup (or --upgrade here)."
echo ""
