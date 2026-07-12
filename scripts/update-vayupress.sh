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
# The binary lives in a service-owned, writable directory so the in-app one-click
# updater can atomically swap it. /usr/local/bin/vayupress is kept as a symlink to
# it for shell use. (Installing directly in /usr/local/bin breaks self-update: it
# is root-owned and read-only under the service sandbox.)
BIN_DIR="${BIN_DIR:-/var/lib/vayupress/bin}"
BIN_PATH="${BIN_PATH:-${BIN_DIR}/vayupress}"
LINK_PATH="${LINK_PATH:-/usr/local/bin/vayupress}"
SERVICE_USER="${SERVICE_USER:-www-data}"
DATA_DIR="${DATA_DIR:-/var/lib/vayupress}"
STATIC_DIR="${STATIC_DIR:-${DATA_DIR}/static}"
LOG_DIR="${LOG_DIR:-/var/log/vayupress}"
CACHE_DIR="${CACHE_DIR:-/var/cache/vayupress}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/vayupress}"
GO_BIN="${GO_BIN:-/usr/local/go/bin/go}"
SERVICE="${SERVICE:-vayupress}"
UNIT_PATH="${UNIT_PATH:-/etc/systemd/system/${SERVICE}.service}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/health/live}"
HEALTH_WAIT="${HEALTH_WAIT:-45}"
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
# built binary reports the version it was actually built from. The canonical
# source of truth is the .release-version file (e.g. "v2.9.5") — the same file CI
# stamps releases from — NOT the fallback default in main.go, which lags behind
# and is the reason a shell-built binary used to report a stale old version.
if [[ -z "$ENGINE_VERSION" ]]; then
  if [[ -s "$SRC_DIR/.release-version" ]]; then
    ENGINE_VERSION="$(tr -d 'vV \t\r\n' < "$SRC_DIR/.release-version")"
  fi
  if [[ -z "$ENGINE_VERSION" ]]; then
    ENGINE_VERSION="$(grep -oE 'var Version = "[0-9]+\.[0-9]+\.[0-9]+"' "$SRC_DIR/cmd/vayupress/main.go" 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
  fi
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
# Install into the service-owned writable directory. The in-app one-click
# updater swaps the binary by creating a temp file in this DIRECTORY and renaming
# it over the binary, so the DIRECTORY (not just the file) must be owned by the
# service user — otherwise the swap fails with "permission denied writing to
# <dir>". chown the directory AND the binary to the service user.
mkdir -p "$BIN_DIR"
# Keep the outgoing binary so a failed update can auto-roll-back to it (§6).
if [[ -f "$BIN_PATH" ]]; then
  cp -a "$BIN_PATH" "${BIN_PATH}.prev" 2>/dev/null || true
fi
install -m 0755 "$TMP_BIN" "$BIN_PATH"
rm -f "$TMP_BIN"
chown "${SERVICE_USER}:${SERVICE_USER}" "$BIN_DIR" "$BIN_PATH" 2>/dev/null || true
chmod 0755 "$BIN_DIR"
ok "Installed new binary at $BIN_PATH (dir owned by ${SERVICE_USER}, so in-app updates work)"

# ── 3b. Point /usr/local/bin at the writable binary (migrate legacy installs) ─
# Older installs placed a real binary at /usr/local/bin/vayupress, which the
# non-root service cannot self-update (root-owned + read-only under the sandbox).
# Replace it with a symlink to the writable copy: shell commands still resolve
# `vayupress` on PATH, and — because ExecStart resolves the symlink at launch —
# os.Executable() reports the writable target, so in-app updates work in place.
if [[ "$LINK_PATH" != "$BIN_PATH" ]]; then
  if [[ -e "$LINK_PATH" && ! -L "$LINK_PATH" ]]; then
    warn "Replacing legacy binary at $LINK_PATH with a symlink to $BIN_PATH."
  fi
  ln -sfn "$BIN_PATH" "$LINK_PATH"
  ok "Symlinked $LINK_PATH → $BIN_PATH"
fi

