#!/usr/bin/env bash
#
# setup-tor-space.sh — stand up a VayuOS **Tor Space** alongside an existing
# Clearnet install, so BOTH worlds run at the same time (ADR-0141).
#
# WHAT A TOR SPACE IS
# A second, fully separate VayuPress instance in anonymous mode
# (VAYUOS_MODE=tor): its OWN database, its OWN .onion address, served over a
# persistent Tor v3 onion — no clearnet domain, no CA-TLS, no clearnet
# callbacks. It shares NOTHING with the Clearnet Space (separate DB → separate
# accounts, content and identities), so the two worlds can never be correlated.
# That is the "no mesh" separation, by construction.
#
# WHAT THIS SCRIPT DOES (idempotent; safe to re-run)
#   1. Reuses the already-installed vayupress binary and the Tor daemon that the
#      main deploy (ADR-0138) set up — no rebuild, no second binary.
#   2. Creates the Tor Space's own dirs under /var/lib/vayupress-tor.
#   3. Adds a PERSISTENT Tor onion (HiddenService → 127.0.0.1:$TOR_PORT) to torrc
#      and reads back the generated .onion hostname.
#   4. Writes /etc/vayupress-tor/env (VAYUOS_MODE=tor, own PORT + DB, DOMAIN=onion)
#      and a vayupress-tor.service unit, then starts it.
#   5. Waits until the app is healthy, prints the auto-generated admin login and
#      the .onion — open http://<onion>/os in Tor Browser and sign in.
#
# The Clearnet Space is NEVER touched. To stop/remove the Tor Space service:
#   sudo bash scripts/setup-tor-space.sh --remove
#
# Run:  sudo bash scripts/setup-tor-space.sh
# Env overrides: TOR_PORT (default 8081), TOR_DATA_DIR, HS_DIR, BIN.

set -u

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
die()  { echo -e "${RED}❌ $*${NC}" >&2; exit 1; }

REMOVE=0
[[ "${1:-}" == "--remove" ]] && REMOVE=1

# ── Config (env → default) ────────────────────────────────────────────────────
TOR_PORT="${TOR_PORT:-8081}"
TOR_DATA_DIR="${TOR_DATA_DIR:-/var/lib/vayupress-tor}"
TOR_CACHE_DIR="${TOR_CACHE_DIR:-/var/cache/vayupress-tor}"
TOR_LOG_DIR="${TOR_LOG_DIR:-/var/log/vayupress-tor}"
TOR_STATIC_DIR="${TOR_STATIC_DIR:-${TOR_DATA_DIR}/static}"
TOR_BACKUP_DIR="${TOR_BACKUP_DIR:-${TOR_DATA_DIR}/backups}"
TOR_DB_PATH="${TOR_DB_PATH:-${TOR_DATA_DIR}/vayupress.db}"
TOR_ENV_DIR=/etc/vayupress-tor
TOR_ENV_FILE="${TOR_ENV_DIR}/env"
TOR_SERVICE=/etc/systemd/system/vayupress-tor.service
TORRC=/etc/tor/torrc
HS_DIR="${HS_DIR:-/var/lib/tor/vayupress-tor-space}"
BIN="${BIN:-/usr/local/bin/vayupress}"

[[ $EUID -eq 0 ]] || die "Run as root: sudo bash scripts/setup-tor-space.sh"

# ── Removal path ──────────────────────────────────────────────────────────────
if [[ $REMOVE -eq 1 ]]; then
  info "Removing the Tor Space service (the Clearnet Space is untouched)…"
  systemctl disable --now vayupress-tor 2>/dev/null || true
  rm -f "$TOR_SERVICE"
  systemctl daemon-reload 2>/dev/null || true
  # The .onion identity and database are KEPT so a later re-run restores the SAME
  # address. Print how to purge them permanently.
  warn "Service stopped and removed. The .onion identity and data are KEPT:"
  warn "   onion keys : ${HS_DIR}"
  warn "   database   : ${TOR_DATA_DIR}"
  warn "To permanently destroy the Tor Space identity + all its data:"
  warn "   rm -rf ${HS_DIR} ${TOR_DATA_DIR} ${TOR_ENV_DIR} ${TOR_CACHE_DIR} ${TOR_LOG_DIR}"
  warn "   then remove the 'VayuOS Tor Space' block from ${TORRC} and: systemctl reload tor"
  ok "Tor Space service removed."
  exit 0
