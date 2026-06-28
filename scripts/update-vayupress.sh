#!/bin/bash
# =============================================================================
#  update-vayupress.sh — Fast in-place update for an existing VayuPress install
# =============================================================================
#
#  Use this when you already ran deploy-vayupress.sh once and just need to pull
#  the latest code, rebuild the binary, refresh static assets, and restart.
#
#  It is deliberately strict: if the build fails it STOPS and does NOT restart
#  the service, so you never silently keep running an old binary (the usual
#  reason "my change isn't showing up" happens).
#
#  USAGE
#    sudo ./scripts/update-vayupress.sh
#
#  Override paths via env if your install differs from the defaults:
#    SRC_DIR=/tmp/VayuPress STATIC_DIR=/var/lib/vayupress/static \
#      sudo -E ./scripts/update-vayupress.sh
# =============================================================================
set -euo pipefail

SRC_DIR="${SRC_DIR:-/tmp/VayuPress}"
BRANCH="${BRANCH:-main}"
BIN_PATH="${BIN_PATH:-/usr/local/bin/vayupress}"
STATIC_DIR="${STATIC_DIR:-/var/lib/vayupress/static}"
GO_BIN="${GO_BIN:-/usr/local/go/bin/go}"
SERVICE="${SERVICE:-vayupress}"
# Version is derived from the freshly-pulled source just before the build (see
# below), so the binary always reports the version it was actually built from.
# Override with ENGINE_VERSION=... if you need a custom stamp.
ENGINE_VERSION="${ENGINE_VERSION:-}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
die()  { echo -e "${RED}❌ $*${NC}" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Run as root (sudo)."
[[ -d "$SRC_DIR/.git" ]] || die "No git checkout at $SRC_DIR. Clone it first:
  git clone https://github.com/johalputt/VayuPress.git $SRC_DIR"
[[ -x "$GO_BIN" ]] || die "Go not found at $GO_BIN. Set GO_BIN=/path/to/go."

# ── 1. Pull latest ───────────────────────────────────────────────────────────
info "Pulling latest from origin/$BRANCH..."
git -C "$SRC_DIR" fetch origin "$BRANCH"
git -C "$SRC_DIR" checkout "$BRANCH"
git -C "$SRC_DIR" pull --ff-only origin "$BRANCH"
NEW_SHA=$(git -C "$SRC_DIR" rev-parse --short HEAD)
ok "At commit $NEW_SHA"

# Derive the version from the freshly-pulled source unless overridden, so the
# built binary reports the version it was actually built from.
if [[ -z "$ENGINE_VERSION" ]]; then
  ENGINE_VERSION="$(grep -oE 'var Version = "[0-9]+\.[0-9]+\.[0-9]+"' "$SRC_DIR/cmd/vayupress/main.go" 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
  ENGINE_VERSION="${ENGINE_VERSION:-dev}"
fi
info "Building version ${ENGINE_VERSION} (commit ${NEW_SHA})"

# ── 2. Build to a temp path FIRST (never clobber a working binary on failure) ─
# Disk preflight — a CGO build needs build-cache headroom; a full disk is the
# single most common cause of a failed/half update (and of the live site dying).
AVAIL_MB=$(df -Pm "$SRC_DIR" 2>/dev/null | awk 'NR==2{print $4+0}')
if [[ "${AVAIL_MB:-0}" -lt 1200 ]]; then
  die "Low disk: only ${AVAIL_MB}MB free at $SRC_DIR (need ~1.2GB for the build).
  Free space and retry, e.g.:
    sudo journalctl --vacuum-size=200M
    \"$GO_BIN\" clean -cache"
fi

# Build at IDLE CPU/IO priority with capped parallelism so the LIVE site (served
# by the still-running old binary) and nginx stay responsive during the compile.
# This is what keeps a deploy from spiking a small VPS into 502s. Override the
# core count with BUILD_JOBS=N if you want it faster (at the cost of headroom).
CORES="$(nproc 2>/dev/null || echo 1)"
BUILD_JOBS="${BUILD_JOBS:-$(( CORES >= 2 ? CORES / 2 : 1 ))}"
NICE_CMD=(nice -n 19)
if command -v ionice >/dev/null 2>&1; then NICE_CMD=(ionice -c3 nice -n 19); fi
TMP_BIN="$(mktemp /tmp/vayupress.XXXXXX)"
info "Building binary at low priority (CGO, ${BUILD_JOBS}/${CORES} cores)..."
if ! (cd "$SRC_DIR" && "${NICE_CMD[@]}" env CGO_ENABLED=1 "GOMAXPROCS=${BUILD_JOBS}" \
        "$GO_BIN" build -p "$BUILD_JOBS" \
        -ldflags="-s -w -X main.Version=${ENGINE_VERSION}" \
        -o "$TMP_BIN" ./cmd/vayupress/); then
  rm -f "$TMP_BIN"
  die "BUILD FAILED — service left running on the OLD binary. Fix the error above and re-run."
fi
ok "Build succeeded ($(du -h "$TMP_BIN" | cut -f1))"

# ── 3. Swap the binary in atomically ─────────────────────────────────────────
install -m 0755 "$TMP_BIN" "$BIN_PATH"
rm -f "$TMP_BIN"
ok "Installed new binary at $BIN_PATH"

# ── 4. Refresh static assets (CSS/JS/fonts) into STATIC_DIR ───────────────────
if [[ -d "$SRC_DIR/static" ]]; then
  info "Refreshing static assets in $STATIC_DIR..."
  mkdir -p "$STATIC_DIR"
  cp -r "$SRC_DIR/static/." "$STATIC_DIR/"
  chown -R www-data:www-data "$STATIC_DIR" 2>/dev/null || true
  ok "Static assets refreshed."
else
  warn "No static/ dir in $SRC_DIR — skipping asset copy."
fi

# Copy docs (ADR registry, guides) to a stable data location so the ADR page
# works even after /tmp is cleared. The handler probes /var/lib/vayupress/docs.
DOCS_DEST="${DOCS_DEST:-/var/lib/vayupress/docs}"
if [[ -d "$SRC_DIR/docs" ]]; then
  info "Refreshing docs in $DOCS_DEST..."
  mkdir -p "$DOCS_DEST"
  # Mirror the repo's ADR directory exactly: remove the destination adr/ first so
  # ADRs that were renamed or removed upstream (and any old auto-generated stubs)
  # do not linger and inflate/duplicate the registry. A plain `cp` never deletes,
  # which is why the on-disk registry could drift away from the shipped set.
  rm -rf "$DOCS_DEST/adr"
  cp -r "$SRC_DIR/docs/." "$DOCS_DEST/"
  chown -R www-data:www-data "$DOCS_DEST" 2>/dev/null || true
  ok "Docs refreshed (ADR registry mirrors the shipped set)."
else
  warn "No docs/ dir in $SRC_DIR — ADR page may be empty."
fi

# ── 5. Back up the database (default ON, consistent, never blocks) ───────────
# A consistent snapshot (sqlite3 online backup) is taken before the restart so a
# migration-bearing release always has a rollback point. A hard timeout
# guarantees it can never stall the update — on a busy/large DB it simply warns
# and continues. Disable with BACKUP_DB=0 if you manage backups elsewhere.
DB_PATH="${DB_PATH:-/var/lib/vayupress/vayupress.db}"
BACKUP_DB="${BACKUP_DB:-1}"
BACKUP_TIMEOUT="${BACKUP_TIMEOUT:-120}"
if [[ "$BACKUP_DB" == "1" ]]; then
  if [[ -f "$DB_PATH" ]] && command -v sqlite3 >/dev/null 2>&1; then
    BACKUP="${DB_PATH%.db}.backup-$(date +%Y%m%d-%H%M%S).db"
    info "Backing up DB to $BACKUP (max ${BACKUP_TIMEOUT}s)..."
    if timeout "$BACKUP_TIMEOUT" sqlite3 -cmd ".timeout 5000" "$DB_PATH" ".backup '$BACKUP'" 2>/dev/null; then
      ok "DB backup written: $BACKUP ($(du -h "$BACKUP" | cut -f1))"
    else
      rm -f "$BACKUP"
      warn "DB backup timed out/failed (continuing). Back up $DB_PATH manually if needed."
    fi
  else
    warn "BACKUP_DB=1 but DB not found at $DB_PATH or sqlite3 missing — skipping backup."
  fi
else
  info "DB backup skipped (set BACKUP_DB=1 to snapshot before updating)."
fi

# ── 6. Restart and verify ────────────────────────────────────────────────────
info "Restarting $SERVICE..."
systemctl restart "$SERVICE"
sleep 2

if ! systemctl is-active --quiet "$SERVICE"; then
  echo ""
  warn "Service is NOT active after restart. Last 30 log lines:"
  journalctl -u "$SERVICE" -n 30 --no-pager || true
  die "Service failed to start — see logs above."
fi
ok "Service is active."

# Probe the local health endpoint (best-effort).
for i in $(seq 1 10); do
  if curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1; then
    ok "Health check passed."
    break
  fi
  sleep 1
  [[ $i -eq 10 ]] && warn "Health check did not respond in 10s — check journalctl -u $SERVICE."
done

echo ""
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  VayuPress updated to ${NEW_SHA} and restarted.${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
echo ""
echo "  Verify in a logged-in browser:"
echo "    • Settings  → https://\$DOMAIN/os/settings   (Save changes button, top-right + bottom)"
echo "    • Modes     → https://\$DOMAIN/os/modes       (should render, not JSON 401)"
echo "    • Media     → https://\$DOMAIN/os/media       (upload should succeed)"
echo ""
echo "  Hard-refresh the browser (Ctrl+Shift+R) to bypass cached CSS/JS."