# ── 3c. Install/refresh the canonical systemd unit (heals drifted units) ──────
# The #1 cause of "runs fine by hand but crash-loops under systemd" is a stale or
# hand-edited unit whose ProtectSystem sandbox omits a directory the current
# binary writes at startup — most often STATIC_DIR, where the binary syncs its
# embedded admin assets on boot. The sandbox denies that write and the process
# exits (after logging "mode journal open"), so the site 502s while `run by hand`
# as root works. Rather than patch one directive via a drop-in, we write a
# complete, known-good unit on every update so the runtime cannot drift away from
# what the binary needs. The previous unit is backed up; operator customisations
# belong in drop-ins under ${SERVICE}.service.d/, which still layer on top.
if [[ -d /etc/systemd/system ]] && command -v systemctl >/dev/null 2>&1; then
  if [[ -f "$UNIT_PATH" ]]; then
    cp -a "$UNIT_PATH" "${UNIT_PATH}.prev" 2>/dev/null || true
  fi
  # Drop our own older ExecStart-only drop-ins; the full unit supersedes them and
  # a lingering "ExecStart=" reset from them could otherwise blank the command.
  rm -f "/etc/systemd/system/${SERVICE}.service.d/10-exec-writable-bin.conf" \
        "/etc/systemd/system/${SERVICE}.service.d/10-writable-bin.conf" 2>/dev/null || true
  cat > "$UNIT_PATH" <<UNIT_EOF
[Unit]
Description=VayuPress CMS Engine ${ENGINE_VERSION}
Documentation=https://github.com/johalputt/vayupress
After=network.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=/etc/vayupress/env
ExecStart=${BIN_PATH}
WorkingDirectory=${DATA_DIR}
Restart=always
RestartSec=5s
TimeoutStopSec=90
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
# Bind the privileged mail ports (25/110/143/465/587/993/995) as a non-root user.
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
# systemd creates and OWNS the state dir as the service user every start, so the
# binary directory is writable and the in-app one-click updater can swap in place.
StateDirectory=vayupress
# EVERY directory the binary writes at startup/runtime must be listed here, or a
# ProtectSystem sandbox denies the write and the process exits. STATIC_DIR is
# essential — the binary syncs its embedded admin assets there on boot.
ReadWritePaths=${DATA_DIR} ${LOG_DIR} ${CACHE_DIR} ${STATIC_DIR} ${BACKUP_DIR}
# Log to journald so a startup failure is visible with 'journalctl -u vayupress'
# instead of hidden in a file — the observability gap that made this hard to debug.
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vayupress

[Install]
WantedBy=multi-user.target
UNIT_EOF
  systemctl daemon-reload 2>/dev/null || true
  ok "Installed canonical systemd unit at ${UNIT_PATH} (logs now go to journald)."
fi

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

# ── 4b. Install/refresh the VayuShield privileged reconcile agent ────────────
# This tiny root service is what makes the Tier 2/3 network-hardening a one-click
# switch in VayuOS WITHOUT giving the (unprivileged) web app any privilege: the
# panel only writes an intent flag; this agent applies the fixed, vetted scripts.
# See deploy/vayushield-agent.sh + ADR-0123. Idempotent and best-effort — a
# failure here never blocks the update, and the agent does nothing until an
# operator flips a toggle. Set VAYUSHIELD_AGENT=0 to skip.
VAYUSHIELD_AGENT="${VAYUSHIELD_AGENT:-1}"
VS_LIB_DIR="${VS_LIB_DIR:-/usr/local/lib/vayushield}"
VS_CONTROL_DIR="${VS_CONTROL_DIR:-${DATA_DIR}/vayushield-control}"
if [[ "$VAYUSHIELD_AGENT" == "1" ]] && command -v systemctl >/dev/null 2>&1 && [[ -f "$SRC_DIR/deploy/vayushield-agent.sh" ]]; then
  info "Installing/refreshing the VayuShield Tier 2/3 helper agent…"
  install -d -m 0755 "$VS_LIB_DIR" 2>/dev/null || true
  install -m 0755 "$SRC_DIR/deploy/vayushield-firewall.sh" "$VS_LIB_DIR/vayushield-firewall.sh" 2>/dev/null || true
  install -m 0755 "$SRC_DIR/deploy/vayushield-agent.sh"    "$VS_LIB_DIR/vayushield-agent.sh"    2>/dev/null || true
  if [[ -f "$SRC_DIR/deploy/nginx-vayushield.conf" ]]; then
    install -m 0644 "$SRC_DIR/deploy/nginx-vayushield.conf" "$VS_LIB_DIR/nginx-vayushield.conf" 2>/dev/null || true
  fi
  # The control dir is owned by the service user so the unprivileged app can write
  # intent flags; the root agent only reads them and writes status/heartbeat.
  install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$VS_CONTROL_DIR" 2>/dev/null || install -d -m 0750 "$VS_CONTROL_DIR" 2>/dev/null || true
  if install -m 0644 "$SRC_DIR/deploy/vayushield-agent.service" /etc/systemd/system/vayushield-agent.service 2>/dev/null; then
    systemctl daemon-reload 2>/dev/null || true
    systemctl enable --now vayushield-agent 2>/dev/null || true
    ok "VayuShield helper agent installed — Tier 2/3 can now be toggled from VayuOS → Bot Shield → Network hardening."
  else
    warn "Could not install the VayuShield agent unit (need root?) — Tier 2/3 stay manual for now."
  fi
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