fi

# ── Preconditions ─────────────────────────────────────────────────────────────
[[ -x "$BIN" ]] || die "vayupress binary not found at ${BIN} — run scripts/deploy-vayupress.sh first."
id www-data >/dev/null 2>&1 || die "www-data service user missing — run the main deploy first."

if ! command -v tor >/dev/null 2>&1; then
  info "Tor not installed — installing…"
  apt-get update -qq || warn "apt-get update failed — attempting install anyway…"
  apt-get install -y -qq tor || die "Failed to install tor."
fi
systemctl enable tor >/dev/null 2>&1 || true

# ── 1. Tor Space directories (own DB → no correlation with clearnet) ──────────
info "Creating Tor Space directories under ${TOR_DATA_DIR}…"
mkdir -p "$TOR_DATA_DIR" "$TOR_CACHE_DIR" "$TOR_LOG_DIR" "$TOR_STATIC_DIR" "$TOR_BACKUP_DIR" "$TOR_ENV_DIR"
chown -R www-data:www-data "$TOR_DATA_DIR" "$TOR_CACHE_DIR" "$TOR_LOG_DIR"
chmod 700 "$TOR_ENV_DIR"

# ── 2. Persistent Tor onion (HiddenService) ───────────────────────────────────
# Tor itself creates and owns HS_DIR (mode 700, debian-tor) on (re)load — we must
# NOT pre-create it, or tor refuses to use a wrongly-owned directory.
if ! grep -q "VayuOS Tor Space" "$TORRC" 2>/dev/null; then
  info "Adding a persistent Tor onion for the Tor Space…"
  cat >> "$TORRC" <<TORCONF

# ── VayuOS Tor Space — added by setup-tor-space.sh ───────────────────────────
# A standing v3 onion that fronts the Tor-mode VayuPress instance on
# 127.0.0.1:${TOR_PORT}. This is a SEPARATE world from the clearnet site.
HiddenServiceDir ${HS_DIR}/
HiddenServicePort 80 127.0.0.1:${TOR_PORT}
TORCONF
else
  info "Tor Space onion already present in torrc — leaving it unchanged."
fi
# Apply the config with a SIGHUP reload, NEVER a full restart: the clearnet
# install's per-domain onions are ephemeral control-port services that a tor
# RESTART would tear down (blacking out the clearnet site's .onions). reload-or-
# restart re-reads torrc — creating the HiddenService and its hostname — and
# starts tor if it was stopped, all without bouncing an already-running daemon.
systemctl reload-or-restart tor || die "tor reload failed — check 'journalctl -u tor'."

# Wait for tor to publish the onion hostname.
ONION=""
for _ in $(seq 1 30); do
  [[ -r "${HS_DIR}/hostname" ]] && ONION="$(cat "${HS_DIR}/hostname" 2>/dev/null)"
  [[ -n "$ONION" ]] && break
  sleep 1
done
[[ -n "$ONION" ]] || die "Tor did not publish an onion at ${HS_DIR}/hostname — check 'journalctl -u tor'."
ok "Tor Space onion: ${ONION}"

# Record the onion where a sibling clearnet install (same www-data owner) can
# read it, so its VayuOS "Spaces" page can display this Tor Space's address. The
# onion is public, so world-readable is fine.
printf '%s\n' "$ONION" > "${TOR_DATA_DIR}/onion.txt"
chmod 644 "${TOR_DATA_DIR}/onion.txt"

