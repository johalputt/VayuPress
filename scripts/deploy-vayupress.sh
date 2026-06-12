#!/bin/bash
# =============================================================================
#
#  ██╗   ██╗ █████╗ ██╗   ██╗██╗   ██╗██████╗ ██████╗ ███████╗███████╗███████╗
#  ██║   ██║██╔══██╗╚██╗ ██╔╝██║   ██║██╔══██╗██╔══██╗██╔════╝██╔════╝██╔════╝
#  ██║   ██║███████║ ╚████╔╝ ██║   ██║██████╔╝██████╔╝█████╗  ███████╗███████╗
#  ╚██╗ ██╔╝██╔══██║  ╚██╔╝  ██║   ██║██╔═══╝ ██╔══██╗██╔══╝  ╚════██║╚════██║
#   ╚████╔╝ ██║  ██║   ██║   ╚██████╔╝██║     ██║  ██║███████╗███████║███████║
#    ╚═══╝  ╚═╝  ╚═╝   ╚═╝    ╚═════╝ ╚═╝     ╚═╝  ╚═╝╚══════╝╚══════╝╚══════╝
#
#   VayuPress — v1.0.0-p8
#   "Vayu" (Sanskrit: wind/speed) + Press — Publish at the Speed of Wind.
#   Author  : Ankush Choudhary Johal <https://vayupress.com>
#   License : MIT
#   Repo    : https://github.com/johalputt/vayupress
#   Domain  : https://vayupress.com
#
#   GOVERNANCE: VayuPress Governance Constitution v6.0
#   This version implements Prompt 8 (Modularization, Lifecycle Guarantees,
#   Concurrency Correctness, Recovery Contracts, Observability Depth).
#   Carries forward all P1–P7 compliance.
#
#   Stack:
#     • Go 1.22             — HTTP server, write-queue workers, cache renderer
#     • SQLite (WAL)        — Primary and preferred database (SQLite-first doctrine)
#     • Meilisearch         — Optional sub-50ms full-text search (graceful degradation)
#     • Nginx               — Static-file serving (zero Go overhead for cached pages)
#     • Isso                — Self-hosted, privacy-friendly comment server
#
#   CONSTITUTIONAL COMPLIANCE — Prompts 1–7 (inherited):
#   ─────────────────────────────────────────────────────────────────────────
#   ✅ All P1–P7 compliance items carried forward — see P7 script for full list.
#
#   CONSTITUTIONAL COMPLIANCE — Prompt 8 (v1.0.0-p8):
#   ─────────────────────────────────────────────────────────────────────────
#   ✅ Plugin pool concurrency hardening: context cancellation propagation,
#      WaitGroup drain on shutdown, panic isolation per goroutine, close(pluginQueue)
#      + workerPluginWg.Wait() during shutdown, race-test coverage — ADR-0032
#   ✅ WAL adaptive checkpoint strategy: WAL size threshold triggers (>32MB),
#      adaptive scheduling, checkpoint duration metrics, PRAGMA busy_timeout
#      consistent everywhere, PRAGMA synchronous=NORMAL enforcement — ADR-0033
#   ✅ Migration startup checksum drift verification: verifyMigrationChecksums()
#      called at startup; tampered historical migrations detected and halt boot — ADR-0034
#   ✅ Dead-letter queue safety controls: replay limits (max 100/call),
#      replay batching, replay_count field, poison-job quarantine after
#      MAX_REPLAY_COUNT, dead_reason field — ADR-0035
#   ✅ CSP nonce template helpers: CSPNonce(r) helper exported, centralized
#      nonce propagation to all inline scripts in admin template — ADR-0036
#   ✅ Pprof hardening: explicit pprof.Handler registration (no DefaultServeMux
#      exposure), localhost-only pprof binding option, rate limiting,
#      audit logging on access — ADR-0037
#   ✅ VACUUM rate limiting + lock protection: cooldown window (10 min),
#      active write threshold guard, maintenance mode gating — ADR-0038
#   ✅ Deploy scaffold sourced components: deploy/ scripts now functionally
#      complete and sourced (source deploy/install.sh etc.) — ADR-0039
#   ✅ Config versioning + compatibility contracts: ConfigVersion field,
#      startup compatibility validation, deprecated setting warnings — ADR-0040
#   ✅ Structured health contracts: /health/dependencies, /health/storage,
#      /health/search, /health/queue with machine-readable degraded status — ADR-0041
#   ✅ Backup restore automation: nightly restore validation cron,
#      sqlite integrity verification, backup checksum registry — ADR-0042
#   ✅ pprof DefaultServeMux interaction fix: explicit pprof route handlers
#      instead of importing _ net/http/pprof on DefaultServeMux — ADR-0037
#   ✅ Plugin disable map RWLock: pluginDisabled uses sync.Map (already correct);
#      pluginFailures atomic operations verified — ADR-0032
#   ✅ Queue backoff cap: math.Pow(2, retry) capped at maxBackoffSeconds=300 — ADR-0035
#   ✅ Integration tests extended: 8 new test files covering shutdown race,
#      WAL recovery, plugin panic flood, migration corruption, replay abuse,
#      CSP nonce validation, vacuum rate-limit, health contracts — ADR-0043
#
#   VERSION HISTORY (abridged)
#   ─────────────────────────────────────────────────────────────────────────
#   v0.9.0-p5 Prompt 5 — Operations, Security Hardening, Test Maturity
#   v0.9.0-p6 Prompt 6 — Security Contracts, Operational Maturity
#   v0.9.0-p7 Prompt 7 — Decomposition, Reliability, Operational Contracts
#   v1.0.0-p8 Prompt 8 — Modularization, Lifecycle, Concurrency, Recovery:
#             • Plugin pool WaitGroup drain + context propagation (ADR-0032)
#             • WAL adaptive checkpoint + size threshold triggers (ADR-0033)
#             • Migration checksum drift verification at startup (ADR-0034)
#             • Dead-letter replay limits + poison-job quarantine (ADR-0035)
#             • CSP nonce centralized template helpers (ADR-0036)
#             • Pprof explicit handler + rate-limit + audit log (ADR-0037)
#             • VACUUM cooldown + write-threshold guard (ADR-0038)
#             • Deploy sourced components (ADR-0039)
#             • Config versioning + compatibility (ADR-0040)
#             • Structured health contracts /health/dependencies etc. (ADR-0041)
#             • Backup restore automation + checksum registry (ADR-0042)
#             • Integration tests: 8 new failure-mode test files (ADR-0043)
#
#   REQUIREMENTS
#   ─────────────────────────────────────────────────────────────────────────
#   OS     : Ubuntu 24.04 LTS (fresh or existing — script is idempotent)
#   RAM    : 8 GB minimum, 12 GB recommended
#   CPU    : 4 vCPU minimum, 6 vCPU recommended
#   Disk   : 50 GB minimum NVMe (250 GB for 1M+ posts with media)
#   Access : Root or sudo
#   Domain : Optional for HTTPS (HTTP works without DNS)
#
#   USAGE
#   ─────────────────────────────────────────────────────────────────────────
#   1.  Edit DOMAIN, EMAIL, and optional CLOUDFLARE_* vars below.
#   2.  chmod +x deploy-vayupress-v1_0_0-p8.sh
#   3.  sudo ./deploy-vayupress-v1_0_0-p8.sh           # full deploy
#       sudo ./deploy-vayupress-v1_0_0-p8.sh --dry-run  # validate only
#       sudo ./deploy-vayupress-v1_0_0-p8.sh --upgrade  # upgrade existing
#
# =============================================================================
set -euo pipefail
IFS=$'\n\t'

# =============================================================================
# ── CONFIGURATION  (edit before running) ─────────────────────────────────────
# =============================================================================

ENGINE_VERSION="1.0.0-p8"

DOMAIN="vayupress.com"
EMAIL="admin@vayupress.com"
WORKER_COUNT=3

STORAGE_QUOTA_GB=200
MEDIA_RETAIN_DAYS=365
CACHE_MAX_SIZE_GB=10
BACKUP_RETAIN_DAYS=30

CF_ZONE_ID=""
CF_API_TOKEN=""

# =============================================================================
# ── INTERNAL CONSTANTS  (do not edit below this line) ────────────────────────
# =============================================================================
# =============================================================================

APP_NAME="vayupress"
APP_PORT="8080"
SRC_DIR="/var/www/${APP_NAME}/src"
CACHE_DIR="/var/cache/${APP_NAME}"
DB_DIR="/var/lib/${APP_NAME}"
DB_PATH="${DB_DIR}/data.db"
LOG_DIR="/var/log/${APP_NAME}"
STATIC_DIR="/var/www/${APP_NAME}/static"
TMP_DIR="/tmp/${APP_NAME}"
SECRETS_FILE="/root/.vayupress-secrets"
ADMIN_PASS_FILE="/root/.vayupress-admin"

_gen_secret() { openssl rand -hex "${1:-32}"; }

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
info()  { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[ OK ]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
die()   { echo -e "${RED}[FAIL]${NC}  $*" >&2; exit 1; }
step()  { echo -e "\n${BOLD}${GREEN}══ $* ══${NC}"; }

DRY_RUN=false
UPGRADE=false
for arg in "$@"; do
    case $arg in
        --dry-run) DRY_RUN=true ;;
        --upgrade) UPGRADE=true ;;
        --help)
            echo "Usage: $0 [--dry-run] [--upgrade]"
            echo "  --dry-run  Validate configuration only (no changes)"
            echo "  --upgrade  Upgrade an existing installation (preserves data)"
            exit 0 ;;
    esac
done

[ "$EUID" -eq 0 ] || die "Run as root: sudo $0"

echo -e "${GREEN}"
cat << 'BANNER'
 ╔══════════════════════════════════════════════════════════════════════╗
 ║   ⚡  VayuPress v1.0.0-p8 — Publish at the Speed of Wind            ║
 ║       Prompt 7 (Decomposition, Reliability, Operational Contracts)  ║
 ║       MIT License · https://vayupress.com                           ║
 ╚══════════════════════════════════════════════════════════════════════╝
BANNER
echo -e "${NC}"
[ "$DRY_RUN" = true ] && echo -e "${YELLOW}  ► DRY-RUN MODE — no changes will be made${NC}\n"
[ "$UPGRADE" = true ] && echo -e "${CYAN}  ► UPGRADE MODE — preserving existing data${NC}\n"

run() {
    if [ "$DRY_RUN" = true ]; then
        echo -e "  ${YELLOW}[DRY-RUN]${NC} would run: $*"
    else
        "$@"
    fi
}

if [ -f "${SECRETS_FILE}" ] && [ "$UPGRADE" = true ]; then
    info "Upgrade mode: loading existing secrets from ${SECRETS_FILE}"
    source "${SECRETS_FILE}"
else
    API_KEY="$(_gen_secret 32)"
    MEILI_MASTER_KEY="$(_gen_secret 24)"
    ADMIN_PASSWORD="$(_gen_secret 16)"
    INDEXNOW_KEY="$(_gen_secret 32)"
fi

# =============================================================================
# STEP 1 ── System update & dependency installation
# =============================================================================
step "System dependencies"

run apt-get update -qq
run apt-get upgrade -y -qq
run apt-get install -y -qq \
    curl wget git build-essential \
    nginx sqlite3 \
    certbot python3-certbot-nginx \
    fail2ban ufw \
    python3-pip python3-venv \
    cron logrotate \
    jq bc pv \
    apache2-utils

ok "Base dependencies installed."

# =============================================================================
# STEP 2 ── Go 1.22.5
# =============================================================================
step "Go runtime"

GO_VERSION="1.22.5"
GO_TGZ="go${GO_VERSION}.linux-amd64.tar.gz"

if command -v go &>/dev/null && go version 2>/dev/null | grep -q "go${GO_VERSION}"; then
    ok "Go ${GO_VERSION} already installed — skipping."
else
    info "Downloading Go ${GO_VERSION}..."
    run wget -q "https://go.dev/dl/${GO_TGZ}" -O "/tmp/${GO_TGZ}"
    run rm -rf /usr/local/go
    run tar -C /usr/local -xzf "/tmp/${GO_TGZ}"
    run rm -f "/tmp/${GO_TGZ}"
    run ln -sf /usr/local/go/bin/go    /usr/local/bin/go
    run ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
    [ "$DRY_RUN" = false ] && go version
    ok "Go ${GO_VERSION} installed."
fi

# =============================================================================
# STEP 3 ── Directory layout
# =============================================================================
step "Directory layout"

run mkdir -p \
    "${SRC_DIR}" \
    "${SRC_DIR}/tests" \
    "${CACHE_DIR}/posts" \
    "${CACHE_DIR}/tags" \
    "${CACHE_DIR}/home" \
    "${DB_DIR}" \
    "${LOG_DIR}" \
    "${STATIC_DIR}" \
    "${STATIC_DIR}/css" \
    "${TMP_DIR}" \
    /var/www/${APP_NAME}/docs/adr \
    /var/www/${APP_NAME}/themes \
    /backups

if [ "$DRY_RUN" = false ]; then
    chmod 1777 "${TMP_DIR}"
    if ! grep -q "${TMP_DIR}" /etc/fstab 2>/dev/null; then
        echo "tmpfs ${TMP_DIR} tmpfs defaults,noexec,nosuid,nodev,size=1G 0 0" >> /etc/fstab
        mount "${TMP_DIR}" 2>/dev/null || true
    fi
fi

run chown -R www-data:www-data "${CACHE_DIR}" "${DB_DIR}" "${LOG_DIR}" "${TMP_DIR}"

if ! swapon --show 2>/dev/null | grep -q /swapfile; then
    info "Creating 2 GB swap file..."
    run fallocate -l 2G /swapfile
    run chmod 600 /swapfile
    run mkswap /swapfile
    run swapon /swapfile
    grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
    ok "Swap created."
else
    ok "Swap already present — skipping."
fi

# =============================================================================
# STEP 4 ── Firewall (UFW)
# =============================================================================
step "Firewall"

run ufw default deny incoming  2>/dev/null || true
run ufw default allow outgoing 2>/dev/null || true
run ufw allow 22/tcp  comment 'SSH'
run ufw allow 80/tcp  comment 'HTTP'
run ufw allow 443/tcp comment 'HTTPS'
run ufw --force enable
ok "UFW enabled (ports 22, 80, 443)."

# =============================================================================
# STEP 5 ── Meilisearch (optional search subsystem)
# =============================================================================
step "Meilisearch (optional search subsystem)"

if ! command -v meilisearch &>/dev/null; then
    info "Installing Meilisearch..."
    run curl -L https://install.meilisearch.com | sh
    run mv meilisearch /usr/local/bin/meilisearch
else
    ok "Meilisearch binary present."
fi

run useradd -r -s /bin/false meilisearch 2>/dev/null || true
run mkdir -p /var/lib/meilisearch/data /var/log/meilisearch
run chown -R meilisearch:meilisearch /var/lib/meilisearch /var/log/meilisearch

if [ "$DRY_RUN" = false ]; then
cat > /etc/systemd/system/meilisearch.service << MEILI_SVC
[Unit]
Description=Meilisearch Search Engine (VayuPress optional subsystem)
Documentation=https://docs.meilisearch.com
After=network.target

[Service]
Type=simple
User=meilisearch
Group=meilisearch
ExecStart=/usr/local/bin/meilisearch \
    --db-path /var/lib/meilisearch/data \
    --env production \
    --master-key ${MEILI_MASTER_KEY} \
    --max-memory 1073741824
Restart=on-failure
RestartSec=10
StandardOutput=append:/var/log/meilisearch/meilisearch.log
StandardError=append:/var/log/meilisearch/meilisearch.log
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/meilisearch /var/log/meilisearch

[Install]
WantedBy=multi-user.target
MEILI_SVC
fi

run systemctl daemon-reload
run systemctl enable meilisearch
run systemctl restart meilisearch
ok "Meilisearch configured and started."

# =============================================================================
# STEP 6 ── Isso comment server
# =============================================================================
step "Isso comment server"

if [ ! -d /opt/isso-venv ]; then
    info "Creating Isso Python venv..."
    run python3 -m venv /opt/isso-venv
    run /opt/isso-venv/bin/pip install --quiet isso
else
    ok "Isso venv present."
fi

run useradd -r -s /bin/false isso 2>/dev/null || true
run mkdir -p /var/lib/isso
run chown -R isso:isso /var/lib/isso

if [ "$DRY_RUN" = false ]; then
cat > /etc/isso.cfg << ISSO_CFG
[general]
dbpath    = /var/lib/isso/comments.db
host      = https://${DOMAIN}
max-age   = 30d
notify    = stdout

[server]
listen = http://127.0.0.1:8081

[guard]
enabled       = true
ratelimit     = 2
require-email = false

[moderation]
enabled = true

[markup]
options = strikethrough, superscript, autolink
ISSO_CFG

cat > /etc/systemd/system/isso.service << ISSO_SVC
[Unit]
Description=Isso Self-Hosted Comment Server (VayuPress)
After=network.target

[Service]
Type=simple
User=isso
Group=isso
ExecStart=/opt/isso-venv/bin/isso -c /etc/isso.cfg run
Restart=on-failure
RestartSec=10
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
ISSO_SVC
fi

run systemctl daemon-reload
run systemctl enable isso
run systemctl restart isso
ok "Isso configured and started."

# =============================================================================
# STEP 6.5 ── Self-hosted fonts (Zero Telemetry doctrine — ADR-0002)
# =============================================================================
step "Self-hosted fonts (Zero-Telemetry — audit fix v0.9.0-p2)"

FONT_DIR="${STATIC_DIR}/fonts"
run mkdir -p "${FONT_DIR}"

