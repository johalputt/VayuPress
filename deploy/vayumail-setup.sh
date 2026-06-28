#!/bin/bash
# vayumail-setup.sh — make VayuMail reachable from real mail apps.
#
# "Could not open connection to server" in a mail client (Gmail app, Apple Mail,
# Thunderbird, Outlook) almost always means the mail PORTS are not reachable —
# not a password problem. On a typical VPS there are two blockers:
#
#   1) VayuPress runs as a NON-ROOT service, so it cannot bind the privileged
#      mail ports (25/110/143/587/993/995) without CAP_NET_BIND_SERVICE.
#   2) The host/cloud FIREWALL does not allow those ports inbound.
#
# This script fixes both, idempotently, via a systemd drop-in (so it works no
# matter how old your installed unit is) and your firewall, then restarts the
# service and verifies the listeners. It also reminds you about DNS + TLS.
#
# Usage:  sudo bash deploy/vayumail-setup.sh
#
# Override the service name or domain if needed:
#   SERVICE=vayupress MAIL_DOMAIN=mail.example.com sudo -E bash deploy/vayumail-setup.sh

set -euo pipefail

SERVICE="${SERVICE:-vayupress}"
MAIL_PORTS=(25 110 143 465 587 993 995)

info() { printf '\033[36mℹ\033[0m  %s\n' "$1"; }
ok()   { printf '\033[32m✅\033[0m %s\n' "$1"; }
warn() { printf '\033[33m⚠\033[0m  %s\n' "$1"; }

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Please run as root (sudo)." >&2
  exit 1
fi

# ── 1) Grant CAP_NET_BIND_SERVICE via a systemd drop-in ─────────────────────
# A drop-in overrides whatever the main unit says, so this is safe even if the
# installed vayupress.service predates VayuMail.
DROPIN_DIR="/etc/systemd/system/${SERVICE}.service.d"
info "Granting CAP_NET_BIND_SERVICE to ${SERVICE} (so it can bind privileged mail ports)..."
mkdir -p "$DROPIN_DIR"
cat > "${DROPIN_DIR}/vayumail.conf" <<'EOF'
[Service]
# Allow the non-root service to bind mail ports below 1024 (25/110/143/587/993/995).
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
EOF
systemctl daemon-reload
ok "Capability drop-in written to ${DROPIN_DIR}/vayumail.conf"

# ── 2) Open the firewall for the mail ports ─────────────────────────────────
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  info "Opening mail ports in ufw..."
  for p in "${MAIL_PORTS[@]}"; do ufw allow "${p}/tcp" >/dev/null 2>&1 || true; done
  ok "ufw rules added for: ${MAIL_PORTS[*]}"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  info "Opening mail ports in firewalld..."
  for p in "${MAIL_PORTS[@]}"; do firewall-cmd --permanent --add-port="${p}/tcp" >/dev/null 2>&1 || true; done
  firewall-cmd --reload >/dev/null 2>&1 || true
  ok "firewalld rules added for: ${MAIL_PORTS[*]}"
else
  warn "No active ufw/firewalld detected. If a host or CLOUD firewall (AWS/GCP/Oracle/etc.) is in front of this VPS, open inbound TCP ${MAIL_PORTS[*]} there manually."
fi

# ── 3) Restart and verify ───────────────────────────────────────────────────
info "Restarting ${SERVICE}..."
systemctl restart "$SERVICE"
sleep 2

info "Checking listeners (you should see 143, 993, 110, 995, 587)..."
LISTEN="$(ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null || true)"
FOUND=0
for p in 143 993 110 995 587 25; do
  if printf '%s\n' "$LISTEN" | grep -qE "[:.]${p}[[:space:]]"; then
    ok "listening on :${p}"
    FOUND=$((FOUND+1))
  else
    warn "NOT listening on :${p}"
  fi
done

echo
if [[ "$FOUND" -ge 3 ]]; then
  ok "VayuMail listeners are up."
else
  warn "Few/no mail listeners are up. Check logs:  journalctl -u ${SERVICE} --since '2 min ago' | grep -i -E 'vayumail|imap|pop3|smtp|bind'"
  warn "If you see 'bind: permission denied', the capability drop-in did not take effect — confirm with:  systemctl show ${SERVICE} -p AmbientCapabilities"
fi

echo
info "Two things this script can't do for you:"
echo "   • DNS — your mail client connects to mail.<domain>. Add a DNS A record:"
echo "       mail.<your-domain>  →  this server's public IP"
echo "   • TLS — mobile clients (incl. the Gmail app) require a TRUSTED certificate."
echo "     Point VayuMail at your Let's Encrypt cert in /etc/vayupress/env (or your EnvironmentFile):"
echo "       VAYUOS_MAIL_TLS_CERT=/etc/letsencrypt/live/mail.<domain>/fullchain.pem"
echo "       VAYUOS_MAIL_TLS_KEY=/etc/letsencrypt/live/mail.<domain>/privkey.pem"
echo "     then: systemctl restart ${SERVICE}"
echo
info "Client settings are also shown in VayuOS → VayuMail → Connect (with live status)."