# ── 6. Restart, verify health, and AUTO-ROLLBACK on failure ───────────────────
# An update must never leave the site down. We restart, then poll the health
# endpoint for up to HEALTH_WAIT seconds (large databases warm slowly). If the
# new version never becomes healthy, we automatically restore the previous binary
# and restart, so the site comes back on the last-known-good version instead of
# crash-looping — then report loudly so the operator can investigate offline.
info "Restarting $SERVICE..."
systemctl restart "$SERVICE" || true

# Poll until healthy, OR the process actually exits (a real crash). We DISTINGUISH
# the two: a service that is still active but not yet answering /health is merely
# starting slowly (a large database warms the caches/search index in the
# background) — that is NOT a reason to roll back a good binary. We only roll back
# when the process has genuinely exited/failed.
healthy=0
crashed=0
for _ in $(seq 1 "$HEALTH_WAIT"); do
  if ! systemctl is-active --quiet "$SERVICE"; then
    crashed=1
    break
  fi
  if curl -sf "$HEALTH_URL" >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 1
done

if [[ "$healthy" == "1" ]]; then
  ok "Health check passed — VayuPress ${ENGINE_VERSION} is live."
elif [[ "$crashed" == "1" && -f "${BIN_PATH}.prev" ]]; then
  echo ""
  warn "New version exited on startup (a real failure). Recent logs:"
  journalctl -u "$SERVICE" -n 25 --no-pager 2>/dev/null || true
  warn "Auto-rolling back to the previous binary so your site comes back up…"
  cp -a "${BIN_PATH}.prev" "$BIN_PATH" 2>/dev/null || true
  chown "${SERVICE_USER}:${SERVICE_USER}" "$BIN_PATH" 2>/dev/null || true
  # Keep the freshly-written canonical unit (a fix, not a regression); it is a
  # superset of permissions, so the previous binary runs cleanly under it.
  systemctl restart "$SERVICE" || true
  for _ in $(seq 1 20); do
    systemctl is-active --quiet "$SERVICE" && curl -sf "$HEALTH_URL" >/dev/null 2>&1 && { healthy=1; break; }
    sleep 1
  done
  if [[ "$healthy" == "1" ]]; then
    die "Update FAILED and was ROLLED BACK — your site is back on the previous version. Check 'journalctl -u $SERVICE' for why the new build exited, then re-run once fixed."
  else
    die "Update FAILED and rollback did NOT restore health — investigate now: 'journalctl -u $SERVICE -n 60 --no-pager'."
  fi
else
  # Still ACTIVE but not answering yet — a slow first start on a large database.
  # Leave it running (do NOT roll back a healthy binary); it will finish warming
  # and start serving shortly.
  echo ""
  warn "VayuPress ${ENGINE_VERSION} is running but not answering /health yet after ${HEALTH_WAIT}s."
  warn "On a large database the first start warms the search index in the background,"
  warn "so this is normal — leaving it running. Watch it come up with:"
  echo "    journalctl -u $SERVICE -f    (wait for 'listening on :')"
fi

# ── 7. Keep the VayuTalk relay subdomain provisioned ──────────────────────────
# If the operator has pointed talk.<domain> at this server (CDN proxy OFF), make
# sure its cert SAN, nginx vhost, and advertisement stay in place across updates —
# so the mobile app's SSE stream keeps bypassing the CDN. Idempotent and non-fatal:
# with no talk.<domain> DNS it does nothing, so this is safe on every update.
TALK_SETUP="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)/setup-talk-subdomain.sh"
if [[ -f "$TALK_SETUP" ]]; then
  CACHE_DIR="$CACHE_DIR" bash "$TALK_SETUP" || true
fi

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