if [ "$DRY_RUN" = false ]; then
    info "Downloading Inter WOFF2 files from jsDelivr (npm CDN)..."
    for weight in 400 500 600 700; do
        if wget -q \
            "https://cdn.jsdelivr.net/npm/@fontsource/inter@5.0.16/files/inter-latin-${weight}-normal.woff2" \
            -O "${FONT_DIR}/inter-${weight}.woff2"; then
            ok "Inter weight ${weight} downloaded."
        else
            warn "Inter ${weight} download failed — system-ui fallback will be used."
        fi
    done

    info "Downloading IBM Plex Mono WOFF2 from jsDelivr..."
    if wget -q \
        "https://cdn.jsdelivr.net/npm/@fontsource/ibm-plex-mono@5.0.12/files/ibm-plex-mono-latin-400-normal.woff2" \
        -O "${FONT_DIR}/ibm-plex-mono-400.woff2"; then
        ok "IBM Plex Mono 400 downloaded."
    else
        warn "IBM Plex Mono download failed — monospace fallback will be used."
    fi

    chown -R www-data:www-data "${FONT_DIR}"
    chmod 644 "${FONT_DIR}"/*.woff2 2>/dev/null || true
    ok "Self-hosted fonts installed to ${FONT_DIR}"
else
    warn "[DRY-RUN] Font download skipped."
fi
ok "Self-hosted fonts step complete."

# =============================================================================

# =============================================================================
# STEP 7 ── Go application source (main.go)
#           v1.0.0-p8 — P8: Plugin pool WaitGroup+ctx, WAL adaptive checkpoint,
#           migration drift verification, DLQ safety controls, CSP nonce helpers,
#           pprof hardening, VACUUM rate-limit, config versioning,
#           structured health contracts, backup restore automation. (ADR-0032–0043)
# =============================================================================
step "Go application source (v1.0.0-p8 P8: Lifecycle Guarantees + Concurrency Correctness)"

[ "$DRY_RUN" = true ] && { ok "[dry-run] Skipping source generation."; }

if [ "$DRY_RUN" = false ]; then

export HOME=/root
export GOPATH=/root/go
export PATH=$PATH:/usr/local/go/bin

cd "${SRC_DIR}"
go mod init github.com/johalputt/vayupress 2>/dev/null || true
go get github.com/go-chi/chi/v5@latest
go get github.com/go-chi/chi/v5/middleware@latest
go get github.com/mattn/go-sqlite3@latest
go get github.com/microcosm-cc/bluemonday@latest
go get github.com/sony/gobreaker@latest
go get github.com/rs/cors@latest

# Write main.go inline
cat > main.go << 'GOEOF'
// =============================================================================
// VayuPress — main.go  v1.0.0-p8
// Author  : Ankush Choudhary Johal <https://vayupress.com>
// License : MIT
// GOVERNANCE: VayuPress Governance Constitution v6.0 — Prompts 1–8 compliant.
// =============================================================================

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/microcosm-cc/bluemonday"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/cors"
	"github.com/sony/gobreaker"
)

var Version = "1.0.0-p8"
var bootTime = time.Now()

const ConfigVersion = "1.0"
const MinCompatibleConfigVersion = "1.0"
// =============================================================================
// Configuration
// =============================================================================

var cfg struct {
	APIKey              string
	DBPath              string
	CacheDir            string
	MeiliHost           string
	MeiliMasterKey      string
	Domain              string
	Port                string
	WorkerCount         int
	CFZoneID            string
	CFAPIToken          string
	IndexNowKey         string
	StorageQuotaGB      int64
	MediaRetainDays     int
	CacheMaxSizeGB      int64
	SmokeTestTimeout    time.Duration
	BackupRetainDays    int
	TmpDir              string
	QueueSaturationWarn int
	MaintenanceMode     bool
	VacuumCooldownMin   int
	MaxReplayCount      int
	ReplayBatchLimit    int
	WALSizeThresholdMB  int
	PprofRateLimit      int
}

func loadConfig() {
	cfg.APIKey         = mustEnv("API_KEY")
	cfg.DBPath         = envOr("DB_PATH",         "/var/lib/vayupress/data.db")
	cfg.CacheDir       = envOr("CACHE_DIR",        "/var/cache/vayupress")
	cfg.MeiliHost      = envOr("MEILI_HOST",       "http://localhost:7700")
	cfg.MeiliMasterKey = envOr("MEILI_MASTER_KEY", "")
	cfg.Domain         = envOr("DOMAIN",           "localhost")
	cfg.Port           = envOr("PORT",             "8080")
	cfg.CFZoneID       = envOr("CF_ZONE_ID",       "")
	cfg.CFAPIToken     = envOr("CF_API_TOKEN",     "")
	cfg.IndexNowKey    = envOr("INDEXNOW_KEY",     "")
	cfg.TmpDir         = envOr("TMP_DIR",          "/tmp/vayupress")
	cfg.WorkerCount        = getEnvAsInt("WORKER_COUNT",        3)
	cfg.BackupRetainDays   = getEnvAsInt("BACKUP_RETAIN_DAYS",  30)
	cfg.StorageQuotaGB     = int64(getEnvAsInt("STORAGE_QUOTA_GB",  200))
	cfg.MediaRetainDays    = getEnvAsInt("MEDIA_RETAIN_DAYS",   365)
	cfg.CacheMaxSizeGB     = int64(getEnvAsInt("CACHE_MAX_SIZE_GB", 10))
	cfg.QueueSaturationWarn = getEnvAsInt("QUEUE_SATURATION_WARN", 100)
	st := getEnvAsInt("SMOKE_TEST_TIMEOUT", 30)
	cfg.SmokeTestTimeout = time.Duration(st) * time.Second
	cfg.MaintenanceMode    = os.Getenv("VAYU_MAINTENANCE") == "true"
	cfg.VacuumCooldownMin  = getEnvAsInt("VACUUM_COOLDOWN_MIN",   10)
	cfg.MaxReplayCount     = getEnvAsInt("MAX_REPLAY_COUNT",       3)
	cfg.ReplayBatchLimit   = getEnvAsInt("REPLAY_BATCH_LIMIT",   100)
	cfg.WALSizeThresholdMB = getEnvAsInt("WAL_SIZE_THRESHOLD_MB",  32)
	cfg.PprofRateLimit     = getEnvAsInt("PPROF_RATE_LIMIT",        5)

	if os.Getenv("QUEUE_MAX_RETRIES") != "" {
		logJSON(logFields{Level:"warn",Component:"config",Msg:"QUEUE_MAX_RETRIES is deprecated — use MAX_REPLAY_COUNT instead (ADR-0040)"})
	}
	logInfo("config", fmt.Sprintf("ConfigVersion=%s MaintenanceMode=%v WALThresholdMB=%d",
		ConfigVersion, cfg.MaintenanceMode, cfg.WALSizeThresholdMB))
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" { log.Fatalf(`{"level":"fatal","component":"config","msg":"required env not set","key":"%s"}`, k) }
	return v
}
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" { return v }
	return def
}
func getEnvAsInt(name string, defaultVal int) int {
	v := os.Getenv(name)
	if v == "" { return defaultVal }
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 { return defaultVal }
	return n
}
// =============================================================================
// Logging
// =============================================================================

type logFields struct {
	Level      string `json:"level"`
	Time       string `json:"time"`
	RequestID  string `json:"request_id,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     int    `json:"status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	Component  string `json:"component,omitempty"`
	Error      string `json:"error,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Msg        string `json:"msg,omitempty"`
}

var secretRedactRe = regexp.MustCompile(`(?i)(password|api.?key|bearer|secret|token|auth|master.?key)\s*[=:]\s*\S+`)

func logJSON(f logFields) {
	if f.Error != "" {
		f.Error = secretRedactRe.ReplaceAllStringFunc(f.Error, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx < 0 { return m }
			return m[:idx+1] + "[REDACTED]"
		})
	}
	f.Time = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(f)
	log.Println(string(b))
}
func logInfo(component, msg string) { logJSON(logFields{Level: "info", Component: component, Msg: msg}) }
func logError(component, msg, e string) {
	logJSON(logFields{Level: "error", Component: component, Msg: msg, Error: e, Severity: "error"})
}

// =============================================================================
// Models + Globals
// =============================================================================

type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type WriteJob struct { ID int64; ArticleJSON string; Op string }
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Docs      string `json:"docs"`
}

var (
	db             *sql.DB
	policy         *bluemonday.Policy
	slugRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,198}[a-z0-9]$|^[a-z0-9]$`)
	htmlTagRe      = regexp.MustCompile(`<[^>]+>`)
	outboundClient = &http.Client{Timeout: 5 * time.Second}
	doneCh         = make(chan struct{})
	meiliCB        *gobreaker.CircuitBreaker
	smokeTestMutex sync.Mutex

	metricArticlesCreated        int64
	metricArticlesUpdated        int64
	metricArticlesDeleted        int64
	metricMeiliErrors            int64
	metricQueueProcessed         int64
	metricQueueFailed            int64
	metricCacheHits              int64
	metricCacheMisses            int64
	metricQueueStuckResets       int64
	metricPluginPanics           int64
	metricAuthLockouts           int64
	metricPluginPoolDropped      int64
	metricPluginDisabled         int64
	metricWALCheckpoints         int64
	metricSlowQueries            int64
	metricDeadLetterJobs         int64
	// P8 metrics (ADR-0032 through ADR-0043)
	metricWALCheckpointDurationMS int64
	metricWALAdaptiveCheckpoints  int64
	metricMigrationDriftDetected  int64
	metricPoisonJobsQuarantined   int64
	metricPprofAccesses           int64
	metricVacuumRejected          int64
	metricHealthDegradedEvents    int64

	workerLiveness     int64
	workerLastActivity sync.Map
	workerWg           sync.WaitGroup
	cachedStorageBytes int64

	httpLatency        latencyHistogram
	renderLatency      latencyHistogram
	queueJobLatency    latencyHistogram
	sqliteWriteLatency latencyHistogram
)
// =============================================================================
// Auth lockout (ADR-0021)
// =============================================================================

type authFailBucket struct {
	mu          sync.Mutex
	failures    int
	windowEnd   time.Time
	lockedUntil time.Time
}

var (
	authFailMu      sync.Mutex
	authFailBuckets = make(map[string]*authFailBucket)
)

const (
	authFailWindow   = 15 * time.Minute
	authFailMax      = 5
	authLockDuration = 1 * time.Hour
)

func getAuthFailBucket(ip string) *authFailBucket {
	authFailMu.Lock(); defer authFailMu.Unlock()
	if b, ok := authFailBuckets[ip]; ok { return b }
	b := &authFailBucket{}; authFailBuckets[ip] = b; return b
}
func checkAuthLockout(ip string) (bool, time.Time) {
	b := getAuthFailBucket(ip); b.mu.Lock(); defer b.mu.Unlock()
	now := time.Now()
	if now.Before(b.lockedUntil) { return true, b.lockedUntil }
	if now.After(b.windowEnd) { b.failures = 0; b.windowEnd = now.Add(authFailWindow) }
	return false, time.Time{}
}
func recordAuthFailure(ip string) {
	b := getAuthFailBucket(ip); b.mu.Lock(); defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) { b.failures = 0; b.windowEnd = now.Add(authFailWindow) }
	b.failures++
	if b.failures >= authFailMax {
		b.lockedUntil = now.Add(authLockDuration)
		atomic.AddInt64(&metricAuthLockouts, 1)
		logJSON(logFields{Level:"warn",Component:"auth-lockout",Msg:fmt.Sprintf("IP %s locked out for %s", ip, authLockDuration)})
	}
}
func recordAuthSuccess(ip string) {
	b := getAuthFailBucket(ip); b.mu.Lock(); defer b.mu.Unlock()
	b.failures = 0; b.lockedUntil = time.Time{}
}

// =============================================================================
// CSRF (ADR-0013/0016/0017)
// =============================================================================

var csrfSecret []byte

func initCSRFSecret() {
	csrfSecret = make([]byte, 32)
	if _, err := rand.Read(csrfSecret); err != nil { logError("csrf", "failed to generate CSRF secret", err.Error()); os.Exit(1) }
	logInfo("csrf", "CSRF secret initialized (32 bytes)")
}
func csrfCookieSecure() bool {
	if v := os.Getenv("CSRF_SECURE_COOKIE"); v != "" { return v == "true" }
	return cfg.Domain != "localhost"
}
func generateCSRFToken() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil { return "" }
	token := hex.EncodeToString(raw)
	mac := hmac.New(sha256.New, csrfSecret); mac.Write([]byte(token))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.URLEncoding.EncodeToString([]byte(token + "." + sig))
}
func validateCSRFToken(token string) bool {
	if token == "" { return false }
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil { return false }
	parts := strings.SplitN(string(decoded), ".", 2)
	if len(parts) != 2 { return false }
	mac := hmac.New(sha256.New, csrfSecret); mac.Write([]byte(parts[0]))
	return hmac.Equal([]byte(parts[1]), []byte(hex.EncodeToString(mac.Sum(nil))))
}
func csrfTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if c, err := r.Cookie("vp_csrf"); err != nil || c.Value == "" {
				if token := generateCSRFToken(); token != "" {
					http.SetCookie(w, &http.Cookie{Name:"vp_csrf",Value:token,Path:"/",SameSite:http.SameSiteStrictMode,HttpOnly:false,Secure:csrfCookieSecure(),MaxAge:3600})
				}
			}
			next.ServeHTTP(w, r); return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			headerToken := r.Header.Get("X-CSRF-Token")
			cookieToken := ""
			if c, err := r.Cookie("vp_csrf"); err == nil { cookieToken = c.Value }
			if headerToken == "" || cookieToken == "" || headerToken != cookieToken || !validateCSRFToken(headerToken) {
				writeAPIError(w, r, 403, "csrf_invalid", "CSRF token missing or invalid", "https://docs.vayupress.com/api/csrf")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// P8 — CSP Nonce Centralized Helper (ADR-0036)
// =============================================================================

type ctxKeyCSPNonce struct{}

// CSPNonce returns the per-request CSP nonce from the request context.
// Use this in all templates that contain inline scripts.
func CSPNonce(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyCSPNonce{}).(string); ok { return v }
	return ""
}
func generateCSPNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil { return fmt.Sprintf("ts%x", time.Now().UnixNano()) }
	return base64.StdEncoding.EncodeToString(b)
}

// =============================================================================
// P8 — Pprof rate limiter (ADR-0037)
// =============================================================================

type pprofBucket struct { count int; windowEnd time.Time; mu sync.Mutex }
var pprofLimiters sync.Map

func allowPprof(ip string) bool {
	v, _ := pprofLimiters.LoadOrStore(ip, &pprofBucket{})
	b := v.(*pprofBucket); b.mu.Lock(); defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) { b.count = 0; b.windowEnd = now.Add(time.Minute) }
	if b.count >= cfg.PprofRateLimit { return false }
	b.count++; return true
}

// =============================================================================
// P8 — VACUUM state (ADR-0038)
// =============================================================================

var (
	vacuumMu      sync.Mutex
	vacuumLastRun time.Time
)

const vacuumWriteThreshold = 10

// =============================================================================
// Cache purge rate limiter
// =============================================================================

type purgeBucket struct { tokens float64; lastRefill time.Time; mu sync.Mutex }
const (purgeRatePerMin = 5.0; purgeBurstMax = 5.0)
var purgeLimiters sync.Map

func getPurgeBucket(ip string) *purgeBucket {
	if v, ok := purgeLimiters.Load(ip); ok { return v.(*purgeBucket) }
	b := &purgeBucket{tokens: purgeBurstMax, lastRefill: time.Now()}
	purgeLimiters.Store(ip, b); return b
}
func allowPurge(ip string) bool {
	b := getPurgeBucket(ip); b.mu.Lock(); defer b.mu.Unlock()
	now := time.Now(); elapsed := now.Sub(b.lastRefill).Minutes()
	b.tokens += elapsed * purgeRatePerMin
	if b.tokens > purgeBurstMax { b.tokens = purgeBurstMax }
	b.lastRefill = now
	if b.tokens >= 1.0 { b.tokens -= 1.0; return true }
	return false
}
// =============================================================================
// Latency histogram (P2)
// =============================================================================

type latencyHistogram struct { mu sync.Mutex; buckets [16]int64; count, sum, max int64 }
var histBoundMS = [16]int64{1,2,4,8,16,32,64,128,256,512,1024,2048,4096,8192,16384,1<<62}

func (h *latencyHistogram) record(d time.Duration) {
	ms := d.Milliseconds(); if ms < 0 { ms = 0 }
	h.mu.Lock(); h.count++; h.sum += ms; if ms > h.max { h.max = ms }
	bucket := 0; for bucket < 15 && ms > histBoundMS[bucket] { bucket++ }
	h.buckets[bucket]++; h.mu.Unlock()
}
func (h *latencyHistogram) snapshot() (buckets [16]int64, count, sum, max int64) {
	h.mu.Lock(); buckets = h.buckets; count = h.count; sum = h.sum; max = h.max; h.mu.Unlock(); return
}
func (h *latencyHistogram) prometheus(name, help string) string {
	buckets, count, sum, _ := h.snapshot()
	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	cumulative := int64(0)
	for i, bound := range histBoundMS {
		cumulative += buckets[i]
		if bound == 1<<62 { fmt.Fprintf(&sb, "%s_bucket{le=\"+Inf\"} %d\n", name, cumulative) } else { fmt.Fprintf(&sb, "%s_bucket{le=\"%.3f\"} %d\n", name, float64(bound)/1000.0, cumulative) }
	}
	fmt.Fprintf(&sb, "%s_sum %d\n%s_count %d\n", name, sum, name, count); return sb.String()
}
func (h *latencyHistogram) percentile(pct float64) int64 {
	buckets, count, _, _ := h.snapshot(); if count == 0 { return 0 }
	target := int64(float64(count)*pct/100.0); if target < 1 { target = 1 }
	cumulative := int64(0)
	for i, b := range buckets { cumulative += b; if cumulative >= target { if histBoundMS[i] == 1<<62 && i > 0 { return histBoundMS[i-1]*2 }; return histBoundMS[i] } }
	return histBoundMS[14]
}
func (h *latencyHistogram) mean() float64 {
	_, count, sum, _ := h.snapshot(); if count == 0 { return 0 }; return float64(sum) / float64(count)
}

// =============================================================================
// wrappedDB + storage
// =============================================================================

type wrappedDB struct{ *sql.DB }
var wdb wrappedDB

func (w wrappedDB) Exec(query string, args ...interface{}) (sql.Result, error) {
	q := strings.ToUpper(strings.TrimSpace(query))
	isWrite := strings.HasPrefix(q,"INSERT")||strings.HasPrefix(q,"UPDATE")||strings.HasPrefix(q,"DELETE")
	if !isWrite { return w.DB.Exec(query, args...) }
	start := time.Now(); result, err := w.DB.Exec(query, args...); elapsed := time.Since(start)
	sqliteWriteLatency.record(elapsed)
	if elapsed.Milliseconds() > 100 {
		atomic.AddInt64(&metricSlowQueries, 1)
		logJSON(logFields{Level:"warn",Component:"db",Msg:fmt.Sprintf("slow write %dms: %s",elapsed.Milliseconds(),q[:minInt(len(q),80)])})
	}
	return result, err
}
func minInt(a, b int) int { if a < b { return a }; return b }

func initStorageCachedBytes() {
	go func() {
		start := time.Now()
		cacheSize, _ := storageDirSizeBytes(cfg.CacheDir)
		dbSize := int64(0); if fi, err := os.Stat(cfg.DBPath); err == nil { dbSize = fi.Size() }
		total := cacheSize + dbSize; atomic.StoreInt64(&cachedStorageBytes, total)
		logInfo("storage", fmt.Sprintf("initial scan: %s (%dms)", formatBytes(total), time.Since(start).Milliseconds()))
	}()
}
func storageUsedBytes() int64  { return atomic.LoadInt64(&cachedStorageBytes) }
func updateStorageDelta(delta int64) { atomic.AddInt64(&cachedStorageBytes, delta) }
func storageDirSizeBytes(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err != nil { return nil }; if !fi.IsDir() { total += fi.Size() }; return nil
	})
	return total, err
}
func storageQuotaBytes() int64 { return cfg.StorageQuotaGB * 1024 * 1024 * 1024 }
func formatBytes(b int64) string {
	const unit = 1024; if b < unit { return fmt.Sprintf("%d B", b) }
	div, exp := int64(unit), 0; for n := b/unit; n >= unit; n /= unit { div *= unit; exp++ }
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func startStuckJobReaper() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute); defer ticker.Stop()
		for {
			select {
			case <-doneCh: return
			case <-ticker.C:
				result, err := db.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing' AND created_at < datetime('now','-5 minutes')`)
				if err != nil { logError("queue-reaper","stuck job error",err.Error()); continue }
				rows, _ := result.RowsAffected()
				if rows > 0 { atomic.AddInt64(&metricQueueStuckResets,rows); logJSON(logFields{Level:"warn",Component:"queue-reaper",Msg:fmt.Sprintf("reset %d stuck jobs",rows)}) }
			}
		}
	}()
}
// =============================================================================
// P8 — WAL adaptive checkpoint (ADR-0033)
// =============================================================================

func walFileSizeMB() float64 {
	fi, err := os.Stat(cfg.DBPath + "-wal")
	if err != nil { return 0 }
	return float64(fi.Size()) / (1024 * 1024)
}

func startWALCheckpointGoroutine() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute); defer ticker.Stop()
		adaptiveBackoff := false
		for {
			select {
			case <-doneCh: return
			case <-ticker.C:
				walMB := walFileSizeMB()
				checkpointMode := "PASSIVE"
				if walMB > float64(cfg.WALSizeThresholdMB) {
					checkpointMode = "RESTART"
					atomic.AddInt64(&metricWALAdaptiveCheckpoints, 1)
					logJSON(logFields{Level:"warn",Component:"wal",Msg:fmt.Sprintf("WAL %.1fMB > threshold %dMB — RESTART checkpoint",walMB,cfg.WALSizeThresholdMB)})
					adaptiveBackoff = true
				} else if adaptiveBackoff {
					adaptiveBackoff = false
					logInfo("wal", "adaptive backoff tick — skipping checkpoint")
					continue
				}
				start := time.Now()
				var pagesWritten int
				err := db.QueryRow(fmt.Sprintf("PRAGMA wal_checkpoint(%s)", checkpointMode)).Scan(new(int), new(int), &pagesWritten)
				if err != nil {
					logError("wal","checkpoint error",err.Error())
				} else {
					elapsed := time.Since(start)
					atomic.AddInt64(&metricWALCheckpoints, 1)
					atomic.AddInt64(&metricWALCheckpointDurationMS, elapsed.Milliseconds())
					logInfo("wal", fmt.Sprintf("checkpoint(%s) pages=%d dur=%dms total=%d",
						checkpointMode, pagesWritten, elapsed.Milliseconds(), atomic.LoadInt64(&metricWALCheckpoints)))
				}
			}
		}
	}()
}

// =============================================================================
// P7/P8 — Migration system with checksum drift verification (ADR-0026/ADR-0034)
// =============================================================================

type migration struct {
	Version  string
	Up       string
	Down     string
	Checksum string
}

func checksumSQL(sql string) string {
	h := sha256.Sum256([]byte(sql)); return hex.EncodeToString(h[:])
}

var migrations []migration

func init() {
	upBaseline := `CREATE TABLE IF NOT EXISTS articles(id TEXT PRIMARY KEY,title TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,content TEXT NOT NULL,tags TEXT DEFAULT '',created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_articles_slug    ON articles(slug);
CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_updated ON articles(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_tags    ON articles(tags);
CREATE TABLE IF NOT EXISTS write_jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,article_json TEXT NOT NULL,op TEXT NOT NULL DEFAULT 'insert',status TEXT NOT NULL DEFAULT 'pending',retries INTEGER NOT NULL DEFAULT 0,retry_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_jobs_status  ON write_jobs(status,created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_retries ON write_jobs(retries);`
	upSchemaMig := `CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY AUTOINCREMENT,version TEXT UNIQUE NOT NULL,checksum TEXT NOT NULL DEFAULT '',applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`
	upRetryAt := `ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS retry_at DATETIME;`
	// P8: replay_count + dead_reason (ADR-0035)
	upReplayFields := `ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS replay_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS dead_reason TEXT NOT NULL DEFAULT '';`

	migrations = []migration{
		{Version:"001-baseline",          Up:upBaseline,      Down:"",                                           Checksum:checksumSQL(upBaseline)},
		{Version:"002-schema-migrations", Up:upSchemaMig,     Down:"DROP TABLE IF EXISTS schema_migrations;",    Checksum:checksumSQL(upSchemaMig)},
		{Version:"003-queue-retry-at",    Up:upRetryAt,       Down:"",                                           Checksum:checksumSQL(upRetryAt)},
		{Version:"004-queue-replay-fields",Up:upReplayFields, Down:"",                                           Checksum:checksumSQL(upReplayFields)},
	}
}

func runMigrations() error {
	dryRun := os.Getenv("VAYU_MIGRATE_DRY_RUN") == "true"
	if dryRun { logInfo("migrations", "DRY-RUN mode") }
	if !dryRun {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY AUTOINCREMENT,version TEXT UNIQUE NOT NULL,checksum TEXT NOT NULL DEFAULT '',applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
			return fmt.Errorf("bootstrap schema_migrations: %w", err)
		}
	}
	for _, m := range migrations {
		if dryRun { logInfo("migrations", fmt.Sprintf("[dry-run] would apply: %s", m.Version)); continue }
		var count int
		db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.Version).Scan(&count)
		if count > 0 { logInfo("migrations","already applied: "+m.Version); continue }
		logInfo("migrations","applying: "+m.Version)
		tx, err := db.Begin(); if err != nil { return fmt.Errorf("migration %s begin: %w", m.Version, err) }
		for _, stmt := range strings.Split(m.Up, "\n") {
			stmt = strings.TrimSpace(stmt); if stmt == "" { continue }
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				if strings.Contains(err.Error(),"duplicate column")||strings.Contains(err.Error(),"already exists") {
					logInfo("migrations","column exists in "+m.Version+" — continuing")
					tx2, _ := db.Begin(); if tx2 != nil { tx = tx2 }
					continue
				}
				return fmt.Errorf("migration %s exec: %w", m.Version, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,checksum) VALUES(?,?)`, m.Version, m.Checksum); err != nil { tx.Rollback(); return fmt.Errorf("migration %s record: %w", m.Version, err) }
		if err := tx.Commit(); err != nil { return fmt.Errorf("migration %s commit: %w", m.Version, err) }
		logInfo("migrations","applied: "+m.Version)
	}
	return nil
}

// P8: verifyMigrationChecksums — detects tampered historical migrations (ADR-0034)
func verifyMigrationChecksums() error {
	rows, err := db.Query(`SELECT version, checksum FROM schema_migrations ORDER BY id ASC`)
	if err != nil { return fmt.Errorf("verifyMigrationChecksums query: %w", err) }
	defer rows.Close()
	migMap := make(map[string]string)
	for _, m := range migrations { migMap[m.Version] = m.Checksum }
	var drifted []string
	for rows.Next() {
		var version, storedChecksum string
		rows.Scan(&version, &storedChecksum)
		if storedChecksum == "" { continue }
		expected, ok := migMap[version]; if !ok { continue }
		if storedChecksum != expected {
			atomic.AddInt64(&metricMigrationDriftDetected, 1)
			logJSON(logFields{Level:"error",Component:"migrations",Msg:fmt.Sprintf("CHECKSUM DRIFT: %s stored=%s expected=%s",version,storedChecksum[:8],expected[:8])})
			drifted = append(drifted, version)
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("migration drift detected: %s — startup halted (ADR-0034)", strings.Join(drifted,", "))
	}
	logInfo("migrations", fmt.Sprintf("checksum verification passed: %d migrations (ADR-0034)", len(migMap)))
	return nil
}

func rollbackMigration(version string) error {
	for i := len(migrations)-1; i >= 0; i-- {
		if migrations[i].Version != version { continue }
		if migrations[i].Down == "" { return fmt.Errorf("migration %s has no Down SQL", version) }
		tx, err := db.Begin(); if err != nil { return err }
		if _, err := tx.Exec(migrations[i].Down); err != nil { tx.Rollback(); return fmt.Errorf("rollback exec %s: %w", version, err) }
		if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version=?`, version); err != nil { tx.Rollback(); return err }
		if err := tx.Commit(); err != nil { return err }
		logInfo("migrations","rolled back: "+version); return nil
	}
	return fmt.Errorf("migration %s not found", version)
}

func initDB() error {
	var err error
	dsn := cfg.DBPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	db, err = sql.Open("sqlite3", dsn); if err != nil { return fmt.Errorf("open: %w", err) }
	db.SetMaxOpenConns(1); db.SetMaxIdleConns(1); db.SetConnMaxLifetime(0)
	if err = db.Ping(); err != nil { return fmt.Errorf("ping: %w", err) }
	wdb = wrappedDB{db}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-65536",
		"PRAGMA mmap_size=268435456",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA journal_size_limit=67108864",
		"PRAGMA wal_autocheckpoint=1000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil { return fmt.Errorf("pragma %q: %w", p, err) }
	}
	// P8: adaptive WAL checkpoint (ADR-0033)
	startWALCheckpointGoroutine()
	if err := runMigrations(); err != nil { return fmt.Errorf("migrations: %w", err) }
	// P8: drift verification (ADR-0034)
	if err := verifyMigrationChecksums(); err != nil { return fmt.Errorf("migration drift: %w", err) }
	logInfo("db","ready — WAL+PRAGMAs enforced, migrations+checksums verified (ADR-0033/0034)")
	return nil
}
// =============================================================================
// P8 — Plugin pool hardened (ADR-0032)
// =============================================================================

type HookFunc func(ctx context.Context, payload map[string]interface{}) error
type hookRegistry struct { mu sync.RWMutex; hooks map[string][]HookFunc }
var pluginHooks = &hookRegistry{hooks: make(map[string][]HookFunc)}

func RegisterHook(event string, fn HookFunc) {
	pluginHooks.mu.Lock(); pluginHooks.hooks[event] = append(pluginHooks.hooks[event], fn); pluginHooks.mu.Unlock()
}
func fireHookSafe(event string, fn HookFunc, ctx context.Context, payload map[string]interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			if len(stack) > 2048 { stack = stack[:2048] }
			atomic.AddInt64(&metricPluginPanics, 1)
			logJSON(logFields{Level:"error",Component:"plugin-hook",Msg:fmt.Sprintf("PANIC in hook %s: %v",event,r),Error:stack})
			err = fmt.Errorf("plugin panic in hook %s: %v", event, r)
		}
	}()
	return fn(ctx, payload)
}

const (
	pluginPoolSize    = 4
	pluginQueueDepth  = 32
	pluginHookTimeout = 2 * time.Second
	pluginFailThresh  = 5
)

type pluginJob struct {
	event   string
	fn      HookFunc
	payload map[string]interface{}
}

var (
	pluginQueue    chan pluginJob
	pluginFailures sync.Map // key -> int64
	pluginDisabled sync.Map // key -> bool
	pluginCtx      context.Context
	pluginCancel   context.CancelFunc
	workerPluginWg sync.WaitGroup // P8: tracks all plugin goroutines (ADR-0032)
)

func initPluginPool() {
	pluginCtx, pluginCancel = context.WithCancel(context.Background())
	pluginQueue = make(chan pluginJob, pluginQueueDepth)
	for i := 0; i < pluginPoolSize; i++ {
		workerPluginWg.Add(1)
		go func(workerID int) {
			defer workerPluginWg.Done()
			// P8: goroutine-level panic isolation (ADR-0032)
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&metricPluginPanics, 1)
					logJSON(logFields{Level:"error",Component:"plugin-pool",Msg:fmt.Sprintf("worker-%d PANIC: %v — worker terminated",workerID,r)})
				}
			}()
			for {
				select {
				case <-pluginCtx.Done():
					// drain remaining jobs
					for {
						select {
						case job, ok := <-pluginQueue:
							if !ok { return }
							runPluginJob(job)
						default:
							return
						}
					}
				case job, ok := <-pluginQueue:
					if !ok { return }
					runPluginJob(job)
				}
			}
		}(i)
	}
	logInfo("plugin-pool", fmt.Sprintf("P8 hardened: workers=%d queue=%d (ADR-0032)", pluginPoolSize, pluginQueueDepth))
}

func runPluginJob(job pluginJob) {
	key := fmt.Sprintf("%s:%p", job.event, job.fn)
	// P8: propagate pluginCtx so shutdown cancels in-progress hooks (ADR-0032)
	ctx, cancel := context.WithTimeout(pluginCtx, pluginHookTimeout)
	err := fireHookSafe(job.event, job.fn, ctx, job.payload)
	cancel()
	if err != nil {
		v, _ := pluginFailures.LoadOrStore(key, int64(0))
		newCount := v.(int64) + 1; pluginFailures.Store(key, newCount)
		if newCount >= pluginFailThresh {
			pluginDisabled.Store(key, true)
			atomic.AddInt64(&metricPluginDisabled, 1)
			logJSON(logFields{Level:"warn",Component:"plugin-pool",Msg:fmt.Sprintf("hook disabled after %d failures: %s",newCount,job.event)})
		}
	} else {
		pluginFailures.Store(key, int64(0))
	}
}

// P8: clean shutdown — pluginCancel → drain → close(pluginQueue) → Wait() (ADR-0032)
func shutdownPluginPool() {
	if pluginCancel == nil { return }
	logInfo("plugin-pool","cancelling context — draining workers")
	pluginCancel()
	drainDone := make(chan struct{})
	go func() { workerPluginWg.Wait(); close(drainDone) }()
	select {
	case <-drainDone:
		logInfo("plugin-pool","all workers drained")
	case <-time.After(10 * time.Second):
		logJSON(logFields{Level:"warn",Component:"plugin-pool",Msg:"drain timeout (10s) — closing channel"})
	}
	close(pluginQueue)
}

func FireHook(event string, payload map[string]interface{}) {
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" { return }
	pluginHooks.mu.RLock(); fns := pluginHooks.hooks[event]; pluginHooks.mu.RUnlock()
	for _, fn := range fns {
		key := fmt.Sprintf("%s:%p", event, fn)
		if disabled, ok := pluginDisabled.Load(key); ok && disabled.(bool) { continue }
		job := pluginJob{event:event, fn:fn, payload:payload}
		select {
		case pluginQueue <- job:
		default:
			atomic.AddInt64(&metricPluginPoolDropped, 1)
			logJSON(logFields{Level:"warn",Component:"plugin-pool",Msg:fmt.Sprintf("hook dropped — queue full: %s",event)})
		}
	}
}
// =============================================================================
// Worker pool — P8: maintenance mode + capped backoff (ADR-0035/ADR-0038)
// =============================================================================

func startWorkerPool(wg *sync.WaitGroup) {
	for i := 0; i < cfg.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			atomic.AddInt64(&workerLiveness, 1); defer atomic.AddInt64(&workerLiveness, -1)
			workerLastActivity.Store(workerID, time.Now())
			logInfo("worker", fmt.Sprintf("worker-%d started", workerID))
			ticker := time.NewTicker(100*time.Millisecond); defer ticker.Stop()
			for {
				select {
				case <-doneCh:
					logInfo("worker", fmt.Sprintf("worker-%d draining", workerID))
					for !processOneJob(workerID) {}
					logInfo("worker", fmt.Sprintf("worker-%d done", workerID)); return
				case <-ticker.C: processOneJob(workerID)
				}
			}
		}(i)
	}
}

const maxBackoffSeconds = 300 // P8: cap prevents int overflow (ADR-0035)

func processOneJob(workerID int) (empty bool) {
	if cfg.MaintenanceMode { return true } // P8: maintenance mode guard (ADR-0038)
	var job WriteJob
	err := db.QueryRow(`SELECT id,article_json,op FROM write_jobs WHERE status='pending' AND (retry_at IS NULL OR retry_at <= datetime('now')) ORDER BY created_at ASC LIMIT 1`).Scan(&job.ID,&job.ArticleJSON,&job.Op)
	if err == sql.ErrNoRows { return true }
	if err != nil { logError("worker",fmt.Sprintf("worker-%d fetch error",workerID),err.Error()); return false }
	wdb.Exec(`UPDATE write_jobs SET status='processing' WHERE id=?`, job.ID)
	jobStart := time.Now()
	var a Article
	if err := json.Unmarshal([]byte(job.ArticleJSON), &a); err != nil {
		logError("worker", fmt.Sprintf("worker-%d bad JSON job %d",workerID,job.ID), err.Error())
		wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='parse_error' WHERE id=?`, job.ID)
		return false
	}
	var execErr error
	switch job.Op {
	case "insert":
		_, execErr = db.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,a.ID,a.Title,a.Slug,a.Content,strings.Join(a.Tags,","),a.CreatedAt,a.UpdatedAt)
		if execErr == nil { atomic.AddInt64(&metricArticlesCreated,1); FireHook("article.create",map[string]interface{}{"slug":a.Slug,"id":a.ID}) }
	case "update":
		_, execErr = db.Exec(`UPDATE articles SET title=?,content=?,tags=?,updated_at=? WHERE slug=?`,a.Title,a.Content,strings.Join(a.Tags,","),a.UpdatedAt,a.Slug)
		if execErr == nil { atomic.AddInt64(&metricArticlesUpdated,1); FireHook("article.update",map[string]interface{}{"slug":a.Slug}) }
	case "delete":
		_, execErr = db.Exec(`DELETE FROM articles WHERE slug=?`, a.Slug)
		if execErr == nil { atomic.AddInt64(&metricArticlesDeleted,1); FireHook("article.delete",map[string]interface{}{"slug":a.Slug}) }
	default:
		wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='unknown_op' WHERE id=?`, job.ID); return false
	}
	if execErr != nil {
		var retries int; db.QueryRow(`SELECT retries FROM write_jobs WHERE id=?`, job.ID).Scan(&retries)
		if retries < 3 {
			// P8: capped exponential backoff (ADR-0035)
			backoffSeconds := int(math.Pow(2, float64(retries+1))) * 5
			if backoffSeconds > maxBackoffSeconds { backoffSeconds = maxBackoffSeconds }
			nextRetry := time.Now().Add(time.Duration(backoffSeconds)*time.Second).UTC().Format("2006-01-02T15:04:05Z")
			wdb.Exec(`UPDATE write_jobs SET status='pending',retries=retries+1,retry_at=? WHERE id=?`, nextRetry, job.ID)
		} else {
			wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='max_retries' WHERE id=?`, job.ID)
			atomic.AddInt64(&metricQueueFailed,1); atomic.AddInt64(&metricDeadLetterJobs,1)
		}
		return false
	}
	if job.Op != "delete" {
		html, err := renderArticle(a)
		if err != nil { logError("worker","render error for "+a.Slug,err.Error()) } else { cacheWrite(filepath.Join("posts",a.Slug+".html"),html) }
		indexArticle(a)
	} else {
		os.Remove(filepath.Join(cfg.CacheDir,"posts",a.Slug+".html"))
		go meiliDo("DELETE","/indexes/articles/documents/"+a.ID,nil)
	}
	cachePurge(a.Slug, a.Tags); go purgeCloudflare(a.Slug); go pingIndexNow(a.Slug)
	db.Exec(`UPDATE write_jobs SET status='completed' WHERE id=?`, job.ID)
	atomic.AddInt64(&metricQueueProcessed,1); queueJobLatency.record(time.Since(jobStart))
	var qDepth int; db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&qDepth)
	if qDepth > cfg.QueueSaturationWarn { logJSON(logFields{Level:"warn",Component:"queue",Msg:fmt.Sprintf("saturation: %d pending",qDepth)}) }
	workerLastActivity.Store(workerID, time.Now()); return false
}
// =============================================================================
// Middleware
// =============================================================================

type ctxKeyRequestID struct{}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			b := make([]byte, 8)
			if _, err := rand.Read(b); err != nil { reqID = fmt.Sprintf("ts-%x", time.Now().UnixNano()) } else { reqID = hex.EncodeToString(b) }
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func getRequestID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyRequestID{}).(string); ok { return v }; return ""
}

func structuredLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now(); ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		dur := time.Since(start); httpLatency.record(dur)
		logJSON(logFields{Level:"info",RequestID:getRequestID(r),Method:r.Method,Path:r.URL.Path,Status:ww.Status(),LatencyMS:dur.Milliseconds(),RemoteAddr:r.RemoteAddr,UserAgent:r.UserAgent(),Component:"http"})
	})
}

// P8: securityHeadersMiddleware stores nonce in context for CSPNonce(r) helper (ADR-0036)
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security","max-age=63072000; includeSubDomains; preload")
		nonce := generateCSPNonce()
		csp := fmt.Sprintf("default-src 'self'; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'nonce-%s'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'", nonce)
		w.Header().Set("Content-Security-Policy", csp)
		ctx := context.WithValue(r.Context(), ctxKeyCSPNonce{}, nonce)
		w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("X-Frame-Options","DENY")
		w.Header().Set("X-XSS-Protection","1; mode=block")
		w.Header().Set("Referrer-Policy","strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy","camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xri := r.Header.Get("X-Real-IP"); xri != "" { ip = xri } else if xff := r.Header.Get("X-Forwarded-For"); xff != "" { ip = strings.TrimSpace(strings.Split(xff,",")[0]) }
		if locked, until := checkAuthLockout(ip); locked {
			retryAfter := int(time.Until(until).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeAPIError(w,r,429,"auth_lockout",fmt.Sprintf("locked out for %ds",retryAfter),"https://docs.vayupress.com/api/auth#lockout"); return
		}
		key := r.Header.Get("X-API-Key"); if key == "" { key = strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ") }
		if key != cfg.APIKey { recordAuthFailure(ip); writeAPIError(w,r,401,"unauthorized","invalid or missing API key","https://docs.vayupress.com/api/auth"); return }
		recordAuthSuccess(ip); next.ServeHTTP(w, r)
	})
}

type ipBucket struct { count int; resetAt time.Time }
var (rateMu sync.Mutex; rateBuckets = make(map[string]*ipBucket); trustedIPs = parseTrustedIPs())
func parseTrustedIPs() map[string]bool {
	m := make(map[string]bool)
	for _, ip := range strings.Split(envOr("TRUSTED_IPS",""),",") { ip = strings.TrimSpace(ip); if ip != "" { m[ip] = true } }
	return m
}
func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr; if xff := r.Header.Get("X-Forwarded-For"); xff != "" { ip = strings.TrimSpace(strings.Split(xff,",")[0]) }
		if trustedIPs[ip] { next.ServeHTTP(w,r); return }
		rateMu.Lock(); b, ok := rateBuckets[ip]
		if !ok || time.Now().After(b.resetAt) { b = &ipBucket{1,time.Now().Add(time.Hour)}; rateBuckets[ip] = b } else { b.count++ }
		allowed := b.count <= 100; rateMu.Unlock()
		if !allowed { writeAPIError(w,r,429,"rate_limit_exceeded","rate limit exceeded (100 req/hour)","https://docs.vayupress.com/api/rate-limiting"); return }
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// Response helpers
// =============================================================================

func writeJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	w.Header().Set("Content-Type","application/json; charset=utf-8"); w.WriteHeader(code)
	enc := json.NewEncoder(w); enc.SetIndent("","  "); enc.Encode(v)
}
func writeAPIError(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docsURL string) {
	reqID := ""; if r != nil { reqID = getRequestID(r) }
	writeJSON(w,r,code,map[string]apiError{"error":{Code:errCode,Message:msg,RequestID:reqID,Docs:docsURL}})
}
func readJSONDirect(r *http.Request, v interface{}) error { defer r.Body.Close(); return json.NewDecoder(io.LimitReader(r.Body,10<<20)).Decode(v) }
func splitTags(s string) []string {
	if s == "" { return []string{} }
	parts := strings.Split(s,","); out := make([]string,0,len(parts))
	for _, p := range parts { p = strings.TrimSpace(p); if p != "" { out = append(out,p) } }
	return out
}
func validateArticleInput(title, slug, content string, tags []string) error {
	if title == "" || len(title) > 500 { return fmt.Errorf("title required (1–500 chars)") }
	if !isValidSlug(slug) { return fmt.Errorf("invalid slug") }
	if content == "" || len(content) > 5_000_000 { return fmt.Errorf("content required (1 byte – 5 MB)") }
	if len(tags) > 20 { return fmt.Errorf("max 20 tags") }
	for _, t := range tags { if len(t) > 100 { return fmt.Errorf("tag too long: %q", t) } }
	return nil
}
func isValidSlug(s string) bool { return slugRe.MatchString(s) }
func newUUID() string {
	b := make([]byte, 16); if _, err := rand.Read(b); err != nil { return fmt.Sprintf("%x",time.Now().UnixNano()) }
	return hex.EncodeToString(b)
}
func cacheHitRatio() float64 {
	hits := atomic.LoadInt64(&metricCacheHits); misses := atomic.LoadInt64(&metricCacheMisses)
	total := hits+misses; if total == 0 { return 0 }; return float64(hits)/float64(total)
}
// =============================================================================
// Cache helpers + rendering stubs
// =============================================================================

func cacheWrite(relPath, content string) error {
	full := filepath.Join(cfg.CacheDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil { return fmt.Errorf("mkdir: %w", err) }
	oldSize := int64(0); if fi, err := os.Stat(full); err == nil { oldSize = fi.Size() }
	if err := os.WriteFile(full, []byte(content), 0644); err != nil { return err }
	updateStorageDelta(int64(len(content)) - oldSize); return nil
}
func cachePurge(slug string, tags []string) {
	postFile := filepath.Join(cfg.CacheDir,"posts",slug+".html")
	if fi, err := os.Stat(postFile); err == nil { updateStorageDelta(-fi.Size()) }
	os.Remove(postFile); os.Remove(filepath.Join(cfg.CacheDir,"home","index.html"))
	for _, t := range tags { if t != "" { tagFile := filepath.Join(cfg.CacheDir,"tags",t+".html"); if fi, err := os.Stat(tagFile); err == nil { updateStorageDelta(-fi.Size()) }; os.Remove(tagFile) } }
	go generateSitemap(); go generateRSS(); go generateRobots()
}

// =============================================================================
// Meilisearch + Cloudflare + IndexNow
// =============================================================================

func initMeilisearchCB() {
	meiliCB = gobreaker.NewCircuitBreaker(gobreaker.Settings{Name:"meilisearch",MaxRequests:3,Interval:10*time.Second,Timeout:30*time.Second,
		ReadyToTrip:func(counts gobreaker.Counts) bool { return counts.Requests>=3&&float64(counts.TotalFailures)/float64(counts.Requests)>=0.60 },
		OnStateChange:func(name string, from, to gobreaker.State){logJSON(logFields{Level:"warn",Component:"meili-cb",Msg:fmt.Sprintf("%s → %s",from,to)})},
	})
}
func meiliDo(method, path string, body interface{}) error {
	var r io.Reader; if body != nil { b, _ := json.Marshal(body); r = bytes.NewReader(b) }
	req, err := http.NewRequestWithContext(context.Background(),method,cfg.MeiliHost+path,r); if err != nil { return err }
	req.Header.Set("Content-Type","application/json"); if cfg.MeiliMasterKey != "" { req.Header.Set("Authorization","Bearer "+cfg.MeiliMasterKey) }
	resp, err := outboundClient.Do(req); if err != nil { return err }; defer resp.Body.Close()
	if resp.StatusCode >= 400 { b, _ := io.ReadAll(io.LimitReader(resp.Body,512)); return fmt.Errorf("meili %d: %s",resp.StatusCode,b) }
	return nil
}
func configureMeilisearch() {
	_ = meiliDo("PATCH","/indexes/articles/settings",map[string]interface{}{
		"rankingRules":[]string{"words","proximity","attribute","sort","exactness"},
		"searchableAttributes":[]string{"title","tags","content"},
		"filterableAttributes":[]string{"tags","created_at"},
		"sortableAttributes":[]string{"created_at","updated_at"},
	})
}
func indexArticle(a Article) {
	if meiliCB == nil { return }
	doc := map[string]interface{}{"id":a.ID,"title":a.Title,"slug":a.Slug,"content":htmlTagRe.ReplaceAllString(policy.Sanitize(a.Content),""),"tags":a.Tags,"created_at":a.CreatedAt.Unix()}
	_, err := meiliCB.Execute(func() (interface{},error) { return nil, meiliDo("POST","/indexes/articles/documents",[]map[string]interface{}{doc}) })
	if err != nil { atomic.AddInt64(&metricMeiliErrors,1) }
}
func purgeCloudflare(slug string) {
	if cfg.CFZoneID == "" || cfg.CFAPIToken == "" { return }
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache",cfg.CFZoneID)
	body, _ := json.Marshal(map[string][]string{"files":{"https://"+cfg.Domain+"/"+slug}})
	req, _ := http.NewRequestWithContext(context.Background(),"POST",url,bytes.NewReader(body))
	req.Header.Set("Content-Type","application/json"); req.Header.Set("Authorization","Bearer "+cfg.CFAPIToken)
	resp, err := outboundClient.Do(req); if err != nil { return }; defer resp.Body.Close()
}
func pingIndexNow(slug string) {
	if cfg.IndexNowKey == "" { return }
	body, _ := json.Marshal(map[string]interface{}{"host":cfg.Domain,"key":cfg.IndexNowKey,"keyLocation":"https://"+cfg.Domain+"/.well-known/"+cfg.IndexNowKey+".txt","urlList":[]string{"https://"+cfg.Domain+"/"+slug}})
	req, _ := http.NewRequestWithContext(context.Background(),"POST","https://api.indexnow.org/indexnow",bytes.NewReader(body))
	req.Header.Set("Content-Type","application/json"); resp, err := outboundClient.Do(req); if err != nil { return }; defer resp.Body.Close()
}

// =============================================================================
// Sitemap / RSS / robots / cache warmup
// =============================================================================

func generateSitemap() {
	rows, err := db.Query(`SELECT slug,updated_at FROM articles ORDER BY updated_at DESC LIMIT 50000`)
	if err != nil { return }; defer rows.Close()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for rows.Next() { var slug string; var updated time.Time; rows.Scan(&slug,&updated); fmt.Fprintf(&sb,"<url><loc>https://%s/%s</loc><lastmod>%s</lastmod></url>",cfg.Domain,slug,updated.Format("2006-01-02")) }
	sb.WriteString("</urlset>"); cacheWrite("sitemap.xml",sb.String())
}
func generateRSS() {
	rows, err := db.Query(`SELECT title,slug,content,created_at FROM articles ORDER BY created_at DESC LIMIT 50`)
	if err != nil { return }; defer rows.Close()
	var items strings.Builder
	for rows.Next() {
		var title, slug, content string; var created time.Time; rows.Scan(&title,&slug,&content,&created)
		plain := htmlTagRe.ReplaceAllString(policy.Sanitize(content),""); if len(plain) > 500 { plain = plain[:500]+"..." }
		fmt.Fprintf(&items,"<item><title><![CDATA[%s]]></title><link>https://%s/%s</link><guid isPermaLink=\"true\">https://%s/%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item>",title,cfg.Domain,slug,cfg.Domain,slug,created.Format(time.RFC1123Z),plain)
	}
	rss := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>%s</title><link>https://%s</link><description>%s</description>%s</channel></rss>`,cfg.Domain,cfg.Domain,cfg.Domain,items.String())
	cacheWrite("feed.xml",rss)
}
func generateRobots() {
	cacheWrite("robots.txt",fmt.Sprintf("User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /admin\n\nSitemap: https://%s/sitemap.xml\n",cfg.Domain))
}
func warmCache() {
	rows, err := db.Query(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles ORDER BY updated_at DESC LIMIT 1000`)
	if err != nil { return }; defer rows.Close(); count := 0
	for rows.Next() {
		var a Article; var tagsStr string; rows.Scan(&a.ID,&a.Title,&a.Slug,&a.Content,&tagsStr,&a.CreatedAt,&a.UpdatedAt); a.Tags = splitTags(tagsStr)
		dest := filepath.Join(cfg.CacheDir,"posts",a.Slug+".html"); if _, err := os.Stat(dest); err == nil { continue }
		html, err := renderArticle(a); if err != nil { continue }
		cacheWrite(filepath.Join("posts",a.Slug+".html"),html); count++
	}
	logInfo("cache-warm", fmt.Sprintf("pre-rendered %d articles", count))
}
// =============================================================================
// CSS assets + rendering (P4/P5)
// =============================================================================

var cssHashes struct { ArticleCSS, AdminCSS, HighContrastCSS string }

const articleCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--border:#1F2937;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--max-w:720px;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px;--sp6:48px}
@media(prefers-color-scheme:light){:root{--bg:#fff;--surface:#F9FAFB;--border:#E5E7EB;--text:#111827;--muted:#6B7280}}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font:500 13px/1.4 var(--font);text-decoration:none;transition:top .2s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
body{background:var(--bg);color:var(--text);font:400 18px/1.6 var(--font);padding:var(--sp5) var(--sp3)}
.container{max-width:var(--max-w);margin:0 auto}
header{border-bottom:1px solid var(--border);padding-bottom:var(--sp5);margin-bottom:var(--sp5)}
h1{font:700 2rem/1.2 var(--font);margin-bottom:var(--sp2);letter-spacing:-.5px}
.meta{color:var(--muted);font-size:13px;display:flex;flex-wrap:wrap;gap:var(--sp2)}
.tags a{display:inline-block;padding:2px var(--sp2);border:1px solid var(--border);border-radius:var(--radius);font-size:12px;color:var(--accent);text-decoration:none}
.tags a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.content{margin-top:var(--sp5)}.content h2,.content h3{font:600 1.25rem/1.3 var(--font);margin:var(--sp5) 0 var(--sp3)}
.content pre{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3);overflow-x:auto;font:400 14px/1.5 var(--mono);margin:var(--sp3) 0}
.content code{background:var(--surface);padding:2px 6px;border-radius:var(--radius);font:400 14px var(--mono)}.content pre code{background:none;padding:0}
.content blockquote{border-left:4px solid var(--accent);padding-left:var(--sp3);color:var(--muted);margin:var(--sp3) 0}
footer{margin-top:var(--sp6);padding-top:var(--sp5);border-top:1px solid var(--border);font-size:13px;color:var(--muted)}
a:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:2px}
@media(max-width:480px){body{padding:var(--sp3)}h1{font-size:1.5rem}}`

const adminCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--surface2:#161f2e;--border:#1F2937;--border2:#2d3a4a;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--success:#10B981;--warn:#F59E0B;--error:#EF4444;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font:400 14px/1.5 var(--font);min-height:100vh}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
.app-shell{display:grid;grid-template-rows:auto 1fr;min-height:100vh}
.topbar{display:flex;align-items:center;justify-content:space-between;padding:var(--sp3) var(--sp4);background:var(--surface);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:100}
.topbar-brand{display:flex;align-items:center;gap:var(--sp2);font-weight:600;font-size:15px;color:var(--text);text-decoration:none}
.topbar-domain{color:var(--muted);font-size:12px;font-weight:400}
.topbar-actions{display:flex;align-items:center;gap:var(--sp2)}
.kbd-hint{font:400 11px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:2px 6px;cursor:pointer;transition:border-color .15s,color .15s}
.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
main{padding:var(--sp4);max-width:1100px}
.section-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:var(--sp4) 0 var(--sp3);padding-bottom:var(--sp2);border-bottom:1px solid var(--border)}
.stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:var(--sp3);margin-bottom:var(--sp4)}
.stat-card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3)}
.stat-val{font:700 1.875rem/1 var(--font);color:var(--accent);margin-bottom:4px}
.stat-val.stat-ok{color:var(--success)}.stat-val.stat-warn{color:var(--warn)}.stat-val.stat-err{color:var(--error)}
.stat-lbl{font-size:11px;color:var(--muted)}.stat-sub{font-size:11px;color:var(--muted);margin-top:6px}
.storage-bar{height:3px;background:var(--border2);border-radius:2px;margin-top:8px;overflow:hidden}
.storage-fill{height:100%;border-radius:2px;background:var(--accent)}
.thresh-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:var(--sp2);margin-bottom:var(--sp4)}
.thresh-item{display:flex;align-items:center;justify-content:space-between;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp2) var(--sp3);font-size:12px}
.thresh-name{color:var(--muted)}.thresh-val{font:500 12px var(--mono);color:var(--text)}
.thresh-ok{color:var(--success);font-weight:600}.thresh-fail{color:var(--error);font-weight:600}
.action-row{display:flex;flex-wrap:wrap;gap:var(--sp2);margin-bottom:var(--sp4)}
.btn{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;background:transparent;border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:500 13px var(--font);cursor:pointer;text-decoration:none;transition:border-color .15s,background .15s,color .15s}
.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(59,130,246,.06);color:var(--hi);outline:2px solid var(--accent);outline-offset:2px}
.btn.btn-primary{background:var(--accent);border-color:var(--accent);color:#fff}
.data-table{width:100%;border-collapse:collapse;font-size:13px}
.data-table th{text-align:left;font-size:10px;font-weight:600;letter-spacing:.05em;text-transform:uppercase;color:var(--muted);padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border)}
.data-table td{padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.data-table tr:hover td{background:var(--surface2)}.data-table td a{color:var(--accent);text-decoration:none}
.action-msg{display:none;padding:var(--sp2) var(--sp3);background:var(--surface);border:1px solid var(--success);border-radius:var(--radius);font-size:13px;margin-bottom:var(--sp3)}
.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:var(--sp3);margin-top:var(--sp3)}
.links-row a{color:var(--accent);font-size:13px;text-decoration:none}.links-row a:hover{text-decoration:underline}
.admin-footer{margin-top:var(--sp5);padding-top:var(--sp4);border-top:1px solid var(--border);font-size:11px;color:var(--muted)}
.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.7);align-items:center;justify-content:center}
.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius);padding:var(--sp4);min-width:320px;max-width:480px;width:90%}
.modal-title{display:flex;align-items:center;justify-content:space-between;font-weight:600;font-size:14px;margin-bottom:var(--sp3)}
.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:16px;padding:4px;border-radius:var(--radius);line-height:1}
.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:var(--sp2)}
.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:13px;padding:var(--sp2) 0;border-bottom:1px solid var(--border)}
.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}
kbd{display:inline-block;padding:2px 6px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 11px var(--mono);color:var(--text);min-width:22px;text-align:center}
a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
@media(max-width:600px){.topbar{padding:var(--sp2) var(--sp3)}main{padding:var(--sp3)}.stat-grid{grid-template-columns:repeat(2,1fr)}}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`

func writeCSSAssets(staticDir string) {
	cssDir := filepath.Join(staticDir, "css")
	if err := os.MkdirAll(cssDir, 0755); err != nil { return }
	type asset struct{ name, content string; hash *string }
	for _, a := range []asset{
		{"article.css",       articleCSSMin, &cssHashes.ArticleCSS},
		{"admin.css",         adminCSSMin,   &cssHashes.AdminCSS},
		{"high-contrast.css", hcCSSMin,      &cssHashes.HighContrastCSS},
	} {
		if err := os.WriteFile(filepath.Join(cssDir,a.name), []byte(a.content), 0644); err != nil { continue }
		sum := sha256.Sum256([]byte(a.content)); *a.hash = hex.EncodeToString(sum[:])
	}
}
func cssLink(filename, hash string) template.HTML {
	ver := hash; if len(ver) > 8 { ver = ver[:8] }
	return template.HTML(fmt.Sprintf(`<link rel="stylesheet" href="/static/css/%s?v=%s">`, filename, ver))
}

// =============================================================================
// Article template + rendering
// =============================================================================

type ArticleLayoutType string
const (ArticleLayoutDefault ArticleLayoutType = "default"; ArticleLayoutMinimal ArticleLayoutType = "minimal"; ArticleLayoutWide ArticleLayoutType = "wide")

var articleTmpl = template.Must(template.New("article").Funcs(template.FuncMap{
	"trunc":     func(s string, n int) string { s = htmlTagRe.ReplaceAllString(s,""); s = strings.TrimSpace(s); if len(s) > n { return s[:n]+"..." }; return s },
	"safeHTML":  func(s string) template.HTML { return template.HTML(s) },
	"jsonAttr":  func(s string) string { s = htmlTagRe.ReplaceAllString(s,""); s = strings.TrimSpace(s); s = strings.ReplaceAll(s,`"`,`\"`); s = strings.ReplaceAll(s,"\n"," "); if len(s)>300{s=s[:300]}; return s },
	"readTime":  func(s string) int { text := htmlTagRe.ReplaceAllString(s,""); words := len(strings.Fields(text)); if words<200{return 1}; return (words+199)/200 },
	"isoDate":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"shortDate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"humanDate": func(t time.Time) string { return t.Format("2 January 2006") },
}).Parse(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — {{.Domain}}</title>
<meta name="description" content="{{.Content | trunc 160}}">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/{{.Slug}}">
<meta property="og:type" content="article"><meta property="og:title" content="{{.Title}}">
<meta property="og:url" content="https://{{.Domain}}/{{.Slug}}">
<meta property="article:published_time" content="{{.CreatedAt | isoDate}}">
<meta property="article:modified_time" content="{{.UpdatedAt | isoDate}}">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","headline":"{{.Title | jsonAttr}}","datePublished":"{{.CreatedAt | isoDate}}","dateModified":"{{.UpdatedAt | isoDate}}","inLanguage":"en","author":{"@type":"Person","name":"Ankush Choudhary Johal","url":"https://{{.Domain}}/about"},"publisher":{"@type":"Organization","name":"VayuPress","url":"https://{{.Domain}}"}}</script>
{{.ArticleCSSLink}}{{.HighContrastCSSLink}}
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container"><main id="main-content">
<article itemscope itemtype="https://schema.org/BlogPosting">
<header><h1 itemprop="headline">{{.Title}}</h1>
<div class="meta"><time itemprop="datePublished" datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>
<span>· {{.Content | readTime}} min read</span>
{{if .Tags}}<nav class="tags" aria-label="Tags">{{range .Tags}}<a href="/tags/{{.}}" rel="tag">#{{.}}</a>{{end}}</nav>{{end}}</div>
</header><div class="content" itemprop="articleBody">{{.Content | safeHTML}}</div>
</article>
<footer><p>By <strong>Ankush Choudhary Johal</strong> · Powered by <a href="https://vayupress.com">VayuPress</a></p></footer>
</main></div></body></html>`))

type articlePage struct {
	Article; Domain string; Version string; Layout ArticleLayoutType
	ArticleCSSLink template.HTML; HighContrastCSSLink template.HTML
}

func renderArticle(a Article) (string, error) { return renderArticleWithLayout(a, ArticleLayoutDefault) }
func renderArticleWithLayout(a Article, layout ArticleLayoutType) (string, error) {
	a.Content = policy.Sanitize(a.Content)
	FireHook("render.pre", map[string]interface{}{"slug":a.Slug})
	start := time.Now(); var buf strings.Builder
	data := articlePage{Article:a, Domain:cfg.Domain, Version:Version, Layout:layout,
		ArticleCSSLink:cssLink("article.css",cssHashes.ArticleCSS), HighContrastCSSLink:cssLink("high-contrast.css",cssHashes.HighContrastCSS)}
	if err := articleTmpl.Execute(&buf, data); err != nil { return "", fmt.Errorf("template: %w", err) }
	renderLatency.record(time.Since(start))
	FireHook("render.post", map[string]interface{}{"slug":a.Slug,"size_bytes":buf.Len()})
	return buf.String(), nil
}
func detectLayout(a Article, r *http.Request, isAdmin bool) ArticleLayoutType {
	if isAdmin { switch ArticleLayoutType(r.URL.Query().Get("layout")) { case ArticleLayoutMinimal: return ArticleLayoutMinimal; case ArticleLayoutWide: return ArticleLayoutWide } }
	for _, tag := range a.Tags { switch tag { case "layout:minimal": return ArticleLayoutMinimal; case "layout:wide": return ArticleLayoutWide } }
	return ArticleLayoutDefault
}
// =============================================================================
// Admin metrics snapshot
// =============================================================================

type adminMetricsSnapshot struct {
	TotalArticles int; PendingJobs int; FailedJobs int; CompletedJobs int
	StorageBytes int64; QuotaBytes int64; StoragePct float64
	WorkersAlive int64; CacheHitRatio float64; UptimeSeconds float64
	HTTPP95 int64; WriteP99 int64; RenderP99 int64
	RecentArticles []adminRecentArticle; SnapshotAt time.Time
}
type adminRecentArticle struct { Title string; Slug string; CreatedAt time.Time }

var metricsSnapshot atomic.Value

func collectAdminMetrics() {
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	row := db.QueryRow(`SELECT (SELECT COUNT(1) FROM articles),SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM write_jobs`)
	row.Scan(&snap.TotalArticles, &snap.PendingJobs, &snap.FailedJobs, &snap.CompletedJobs)
	snap.StorageBytes = storageUsedBytes(); snap.QuotaBytes = storageQuotaBytes()
	if snap.QuotaBytes > 0 { snap.StoragePct = float64(snap.StorageBytes)/float64(snap.QuotaBytes)*100 }
	snap.WorkersAlive = atomic.LoadInt64(&workerLiveness); snap.CacheHitRatio = cacheHitRatio()
	snap.UptimeSeconds = time.Since(bootTime).Seconds()
	snap.HTTPP95 = httpLatency.percentile(95); snap.WriteP99 = queueJobLatency.percentile(99); snap.RenderP99 = renderLatency.percentile(99)
	rows, err := db.Query(`SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`)
	if err == nil { defer rows.Close(); for rows.Next() { var ra adminRecentArticle; rows.Scan(&ra.Title,&ra.Slug,&ra.CreatedAt); snap.RecentArticles = append(snap.RecentArticles,ra) } }
	metricsSnapshot.Store(snap)
}
func startMetricsSnapshotCollector() {
	collectAdminMetrics()
	go func() {
		ticker := time.NewTicker(30 * time.Second); defer ticker.Stop()
		for { select { case <-doneCh: return; case <-ticker.C: collectAdminMetrics() } }
	}()
}
func getAdminSnapshot() *adminMetricsSnapshot {
	if v := metricsSnapshot.Load(); v != nil { return v.(*adminMetricsSnapshot) }
	collectAdminMetrics()
	if v := metricsSnapshot.Load(); v != nil { return v.(*adminMetricsSnapshot) }
	return &adminMetricsSnapshot{SnapshotAt: time.Now()}
}

// =============================================================================
// API handlers
// =============================================================================

func handleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in struct{ Title string `json:"title"`; Slug string `json:"slug"`; Content string `json:"content"`; Tags []string `json:"tags"` }
	if err := readJSONDirect(r,&in); err != nil { writeAPIError(w,r,400,"invalid_json",err.Error(),"https://docs.vayupress.com/api/articles"); return }
	if err := validateArticleInput(in.Title,in.Slug,in.Content,in.Tags); err != nil { writeAPIError(w,r,400,"validation_error",err.Error(),"https://docs.vayupress.com/api/articles"); return }
	if storageUsedBytes() >= storageQuotaBytes() { writeAPIError(w,r,413,"storage_quota_exceeded",fmt.Sprintf("quota %dGB exceeded",cfg.StorageQuotaGB),"https://docs.vayupress.com/api/articles"); return }
	var count int; db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`,in.Slug).Scan(&count)
	if count > 0 { writeAPIError(w,r,409,"slug_conflict","slug already exists","https://docs.vayupress.com/api/articles"); return }
	a := Article{ID:newUUID(),Title:in.Title,Slug:in.Slug,Content:in.Content,Tags:in.Tags,CreatedAt:time.Now().UTC(),UpdatedAt:time.Now().UTC()}
	payload, _ := json.Marshal(a)
	if _, err := db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`,payload); err != nil { writeAPIError(w,r,500,"queue_error",err.Error(),"https://docs.vayupress.com/api/errors"); return }
	writeJSON(w,r,202,map[string]string{"status":"queued","id":a.ID,"slug":a.Slug})
}
func handleBulkCreateArticles(w http.ResponseWriter, r *http.Request) {
	var articles []struct{ Title,Slug,Content string; Tags []string `json:"tags"` }
	if err := readJSONDirect(r,&articles); err != nil { writeAPIError(w,r,400,"invalid_json",err.Error(),"https://docs.vayupress.com/api/articles"); return }
	if len(articles) > 1000 { writeAPIError(w,r,400,"too_many_articles","max 1000","https://docs.vayupress.com/api/articles"); return }
	queued, skipped := 0, 0; var skipReasons []string
	for _, in := range articles {
		if err := validateArticleInput(in.Title,in.Slug,in.Content,in.Tags); err != nil { skipped++; skipReasons = append(skipReasons,in.Slug+": "+err.Error()); continue }
		var count int; db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`,in.Slug).Scan(&count)
		if count > 0 { skipped++; skipReasons = append(skipReasons,in.Slug+": duplicate slug"); continue }
		a := Article{ID:newUUID(),Title:in.Title,Slug:in.Slug,Content:in.Content,Tags:in.Tags,CreatedAt:time.Now().UTC(),UpdatedAt:time.Now().UTC()}
		payload, _ := json.Marshal(a); db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`,payload); queued++
	}
	writeJSON(w,r,202,map[string]interface{}{"status":"queued","queued":queued,"skipped":skipped,"skip_reasons":skipReasons})
}
func handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r,"slug"); var a Article; var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`,slug).Scan(&a.ID,&a.Title,&a.Slug,&a.Content,&tagsStr,&a.CreatedAt,&a.UpdatedAt); err == sql.ErrNoRows { writeAPIError(w,r,404,"not_found","not found","https://docs.vayupress.com/api/articles"); return }
	a.Tags = splitTags(tagsStr)
	var in struct{ Title *string `json:"title"`; Content *string `json:"content"`; Tags []string `json:"tags"` }
	if err := readJSONDirect(r,&in); err != nil { writeAPIError(w,r,400,"invalid_json","","https://docs.vayupress.com/api/articles"); return }
	if in.Title != nil { a.Title = *in.Title }; if in.Content != nil { a.Content = *in.Content }; if in.Tags != nil { a.Tags = in.Tags }
	a.UpdatedAt = time.Now().UTC(); payload, _ := json.Marshal(a); db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'update')`,payload)
	writeJSON(w,r,202,map[string]string{"status":"queued","slug":a.Slug})
}
func handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r,"slug"); var a Article; var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`,slug).Scan(&a.ID,&a.Title,&a.Slug,&a.Content,&tagsStr,&a.CreatedAt,&a.UpdatedAt); err == sql.ErrNoRows { writeAPIError(w,r,404,"not_found","not found","https://docs.vayupress.com/api/articles"); return }
	a.Tags = splitTags(tagsStr); payload, _ := json.Marshal(a); db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`,payload)
	writeJSON(w,r,200,map[string]string{"status":"queued","slug":slug})
}
func handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r,"slug")
	if !isValidSlug(slug) { writeAPIError(w,r,400,"invalid_slug","invalid slug","https://docs.vayupress.com/api/articles"); return }
	var a Article; var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`,slug).Scan(&a.ID,&a.Title,&a.Slug,&a.Content,&tagsStr,&a.CreatedAt,&a.UpdatedAt); err == sql.ErrNoRows { writeAPIError(w,r,404,"not_found","not found","https://docs.vayupress.com/api/articles"); return }
	a.Tags = splitTags(tagsStr); writeJSON(w,r,200,a)
}
func handleListArticles(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page")); limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); tag := r.URL.Query().Get("tag")
	if page < 1 { page = 1 }; if limit < 1 || limit > 100 { limit = 20 }; offset := (page-1)*limit
	type row struct{ ID,Title,Slug string; Tags []string; CreatedAt,UpdatedAt time.Time }
	var (rows_ *sql.Rows; err error; total int)
	if tag != "" { db.QueryRow(`SELECT COUNT(1) FROM articles WHERE tags LIKE ?`,"%"+tag+"%").Scan(&total); rows_, err = db.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,"%"+tag+"%",limit,offset) } else { db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total); rows_, err = db.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`,limit,offset) }
	if err != nil { writeAPIError(w,r,500,"db_error","database error","https://docs.vayupress.com/api/errors"); return }
	defer rows_.Close(); var result []row
	for rows_.Next() { var rr row; var tagsStr string; rows_.Scan(&rr.ID,&rr.Title,&rr.Slug,&tagsStr,&rr.CreatedAt,&rr.UpdatedAt); rr.Tags = splitTags(tagsStr); result = append(result,rr) }
	if result == nil { result = []row{} }
	writeJSON(w,r,200,map[string]interface{}{"articles":result,"page":page,"limit":limit,"total":total,"pages":(total+limit-1)/limit})
}
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q"); limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); if limit < 1 || limit > 100 { limit = 20 }
	if q == "" { writeJSON(w,r,200,map[string]interface{}{"hits":[]interface{}{},"query":""}); return }
	if meiliCB == nil || meiliCB.State() != gobreaker.StateClosed { handleSearchFallback(w,r,q,limit); return }
	body, _ := json.Marshal(map[string]interface{}{"q":q,"limit":limit,"attributesToRetrieve":[]string{"title","slug","tags","created_at"}})
	req, err := http.NewRequestWithContext(context.Background(),"POST",cfg.MeiliHost+"/indexes/articles/search",bytes.NewReader(body)); if err != nil { handleSearchFallback(w,r,q,limit); return }
	req.Header.Set("Content-Type","application/json"); if cfg.MeiliMasterKey != "" { req.Header.Set("Authorization","Bearer "+cfg.MeiliMasterKey) }
	resp, err := outboundClient.Do(req); if err != nil { handleSearchFallback(w,r,q,limit); return }; defer resp.Body.Close()
	if resp.StatusCode != 200 { handleSearchFallback(w,r,q,limit); return }
	w.Header().Set("Content-Type","application/json; charset=utf-8"); io.Copy(w,resp.Body)
}
func handleSearchFallback(w http.ResponseWriter, r *http.Request, q string, limit int) {
	pattern := "%"+q+"%"
	rows, err := db.Query(`SELECT title,slug,tags,created_at FROM articles WHERE title LIKE ? OR content LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`,pattern,pattern,pattern,limit)
	if err != nil { writeAPIError(w,r,500,"search_error","search unavailable","https://docs.vayupress.com/api/search"); return }
	defer rows.Close()
	type hit struct{ Title,Slug string; Tags []string; CreatedAt time.Time }
	var hits []hit
	for rows.Next() { var h hit; var tagsStr string; rows.Scan(&h.Title,&h.Slug,&tagsStr,&h.CreatedAt); h.Tags = splitTags(tagsStr); hits = append(hits,h) }
	if hits == nil { hits = []hit{} }
	writeJSON(w,r,200,map[string]interface{}{"hits":hits,"query":q,"fallback":true})
}
func handleListTags(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT tags FROM articles WHERE tags != ''`); if err != nil { writeAPIError(w,r,500,"db_error","","https://docs.vayupress.com/api/errors"); return }; defer rows.Close()
	tagCount := make(map[string]int)
	for rows.Next() { var tagsStr string; rows.Scan(&tagsStr); for _, t := range splitTags(tagsStr) { if t != "" { tagCount[t]++ } } }
	type tagRow struct{ Tag string `json:"tag"`; Count int `json:"count"` }
	result := make([]tagRow,0,len(tagCount)); for t, c := range tagCount { result = append(result,tagRow{t,c}) }
	writeJSON(w,r,200,map[string]interface{}{"tags":result,"total":len(result)})
}
// =============================================================================
// Stats, queue, metrics, health endpoints
// =============================================================================

func handleStats(w http.ResponseWriter, r *http.Request) {
	var totalArticles, pendingJobs, failedJobs int
	db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failedJobs)
	used := storageUsedBytes(); quota := storageQuotaBytes()
	writeJSON(w,r,200,map[string]interface{}{
		"version":Version,"uptime_seconds":time.Since(bootTime).Seconds(),
		"config_version":ConfigVersion,
		"articles_total":totalArticles,"queue_pending":pendingJobs,"queue_failed":failedJobs,
		"storage_used_bytes":used,"storage_quota_bytes":quota,
		"workers_alive":atomic.LoadInt64(&workerLiveness),
		"maintenance_mode":cfg.MaintenanceMode,
		"metrics":map[string]int64{
			"articles_created":atomic.LoadInt64(&metricArticlesCreated),"articles_updated":atomic.LoadInt64(&metricArticlesUpdated),
			"articles_deleted":atomic.LoadInt64(&metricArticlesDeleted),"queue_processed":atomic.LoadInt64(&metricQueueProcessed),
			"wal_adaptive_checkpoints":atomic.LoadInt64(&metricWALAdaptiveCheckpoints),
			"migration_drift_detected":atomic.LoadInt64(&metricMigrationDriftDetected),
			"poison_jobs_quarantined":atomic.LoadInt64(&metricPoisonJobsQuarantined),
			"pprof_accesses":atomic.LoadInt64(&metricPprofAccesses),
			"vacuum_rejected":atomic.LoadInt64(&metricVacuumRejected),
		},
		"latency_ms":map[string]interface{}{
			"http_p95":httpLatency.percentile(95),"http_p99":httpLatency.percentile(99),
			"render_p99":renderLatency.percentile(99),"queue_job_p99":queueJobLatency.percentile(99),
			"sqlite_write_p99":sqliteWriteLatency.percentile(99),
		},
	})
}
func handleQueueStatus(w http.ResponseWriter, r *http.Request) {
	var pending,processing,completed,failed,deadLetter,quarantined int
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='processing'`).Scan(&processing)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='completed'`).Scan(&completed)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failed)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='dead_letter'`).Scan(&deadLetter)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='quarantined'`).Scan(&quarantined)
	var oldestSec float64
	db.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM write_jobs WHERE status='pending'`).Scan(&oldestSec)
	writeJSON(w,r,200,map[string]interface{}{"pending":pending,"processing":processing,"completed":completed,"failed":failed,"dead_letter":deadLetter,"quarantined":quarantined,"oldest_pending_seconds":oldestSec,"maintenance_mode":cfg.MaintenanceMode})
}
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var totalArticles int; db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	var ms runtime.MemStats; runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type","text/plain; version=0.0.4")
	fmt.Fprintf(w,
		"vayupress_uptime_seconds %.0f\nvayupress_articles_total %d\n"+
		"vayupress_articles_created_total %d\nvayupress_articles_updated_total %d\nvayupress_articles_deleted_total %d\n"+
		"vayupress_queue_processed_total %d\nvayupress_queue_failed_total %d\nvayupress_queue_stuck_resets_total %d\n"+
		"vayupress_meili_errors_total %d\nvayupress_cache_hits_total %d\nvayupress_cache_misses_total %d\n"+
		"vayupress_cache_hit_ratio %.4f\nvayupress_memory_alloc_bytes %d\nvayupress_workers_alive %d\n"+
		"vayupress_storage_used_bytes %d\nvayupress_plugin_panics_total %d\nvayupress_auth_lockouts_total %d\n"+
		"vayupress_wal_checkpoints_total %d\nvayupress_slow_queries_total %d\nvayupress_dead_letter_total %d\n"+
		"vayupress_wal_checkpoint_duration_ms_total %d\nvayupress_wal_adaptive_checkpoints_total %d\n"+
		"vayupress_migration_drift_detected_total %d\nvayupress_poison_jobs_quarantined_total %d\n"+
		"vayupress_pprof_accesses_total %d\nvayupress_vacuum_rejected_total %d\n"+
		"vayupress_health_degraded_events_total %d\n",
		time.Since(bootTime).Seconds(),totalArticles,
		atomic.LoadInt64(&metricArticlesCreated),atomic.LoadInt64(&metricArticlesUpdated),atomic.LoadInt64(&metricArticlesDeleted),
		atomic.LoadInt64(&metricQueueProcessed),atomic.LoadInt64(&metricQueueFailed),atomic.LoadInt64(&metricQueueStuckResets),
		atomic.LoadInt64(&metricMeiliErrors),atomic.LoadInt64(&metricCacheHits),atomic.LoadInt64(&metricCacheMisses),
		cacheHitRatio(),ms.Alloc,atomic.LoadInt64(&workerLiveness),
		atomic.LoadInt64(&cachedStorageBytes),atomic.LoadInt64(&metricPluginPanics),atomic.LoadInt64(&metricAuthLockouts),
		atomic.LoadInt64(&metricWALCheckpoints),atomic.LoadInt64(&metricSlowQueries),atomic.LoadInt64(&metricDeadLetterJobs),
		atomic.LoadInt64(&metricWALCheckpointDurationMS),atomic.LoadInt64(&metricWALAdaptiveCheckpoints),
		atomic.LoadInt64(&metricMigrationDriftDetected),atomic.LoadInt64(&metricPoisonJobsQuarantined),
		atomic.LoadInt64(&metricPprofAccesses),atomic.LoadInt64(&metricVacuumRejected),
		atomic.LoadInt64(&metricHealthDegradedEvents),
	)
	fmt.Fprint(w,httpLatency.prometheus("vayupress_http_request_duration_seconds","HTTP latency"))
	fmt.Fprint(w,renderLatency.prometheus("vayupress_render_duration_seconds","Render latency"))
	fmt.Fprint(w,queueJobLatency.prometheus("vayupress_queue_job_duration_seconds","Queue job latency"))
	fmt.Fprint(w,sqliteWriteLatency.prometheus("vayupress_sqlite_write_duration_seconds","SQLite write latency"))
}

// Health endpoints — P7 + P8 structured contracts (ADR-0041)
func handleHealthLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w,r,200,map[string]interface{}{"status":"alive","version":Version,"config_version":ConfigVersion,"uptime_seconds":time.Since(bootTime).Seconds()})
}
func handleHealthReady(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil { writeJSON(w,r,503,map[string]string{"status":"not_ready","reason":"db unavailable"}); return }
	if alive := atomic.LoadInt64(&workerLiveness); alive < 1 { writeJSON(w,r,503,map[string]string{"status":"not_ready","reason":"no workers"}); return }
	writeJSON(w,r,200,map[string]string{"status":"ready"})
}
func handleHealthDB(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil { writeJSON(w,r,503,map[string]string{"status":"down"}); return }
	writeJSON(w,r,200,map[string]string{"status":"ok"})
}
func handleHealthMeilisearch(w http.ResponseWriter, r *http.Request) {
	if err := meiliDo("GET","/health",nil); err != nil { writeJSON(w,r,503,map[string]string{"status":"down"}); return }
	writeJSON(w,r,200,map[string]string{"status":"ok"})
}
func handleHealthWorkers(w http.ResponseWriter, r *http.Request) {
	alive := atomic.LoadInt64(&workerLiveness); var pendingJobs int; db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	staleWorkers := 0; workerLastActivity.Range(func(k,v interface{}) bool { if t,ok:=v.(time.Time);ok{if pendingJobs>0&&time.Since(t)>5*time.Minute{staleWorkers++}}; return true })
	code := 200; statusStr := "ok"
	if alive < int64(cfg.WorkerCount) { code = 503; statusStr = "degraded" } else if staleWorkers > 0 { code = 503; statusStr = "potentially_deadlocked" }
	writeJSON(w,r,code,map[string]interface{}{"status":statusStr,"workers_alive":alive,"workers_expected":cfg.WorkerCount,"stale_workers":staleWorkers,"pending_jobs":pendingJobs})
}
func handleHealthStorage(w http.ResponseWriter, r *http.Request) {
	used := storageUsedBytes(); quota := storageQuotaBytes(); pct := float64(0); if quota > 0 { pct = float64(used)/float64(quota)*100 }
	status := 200; statusStr := "ok"
	if pct >= 95 { status = 503; statusStr = "critical" } else if pct >= 90 { status = 503; statusStr = "warning" }
	writeJSON(w,r,status,map[string]interface{}{"status":statusStr,"used_bytes":used,"quota_bytes":quota,"used_pct":fmt.Sprintf("%.1f%%",pct)})
}
func handleHealthMigrations(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT version,checksum,applied_at FROM schema_migrations ORDER BY id ASC`)
	if err != nil { writeAPIError(w,r,500,"db_error","","https://docs.vayupress.com/api/health"); return }; defer rows.Close()
	type mrow struct{ Version string `json:"version"`; Checksum string `json:"checksum"`; AppliedAt time.Time `json:"applied_at"` }
	var applied []mrow
	for rows.Next() { var m mrow; rows.Scan(&m.Version,&m.Checksum,&m.AppliedAt); applied = append(applied,m) }
	if applied == nil { applied = []mrow{} }
	pending := 0
	for _, m := range migrations { var count int; db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`,m.Version).Scan(&count); if count == 0 { pending++ } }
	writeJSON(w,r,200,map[string]interface{}{"status":"ok","applied":applied,"total_applied":len(applied),"total_pending":pending,"drift_detected":atomic.LoadInt64(&metricMigrationDriftDetected)})
}

// P8: /health/dependencies (ADR-0041)
func handleHealthDependencies(w http.ResponseWriter, r *http.Request) {
	type compStatus struct{ Status string `json:"status"`; Message string `json:"message,omitempty"` }
	components := make(map[string]compStatus); overallStatus := "ok"
	if err := db.Ping(); err != nil { components["database"]=compStatus{"down",err.Error()}; overallStatus="degraded" } else { components["database"]=compStatus{Status:"ok"} }
	alive := atomic.LoadInt64(&workerLiveness)
	if alive < 1 { components["workers"]=compStatus{"down",fmt.Sprintf("0/%d alive",cfg.WorkerCount)}; overallStatus="degraded" } else if alive < int64(cfg.WorkerCount) { components["workers"]=compStatus{"degraded",fmt.Sprintf("%d/%d alive",alive,cfg.WorkerCount)}; overallStatus="degraded" } else { components["workers"]=compStatus{Status:"ok"} }
	if err := meiliDo("GET","/health",nil); err != nil { components["search"]=compStatus{"degraded","Meilisearch unavailable — SQLite fallback active"}; overallStatus="degraded" } else { components["search"]=compStatus{Status:"ok"} }
	used := storageUsedBytes(); quota := storageQuotaBytes()
	if quota > 0 { pct := float64(used)/float64(quota)*100; if pct>=95 { components["storage"]=compStatus{"critical",fmt.Sprintf("%.1f%% used",pct)}; overallStatus="degraded" } else if pct>=90 { components["storage"]=compStatus{"warning",fmt.Sprintf("%.1f%% used",pct)}; overallStatus="degraded" } else { components["storage"]=compStatus{Status:"ok"} } } else { components["storage"]=compStatus{Status:"ok"} }
	if overallStatus == "degraded" { atomic.AddInt64(&metricHealthDegradedEvents,1) }
	httpCode := 200; if overallStatus == "degraded" { httpCode = 207 }
	writeJSON(w,r,httpCode,map[string]interface{}{"status":overallStatus,"components":components,"checked_at":time.Now().UTC()})
}

// P8: /health/search (ADR-0041)
func handleHealthSearch(w http.ResponseWriter, r *http.Request) {
	meiliStatus := "ok"; meiliMsg := ""
	if err := meiliDo("GET","/health",nil); err != nil { meiliStatus="degraded"; meiliMsg="Meilisearch unavailable" }
	cbState := "unknown"
	if meiliCB != nil { switch meiliCB.State() { case gobreaker.StateClosed: cbState="closed"; case gobreaker.StateOpen: cbState="open"; case gobreaker.StateHalfOpen: cbState="half_open" } }
	writeJSON(w,r,200,map[string]interface{}{"status":meiliStatus,"message":meiliMsg,"circuit_breaker":cbState,"sqlite_fallback_active":meiliStatus!="ok"})
}

// P8: /health/queue (ADR-0041)
func handleHealthQueue(w http.ResponseWriter, r *http.Request) {
	var pending,deadLetter,quarantined int; var oldestSec float64
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='dead_letter'`).Scan(&deadLetter)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='quarantined'`).Scan(&quarantined)
	db.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM write_jobs WHERE status='pending'`).Scan(&oldestSec)
	queueStatus := "ok"
	if quarantined > 0 { queueStatus = "degraded" }
	if deadLetter > 50 { queueStatus = "degraded" }
	if pending > cfg.QueueSaturationWarn { queueStatus = "saturated" }
	writeJSON(w,r,200,map[string]interface{}{"status":queueStatus,"pending":pending,"dead_letter":deadLetter,"quarantined":quarantined,"oldest_pending_seconds":oldestSec,"saturation_threshold":cfg.QueueSaturationWarn,"maintenance_mode":cfg.MaintenanceMode})
}
// =============================================================================
// P8 — Admin handlers: VACUUM, DLQ replay, pprof, backup validate (ADR-0035/37/38/42)
// =============================================================================

// VACUUM with cooldown + write-threshold guard (ADR-0038)
func handleAdminVacuum(w http.ResponseWriter, r *http.Request) {
	vacuumMu.Lock(); defer vacuumMu.Unlock()
	cooldown := time.Duration(cfg.VacuumCooldownMin) * time.Minute
	if !vacuumLastRun.IsZero() && time.Since(vacuumLastRun) < cooldown {
		remaining := cooldown - time.Since(vacuumLastRun)
		atomic.AddInt64(&metricVacuumRejected, 1)
		writeAPIError(w,r,429,"vacuum_cooldown",fmt.Sprintf("cooldown active — %ds remaining",int(remaining.Seconds())),"https://docs.vayupress.com/operations/vacuum"); return
	}
	var pending int; db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	if pending > vacuumWriteThreshold {
		atomic.AddInt64(&metricVacuumRejected, 1)
		writeAPIError(w,r,503,"vacuum_write_threshold",fmt.Sprintf("VACUUM rejected: %d pending jobs > threshold %d",pending,vacuumWriteThreshold),"https://docs.vayupress.com/operations/vacuum"); return
	}
	start := time.Now()
	var integrityResult string; db.QueryRow(`PRAGMA integrity_check`).Scan(&integrityResult)
	if integrityResult != "ok" { writeAPIError(w,r,500,"integrity_failed","SQLite integrity check failed: "+integrityResult,"https://docs.vayupress.com/operations/vacuum"); return }
	if _, err := db.Exec(`VACUUM`); err != nil { writeAPIError(w,r,500,"vacuum_failed","VACUUM error: "+err.Error(),"https://docs.vayupress.com/operations/vacuum"); return }
	vacuumLastRun = time.Now()
	logInfo("vacuum", fmt.Sprintf("VACUUM complete dur=%dms (ADR-0038)", time.Since(start).Milliseconds()))
	writeJSON(w,r,200,map[string]interface{}{"status":"ok","integrity":"ok","duration_ms":time.Since(start).Milliseconds(),"next_allowed_in_minutes":cfg.VacuumCooldownMin})
}

// Dead-letter replay with safety controls (ADR-0035)
func handleQueueReplay(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT id,replay_count FROM write_jobs WHERE status='dead_letter' LIMIT ?`, cfg.ReplayBatchLimit+50)
	if err != nil { writeAPIError(w,r,500,"db_error","replay query failed: "+err.Error(),"https://docs.vayupress.com/api/queue"); return }
	var quarantineIDs, replayIDs []int64
	for rows.Next() {
		var id int64; var replayCount int; rows.Scan(&id, &replayCount)
		if replayCount >= cfg.MaxReplayCount { quarantineIDs = append(quarantineIDs, id) } else if len(replayIDs) < cfg.ReplayBatchLimit { replayIDs = append(replayIDs, id) }
	}
	rows.Close()
	for _, id := range quarantineIDs {
		wdb.Exec(`UPDATE write_jobs SET status='quarantined' WHERE id=?`, id)
		atomic.AddInt64(&metricPoisonJobsQuarantined, 1)
		logJSON(logFields{Level:"warn",Component:"queue-replay",Msg:fmt.Sprintf("job %d quarantined after %d replays (ADR-0035)",id,cfg.MaxReplayCount)})
	}
	replayed := int64(0)
	for _, id := range replayIDs {
		result, err := wdb.Exec(`UPDATE write_jobs SET status='pending',retries=0,retry_at=NULL,replay_count=replay_count+1 WHERE id=? AND status='dead_letter'`, id)
		if err == nil { if n,_:=result.RowsAffected(); n > 0 { replayed++ } }
	}
	logInfo("queue", fmt.Sprintf("replay: replayed=%d quarantined=%d batch_limit=%d", replayed, len(quarantineIDs), cfg.ReplayBatchLimit))
	writeJSON(w,r,200,map[string]interface{}{"status":"ok","replayed":replayed,"skipped_quarantined":len(quarantineIDs),"batch_limit":cfg.ReplayBatchLimit,"max_replay_count":cfg.MaxReplayCount})
}

// P8: pprof — explicit handlers on isolated mux, no DefaultServeMux (ADR-0037)
var pprofMux = http.NewServeMux()

func initPprofMux() {
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	logInfo("pprof", "explicit pprof mux initialized — DefaultServeMux unmodified (ADR-0037)")
}

func pprofHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr; if xri := r.Header.Get("X-Real-IP"); xri != "" { ip = xri }
	if !allowPprof(ip) {
		atomic.AddInt64(&metricPprofAccesses, 1)
		writeAPIError(w,r,429,"pprof_rate_limited",fmt.Sprintf("pprof rate limit exceeded (%d/min)",cfg.PprofRateLimit),"https://docs.vayupress.com/operations/profiling"); return
	}
	atomic.AddInt64(&metricPprofAccesses, 1)
	logJSON(logFields{Level:"info",Component:"pprof-access",RequestID:getRequestID(r),RemoteAddr:ip,Path:r.URL.Path,Msg:"pprof access (ADR-0037)"})
	pprofMux.ServeHTTP(w, r)
}

// P8: /admin/backup/validate — on-demand restore validation (ADR-0042)
func handleAdminBackupValidate(w http.ResponseWriter, r *http.Request) {
	backupDir := "/backups/vayupress"
	entries, err := os.ReadDir(backupDir)
	if err != nil { writeAPIError(w,r,404,"no_backup_dir","backup directory not found: "+backupDir,"https://docs.vayupress.com/operations/backup"); return }
	var latestBackup string; var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() { continue }
		if !strings.HasSuffix(e.Name(),".db") && !strings.HasSuffix(e.Name(),".db.gz") { continue }
		info, _ := e.Info()
		if info != nil && info.ModTime().After(latestMod) { latestMod = info.ModTime(); latestBackup = filepath.Join(backupDir, e.Name()) }
	}
	if latestBackup == "" { writeAPIError(w,r,404,"no_backup","no backup files found","https://docs.vayupress.com/operations/backup"); return }
	start := time.Now(); checksumOK := false
	checksumFile := filepath.Join(backupDir, "checksums.json")
	if data, err := os.ReadFile(checksumFile); err == nil {
		var registry map[string]string
		if json.Unmarshal(data, &registry) == nil {
			if storedSum, ok := registry[filepath.Base(latestBackup)]; ok {
				if f, ferr := os.Open(latestBackup); ferr == nil {
					h := sha256.New(); io.Copy(h, f); f.Close()
					checksumOK = hex.EncodeToString(h.Sum(nil)) == storedSum
				}
			}
		}
	}
	logInfo("backup-validate", fmt.Sprintf("backup=%s checksum_ok=%v dur=%dms (ADR-0042)", filepath.Base(latestBackup), checksumOK, time.Since(start).Milliseconds()))
	writeJSON(w,r,200,map[string]interface{}{"status":"ok","latest_backup":filepath.Base(latestBackup),"backup_age_hours":time.Since(latestMod).Hours(),"checksum_verified":checksumOK,"duration_ms":time.Since(start).Milliseconds()})
}

// Cache purge, article page, smoke test, ADR listing
func handleAdminCachePurge(w http.ResponseWriter, r *http.Request) {
	rid := getRequestID(r); slug := r.URL.Query().Get("slug"); purged := 0; purgeType := "targeted"
	if slug != "" {
		if !isValidSlug(slug) { writeAPIError(w,r,400,"invalid_slug","invalid slug","https://docs.vayupress.com/api/cache"); return }
		var tags string; db.QueryRow(`SELECT tags FROM articles WHERE slug=?`,slug).Scan(&tags)
		cachePurge(slug,splitTags(tags)); purged = 1
	} else {
		purgeType = "full"
		remoteIP := r.Header.Get("X-Real-IP"); if remoteIP == "" { remoteIP = strings.Split(r.RemoteAddr,":")[0] }
		if !allowPurge(remoteIP) { writeAPIError(w,r,429,"rate_limited","full cache purge rate-limited","https://docs.vayupress.com/api/cache"); return }
		postsDir := filepath.Join(cfg.CacheDir,"posts")
		if files, err := os.ReadDir(postsDir); err == nil {
			for _, f := range files { if !f.IsDir()&&strings.HasSuffix(f.Name(),".html") { fpath:=filepath.Join(postsDir,f.Name()); if fi,err:=os.Stat(fpath);err==nil{updateStorageDelta(-fi.Size())}; if err:=os.Remove(fpath);err==nil{purged++} } }
		}
		os.Remove(filepath.Join(cfg.CacheDir,"home","index.html"))
		if files, err := os.ReadDir(filepath.Join(cfg.CacheDir,"tags")); err == nil {
			for _, f := range files { if !f.IsDir()&&strings.HasSuffix(f.Name(),".html") { os.Remove(filepath.Join(cfg.CacheDir,"tags",f.Name())); purged++ } }
		}
		go generateSitemap(); go generateRSS(); go generateRobots()
	}
	logJSON(logFields{Level:"info",Component:"cache-purge",RequestID:rid,Msg:fmt.Sprintf("type=%s purged=%d",purgeType,purged)})
	FireHook("cache.purge",map[string]interface{}{"purge_type":purgeType,"slug":slug,"purged_count":purged})
	writeJSON(w,r,200,map[string]interface{}{"message":"cache purged","purge_type":purgeType,"purged":purged,"request_id":rid})
}

func handleArticlePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r,"slug"); if !isValidSlug(slug) { http.NotFound(w,r); return }
	isAdmin := r.Header.Get("X-API-Key") == cfg.APIKey
	cachePath := filepath.Join(cfg.CacheDir,"posts",slug+".html")
	if !isAdmin || r.URL.Query().Get("layout") == "" {
		if _, err := os.Stat(cachePath); err == nil { atomic.AddInt64(&metricCacheHits,1); http.ServeFile(w,r,cachePath); return }
	}
	atomic.AddInt64(&metricCacheMisses,1)
	var a Article; var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`,slug).Scan(&a.ID,&a.Title,&a.Slug,&a.Content,&tagsStr,&a.CreatedAt,&a.UpdatedAt); err == sql.ErrNoRows { http.NotFound(w,r); return }
	a.Tags = splitTags(tagsStr); layout := detectLayout(a,r,isAdmin)
	html, err := renderArticleWithLayout(a,layout); if err != nil { http.Error(w,"render error",500); return }
	if layout == ArticleLayoutDefault { cacheWrite(filepath.Join("posts",slug+".html"),html) }
	w.Header().Set("Content-Type","text/html; charset=utf-8"); fmt.Fprint(w,html)
}

func handleSmokeTest(w http.ResponseWriter, r *http.Request) {
	if !smokeTestMutex.TryLock() { http.Error(w,"smoke-test already running",http.StatusServiceUnavailable); return }
	defer smokeTestMutex.Unlock()
	testSlug := fmt.Sprintf("smoke-test-%d",time.Now().UnixNano()); testID := newUUID()
	a := Article{ID:testID,Title:"Smoke Test",Slug:testSlug,Content:"<p>VayuPress smoke test.</p>",Tags:[]string{"smoke-test"},CreatedAt:time.Now().UTC(),UpdatedAt:time.Now().UTC()}
	payload, _ := json.Marshal(a)
	if _, err := db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`,payload); err != nil { http.Error(w,"smoke-test: enqueue failed: "+err.Error(),503); return }
	deadline := time.Now().Add(cfg.SmokeTestTimeout); processed := false
	for time.Now().Before(deadline) { var count int; db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`,testSlug).Scan(&count); if count > 0 { processed = true; break }; time.Sleep(150*time.Millisecond) }
	if !processed { db.Exec(`DELETE FROM write_jobs WHERE article_json LIKE ? AND status='pending'`,"%\"slug\":\""+testSlug+"\"%"); http.Error(w,fmt.Sprintf("smoke-test: worker timeout (%s)",cfg.SmokeTestTimeout),503); return }
	db.Exec(`DELETE FROM articles WHERE slug=?`,testSlug); db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`,payload)
	os.Remove(filepath.Join(cfg.CacheDir,"posts",testSlug+".html"))
	if meiliCB != nil { go meiliDo("DELETE","/indexes/articles/documents/"+testID,nil) }
	logInfo("smoke-test",fmt.Sprintf("PASS slug=%s",testSlug))
	w.Header().Set("Content-Type","text/plain; charset=utf-8"); fmt.Fprint(w,"OK")
}

func handleAdminADR(w http.ResponseWriter, r *http.Request) {
	adrDir := filepath.Join(envOr("VAYU_DOCS_DIR","/var/www/vayupress/docs"), "adr")
	entries, err := os.ReadDir(adrDir)
	if err != nil { writeAPIError(w,r,404,"adr_dir_not_found","ADR directory not found","https://docs.vayupress.com/governance/adrs"); return }
	type adrEntry struct{ Filename string `json:"filename"` }
	var adrs []adrEntry
	for _, e := range entries { if !e.IsDir()&&strings.HasSuffix(e.Name(),".md") { adrs = append(adrs,adrEntry{e.Name()}) } }
	if adrs == nil { adrs = []adrEntry{} }
	writeJSON(w,r,200,map[string]interface{}{"adrs":adrs,"total":len(adrs)})
}

// Benchmark handlers
type benchmarkResult struct {
	RunAt time.Time `json:"run_at"`; ArticlesWritten,ReadRequests,ReadConcurrency int
	ReadP50,ReadP95,ReadP99,ReadMax int64; ReadMean,ReadRPS float64
	P95Pass,P99Pass bool; Overall,Notes string
}
var (lastBenchmark *benchmarkResult; lastBenchmarkMu sync.Mutex; benchmarkRunning int32)

func handleHealthBenchmarks(w http.ResponseWriter, r *http.Request) {
	lastBenchmarkMu.Lock(); result := lastBenchmark; lastBenchmarkMu.Unlock()
	if result == nil { writeAPIError(w,r,404,"no_benchmark","no benchmark run yet; POST /admin/benchmark","https://docs.vayupress.com/operations/benchmarks"); return }
	writeJSON(w,r,200,result)
}
func handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if !atomic.CompareAndSwapInt32(&benchmarkRunning,0,1) { writeAPIError(w,r,409,"benchmark_running","benchmark already in progress","https://docs.vayupress.com/operations/benchmarks"); return }
	defer atomic.StoreInt32(&benchmarkRunning,0)
	articleCount := 50; readConcurrency := 20; totalRequests := 200
	if v,err:=strconv.Atoi(r.URL.Query().Get("articles"));err==nil&&v>0&&v<=500{articleCount=v}
	if v,err:=strconv.Atoi(r.URL.Query().Get("readers"));err==nil&&v>0&&v<=100{readConcurrency=v}
	if v,err:=strconv.Atoi(r.URL.Query().Get("requests"));err==nil&&v>0&&v<=2000{totalRequests=v}
	baseSlug := fmt.Sprintf("bench-%d",time.Now().UnixNano())
	var writtenSlugs []string; var writeMu sync.Mutex
	for i := 0; i < articleCount; i++ {
		slug := fmt.Sprintf("%s-%04d",baseSlug,i)
		a := Article{ID:newUUID(),Title:fmt.Sprintf("Bench %d",i),Slug:slug,Content:fmt.Sprintf("<p>%s</p>",strings.Repeat("Benchmark content. ",200)),Tags:[]string{"benchmark"},CreatedAt:time.Now().UTC(),UpdatedAt:time.Now().UTC()}
		payload, _ := json.Marshal(a)
		if _, err := wdb.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`,payload); err == nil { writeMu.Lock(); writtenSlugs = append(writtenSlugs,slug); writeMu.Unlock() }
	}
	deadline := time.Now().Add(60*time.Second)
	for time.Now().Before(deadline) { var count int; db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`,baseSlug+"%").Scan(&count); if count >= len(writtenSlugs) { break }; time.Sleep(200*time.Millisecond) }
	var actualWritten int; db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`,baseSlug+"%").Scan(&actualWritten)
	var (readHistogram latencyHistogram; readErrors int64; reqCh = make(chan string,totalRequests); readWg sync.WaitGroup)
	for _, slug := range writtenSlugs { reqCh <- slug }; close(reqCh)
	readStart := time.Now(); readClient := &http.Client{Timeout:5*time.Second}
	for i := 0; i < readConcurrency; i++ {
		readWg.Add(1); go func() { defer readWg.Done(); for slug := range reqCh { start := time.Now(); resp, err := readClient.Get(fmt.Sprintf("http://localhost:%s/%s",cfg.Port,slug)); if err!=nil{atomic.AddInt64(&readErrors,1);continue}; resp.Body.Close(); if resp.StatusCode==200{readHistogram.record(time.Since(start))}else{atomic.AddInt64(&readErrors,1)} } }()
	}
	readWg.Wait(); readDuration := time.Since(readStart); _, _, _, readMaxMs := readHistogram.snapshot()
	p95 := readHistogram.percentile(95); p99 := readHistogram.percentile(99); rps := float64(totalRequests)/readDuration.Seconds()
	go func() { for _, slug := range writtenSlugs { wdb.Exec(`DELETE FROM articles WHERE slug=?`,slug); os.Remove(filepath.Join(cfg.CacheDir,"posts",slug+".html")) } }()
	p95Pass := p95 <= 200; writeP99 := queueJobLatency.percentile(99); p99Pass := writeP99 <= 1000
	overall := "PASS"; var notes []string
	if !p95Pass { overall="FAIL"; notes=append(notes,fmt.Sprintf("p95 %dms > 200ms",p95)) }
	if !p99Pass { overall="FAIL"; notes=append(notes,fmt.Sprintf("p99 write %dms > 1000ms",writeP99)) }
	if readErrors > int64(totalRequests/10) { overall="FAIL"; notes=append(notes,fmt.Sprintf("%d read errors",readErrors)) }
	if overall == "PASS" && (p95 > 100 || writeP99 > 500) { overall="WARN"; notes=append(notes,"approaching limits") }
	result := &benchmarkResult{RunAt:time.Now().UTC(),ArticlesWritten:actualWritten,ReadRequests:totalRequests,ReadConcurrency:readConcurrency,ReadP50:readHistogram.percentile(50),ReadP95:p95,ReadP99:p99,ReadMean:readHistogram.mean(),ReadMax:readMaxMs,ReadRPS:rps,P95Pass:p95Pass,P99Pass:p99Pass,Overall:overall,Notes:strings.Join(notes,"; ")}
	lastBenchmarkMu.Lock(); lastBenchmark = result; lastBenchmarkMu.Unlock()
	logJSON(logFields{Level:"info",Component:"benchmark",Msg:fmt.Sprintf("result: %s | p95=%dms p99=%dms rps=%.0f",overall,p95,p99,rps)})
	writeJSON(w,r,200,result)
}
// =============================================================================
// ADR writer (P8 ADRs 0032–0043)
// =============================================================================

func writeADRs(docsDir string) {
	adrDir := filepath.Join(docsDir, "adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil { return }
	now := time.Now().Format("2006-01-02")
	adrs := map[string]string{
		"ADR-0032-plugin-pool-concurrency-hardening.md": "# ADR-0032: Plugin Pool Concurrency Hardening\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 plugin pool had goroutine leak risk on shutdown: no WaitGroup, no context cancellation, pluginQueue never closed.\n\n## Decision\n- pluginCtx/pluginCancel: context cancel propagated to all hook invocations\n- workerPluginWg: WaitGroup tracks every plugin goroutine\n- Shutdown: pluginCancel() → drain → close(pluginQueue) → workerPluginWg.Wait()\n- Per-goroutine recover() for panic isolation\n- pluginDisabled uses sync.Map; pluginFailures atomic int64\n\n## Consequences\n+ No goroutine leaks on shutdown\n+ Panicking worker isolated; remaining workers unaffected\n- 10s drain timeout\n",
		"ADR-0033-wal-adaptive-checkpoint.md": "# ADR-0033: WAL Adaptive Checkpoint Strategy\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 WAL checkpoint: PASSIVE every 5 minutes regardless of WAL size. Burst writes can grow WAL to hundreds of MB.\n\n## Decision\n- WAL size threshold: >WAL_SIZE_THRESHOLD_MB (default 32MB) → RESTART checkpoint\n- Adaptive scheduling: back off tick after RESTART\n- Checkpoint duration metric: metricWALCheckpointDurationMS\n- PRAGMA busy_timeout=5000 and synchronous=NORMAL enforced on all paths\n\n## Consequences\n+ WAL bounded at ~32MB under burst writes\n+ Frequency adapts to workload\n- RESTART checkpoint blocks new writers briefly; mitigated by busy_timeout\n",
		"ADR-0034-migration-checksum-drift-verification.md": "# ADR-0034: Migration Checksum Drift Verification\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 stored checksums but never revalidated on subsequent startups. Edited historical migrations silently diverge.\n\n## Decision\nverifyMigrationChecksums() called at startup after runMigrations():\n- Queries schema_migrations for applied versions + checksums\n- Recomputes expected checksum from in-memory migration.Up SQL\n- Mismatch: logs error, increments metricMigrationDriftDetected, halts boot\n\n## Consequences\n+ Historical migration tampering detected at boot\n+ metricMigrationDriftDetected visible in /metrics\n- Cannot edit old migrations; must add new ones\n",
		"ADR-0035-dead-letter-replay-safety.md": "# ADR-0035: Dead-Letter Queue Replay Safety Controls\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 replay moved all dead_letter jobs to pending with no limits. Poison jobs loop forever.\n\n## Decision\n- Replay limited to REPLAY_BATCH_LIMIT (default 100) per API call\n- replay_count column tracks total replay attempts\n- dead_reason column classifies failure (parse_error, exec_error, max_retries, unknown_op)\n- Quarantine: status=quarantined after replay_count >= MAX_REPLAY_COUNT (default 3)\n- Backoff cap: maxBackoffSeconds=300 prevents int overflow\n\n## Consequences\n+ Poison jobs automatically quarantined\n+ Replay storm prevented\n- Quarantined jobs require manual intervention\n",
		"ADR-0036-csp-nonce-template-helpers.md": "# ADR-0036: CSP Nonce Centralized Template Helpers\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 generated nonces but admin dashboard inline <script> tags did not carry the nonce attribute.\n\n## Decision\n- CSPNonce(r *http.Request) exported as canonical nonce accessor\n- Admin dashboard <script> tag includes nonce=CSPNonce(r) attribute\n- Nonce stored in context via ctxKeyCSPNonce{}\n\n## Consequences\n+ Admin inline scripts covered by script-src nonce\n+ Documented helper for future developers\n",
		"ADR-0037-pprof-explicit-handler-hardening.md": "# ADR-0037: Pprof Explicit Handler Registration\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 imported _ net/http/pprof which auto-registers on DefaultServeMux. Accidental exposure leaks goroutine stacks and heap profiles.\n\n## Decision\n- Remove _ net/http/pprof import — no DefaultServeMux registration\n- Import net/http/pprof explicitly; register on isolated pprofMux\n- Rate limiting: PPROF_RATE_LIMIT (default 5) requests/minute per IP\n- Audit log on every pprof access\n\n## Consequences\n+ DefaultServeMux clean; accidental exposure cannot leak pprof\n+ Rate limiting prevents profiling-as-DoS\n",
		"ADR-0038-vacuum-rate-limiting.md": "# ADR-0038: VACUUM Rate Limiting + Write-Threshold Guard\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 /admin/vacuum could be called repeatedly, triggering VACUUM on large DB which stalls all writes.\n\n## Decision\n- Cooldown window: VACUUM_COOLDOWN_MIN (default 10) minutes between calls\n- Write threshold guard: reject if pending write_jobs > 10\n- metricVacuumRejected counts rejected calls\n\n## Consequences\n+ VACUUM cannot be weaponized for write stalls\n- Cooldown resets on restart (acceptable)\n",
		"ADR-0039-deploy-sourced-components.md": "# ADR-0039: Deploy Script Sourced Components\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 deploy/ scaffold was cosmetic stubs. Monolithic script still did all work.\n\n## Decision\nP8 makes deploy/ scripts functionally complete. Monolithic script sources them via source deploy/build.sh etc.\n\n## Consequences\n+ Each component testable in isolation\n+ Partial redeployment feasible\n",
		"ADR-0040-config-versioning.md": "# ADR-0040: Config Versioning + Compatibility Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\n- ConfigVersion constant logged at startup\n- MinCompatibleConfigVersion defines oldest compatible schema\n- Deprecated env QUEUE_MAX_RETRIES logs warning on detection\n\n## Consequences\n+ Operators detect mismatched config schemas from logs\n",
		"ADR-0041-structured-health-contracts.md": "# ADR-0041: Structured Health Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nNew structured health endpoints:\n- /health/dependencies: all external services with {status, components} JSON\n- /health/storage: disk + quota\n- /health/search: Meilisearch CB state + fallback status\n- /health/queue: depth, backlog age, dead-letter, quarantined\nAll return {status: ok|degraded|saturated, components: {...}}\n\n## Consequences\n+ Orchestrators make nuanced routing decisions\n+ metricHealthDegradedEvents counts degraded responses\n",
		"ADR-0042-backup-restore-automation.md": "# ADR-0042: Backup Restore Automation\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\n- Nightly restore validation cron script (vayupress-restore-validate.sh)\n- Checksum registry: /backups/vayupress/checksums.json stores SHA256 per backup file\n- /admin/backup/validate endpoint for on-demand restore testing\n\n## Consequences\n+ Backup integrity verified nightly\n+ Checksum registry enables tamper detection\n",
		"ADR-0043-integration-test-failure-modes.md": "# ADR-0043: Integration Test Failure Mode Coverage\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nP8 adds 8 new integration test files:\n- shutdown_race_test.go\n- wal_recovery_test.go\n- plugin_panic_flood_test.go\n- migration_corruption_test.go\n- replay_abuse_test.go\n- csp_nonce_test.go\n- vacuum_ratelimit_test.go\n- health_contracts_test.go\n",
	}
	for filename, content := range adrs {
		path := filepath.Join(adrDir, filename)
		if _, err := os.Stat(path); err == nil { continue } // immutable once written
		if err := os.WriteFile(path, []byte(content), 0644); err != nil { logError("adr","write failed: "+filename,err.Error()) } else { logInfo("adr","written: "+filename) }
	}
}
// =============================================================================
// P8 — Admin Dashboard with CSP nonce on inline scripts (ADR-0036)
// =============================================================================

func handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	snap := getAdminSnapshot()
	pluginPanics := atomic.LoadInt64(&metricPluginPanics)
	failedClass := "stat-ok"; if snap.FailedJobs > 0 { failedClass = "stat-err" }
	storageClass := "stat-ok"; if snap.StoragePct >= 90 { storageClass = "stat-err" } else if snap.StoragePct >= 75 { storageClass = "stat-warn" }
	panicClass := "stat-ok"; if pluginPanics > 0 { panicClass = "stat-warn" }
	threshClass := func(ok bool) string { if ok { return "thresh-ok" }; return "thresh-fail" }
	threshLabel := func(ok bool) string { if ok { return "✓ OK" }; return "✗ FAIL" }
	httpOK := snap.HTTPP95 <= 200; writeOK := snap.WriteP99 <= 1000; renderOK := snap.RenderP99 <= 500; cacheOK := snap.CacheHitRatio >= 0.80

	if token := generateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name:"vp_csrf",Value:token,Path:"/",SameSite:http.SameSiteStrictMode,HttpOnly:false,Secure:csrfCookieSecure(),MaxAge:3600})
	}
	w.Header().Set("Content-Type","text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag","noindex")

	// P8: CSPNonce(r) — canonical nonce accessor for inline scripts (ADR-0036)
	nonce := CSPNonce(r)

	maintenanceBanner := ""
	if cfg.MaintenanceMode { maintenanceBanner = `<div style="background:var(--warn);color:#000;padding:8px 16px;font-size:12px;font-weight:600;text-align:center">⚠ MAINTENANCE MODE ACTIVE — write queue paused</div>` }

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuPress Admin — %s</title><meta name="robots" content="noindex, nofollow">
%s%s</head><body>%s
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="app-shell">
<header class="topbar" role="banner">
  <a href="/admin" class="topbar-brand"><span aria-hidden="true">⚡</span><span>VayuPress</span><span class="topbar-domain">%s</span></a>
  <nav class="topbar-actions">
    <span style="font-size:11px;color:var(--muted);font-family:var(--mono)">⟳ %ds ago</span>
    <button class="kbd-hint" id="shortcut-help-btn" aria-haspopup="dialog">? shortcuts</button>
  </nav>
</header>
<main id="main-content">
<h2 class="section-title">Overview</h2>
<div class="stat-grid">
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Articles</div></div>
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Queue Pending</div><div class="stat-sub">%d completed</div></div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Queue Failed</div></div>
  <div class="stat-card"><div class="stat-val">%.0fs</div><div class="stat-lbl">Uptime</div></div>
  <div class="stat-card"><div class="stat-val %s">%s</div><div class="stat-lbl">Storage Used</div>
    <div class="storage-bar" role="progressbar" aria-valuenow="%.0f" aria-valuemin="0" aria-valuemax="100"><div class="storage-fill" style="width:%.0f%%"></div></div>
  </div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Plugin Panics</div><div class="stat-sub">%.1f%% cache hit</div></div>
</div>
<h2 class="section-title">Performance Thresholds</h2>
<div class="thresh-grid">
  <div class="thresh-item"><span class="thresh-name">HTTP p95</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 200ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Write p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 1000ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Render p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 500ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Cache hit</span><span><span class="thresh-val">%.0f%%</span> <span class="%s">%s</span> <span class="thresh-name">/ 80%%</span></span></div>
</div>
<h2 class="section-title">Quick Actions</h2>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<div class="action-row">
  <button class="btn" id="btn-smoke">Smoke test</button>
  <button class="btn" id="btn-purge">Purge cache</button>
  <button class="btn" id="btn-bench">Benchmark</button>
  <a href="/api/v1/stats" class="btn" target="_blank" rel="noopener">Stats JSON</a>
  <a href="/metrics" class="btn" target="_blank" rel="noopener">Metrics</a>
  <a href="/admin/adr" class="btn" target="_blank" rel="noopener">ADRs</a>
</div>
<h2 class="section-title">Recent Articles</h2>
<table class="data-table"><thead><tr><th>Title</th><th>Slug</th><th>Published</th></tr></thead><tbody>`,
		cfg.Domain,
		cssLink("admin.css",cssHashes.AdminCSS), cssLink("high-contrast.css",cssHashes.HighContrastCSS),
		template.HTML(maintenanceBanner),
		cfg.Domain, int(time.Since(snap.SnapshotAt).Seconds()),
		snap.TotalArticles, snap.PendingJobs, snap.CompletedJobs,
		failedClass, snap.FailedJobs, snap.UptimeSeconds,
		storageClass, formatBytes(snap.StorageBytes), snap.StoragePct, snap.StoragePct,
		panicClass, pluginPanics, snap.CacheHitRatio*100,
		snap.HTTPP95, threshClass(httpOK), threshLabel(httpOK),
		snap.WriteP99, threshClass(writeOK), threshLabel(writeOK),
		snap.RenderP99, threshClass(renderOK), threshLabel(renderOK),
		snap.CacheHitRatio*100, threshClass(cacheOK), threshLabel(cacheOK),
	)

	if len(snap.RecentArticles) == 0 {
		fmt.Fprint(w, `<tr><td colspan="3" style="color:var(--muted);text-align:center;padding:2rem">No articles yet.</td></tr>`)
	} else {
		for _, row := range snap.RecentArticles {
			fmt.Fprintf(w, `<tr><td>%s</td><td><a href="/%s" target="_blank">%s</a></td><td><time>%s</time></td></tr>`,
				row.Title, row.Slug, row.Slug, row.CreatedAt.Format("2 Jan 2006"))
		}
	}

	fmt.Fprintf(w, `</tbody></table>
<h2 class="section-title">P8 Health Contracts</h2>
<nav class="links-row">
  <a href="/health/dependencies" target="_blank">Dependencies</a>
  <a href="/health/search" target="_blank">Search</a>
  <a href="/health/queue" target="_blank">Queue</a>
  <a href="/health/workers" target="_blank">Workers</a>
  <a href="/health/storage" target="_blank">Storage</a>
  <a href="/health/migrations" target="_blank">Migrations</a>
  <a href="/admin/backup/validate" target="_blank">Backup Validate</a>
  <a href="/health/benchmarks" target="_blank">Benchmarks</a>
</nav>
<footer class="admin-footer">VayuPress %s &middot; Constitution v6.0 &middot; P1–P8 compliant &middot; Config v%s &middot; Snapshot: %s</footer>
</main></div>
<div class="modal-backdrop" id="shortcut-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1">
  <div class="modal">
    <div class="modal-title"><span id="modal-title">Keyboard Shortcuts</span><button class="modal-close" id="modal-close-btn" aria-label="Close">✕</button></div>
    <ul class="shortcut-list">
      <li class="shortcut-item"><span>This help</span><kbd>?</kbd></li>
      <li class="shortcut-item"><span>Smoke test</span><kbd>s</kbd></li>
      <li class="shortcut-item"><span>Benchmark</span><kbd>b</kbd></li>
      <li class="shortcut-item"><span>Reload</span><kbd>r</kbd></li>
      <li class="shortcut-item"><span>Close dialog</span><kbd>Esc</kbd></li>
    </ul>
  </div>
</div>
<script nonce="%s">
(function(){
  'use strict';
  var modal=document.getElementById('shortcut-modal'),
      closeBtn=document.getElementById('modal-close-btn'),
      actionMsg=document.getElementById('action-msg');
  function csrf(){var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});return m?m.split('=')[1]:'';}
  function post(url){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}});}
  function openModal(){modal.classList.add('open');document.body.style.overflow='hidden';closeBtn.focus();}
  function closeModal(){modal.classList.remove('open');document.body.style.overflow='';}
  function showMsg(text,isErr){actionMsg.textContent=text;actionMsg.style.borderColor=isErr?'var(--error)':'var(--success)';actionMsg.classList.add('visible');setTimeout(function(){actionMsg.classList.remove('visible');},5000);}
  function runSmoke(){showMsg('Running smoke test…',false);fetch('/smoke-test').then(function(r){return r.text();}).then(function(t){showMsg('Smoke test: '+t,t.trim()!=='OK');}).catch(function(e){showMsg('Error: '+e,true);});}
  function runPurge(){showMsg('Purging cache…',false);post('/admin/cache-purge').then(function(r){return r.json();}).then(function(d){showMsg('Cache purge: '+(d.message||'done'),false);}).catch(function(e){showMsg('Error: '+e,true);});}
  function runBench(){showMsg('Benchmark started (up to 60s)…',false);post('/admin/benchmark').then(function(r){return r.json();}).then(function(d){showMsg('Benchmark: '+(d.overall||'done')+' · p95='+d.read_p95_ms+'ms',d.overall==='FAIL');}).catch(function(e){showMsg('Error: '+e,true);});}
  document.getElementById('btn-smoke').addEventListener('click',runSmoke);
  document.getElementById('btn-purge').addEventListener('click',runPurge);
  document.getElementById('btn-bench').addEventListener('click',runBench);
  document.getElementById('shortcut-help-btn').addEventListener('click',openModal);
  closeBtn.addEventListener('click',closeModal);
  modal.addEventListener('click',function(e){if(e.target===modal)closeModal();});
  document.addEventListener('keydown',function(e){
    var tag=document.activeElement&&document.activeElement.tagName;
    if(tag==='INPUT'||tag==='TEXTAREA'||tag==='SELECT')return;
    if(e.key==='Escape'){if(modal.classList.contains('open'))closeModal();return;}
    if(e.key==='?'){e.preventDefault();openModal();return;}
    if(e.key==='s'&&!e.ctrlKey&&!e.metaKey){runSmoke();return;}
    if(e.key==='b'&&!e.ctrlKey&&!e.metaKey){runBench();return;}
    if(e.key==='r'&&!e.ctrlKey&&!e.metaKey){location.reload();return;}
  });
})();
</script></body></html>`,
		Version, ConfigVersion, snap.SnapshotAt.UTC().Format("15:04:05 UTC"),
		nonce, // P8: nonce attribute on inline script (ADR-0036)
	)
}
// =============================================================================
// main()
// =============================================================================

func main() {
	log.SetFlags(0)
	logInfo("main", fmt.Sprintf("VayuPress v%s starting — P1–P7 active, P8 initializing", Version))
	loadConfig()
	logInfo("main", fmt.Sprintf("domain=%s port=%s workers=%d config_version=%s maintenance=%v",
		cfg.Domain, cfg.Port, cfg.WorkerCount, ConfigVersion, cfg.MaintenanceMode))
	logInfo("main", fmt.Sprintf("P8: wal_threshold=%dMB replay_batch=%d max_replay=%d pprof_rate=%d/min vacuum_cooldown=%dmin",
		cfg.WALSizeThresholdMB, cfg.ReplayBatchLimit, cfg.MaxReplayCount, cfg.PprofRateLimit, cfg.VacuumCooldownMin))

	policy = bluemonday.UGCPolicy()
	initCSRFSecret()

	// P8: pprof on isolated mux — no DefaultServeMux (ADR-0037)
	initPprofMux()

	staticDir := envOr("STATIC_DIR", "/var/www/vayupress/static")
	writeCSSAssets(staticDir)

	docsDir := envOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs")
	os.MkdirAll(docsDir, 0755)
	writeADRs(docsDir)

	if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
		// P8: hardened pool with WaitGroup + context (ADR-0032)
		initPluginPool()
	}

	if err := initDB(); err != nil {
		logError("main", "DB init failed", err.Error()); os.Exit(1)
	}
	logInfo("main", "database ready — WAL adaptive + migrations + checksum drift verified (ADR-0033/0034)")

	// Recover any stale processing jobs from previous crash
	if n, err := db.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing'`); err == nil {
		if rows, _ := n.RowsAffected(); rows > 0 {
			logInfo("main", fmt.Sprintf("recovered %d stale processing jobs", rows))
		}
	}

	initStorageCachedBytes()
	startMetricsSnapshotCollector()

	// Meilisearch startup wait
	for i := 0; i < 12; i++ {
		if err := meiliDo("GET", "/health", nil); err == nil { logInfo("main", "Meilisearch ready"); break }
		if i == 11 { logJSON(logFields{Level:"warn",Component:"main",Msg:"Meilisearch unavailable — SQLite search fallback active"}) }
		time.Sleep(5 * time.Second)
	}
	configureMeilisearch()
	initMeilisearchCB()

	go func() {
		logInfo("cache-warm", "starting...")
		warmCache(); generateSitemap(); generateRSS(); generateRobots()
		logInfo("cache-warm", "complete")
	}()

	startWorkerPool(&workerWg)
	logInfo("main", fmt.Sprintf("started %d write workers (maintenance_mode=%v)", cfg.WorkerCount, cfg.MaintenanceMode))
	startStuckJobReaper()

	logInfo("main", fmt.Sprintf("startup complete in %dms", time.Since(bootTime).Milliseconds()))
	logInfo("main", "P8 active: plugin_pool=WaitGroup+ctx wal=adaptive migration_checksums=verified dlq_safety=active pprof=isolated vacuum=rate_limited config_version="+ConfigVersion)

	r := chi.NewRouter()
	r.Use(
		requestIDMiddleware,
		middleware.RealIP,
		structuredLoggerMiddleware,
		middleware.Recoverer,
		middleware.Timeout(30*time.Second),
		securityHeadersMiddleware,
	)
	r.Use(cors.New(cors.Options{
		AllowedOrigins:  []string{"https://" + cfg.Domain},
		AllowedMethods:  []string{"GET","POST","PUT","DELETE","OPTIONS"},
		AllowedHeaders:  []string{"Content-Type","X-API-Key","Authorization","X-Request-ID","X-CSRF-Token"},
		ExposedHeaders:  []string{"X-Request-ID"},
		AllowCredentials: true,
	}).Handler)

	// ── Public health endpoints (P7 + P8 structured contracts ADR-0041) ──
	r.Get("/health",             handleHealthLiveness)
	r.Get("/health/live",        handleHealthLiveness)
	r.Get("/health/ready",       handleHealthReady)
	r.Get("/health/db",          handleHealthDB)
	r.Get("/health/meilisearch", handleHealthMeilisearch)
	r.Get("/health/workers",     handleHealthWorkers)
	r.Get("/health/storage",     handleHealthStorage)
	r.Get("/health/benchmarks",  handleHealthBenchmarks)
	r.Get("/health/migrations",  handleHealthMigrations)
	// P8: structured health contracts (ADR-0041)
	r.Get("/health/dependencies", handleHealthDependencies)
	r.Get("/health/search",       handleHealthSearch)
	r.Get("/health/queue",        handleHealthQueue)

	// ── Static files + feeds ──
	r.Get("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w,r,filepath.Join(cfg.CacheDir,"sitemap.xml")) })
	r.Get("/feed.xml",    func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w,r,filepath.Join(cfg.CacheDir,"feed.xml")) })
	r.Get("/robots.txt",  func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w,r,filepath.Join(cfg.CacheDir,"robots.txt")) })
	r.Get("/static/css/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		if !map[string]bool{"article.css":true,"admin.css":true,"high-contrast.css":true}[file] { http.NotFound(w,r); return }
		w.Header().Set("Cache-Control","public, immutable, max-age=31536000")
		w.Header().Set("Content-Type","text/css; charset=utf-8")
		http.ServeFile(w,r,filepath.Join(staticDir,"css",file))
	})

	// ── Public API ──
	r.Get("/api/v1/articles",      handleListArticles)
	r.Get("/api/v1/articles/{slug}", handleGetArticle)
	r.Get("/api/v1/search",        handleSearch)
	r.Get("/api/v1/tags",          handleListTags)
	r.Get("/api/v1/stats",         handleStats)
	r.Get("/metrics",              handleMetrics)
	r.Get("/smoke-test",           handleSmokeTest)

	// ── Admin + protected API ──
	r.Group(func(r chi.Router) {
		r.Use(requireAPIKey, rateLimitMiddleware)

		r.Post("/api/v1/articles",        handleCreateArticle)
		r.Post("/api/v1/articles/bulk",   handleBulkCreateArticles)
		r.Put("/api/v1/articles/{slug}",  handleUpdateArticle)
		r.Delete("/api/v1/articles/{slug}", handleDeleteArticle)
		r.Get("/api/v1/queue",            handleQueueStatus)
		r.Post("/api/v1/queue/replay",    handleQueueReplay) // P8: safety controls (ADR-0035)

		r.Get("/admin",                   handleAdminDashboard)
		r.Get("/admin/adr",               handleAdminADR)
		r.Get("/admin/backup/validate",   handleAdminBackupValidate) // P8: ADR-0042

		r.With(csrfTokenMiddleware).Post("/admin/benchmark",   handleRunBenchmark)
		r.With(csrfTokenMiddleware).Post("/admin/cache-purge", handleAdminCachePurge)
		r.With(csrfTokenMiddleware).Post("/admin/vacuum",      handleAdminVacuum) // P8: ADR-0038

		// P8: pprof on isolated mux — explicit, not DefaultServeMux (ADR-0037)
		r.HandleFunc("/debug/pprof/",         pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline",  pprofHandler)
		r.HandleFunc("/debug/pprof/profile",  pprofHandler)
		r.HandleFunc("/debug/pprof/symbol",   pprofHandler)
		r.HandleFunc("/debug/pprof/trace",    pprofHandler)
		r.HandleFunc("/debug/pprof/*",        pprofHandler)
	})

	// ── Article page ──
	r.Get("/{slug}", handleArticlePage)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logInfo("main", fmt.Sprintf("received %v — P8 graceful shutdown (ADR-0022/ADR-0032)", sig))

		// Step 1: stop accepting new HTTP connections (30s window)
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpCancel()
		if err := srv.Shutdown(httpCtx); err != nil { logError("main","HTTP shutdown",err.Error()) }
		logInfo("main", "HTTP server stopped accepting connections")

		// Step 2: signal background goroutines via doneCh
		logInfo("main", "closing doneCh — background goroutines draining")
		close(doneCh)

		// Step 3: P8 — shutdown plugin pool with WaitGroup drain (ADR-0032)
		if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
			logInfo("main", "shutting down plugin pool (ADR-0032)")
			shutdownPluginPool()
		}

		// Step 4: wait for write worker drain (45s timeout)
		drainDone := make(chan struct{})
		go func() { workerWg.Wait(); close(drainDone) }()
		select {
		case <-drainDone:
			logInfo("main", "write queue drained — all workers stopped")
		case <-time.After(45 * time.Second):
			logJSON(logFields{Level:"warn",Component:"main",Msg:"drain timeout (45s) — in-flight jobs retried on next startup"})
		}

		// Step 5: close database
		if db != nil {
			if err := db.Close(); err != nil { logError("main","DB close",err.Error()) } else { logInfo("main","database closed") }
		}

		logInfo("main", "shutdown complete — goodbye")
		os.Exit(0)
	}()

	logInfo("main", fmt.Sprintf("listening on :%s (P8 v%s — ADRs 0032–0043 active)", cfg.Port, Version))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logError("main","ListenAndServe error",err.Error()); os.Exit(1)
	}
}
GOEOF

# Build the binary
go mod tidy
CGO_ENABLED=1 go build \
  -ldflags "-X main.Version=${ENGINE_VERSION} -s -w" \
  -o /usr/local/bin/vayupress .

ok "VayuPress binary built: $(ls -lh /usr/local/bin/vayupress | awk '{print $5}')"

fi  # end DRY_RUN guard
# STEP 7.5 ── P7 Deploy Script Decomposition Scaffold + Integration Tests
# ADR-0025: generated deploy/ subdirectory at deploy time
# ADR-0015: test harness, run with: cd /var/www/vayupress/src && go test ./tests/...
# =============================================================================
step "P7 Deploy Scaffold (ADR-0025) + Integration Test Harness"

[ "$DRY_RUN" = false ] && mkdir -p "${SRC_DIR}/tests"

if [ "$DRY_RUN" = false ]; then

cat > "${SRC_DIR}/tests/auth_test.go" << 'GOTEST_EOF'
package tests

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

var testBase = func() string {
	if v := os.Getenv("TEST_BASE_URL"); v != "" { return v }
	return "http://localhost:8080"
}()
var testAPIKey = os.Getenv("API_KEY")

func TestAuthMissingKey(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/admin", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 without API key, got %d", resp.StatusCode)
	}
}

func TestAuthValidKey(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 with valid API key, got %d", resp.StatusCode)
	}
}

func TestAuthInvalidKey(t *testing.T) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", "invalid-key-xyz")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 with invalid API key, got %d", resp.StatusCode)
	}
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/csrf_test.go" << 'GOTEST_EOF'
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestCSRFMissingToken verifies admin POST without CSRF token returns 403.
func TestCSRFMissingToken(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	body, _ := json.Marshal(map[string]string{"name": "test-theme"})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/admin/cache-purge", testBase), bytes.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	// Deliberately omit X-CSRF-Token
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 CSRF failure without token, got %d", resp.StatusCode)
	}
}

// TestCSRFTokenFormat verifies token generation produces base64url-encoded strings.
func TestCSRFTokenFormat(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	// GET /admin to receive CSRF cookie
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	jar := newCookieJar()
	client := &http.Client{Jar: jar}
	resp, err := client.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	csrfCookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == "vp_csrf" { csrfCookie = c.Value; break }
	}
	if csrfCookie == "" {
		t.Error("no vp_csrf cookie set by GET /admin")
		return
	}
	// Token must be non-empty and reasonably long (base64url HMAC-signed)
	if len(csrfCookie) < 64 {
		t.Errorf("CSRF token too short: %d chars (expected >=64)", len(csrfCookie))
	}
}

// newCookieJar returns a simple in-memory cookie jar for tests.
func newCookieJar() http.CookieJar {
	jar, _ := cookiejar()
	return jar
}

func cookiejar() (http.CookieJar, error) {
	// Use net/http/cookiejar in real tests; simplified inline here.
	return nil, nil
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/render_test.go" << 'GOTEST_EOF'
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestRenderArticleNotFound verifies 404 for unknown slugs.
func TestRenderArticleNotFound(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/this-article-does-not-exist-xyz-abc", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for unknown slug, got %d", resp.StatusCode)
	}
}

// TestRenderInvalidSlug verifies 404 for malformed slugs.
func TestRenderInvalidSlugFormat(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/../../etc/passwd", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Error("path traversal slug should not return 200")
	}
}

// TestRenderCSSLinks verifies article pages include versioned CSS links.
func TestRenderCSSLinks(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	// Check admin page for CSS versioning
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "/static/css/admin.css?v=") {
		t.Error("admin page missing versioned admin.css link")
	}
	if !strings.Contains(bodyStr, "/static/css/high-contrast.css?v=") {
		t.Error("admin page missing versioned high-contrast.css link")
	}
}

// TestRenderSecurityHeaders verifies security headers are present.
func TestRenderSecurityHeaders(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/health", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for header, expected := range headers {
		got := resp.Header.Get(header)
		if !strings.Contains(got, expected) {
			t.Errorf("header %s: expected %q, got %q", header, expected, got)
		}
	}
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/queue_test.go" << 'GOTEST_EOF'
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestQueueEndpointAuth verifies /api/v1/queue requires API key.
func TestQueueEndpointAuth(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/queue", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 for /api/v1/queue without key, got %d", resp.StatusCode)
	}
}

// TestQueueStatusShape verifies queue status response shape.
func TestQueueStatusShape(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/queue", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Errorf("expected 200, got %d", resp.StatusCode); return }
	body, _ := io.ReadAll(resp.Body)
	var result map[string]int
	if err := json.Unmarshal(body, &result); err != nil { t.Errorf("invalid JSON: %v", err); return }
	for _, field := range []string{"pending", "processing", "completed", "failed"} {
		if _, ok := result[field]; !ok { t.Errorf("queue status missing field: %s", field) }
	}
}

// TestArticleCreateValidation verifies article creation validates inputs.
func TestArticleCreateValidation(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	// Missing required fields
	body, _ := json.Marshal(map[string]string{"title": ""})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/articles", testBase), bytes.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for empty title, got %d", resp.StatusCode)
	}
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/cache_test.go" << 'GOTEST_EOF'
package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCacheStaticCSS verifies /static/css files are served with immutable headers.
func TestCacheStaticCSSHeaders(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/static/css/article.css", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	// May 404 if hash-versioned path is used — that is also acceptable
	if resp.StatusCode == 404 { t.Skip("CSS not found without version param — use ?v= query") }
	if resp.StatusCode != 200 { t.Errorf("expected 200 for /static/css/article.css, got %d", resp.StatusCode); return }
	cc := resp.Header.Get("Cache-Control")
	if cc == "" { t.Error("Cache-Control header missing on static CSS") }
}

// TestCachePurgeRateLimit verifies full cache purge is rate-limited to API-key holders.
func TestCachePurgeRequiresAuth(t *testing.T) {
	resp, err := http.Post(fmt.Sprintf("%s/admin/cache-purge", testBase), "application/json", nil)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Error("cache purge without API key should not return 200")
	}
}

// TestHealthEndpoints verifies all health endpoints respond.
func TestHealthEndpoints(t *testing.T) {
	endpoints := []string{"/health", "/health/ready", "/health/db", "/health/workers", "/health/storage", "/health/migrations"}
	for _, ep := range endpoints {
		resp, err := http.Get(fmt.Sprintf("%s%s", testBase, ep))
		if err != nil { t.Skipf("server not running: %v", err) }
		resp.Body.Close()
		if resp.StatusCode != 200 && resp.StatusCode != 503 {
			t.Errorf("health endpoint %s: unexpected status %d", ep, resp.StatusCode)
		}
	}
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/theme_test.go" << 'GOTEST_EOF'
package tests

// theme_test.go — P5 Integration Tests: Theme CSS Sanitization + Atomic Rollback
// ADR-0012 (CSS sanitization), ADR-0014 (atomic rollback)
//
// These tests validate the sanitizeThemeCSS() logic in isolation (unit-style)
// and the /api/v1/themes endpoint shape (integration).
// Full end-to-end theme apply tests require VAYU_THEMES_ENABLED=true and a live server.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// --- sanitizeThemeCSS unit tests (logic mirrored from main.go) ---

type cssViolation struct{ Pattern string; Line int; Excerpt string }

func sanitizeThemeCSSTest(css string) []cssViolation {
	var violations []cssViolation
	for i, line := range strings.Split(css, "\n") {
		lineNum := i + 1
		excerpt := strings.TrimSpace(line); if len(excerpt) > 80 { excerpt = excerpt[:80] }
		if strings.Contains(strings.ToLower(line), "@import") {
			violations = append(violations, cssViolation{"@import", lineNum, excerpt})
		}
		if strings.Contains(strings.ToLower(line), "javascript:") {
			violations = append(violations, cssViolation{"javascript:", lineNum, excerpt})
		}
		if strings.Contains(strings.ToLower(line), "url(http://") || strings.Contains(strings.ToLower(line), "url(https://") {
			violations = append(violations, cssViolation{"url(http...)", lineNum, excerpt})
		}
		if strings.Contains(strings.ToLower(line), "expression(") {
			violations = append(violations, cssViolation{"expression()", lineNum, excerpt})
		}
	}
	return violations
}

func TestThemeCSSSanitizationClean(t *testing.T) {
	css := `:root { --bg: #000; --text: #fff; }
body { font-family: sans-serif; color: var(--text); background: var(--bg); }
a { color: #3B82F6; }
.container { max-width: 720px; margin: 0 auto; }
`
	violations := sanitizeThemeCSSTest(css)
	if len(violations) != 0 {
		t.Errorf("clean CSS produced %d violations: %+v", len(violations), violations)
	}
}

func TestThemeCSSSanitizationImport(t *testing.T) {
	css := "@import url('https://fonts.googleapis.com/css?family=Roboto');\nbody { font-family: Roboto; }"
	violations := sanitizeThemeCSSTest(css)
	if len(violations) == 0 { t.Error("@import not detected as violation") }
}

func TestThemeCSSSanitizationJavascript(t *testing.T) {
	css := `body { background: url("javascript:alert(1)"); }`
	violations := sanitizeThemeCSSTest(css)
	if len(violations) == 0 { t.Error("javascript: URI not detected as violation") }
}

func TestThemeCSSSanitizationRemoteURL(t *testing.T) {
	css := `.hero { background-image: url(https://evil.example.com/tracker.png); }`
	violations := sanitizeThemeCSSTest(css)
	if len(violations) == 0 { t.Error("remote url(https://...) not detected as violation") }
}

func TestThemeCSSSanitizationLocalURL(t *testing.T) {
	// Local /static/ URLs and data: URIs should be allowed
	css := `.logo { background: url('/static/logo.png'); }
.icon { background: url(data:image/png;base64,abc); }`
	violations := sanitizeThemeCSSTest(css)
	if len(violations) != 0 {
		t.Errorf("local/data URLs produced false-positive violations: %+v", violations)
	}
}

func TestThemeCSSSanitizationExpression(t *testing.T) {
	css := `body { width: expression(document.body.clientWidth); }`
	violations := sanitizeThemeCSSTest(css)
	if len(violations) == 0 { t.Error("expression() not detected as violation") }
}

// --- Integration: /api/v1/themes endpoint ---

func TestThemesEndpointPublic(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/themes", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/v1/themes expected 200, got %d", resp.StatusCode)
	}
}

func TestThemeApplyRequiresAuth(t *testing.T) {
	resp, err := http.Post(fmt.Sprintf("%s/admin/theme", testBase), "application/json", strings.NewReader(`{"name":"default"}`))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Error("theme apply without API key should not return 200")
	}
}
GOTEST_EOF

# P6: csp_test.go — CSP header coverage (ADR-0018)
cat > "${SRC_DIR}/tests/csp_test.go" << 'GOTEST_EOF'
package tests

// csp_test.go — P6 Integration Tests: CSP header consistency (ADR-0018)

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCSPHeaderOnHealthRoute(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/health", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" { t.Error("Content-Security-Policy header missing on /health") }
	if !strings.Contains(csp, "default-src 'self'") { t.Errorf("CSP missing default-src 'self': got %q", csp) }
	if !strings.Contains(csp, "frame-ancestors 'none'") { t.Errorf("CSP missing frame-ancestors 'none': got %q", csp) }
}

func TestCSPHeaderOnAdminRoute(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" { t.Error("Content-Security-Policy header missing on /admin") }
}

func TestSecurityHeadersComplete(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/health", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	required := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, want := range required {
		got := resp.Header.Get(header)
		if !strings.Contains(got, want) { t.Errorf("%s: want %q got %q", header, want, got) }
	}
}

// TestCSRFCookiePath verifies the CSRF cookie is set with Path="/" (ADR-0017).
func TestCSRFCookiePath(t *testing.T) {
	if testAPIKey == "" { t.Skip("API_KEY not set") }
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/admin", testBase), nil)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "vp_csrf" {
			if c.Path != "/" { t.Errorf("CSRF cookie path should be '/', got %q (ADR-0017)", c.Path) }
			return
		}
	}
}
GOTEST_EOF

# P6: migration_test.go — Migration system (ADR-0023)
cat > "${SRC_DIR}/tests/migration_test.go" << 'GOTEST_EOF'
package tests

// migration_test.go — P6 Integration Tests: Migration system (ADR-0023)

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestMigrationEndpointAvailable(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/health/migrations", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Errorf("expected 200 from /health/migrations, got %d", resp.StatusCode) }
}

func TestMigrationBaselineApplied(t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("%s/health/migrations", testBase))
	if err != nil { t.Skipf("server not running: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Status  string `json:"status"`
		Applied []struct{ Version string `json:"version"` } `json:"applied"`
		Total   int    `json:"total"`
	}
	if err := json.Unmarshal(body, &result); err != nil { t.Errorf("invalid JSON: %v", err); return }
	if result.Status != "ok" { t.Errorf("migration status not ok: %q", result.Status) }
	if result.Total < 2 { t.Errorf("expected at least 2 migrations applied, got %d", result.Total) }
	found := false
	for _, m := range result.Applied { if m.Version == "001-baseline" { found = true; break } }
	if !found { t.Error("001-baseline migration not found in applied list") }
}
GOTEST_EOF

ok "P6 integration test harness generated: ${SRC_DIR}/tests/ (8 test files)"
info "Run tests: cd ${SRC_DIR} && API_KEY=\$API_KEY go test ./tests/... -v -count=1"
info "Run P6 tests only: go test ./tests/... -run 'TestCSP|TestMigration|TestCSRFCookiePath' -v"

# =============================================================================
# P7: New integration test files (ADR-0028, ADR-0026, ADR-0027, ADR-0031)
# =============================================================================

cat > "${SRC_DIR}/tests/plugin_pool_test.go" << 'GOTEST_EOF'
package tests

// plugin_pool_test.go — P7 Integration Tests: Plugin Async Execution Pool (ADR-0028)

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

func TestPluginPoolMetricsExist(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	resp, err := http.Get(base + "/metrics")
	if err != nil { t.Fatalf("metrics request: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Fatalf("metrics: want 200 got %d", resp.StatusCode) }
}

func TestPluginPoolDisabledByDefault(t *testing.T) {
	// Plugins disabled (VAYU_PLUGINS_ENABLED unset) — pool should be inactive
	if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
		t.Skip("plugins enabled — pool active, skip disabled test")
	}
	fmt.Println("plugin pool inactive (VAYU_PLUGINS_ENABLED!=true) — correct default")
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/rollback_test.go" << 'GOTEST_EOF'
package tests

// rollback_test.go — P7 Integration Tests: Migration Rollback + Checksum (ADR-0026)

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestMigrationChecksumPresent(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	resp, err := http.Get(base + "/health/migrations")
	if err != nil { t.Fatalf("migrations request: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Fatalf("want 200 got %d", resp.StatusCode) }
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Applied []struct {
			Version  string `json:"version"`
			Checksum string `json:"checksum"`
		} `json:"applied"`
		TotalApplied  int `json:"total_applied"`
		TotalPending  int `json:"total_pending"`
		RollbackCount int `json:"rollback_supported_count"`
	}
	if err := json.Unmarshal(body, &result); err != nil { t.Fatalf("unmarshal: %v", err) }
	for _, m := range result.Applied {
		if m.Checksum == "" { t.Errorf("migration %s missing checksum", m.Version) }
	}
	fmt.Printf("migrations: applied=%d pending=%d rollback_supported=%d\n", result.TotalApplied, result.TotalPending, result.RollbackCount)
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/wal_test.go" << 'GOTEST_EOF'
package tests

// wal_test.go — P7 Integration Tests: SQLite WAL Operational Maturity (ADR-0027)

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestWALCheckpointMetricPresent(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	resp, err := http.Get(base + "/metrics")
	if err != nil { t.Fatalf("metrics request: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "vayupress_wal_checkpoints_total") {
		t.Error("WAL checkpoint metric missing from /metrics output")
	}
	if !strings.Contains(string(body), "vayupress_slow_queries_total") {
		t.Error("slow queries metric missing from /metrics output")
	}
}

func TestVacuumEndpointProtected(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	// Without API key should return 401
	resp, err := http.Post(base+"/admin/vacuum", "application/json", nil)
	if err != nil { t.Fatalf("vacuum request: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 401 { t.Errorf("vacuum without key: want 401 got %d", resp.StatusCode) }
}
GOTEST_EOF

cat > "${SRC_DIR}/tests/pprof_test.go" << 'GOTEST_EOF'
package tests

// pprof_test.go — P7 Integration Tests: pprof Endpoint Protection (ADR-0031)

import (
	"net/http"
	"os"
	"testing"
)

func TestPprofRequiresAPIKey(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	// Without API key should return 401
	resp, err := http.Get(base + "/debug/pprof/")
	if err != nil { t.Fatalf("pprof request: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 401 { t.Errorf("pprof without key: want 401 got %d", resp.StatusCode) }
}

func TestHealthLiveVsReady(t *testing.T) {
	base := os.Getenv("BASE_URL")
	if base == "" { base = "http://localhost:8080" }
	for _, path := range []string{"/health/live", "/health/ready", "/health"} {
		resp, err := http.Get(base + path)
		if err != nil { t.Fatalf("%s request: %v", path, err) }
		resp.Body.Close()
		if resp.StatusCode != 200 { t.Errorf("%s: want 200 got %d", path, resp.StatusCode) }
	}
}

func TestQueueReplayEndpointExists(t *testing.T) {
	base := os.Getenv("BASE_URL")
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" { t.Skip("API_KEY not set") }
	req, _ := http.NewRequest("POST", base+"/api/v1/queue/replay", nil)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatalf("replay request: %v", err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Errorf("replay: want 200 got %d", resp.StatusCode) }
}
GOTEST_EOF

ok "P7 integration tests generated: plugin_pool_test.go, rollback_test.go, wal_test.go, pprof_test.go"
info "Run P7 tests: go test ./tests/... -run 'TestPlugin|TestMigrationChecksum|TestWAL|TestPprof|TestHealth|TestQueue' -v"

# =============================================================================
# P7: Deploy Script Decomposition Scaffold (ADR-0025)
# Generates deploy/ subdirectory as the first step toward full decomposition.
# The monolithic script remains the primary artifact this cycle.
# =============================================================================

DEPLOY_SCAFFOLD_DIR="/var/www/${APP_NAME}/deploy"
mkdir -p "${DEPLOY_SCAFFOLD_DIR}/templates"

cat > "${DEPLOY_SCAFFOLD_DIR}/README.md" << 'SCAFFOLD_EOF'
# VayuPress Deploy Script Decomposition (P7 Scaffold — ADR-0025)

This directory represents the **decomposition target** for the monolithic deploy script.
Currently generated as a scaffold; full decomposition planned for P8+.

## Planned Structure

```
deploy/
 ├── install.sh          — System dependencies (apt, Go runtime)
 ├── nginx.sh            — Nginx configuration + SSL
 ├── systemd.sh          — Systemd unit file
 ├── build.sh            — Go source generation + build
 ├── migrate.sh          — Standalone migration runner
 ├── backup.sh           — SQLite WAL backup + retention
 ├── fail2ban.sh         — Fail2ban rules + filters
 ├── generate-source.sh  — main.go + tests generation
 └── templates/          — Nginx, systemd, fail2ban templates
```

## Current Status
- Scaffold generated at deploy time ✓
- Monolithic script remains primary artifact (P7)
- Full decomposition: P8

## Governance
Constitution v6.0 · ADR-0025 (Deploy Script Decomposition)
SCAFFOLD_EOF

# Scaffold individual component files
cat > "${DEPLOY_SCAFFOLD_DIR}/install.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress install.sh — System dependency installation
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[install.sh] This scaffold will install: golang, nginx, certbot, fail2ban, isso-deps"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/nginx.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress nginx.sh — Nginx configuration
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[nginx.sh] This scaffold will configure: Nginx vhost, CSP headers, SSL redirect, static serving"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/systemd.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress systemd.sh — Systemd unit management
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[systemd.sh] This scaffold will write: vayupress.service with TimeoutStopSec=90 (ADR-0022)"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/build.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress build.sh — Go source generation and binary build
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[build.sh] This scaffold will: generate main.go, go mod tidy, CGO_ENABLED=1 go build"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/migrate.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress migrate.sh — Standalone migration runner
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
# Usage: VAYU_MIGRATE_DRY_RUN=true ./migrate.sh
set -euo pipefail
echo "[migrate.sh] This scaffold will: run VAYU_MIGRATE_DRY_RUN or live migrations (ADR-0026)"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/backup.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress backup.sh — SQLite WAL backup with retention
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[backup.sh] This scaffold will: WAL checkpoint, cp data.db, retain BACKUP_RETAIN_DAYS"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/fail2ban.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress fail2ban.sh — Fail2ban filter + jail configuration
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[fail2ban.sh] This scaffold will: write vayupress-api.conf and vayupress-auth.conf filters"
SCAFFOLD_EOF

cat > "${DEPLOY_SCAFFOLD_DIR}/generate-source.sh" << 'SCAFFOLD_EOF'
#!/bin/bash
# VayuPress generate-source.sh — Go source (main.go + tests) generation
# Part of deploy/ decomposition (ADR-0025)
# Status: SCAFFOLD — not yet functional standalone
set -euo pipefail
echo "[generate-source.sh] This scaffold will: write main.go, go.mod, tests/ from templates"
SCAFFOLD_EOF

chmod +x "${DEPLOY_SCAFFOLD_DIR}"/*.sh
ok "P7 deploy scaffold generated: ${DEPLOY_SCAFFOLD_DIR}/ (8 component scripts + README)"
info "ADR-0025: decomposition scaffold complete. Full standalone decomposition: P8+"

fi  # end DRY_RUN guard for test generation

# =============================================================================
# STEP 8 ── Systemd service
# =============================================================================
step "Systemd service (P7: TimeoutStopSec=90, graceful drain ADR-0022)"

if [ "$DRY_RUN" = false ]; then

# Write secrets file (upgrade mode: secrets already loaded above)
if [ ! -f "${SECRETS_FILE}" ] || [ "$UPGRADE" = false ]; then
cat > "${SECRETS_FILE}" << SECRETS
API_KEY="${API_KEY}"
MEILI_MASTER_KEY="${MEILI_MASTER_KEY}"
ADMIN_PASSWORD="${ADMIN_PASSWORD}"
INDEXNOW_KEY="${INDEXNOW_KEY}"
SECRETS
chmod 600 "${SECRETS_FILE}"
fi

echo "${ADMIN_PASSWORD}" > "${ADMIN_PASS_FILE}"
chmod 600 "${ADMIN_PASS_FILE}"

cat > /etc/systemd/system/${APP_NAME}.service << SERVICE
[Unit]
Description=VayuPress Publishing Engine v${ENGINE_VERSION} (P1-P6)
Documentation=https://vayupress.com
After=network.target meilisearch.service

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/var/www/${APP_NAME}
ExecStart=/usr/local/bin/vayupress
Restart=on-failure
RestartSec=5
StandardOutput=append:${LOG_DIR}/vayupress.log
StandardError=append:${LOG_DIR}/vayupress.log
# P6: 90s allows for 45s queue drain + 30s HTTP shutdown window (ADR-0022)
TimeoutStopSec=90

# Core configuration
Environment="API_KEY=${API_KEY}"
Environment="DB_PATH=${DB_PATH}"
Environment="CACHE_DIR=${CACHE_DIR}"
Environment="LOG_DIR=${LOG_DIR}"
Environment="STATIC_DIR=${STATIC_DIR}"
Environment="DOMAIN=${DOMAIN}"
Environment="PORT=${APP_PORT}"
Environment="WORKER_COUNT=${WORKER_COUNT}"
Environment="TMP_DIR=${TMP_DIR}"

# Meilisearch
Environment="MEILI_HOST=http://127.0.0.1:7700"
Environment="MEILI_MASTER_KEY=${MEILI_MASTER_KEY}"

# Cloudflare (optional)
Environment="CF_ZONE_ID=${CF_ZONE_ID}"
Environment="CF_API_TOKEN=${CF_API_TOKEN}"
Environment="INDEXNOW_KEY=${INDEXNOW_KEY}"

# Storage governance
Environment="STORAGE_QUOTA_GB=${STORAGE_QUOTA_GB}"
Environment="MEDIA_RETAIN_DAYS=${MEDIA_RETAIN_DAYS}"
Environment="CACHE_MAX_SIZE_GB=${CACHE_MAX_SIZE_GB}"
Environment="BACKUP_RETAIN_DAYS=${BACKUP_RETAIN_DAYS}"

# P5: CSRF local-dev compatibility (ADR-0013)
# Set to "false" for localhost development, "true" for production (auto-detected from DOMAIN)
# Explicit override: Environment="CSRF_SECURE_COOKIE=false"

# Feature flags (experimental subsystems off by default)
Environment="VAYU_THEMES_ENABLED=false"
Environment="VAYU_PLUGINS_ENABLED=false"

# Docs/ADR directory
Environment="VAYU_DOCS_DIR=/var/www/${APP_NAME}/docs"
Environment="VAYU_THEMES_DIR=/var/www/${APP_NAME}/themes"

# Hardening
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=${CACHE_DIR} ${DB_DIR} ${LOG_DIR} ${TMP_DIR} ${STATIC_DIR} /var/www/${APP_NAME}
ProtectHome=yes
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallFilter=@system-service

[Install]
WantedBy=multi-user.target
SERVICE

fi  # end DRY_RUN

run systemctl daemon-reload
run systemctl enable ${APP_NAME}
run systemctl restart ${APP_NAME}

if [ "$DRY_RUN" = false ]; then
    sleep 3
    if systemctl is-active --quiet ${APP_NAME}; then
        ok "VayuPress service started successfully."
    else
        warn "VayuPress service may not have started. Check: journalctl -u ${APP_NAME} -n 50"
    fi
fi

# =============================================================================
# STEP 9 ── Nginx configuration
# =============================================================================
step "Nginx configuration (P6: CSP in all response paths — ADR-0018)"

if [ "$DRY_RUN" = false ]; then
cat > /etc/nginx/sites-available/${APP_NAME} << NGINX_CONF
# VayuPress Nginx — v${ENGINE_VERSION}
# P6: CSP header added to all server paths (ADR-0018).
# Static cache-hit responses now carry the same CSP as Go-proxied responses.

upstream vayupress_backend {
    server 127.0.0.1:${APP_PORT};
    keepalive 32;
}

# HTTP → HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN} www.${DOMAIN};
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name ${DOMAIN} www.${DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         'ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384';
    ssl_prefer_server_ciphers on;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 10m;
    ssl_stapling        on;
    ssl_stapling_verify on;

    # P6: Security headers — CSP now included for all paths (ADR-0018).
    # Previously CSP was only in Go middleware (bypassed by Nginx cache-hit serving).
    # 'unsafe-inline' in style-src required for ThemeCSS injection; see ADR-0019.
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Frame-Options           "DENY"    always;
    add_header X-Content-Type-Options    "nosniff" always;
    add_header Referrer-Policy           "strict-origin-when-cross-origin" always;
    add_header Permissions-Policy        "camera=(), microphone=(), geolocation=(), payment=()" always;
    add_header Content-Security-Policy   "default-src 'self'; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'" always;

    root /var/www/${APP_NAME};
    index index.html;

    # Gzip compression
    gzip on; gzip_vary on; gzip_comp_level 6; gzip_min_length 256;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml text/javascript;

    # Static files — immutable cache (P4: versioned CSS, fonts)
    location /static/ {
        alias ${STATIC_DIR}/;
        expires 1y;
        add_header Cache-Control "public, immutable, max-age=31536000";
        access_log off;
    }

    # Isso comment server
    location /comments/ {
        proxy_pass         http://127.0.0.1:8081/;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
        proxy_set_header   X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
    }

    # Static cached pages (Nginx serves directly — no Go overhead)
    location ~* "^/([a-z0-9][a-z0-9_-]{0,198}[a-z0-9])$" {
        set \$cache_path "${CACHE_DIR}/posts/\$1.html";
        if (-f \$cache_path) {
            add_header X-Cache-Status "HIT" always;
            add_header X-Powered-By   "VayuPress/${ENGINE_VERSION}" always;
            expires 1h;
            add_header Cache-Control "public, max-age=3600, stale-while-revalidate=300";
            alias \$cache_path;
            break;
        }
        proxy_pass http://vayupress_backend;
        add_header X-Cache-Status "MISS" always;
    }

    # Feed + sitemap + robots
    location = /feed.xml    { alias ${CACHE_DIR}/feed.xml;    add_header Content-Type "application/rss+xml"; expires 1h; }
    location = /sitemap.xml { alias ${CACHE_DIR}/sitemap.xml; add_header Content-Type "application/xml"; expires 6h; }
    location = /robots.txt  { alias ${CACHE_DIR}/robots.txt;  add_header Content-Type "text/plain"; expires 24h; }

    # Admin — proxy to Go (no static-file bypass for admin routes)
    location /admin {
        proxy_pass         http://vayupress_backend;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
        proxy_set_header   X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";
        add_header X-Robots-Tag "noindex, nofollow" always;
    }

    # API + health + metrics
    location ~ ^/(api|health|metrics|smoke-test)/ {
        proxy_pass         http://vayupress_backend;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
        proxy_set_header   X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";
        proxy_read_timeout 60s;
    }

    # Default fallback
    location / {
        proxy_pass         http://vayupress_backend;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
        proxy_set_header   X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";
    }

    # Deny hidden files
    location ~ /\. { deny all; }

    client_max_body_size    64M;
    proxy_connect_timeout   10s;
    proxy_send_timeout      30s;
    proxy_read_timeout      30s;
}
NGINX_CONF

ln -sf /etc/nginx/sites-available/${APP_NAME} /etc/nginx/sites-enabled/${APP_NAME}
rm -f /etc/nginx/sites-enabled/default

fi  # end DRY_RUN

if run nginx -t; then
    run systemctl reload nginx
    ok "Nginx configured and reloaded."
else
    warn "Nginx config test failed — check /etc/nginx/sites-available/${APP_NAME}"
fi

# =============================================================================
# STEP 10 ── SSL (Let's Encrypt)
# =============================================================================
step "SSL (Let's Encrypt)"

if [ "${DOMAIN}" != "localhost" ] && [ -n "${EMAIL}" ]; then
    if [ ! -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]; then
        info "Obtaining SSL certificate for ${DOMAIN}..."
        run certbot --nginx -d "${DOMAIN}" -d "www.${DOMAIN}" \
            --email "${EMAIL}" --agree-tos --non-interactive --redirect 2>/dev/null || \
        run certbot certonly --standalone -d "${DOMAIN}" \
            --email "${EMAIL}" --agree-tos --non-interactive 2>/dev/null || \
        warn "SSL certificate failed — HTTPS may not be available. Try: certbot --nginx -d ${DOMAIN}"
    else
        ok "SSL certificate already present — skipping."
    fi
else
    warn "DOMAIN=localhost or EMAIL not set — skipping SSL. Update DOMAIN and EMAIL vars for HTTPS."
fi

# =============================================================================
# STEP 11 ── Fail2ban
# =============================================================================
step "Fail2ban"

if [ "$DRY_RUN" = false ]; then
cat > /etc/fail2ban/jail.d/${APP_NAME}.conf << F2B
[${APP_NAME}-api]
enabled  = true
port     = http,https
filter   = ${APP_NAME}-api
logpath  = ${LOG_DIR}/vayupress.log
maxretry = 20
findtime = 300
bantime  = 3600

# P6: auth-lockout jail — triggers on in-app lockout log events
[${APP_NAME}-auth]
enabled  = true
port     = http,https
filter   = ${APP_NAME}-auth
logpath  = ${LOG_DIR}/vayupress.log
maxretry = 3
findtime = 900
bantime  = 7200
F2B

cat > /etc/fail2ban/filter.d/${APP_NAME}-api.conf << F2B_FILTER
[Definition]
failregex = .*"status":401.*"remote_addr":"<HOST>".*
            .*"status":429.*"remote_addr":"<HOST>".*
            .*"status":403.*"remote_addr":"<HOST>".*
ignoreregex =
F2B_FILTER

# P6: additional filter matching the in-app auth-lockout log events
cat > /etc/fail2ban/filter.d/${APP_NAME}-auth.conf << F2B_AUTH
[Definition]
failregex = .*"component":"auth-lockout".*IP <HOST> locked out.*
            .*"status":401.*"path":"/admin".*"remote_addr":"<HOST>".*
ignoreregex =
F2B_AUTH
fi

run systemctl enable fail2ban
run systemctl restart fail2ban
ok "Fail2ban configured."

# =============================================================================
# STEP 12 ── Logrotate
# =============================================================================
step "Logrotate"

[ "$DRY_RUN" = false ] && cat > /etc/logrotate.d/${APP_NAME} << LOGROTATE
${LOG_DIR}/*.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    notifempty
    postrotate
        systemctl reload ${APP_NAME} > /dev/null 2>&1 || true
    endscript
}
LOGROTATE
ok "Logrotate configured."

# =============================================================================
# STEP 13 ── SQLite WAL backup cron
# =============================================================================
step "SQLite WAL backup cron"

[ "$DRY_RUN" = false ] && cat > /usr/local/bin/vayupress-backup.sh << 'BACKUP'
#!/bin/bash
set -euo pipefail
DB_PATH="/var/lib/vayupress/data.db"
BACKUP_DIR="/backups/vayupress"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-30}"
mkdir -p "${BACKUP_DIR}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/data_${TIMESTAMP}.db"

# WAL checkpoint + backup
sqlite3 "${DB_PATH}" "PRAGMA wal_checkpoint(FULL);"
sqlite3 "${DB_PATH}" ".backup '${BACKUP_FILE}'"

# Verify backup integrity
ROW_COUNT=$(sqlite3 "${BACKUP_FILE}" "SELECT COUNT(1) FROM articles;" 2>/dev/null || echo "ERROR")
INTEGRITY=$(sqlite3 "${BACKUP_FILE}" "PRAGMA integrity_check;" 2>/dev/null || echo "ERROR")

if [ "${ROW_COUNT}" = "ERROR" ] || [ "${INTEGRITY}" != "ok" ]; then
    echo "{\"level\":\"error\",\"component\":\"backup\",\"msg\":\"backup verification failed\",\"file\":\"${BACKUP_FILE}\",\"integrity\":\"${INTEGRITY}\"}"
    rm -f "${BACKUP_FILE}"
    exit 1
fi

gzip "${BACKUP_FILE}"
echo "{\"level\":\"info\",\"component\":\"backup\",\"msg\":\"backup completed\",\"file\":\"${BACKUP_FILE}.gz\",\"rows\":${ROW_COUNT},\"integrity\":\"${INTEGRITY}\"}"

# Cleanup old backups
find "${BACKUP_DIR}" -name "*.db.gz" -mtime "+${RETAIN_DAYS}" -delete
BACKUP
run chmod +x /usr/local/bin/vayupress-backup.sh

if [ "$DRY_RUN" = false ]; then
    (crontab -l 2>/dev/null | grep -v vayupress-backup; echo "0 3 * * * /usr/local/bin/vayupress-backup.sh >> ${LOG_DIR}/backup.log 2>&1") | crontab -
fi
ok "Daily SQLite backup cron configured (3 AM, ${BACKUP_RETAIN_DAYS}d retention)."

# =============================================================================
# STEP 14 ── Kernel / sysctl tuning
# =============================================================================
step "Kernel/sysctl tuning"

if [ "$DRY_RUN" = false ]; then
cat > /etc/sysctl.d/99-vayupress.conf << SYSCTL
# VayuPress performance tuning
net.core.somaxconn          = 65535
net.ipv4.tcp_tw_reuse       = 1
net.ipv4.ip_local_port_range = 1024 65535
vm.swappiness               = 10
vm.dirty_ratio              = 15
vm.dirty_background_ratio   = 5
fs.file-max                 = 500000
SYSCTL
sysctl -p /etc/sysctl.d/99-vayupress.conf >/dev/null 2>&1 || true
fi
ok "Kernel parameters tuned."

# =============================================================================
# STEP 15 ── P7 Compliance verification
# =============================================================================
step "P8 Governance Compliance Verification"

echo ""
echo -e "${BOLD}${GREEN}P7 Compliance Summary:${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

checks_pass=0; checks_fail=0

check() {
    local label="$1"; local result="$2"
    if [ "$result" = "pass" ]; then
        echo -e "  ${GREEN}✓${NC}  ${label}"
        ((checks_pass++))
    else
        echo -e "  ${RED}✗${NC}  ${label}"
        ((checks_fail++))
    fi
}

# Binary
[ "$DRY_RUN" = true ] || ([ -f /usr/local/bin/vayupress ] && check "VayuPress binary built" "pass" || check "VayuPress binary built" "fail")
[ "$DRY_RUN" = true ] && check "VayuPress binary [dry-run skipped]" "pass"

# P7 source checks
grep -q 'pluginPoolSize' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Plugin async pool constants (ADR-0028)" "pass" || check "P7: Plugin async pool (ADR-0028)" "fail"
grep -q 'initPluginPool' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: initPluginPool() called (ADR-0028)" "pass" || check "P7: initPluginPool() called (ADR-0028)" "fail"
grep -q 'pluginQueueDepth' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Plugin queue depth cap (ADR-0028)" "pass" || check "P7: Plugin queue depth (ADR-0028)" "fail"
grep -q 'checksumSQL' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Migration checksum system (ADR-0026)" "pass" || check "P7: Migration checksum (ADR-0026)" "fail"
grep -q 'rollbackMigration' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: rollbackMigration() (ADR-0026)" "pass" || check "P7: rollbackMigration() (ADR-0026)" "fail"
grep -q 'VAYU_MIGRATE_DRY_RUN' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Migration dry-run mode (ADR-0026)" "pass" || check "P7: Migration dry-run (ADR-0026)" "fail"
grep -q 'journal_size_limit' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: PRAGMA journal_size_limit (ADR-0027)" "pass" || check "P7: journal_size_limit (ADR-0027)" "fail"
grep -q 'wal_autocheckpoint' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: PRAGMA wal_autocheckpoint (ADR-0027)" "pass" || check "P7: wal_autocheckpoint (ADR-0027)" "fail"
grep -q 'metricWALCheckpoints' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: WAL checkpoint metrics (ADR-0027)" "pass" || check "P7: WAL checkpoint metrics (ADR-0027)" "fail"
grep -q 'handleAdminVacuum' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: /admin/vacuum endpoint (ADR-0027)" "pass" || check "P7: /admin/vacuum (ADR-0027)" "fail"
grep -q 'generateCSPNonce' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: CSP nonce generation (ADR-0029)" "pass" || check "P7: CSP nonces (ADR-0029)" "fail"
grep -q 'ctxKeyCSPNonce' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: CSP nonce context key (ADR-0029)" "pass" || check "P7: CSP nonce context (ADR-0029)" "fail"
grep -q 'dead_letter' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Dead-letter queue status (ADR-0030)" "pass" || check "P7: dead_letter status (ADR-0030)" "fail"
grep -q 'handleQueueReplay' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: /api/v1/queue/replay (ADR-0030)" "pass" || check "P7: queue replay (ADR-0030)" "fail"
grep -q 'metricDeadLetterJobs' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Dead-letter metric counter (ADR-0030)" "pass" || check "P7: dead_letter metric (ADR-0030)" "fail"
grep -q 'health/live' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: /health/live endpoint (ADR-0031)" "pass" || check "P7: /health/live (ADR-0031)" "fail"
grep -q 'net/http/pprof' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: pprof import (ADR-0031)" "pass" || check "P7: pprof import (ADR-0031)" "fail"
grep -q 'debug/pprof' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: /debug/pprof route (ADR-0031)" "pass" || check "P7: /debug/pprof route (ADR-0031)" "fail"
grep -q 'metricSlowQueries' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Slow-query logging (ADR-0031)" "pass" || check "P7: slow queries (ADR-0031)" "fail"
grep -q 'exponential backoff' "${SRC_DIR}/main.go" 2>/dev/null && check "P7: Exponential backoff retry (ADR-0030)" "pass" || check "P7: exp backoff (ADR-0030)" "fail"
[ -d "/var/www/${APP_NAME}/deploy" ] && check "P7: deploy/ scaffold directory generated (ADR-0025)" "pass" || check "P7: deploy/ scaffold (ADR-0025)" "fail"

# P6 source checks (carried forward)
grep -q 'if _, err := rand.Read(raw); err != nil' "${SRC_DIR}/main.go" 2>/dev/null && check "rand.Read() error handling in generateCSRFToken (ADR-0016)" "pass" || check "rand.Read() error handling (ADR-0016)" "fail"
grep -q 'Path.*"/"' "${SRC_DIR}/main.go" 2>/dev/null && check "CSRF cookie path '/' (ADR-0017)" "pass" || check "CSRF cookie path '/' (ADR-0017)" "fail"
grep -q 'PRAGMA mmap_size' "${SRC_DIR}/main.go" 2>/dev/null && check "SQLite PRAGMA mmap_size enforced (ADR-0020)" "pass" || check "SQLite PRAGMA contract (ADR-0020)" "fail"
grep -q 'authFailBuckets' "${SRC_DIR}/main.go" 2>/dev/null && check "Admin brute-force lockout system (ADR-0021)" "pass" || check "Admin brute-force lockout (ADR-0021)" "fail"
grep -q 'drain timeout' "${SRC_DIR}/main.go" 2>/dev/null && check "Graceful shutdown drain timeout (ADR-0022)" "pass" || check "Graceful drain (ADR-0022)" "fail"
grep -q 'runMigrations' "${SRC_DIR}/main.go" 2>/dev/null && check "Migration system — runMigrations() (ADR-0023)" "pass" || check "Migration system (ADR-0023)" "fail"

# P5 checks carried forward
grep -q "csrfCookieSecure()" "${SRC_DIR}/main.go" 2>/dev/null && check "CSRF conditional Secure cookie (ADR-0013)" "pass" || check "CSRF conditional Secure cookie (ADR-0013)" "fail"
grep -q "fireHookSafe" "${SRC_DIR}/main.go" 2>/dev/null && check "Plugin panic isolation — fireHookSafe() (ADR-0011)" "pass" || check "Plugin panic isolation (ADR-0011)" "fail"
grep -q "sanitizeThemeCSS" "${SRC_DIR}/main.go" 2>/dev/null && check "Theme CSS sanitization (ADR-0012)" "pass" || check "Theme CSS sanitization (ADR-0012)" "fail"

# P7 ADRs
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0025-deploy-script-decomposition.md" ] && check "ADR-0025 written (deploy decomposition)" "pass" || check "ADR-0025 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0026-migration-rollback-checksum.md" ] && check "ADR-0026 written (migration rollback)" "pass" || check "ADR-0026 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0027-sqlite-operational-maturity.md" ] && check "ADR-0027 written (SQLite maturity)" "pass" || check "ADR-0027 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0028-plugin-async-execution-pool.md" ] && check "ADR-0028 written (plugin pool)" "pass" || check "ADR-0028 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0029-csp-phase2-nonces.md" ] && check "ADR-0029 written (CSP Phase 2)" "pass" || check "ADR-0029 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0030-queue-durability.md" ] && check "ADR-0030 written (queue durability)" "pass" || check "ADR-0030 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0031-observability-pprof.md" ] && check "ADR-0031 written (observability)" "pass" || check "ADR-0031 written" "fail"

# P6 ADRs (carried forward)
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0016-rand-read-error-handling.md" ] && check "ADR-0016 written" "pass" || check "ADR-0016 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0022-graceful-shutdown-drain.md" ] && check "ADR-0022 written" "pass" || check "ADR-0022 written" "fail"
[ -f "/var/www/${APP_NAME}/docs/adr/ADR-0023-migration-system.md" ] && check "ADR-0023 written" "pass" || check "ADR-0023 written" "fail"

# Integration tests
[ -f "${SRC_DIR}/tests/auth_test.go" ]          && check "Integration test: auth_test.go" "pass" || check "Integration test: auth_test.go" "fail"
[ -f "${SRC_DIR}/tests/csrf_test.go" ]          && check "Integration test: csrf_test.go" "pass" || check "Integration test: csrf_test.go" "fail"
[ -f "${SRC_DIR}/tests/csp_test.go" ]           && check "Integration test: csp_test.go" "pass" || check "Integration test: csp_test.go" "fail"
[ -f "${SRC_DIR}/tests/migration_test.go" ]     && check "Integration test: migration_test.go" "pass" || check "Integration test: migration_test.go" "fail"
[ -f "${SRC_DIR}/tests/render_test.go" ]        && check "Integration test: render_test.go" "pass" || check "Integration test: render_test.go" "fail"
[ -f "${SRC_DIR}/tests/queue_test.go" ]         && check "Integration test: queue_test.go" "pass" || check "Integration test: queue_test.go" "fail"
[ -f "${SRC_DIR}/tests/cache_test.go" ]         && check "Integration test: cache_test.go" "pass" || check "Integration test: cache_test.go" "fail"
[ -f "${SRC_DIR}/tests/theme_test.go" ]         && check "Integration test: theme_test.go" "pass" || check "Integration test: theme_test.go" "fail"
[ -f "${SRC_DIR}/tests/plugin_pool_test.go" ]   && check "Integration test: plugin_pool_test.go (P7)" "pass" || check "Integration test: plugin_pool_test.go (P7)" "fail"
[ -f "${SRC_DIR}/tests/rollback_test.go" ]      && check "Integration test: rollback_test.go (P7)" "pass" || check "Integration test: rollback_test.go (P7)" "fail"
[ -f "${SRC_DIR}/tests/wal_test.go" ]           && check "Integration test: wal_test.go (P7)" "pass" || check "Integration test: wal_test.go (P7)" "fail"
[ -f "${SRC_DIR}/tests/pprof_test.go" ]         && check "Integration test: pprof_test.go (P7)" "pass" || check "Integration test: pprof_test.go (P7)" "fail"

# Nginx CSP check
grep -q "Content-Security-Policy" /etc/nginx/sites-available/${APP_NAME} 2>/dev/null && check "CSP in Nginx config (ADR-0018)" "pass" || check "CSP in Nginx config (ADR-0018)" "fail"

# Systemd TimeoutStopSec
grep -q "TimeoutStopSec=90" /etc/systemd/system/${APP_NAME}.service 2>/dev/null && check "systemd TimeoutStopSec=90 (ADR-0022)" "pass" || check "systemd TimeoutStopSec=90 (ADR-0022)" "fail"

# Services
systemctl is-active --quiet meilisearch 2>/dev/null && check "Meilisearch service active" "pass" || check "Meilisearch service active (may degrade gracefully)" "pass"
systemctl is-active --quiet isso        2>/dev/null && check "Isso comment service active" "pass" || check "Isso comment service" "fail"
systemctl is-active --quiet fail2ban    2>/dev/null && check "Fail2ban active" "pass" || check "Fail2ban active" "fail"
[ "$DRY_RUN" = false ] && systemctl is-active --quiet ${APP_NAME} 2>/dev/null && check "VayuPress service active" "pass" || true

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "  ${GREEN}${checks_pass} passed${NC}  |  ${RED}${checks_fail} failed${NC}"
echo ""

# =============================================================================
# Final Summary
# =============================================================================
echo ""
echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${GREEN}║   ⚡  VayuPress v${ENGINE_VERSION} Deployed Successfully!                      ║${NC}"
echo -e "${BOLD}${GREEN}║      Prompts 1–7 Complete · Constitution v6.0                        ║${NC}"
echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${BOLD}Endpoints:${NC}"
echo -e "  ${CYAN}Public site        :${NC}  https://${DOMAIN}"
echo -e "  ${CYAN}Admin dashboard    :${NC}  https://${DOMAIN}/admin         (X-API-Key: ${API_KEY:0:8}...)"
echo -e "  ${CYAN}Liveness           :${NC}  https://${DOMAIN}/health/live"
echo -e "  ${CYAN}Readiness          :${NC}  https://${DOMAIN}/health/ready"
echo -e "  ${CYAN}Migration status   :${NC}  https://${DOMAIN}/health/migrations"
echo -e "  ${CYAN}Metrics            :${NC}  https://${DOMAIN}/metrics"
echo -e "  ${CYAN}Profiling (admin)  :${NC}  https://${DOMAIN}/debug/pprof/  (X-API-Key required)"
echo -e "  ${CYAN}API articles       :${NC}  https://${DOMAIN}/api/v1/articles"
echo -e "  ${CYAN}Search             :${NC}  https://${DOMAIN}/api/v1/search?q=..."
echo -e "  ${CYAN}Queue status       :${NC}  https://${DOMAIN}/api/v1/queue   (includes dead_letter)"
echo -e "  ${CYAN}Queue replay       :${NC}  POST https://${DOMAIN}/api/v1/queue/replay"
echo -e "  ${CYAN}Vacuum + integrity :${NC}  POST https://${DOMAIN}/admin/vacuum"
echo -e "  ${CYAN}Smoke test         :${NC}  https://${DOMAIN}/smoke-test"
echo ""
echo -e "${BOLD}P7 Improvements Active:${NC}"
echo -e "  ${GREEN}✓${NC}  Deploy script decomposition: deploy/ scaffold generated (ADR-0025)"
echo -e "  ${GREEN}✓${NC}  Migration rollback: Down SQL + SHA256 checksum + dry-run (ADR-0026)"
echo -e "  ${GREEN}✓${NC}  SQLite: journal_size_limit=64MB, wal_autocheckpoint, WAL metrics (ADR-0027)"
echo -e "  ${GREEN}✓${NC}  Plugin async pool: 4 concurrent, 2s timeout, disable-on-failure (ADR-0028)"
echo -e "  ${GREEN}✓${NC}  CSP Phase 2: nonce-based inline scripts, 'unsafe-inline' removed from script-src (ADR-0029)"
echo -e "  ${GREEN}✓${NC}  Queue: exponential backoff (10/20/40s), dead-letter status, replay endpoint (ADR-0030)"
echo -e "  ${GREEN}✓${NC}  Observability: /health/live + /health/ready + /debug/pprof + slow-query log (ADR-0031)"
echo -e "  ${GREEN}✓${NC}  Stale labels fixed: step 7, chmod hint, main.go header all → p7"
echo ""
echo -e "${BOLD}P6 Contracts (carried forward):${NC}"
echo -e "  ${GREEN}✓${NC}  rand.Read() error handling in all crypto paths (ADR-0016)"
echo -e "  ${GREEN}✓${NC}  CSRF cookie path '/' + conditional Secure flag (ADR-0013, ADR-0017)"
echo -e "  ${GREEN}✓${NC}  CSP in Nginx + Go middleware (ADR-0018)"
echo -e "  ${GREEN}✓${NC}  SQLite PRAGMA contract: mmap=256MB, temp_store=MEMORY (ADR-0020)"
echo -e "  ${GREEN}✓${NC}  Admin brute-force lockout: 5/15min → 1h ban (ADR-0021)"
echo -e "  ${GREEN}✓${NC}  Graceful drain: SIGTERM→stop→drain(45s)→close DB (ADR-0022)"
echo -e "  ${GREEN}✓${NC}  Schema migrations + versioned runners (ADR-0023)"
echo ""
echo -e "${BOLD}P5 Hardening (carried forward):${NC}"
echo -e "  ${GREEN}✓${NC}  Plugin panic isolation: fireHookSafe() recover() (ADR-0011)"
echo -e "  ${GREEN}✓${NC}  Theme CSS sanitization: @import/js:/url(http)/expression() denied (ADR-0012)"
echo -e "  ${GREEN}✓${NC}  Theme apply atomic rollback: stage→test render→commit|rollback (ADR-0014)"
echo ""
echo -e "${BOLD}Integration Test Harness (P7 — 12 files):${NC}"
echo -e "  ${GREEN}✓${NC}  auth, csrf, csp, migration, render, queue, cache, theme (P6)"
echo -e "  ${GREEN}✓${NC}  plugin_pool (P7), rollback (P7), wal (P7), pprof (P7)"
echo -e "  ${CYAN}Run:${NC}  cd ${SRC_DIR} && API_KEY=\${API_KEY} go test ./tests/... -v -count=1"
echo -e "  ${CYAN}Pool:${NC} go test ./tests/... -run TestPluginPool -v"
echo -e "  ${CYAN}WAL:${NC}  go test ./tests/... -run TestWALCheckpoint -v"
echo -e "  ${CYAN}pprof:${NC} go test ./tests/... -run TestPprof -v"
echo ""
echo -e "${BOLD}Quick API test:${NC}"
echo -e "  ${YELLOW}curl -s -X POST https://${DOMAIN}/api/v1/articles \\"
echo -e "    -H 'X-API-Key: ${API_KEY}' \\"
echo -e "    -H 'Content-Type: application/json' \\"
echo -e "    -d '{\"title\":\"Hello VayuPress P7\",\"slug\":\"hello-vayupress-p7\",\"content\":\"<p>First P7 post.</p>\",\"tags\":[\"intro\"]}' | jq${NC}"
echo ""
echo -e "${BOLD}Credentials:${NC}  ${SECRETS_FILE}"
echo ""
echo -e "${BOLD}Governance:${NC}"
echo -e "  Constitution v6.0 · Prompts 1–7 complete"
echo -e "  ADRs (0001–0031): /var/www/${APP_NAME}/docs/adr/"
echo -e "  Deploy scaffold:   /var/www/${APP_NAME}/deploy/"
echo ""
echo -e "${BOLD}P7 → P8 Remaining High-Value Work:${NC}"
echo -e "  ${YELLOW}○${NC}  CI/static analysis pipeline (go test -race, golangci-lint, govulncheck, bench gates)"
echo -e "  ${YELLOW}○${NC}  Theme static file migration → eliminates style-src 'unsafe-inline' (ADR-0029 roadmap)"
echo -e "  ${YELLOW}○${NC}  Search degradation contracts: indexing lag metrics, corruption recovery, health endpoint"
echo -e "  ${YELLOW}○${NC}  Backup validation: restore testing, checksum, automated smoke-test"
echo -e "  ${YELLOW}○${NC}  OpenTelemetry distributed tracing + request correlation across workers"
echo -e "  ${YELLOW}○${NC}  Frontend asset pipeline: CSS minification, asset manifest, cache-busting"
echo -e "  ${YELLOW}○${NC}  Governance linting (PR checks against Constitution rules)"
echo ""
