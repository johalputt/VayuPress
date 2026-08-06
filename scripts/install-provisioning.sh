#!/usr/bin/env bash
#
# install-provisioning.sh — install one-click subdomain provisioning, then run it.
#
# WHY A SEPARATE SCRIPT
# Installing a systemd unit needs root, and the VayuPress service runs as
# www-data with NoNewPrivileges — by design, so it can never do this itself. That
# leaves exactly one root step in an otherwise terminal-free product, and the
# guidance for it used to be "re-run deploy-vayupress.sh", which is a full
# rebuild-and-restart of a live site to install three small files. Operators
# reasonably hesitated, and the certificate never got issued.
#
# This does only the privileged bit: place the helpers root-owned, write the
# units, enable them, and run one pass immediately so the certificate appears
# now rather than at the next daily sweep. It touches neither the binary nor the
# database, and it is safe to re-run.
#
#   curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/install-provisioning.sh | sudo bash
#
# After this, VayuOS → Operations → Domains & DNS provisions everything, and a
# daily timer picks up any record pointed later. No further terminal use.

set -euo pipefail

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
die()  { echo -e "${RED}❌ $*${NC}" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Run as root:  sudo bash $0"

LIB_DIR=/usr/local/lib/vayupress
RAW="https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts"
HELPERS=(provision-subdomains.sh setup-openpgpkey-subdomain.sh setup-talk-subdomain.sh
         setup-mcp-subdomain.sh setup-api-subdomain.sh setup-vayudomain.sh
         vayuveil-harden.sh)

# ── 1. Find the helpers locally, or fetch them ───────────────────────────────
# Prefer a local checkout so an air-gapped or pinned install is not silently
# upgraded to whatever main holds today.
SRC=""
for cand in "$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)" \
            /tmp/VayuPress/scripts /opt/VayuPress/scripts /root/VayuPress/scripts; do
  [[ -n "$cand" && -f "${cand}/provision-subdomains.sh" ]] && { SRC="$cand"; break; }
done

mkdir -p "$LIB_DIR"
if [[ -n "$SRC" ]]; then
  info "Installing helpers from ${SRC}"
  for h in "${HELPERS[@]}"; do
    [[ -f "${SRC}/${h}" ]] && install -o root -g root -m 0755 "${SRC}/${h}" "${LIB_DIR}/${h}"
  done
else
  info "No local checkout found — fetching helpers"
  command -v curl >/dev/null 2>&1 || die "curl is required to fetch the helpers."
  for h in "${HELPERS[@]}"; do
    tmp="$(mktemp)"
    if curl -fsSL "${RAW}/${h}" -o "$tmp"; then
      # Refuse anything that is not a shell script. A truncated download or an
      # error page installed to a root-executed path is far worse than a failure.
      if head -1 "$tmp" | grep -q '^#!'; then
        install -o root -g root -m 0755 "$tmp" "${LIB_DIR}/${h}"
      else
        warn "skipping ${h}: downloaded content is not a script"
      fi
    else
      warn "skipping ${h}: download failed"
    fi
    rm -f "$tmp"
  done
fi

[[ -x "${LIB_DIR}/provision-subdomains.sh" ]] || die "Could not install the provisioning worker."

# root:root 0755 — world-readable, writable only by root. This is the line that
# makes the whole mechanism safe: the unprivileged service must never be able to
# change what the root unit executes.
chown root:root "$LIB_DIR"
chmod 0755 "$LIB_DIR"
ok "Helpers installed to ${LIB_DIR} (root-owned)"

# ── 2. systemd units ─────────────────────────────────────────────────────────
cat > /etc/systemd/system/vayupress-provision.service <<'SYSTEMD'
[Unit]
Description=VayuPress — provision subdomain certificates and vhosts
Documentation=https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md
After=network-online.target nginx.service
Wants=network-online.target

[Service]
Type=oneshot
# The service's own configuration, so the helpers can call the VayuPress CLI.
#
# Its absence is what stopped provisioning entirely on at least one install: the
# worker ran with a bare environment, every `vayupress domains …` call inside it
# died on `required env not set: API_KEY`, and the helper — which discarded
# stderr and never checked the exit status — logged "No sync-approved secondary
# domains, nothing to do". A fatal configuration error read as a clean no-op,
# every day, for a week. The leading "-" keeps a box with no env file starting
# rather than failing, which is the historic behaviour.
EnvironmentFile=-/etc/vayupress/env
# Root because certbot and `nginx -s reload` require it. A fixed, root-owned
# path with no arguments — nothing the requesting service supplies reaches here.
ExecStart=/usr/local/lib/vayupress/provision-subdomains.sh
TimeoutStartSec=900
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vayupress-provision
SYSTEMD

cat > /etc/systemd/system/vayupress-provision.path <<'SYSTEMD'
[Unit]
Description=VayuPress — watch for a subdomain provisioning request

[Path]
# The unprivileged service creates this file to request a run. Its CONTENTS are
# never read: the request is the file's existence and nothing more.
PathExists=/var/lib/vayupress/provision.request
Unit=vayupress-provision.service

[Install]
WantedBy=multi-user.target
SYSTEMD

cat > /etc/systemd/system/vayupress-provision.timer <<'SYSTEMD'
[Unit]
Description=VayuPress — daily subdomain provisioning sweep

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
SYSTEMD

# ── Zero-downtime restarts: socket activation (ADR-0155 P5) ──────────────────
#
# systemd binds and HOLDS the listening socket; the service inherits it. Across a
# restart the socket stays open, so connections queue in the kernel backlog
# instead of being refused — nginx sees a slow response rather than a connection
# error, and the visitor gets latency rather than a 502.
#
# Written only when the service unit exists and does not already carry a
# Requires= for it, and NEVER enabled here. Installing this is a change to how
# the service acquires its listener; it takes effect on the operator's next
# restart, chosen by them, not as a side effect of a script that was run to fix
# a certificate.
VP_PORT="$(sed -n 's/^PORT=//p' /etc/vayupress/env 2>/dev/null | head -1 | tr -d "\"' " || true)"
VP_PORT="${VP_PORT:-8080}"
if [[ -f /etc/systemd/system/vayupress.service ]]; then
  cat > /etc/systemd/system/vayupress.socket <<SOCKET
