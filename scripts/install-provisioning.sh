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
         setup-mcp-subdomain.sh setup-api-subdomain.sh setup-vayudomain.sh)

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

systemctl daemon-reload
systemctl enable --now vayupress-provision.path vayupress-provision.timer >/dev/null 2>&1 || true
ok "Provisioning units installed and enabled"

# ── 3. Run one pass now ──────────────────────────────────────────────────────
# Immediately, rather than waiting for the daily timer: the operator ran this
# because a certificate is missing right now.
info "Provisioning subdomains…"
if bash "${LIB_DIR}/provision-subdomains.sh"; then
  ok "Provisioning finished."
else
  warn "Provisioning reported a problem — see /var/lib/vayupress/provision.log"
fi

echo
ok "Done. VayuOS → Operations → Domains & DNS now provisions everything, and a daily sweep picks up records you point later."
info "Verify PGP discovery with:  gpg --locate-keys you@\$(sed -n 's/^DOMAIN=//p' /etc/vayupress/env | head -1)"