# ── 3. Runtime env for the Tor Space ──────────────────────────────────────────
info "Writing ${TOR_ENV_FILE}…"
cat > "$TOR_ENV_FILE" <<ENV
# VayuOS Tor Space — anonymous world (ADR-0141). Separate DB + identity from the
# clearnet install; VAYUOS_MODE=tor disables every clearnet callback.
VAYUOS_MODE=tor
DOMAIN=${ONION}
PORT=${TOR_PORT}
DB_PATH=${TOR_DB_PATH}
CACHE_DIR=${TOR_CACHE_DIR}
STATIC_DIR=${TOR_STATIC_DIR}
LOG_DIR=${TOR_LOG_DIR}
# Served by its own persistent onion (torrc HiddenService), not the per-domain
# VayuTor engine — so the engine's control port stays off in this world.
VAYUOS_TOR=off
ENV
chmod 600 "$TOR_ENV_FILE"
ok "Runtime config written to ${TOR_ENV_FILE}"

# ── 4. systemd unit for the Tor Space ─────────────────────────────────────────
info "Writing ${TOR_SERVICE}…"
cat > "$TOR_SERVICE" <<SYSTEMD
[Unit]
Description=VayuPress — VayuOS Tor Space (anonymous .onion world)
Documentation=https://vayupress.com/docs
After=network.target tor.service
Wants=tor.service

[Service]
Type=simple
User=www-data
Group=www-data
EnvironmentFile=${TOR_ENV_FILE}
ExecStart=${BIN}
WorkingDirectory=${TOR_DATA_DIR}
Restart=always
RestartSec=5s
TimeoutStopSec=90
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
# The Tor world is web-only (no SMTP/IMAP over Tor), so NO privileged-port
# capability is granted here — a tighter sandbox than the clearnet service.
StateDirectory=vayupress-tor
ReadWritePaths=${TOR_DATA_DIR} ${TOR_LOG_DIR} ${TOR_CACHE_DIR} ${TOR_STATIC_DIR} ${TOR_BACKUP_DIR}
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vayupress-tor

[Install]
WantedBy=multi-user.target
SYSTEMD

systemctl daemon-reload
systemctl enable vayupress-tor >/dev/null 2>&1 || true
# restart (not just start) so a RE-RUN converges the live process onto the
# rewritten unit/env; restart also starts the service on a first run.
systemctl restart vayupress-tor || die "vayupress-tor.service failed to start — see 'journalctl -u vayupress-tor'."

# Type=simple marks the unit active the instant the process is exec'd, so poll
# until the app actually answers before declaring success — this also surfaces a
# crash-loop (e.g. a port collision) instead of printing a false "started".
info "Waiting for the Tor Space to become ready…"
READY=0
for _ in $(seq 1 30); do
  if systemctl is-active --quiet vayupress-tor; then
    if ! command -v curl >/dev/null 2>&1 || curl -sf -o /dev/null "http://127.0.0.1:${TOR_PORT}/health" 2>/dev/null; then
      READY=1; break
    fi
  else
    die "vayupress-tor.service exited during startup — see 'journalctl -u vayupress-tor'."
  fi
  sleep 1
done
[[ $READY -eq 1 ]] || die "Tor Space did not answer on 127.0.0.1:${TOR_PORT}/health — see 'journalctl -u vayupress-tor'."
ok "Tor Space service is up and healthy (vayupress-tor.service, 127.0.0.1:${TOR_PORT})."

# ── First-run admin credentials ───────────────────────────────────────────────
# There is NO web signup: on a fresh DB the binary bootstraps an admin and writes
# it here. Surface it so the operator can actually sign in (deploy does the same).
CRED_FILE="${TOR_DATA_DIR}/initial-admin.txt"
echo
if [[ -r "$CRED_FILE" ]]; then
  ok "First-run admin credentials for the Tor Space (also saved at ${CRED_FILE}):"
  sed 's/^/    /' "$CRED_FILE"
else
  info "An admin account already exists for this Tor Space — sign in with your saved credentials."
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo
ok "VayuOS Tor Space is live — running ALONGSIDE your Clearnet Space."
info "Onion address : http://${ONION}"
info "Admin (VayuOS): http://${ONION}/os   (open in Tor Browser and sign in with the credentials above)"
info "Service        : systemctl status vayupress-tor  |  journalctl -u vayupress-tor -f"
echo
info "The two worlds share NOTHING (separate databases). To move content across,"
info "use the offline bundle:  vayupress migrate export --file bundle.vaybundle"
exit 0