[Unit]
Description=VayuPress — listening socket, held across restarts
Documentation=https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0155-certificates-without-a-restart.md

[Socket]
# Loopback only. nginx terminates TLS and proxies here, so nothing outside this
# machine should reach the app directly — and in Tor mode the binary REFUSES a
# non-loopback inherited socket outright rather than publishing an install whose
# whole premise is that it is reachable only through its onion service.
ListenStream=127.0.0.1:${VP_PORT}
# The queue that turns a restart into latency instead of an error. Too small and
# a busy site still refuses connections during the gap.
Backlog=1024

[Install]
WantedBy=sockets.target
SOCKET
  systemctl daemon-reload >/dev/null 2>&1 || true
  ok "Socket unit written (vayupress.socket, 127.0.0.1:${VP_PORT}) — NOT enabled."
  info "  Enable it when you are ready for restarts to queue instead of 502:"
  info "    sudo systemctl enable --now vayupress.socket && sudo systemctl restart vayupress"
  info "  The binary uses an inherited socket only if one is passed, so nothing changes until then."
else
  info "No vayupress.service on this host; skipping the socket unit."
fi

# ── VayuVeil unit hardening (ADR-0150 S6) ────────────────────────────────────
# Requested from VayuOS → VayuVeil, never scheduled. There is deliberately NO
# .timer beside this one: the worker restarts the service so the directives take
# effect, and a daily sweep that bounces a live install to re-apply hardening it
# already has would be a self-inflicted outage on a schedule.
cat > /etc/systemd/system/vayupress-veilharden.service <<'SYSTEMD'
[Unit]
Description=VayuPress — apply the VayuVeil hardening drop-in and verify the service came back
Documentation=https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0150-vayuveil-endpoint-observation-control.md

[Service]
Type=oneshot
EnvironmentFile=-/etc/vayupress/env
# Root because editing a unit and reloading systemd require it. A fixed,
# root-owned path with no arguments — the requesting service supplies nothing
# that reaches here, and the directive list is inside the script.
ExecStart=/usr/local/lib/vayupress/vayuveil-harden.sh
TimeoutStartSec=300
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vayupress-veilharden
SYSTEMD

cat > /etc/systemd/system/vayupress-veilharden.path <<'SYSTEMD'
[Unit]
Description=VayuPress — watch for a VayuVeil hardening request

[Path]
# The unprivileged service creates this file to request a run. Its CONTENTS are
# never read: the request is the file's existence and nothing more.
PathExists=/var/lib/vayupress/veilharden.request
Unit=vayupress-veilharden.service

[Install]
WantedBy=multi-user.target
SYSTEMD

systemctl daemon-reload
# REPORTED, NOT DISCARDED. This line used to end in `|| true` with its output
# sent to /dev/null — the same shape as the reload bug that cost this project a
# day, and with a worse consequence. The .path unit is what consumes a
# provisioning request from the panel. If enabling it fails and nobody is told,
# every request the operator makes queues forever, the daily timer becomes the
# only pass that ever runs, and because the helper self-upgrade only happens
# when the worker runs, no fix shipped afterwards can reach this machine. From
# the panel that is indistinguishable from a repair that did not work.
if enable_out="$(systemctl enable --now vayupress-provision.path vayupress-provision.timer \
                                       vayupress-veilharden.path 2>&1)"; then
  ok "Provisioning units installed and enabled"
else
  warn "The provisioning units were written but could NOT be enabled. systemd said:"
  printf '%s\n' "${enable_out:-no output}" | sed 's/^/      /' >&2
  warn "Until this is resolved, a Provision request from the panel will not be picked up,"
  warn "and only the daily sweep will run. VayuShield → Certificate helpers → Repair the"
  warn "certificate helpers re-arms these units without needing a shell."
fi

if [[ -x "${LIB_DIR}/vayuveil-harden.sh" ]]; then
  ok "VayuVeil hardening worker installed — request it from VayuOS → VayuVeil"
else
  warn "The VayuVeil hardening worker is not present; VayuOS → VayuVeil will say so rather than"
  warn "offering a button that does nothing."
fi

# ── 3. Run one pass now ──────────────────────────────────────────────────────
# Immediately, rather than waiting for the daily timer: the operator ran this
# because a certificate is missing right now.
#
# The hardening worker is NOT run here. It restarts the VayuPress service, and a
# script installed to fix a missing certificate must not take a live site down as
# a side effect of something the operator did not ask for. It runs when the panel
# requests it, which is the point at which the operator has read what it does.
info "Provisioning subdomains…"
if bash "${LIB_DIR}/provision-subdomains.sh"; then
  ok "Provisioning finished."
else
  warn "Provisioning reported a problem — see /var/lib/vayupress/provision.log"
fi

echo
ok "Done. VayuOS → Operations → Domains & DNS now provisions everything, and a daily sweep picks up records you point later."
info "Verify PGP discovery with:  gpg --locate-keys you@\$(sed -n 's/^DOMAIN=//p' /etc/vayupress/env | head -1)"
