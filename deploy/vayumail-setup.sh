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
# It also provisions a trusted Let's Encrypt TLS certificate for mail.<domain>
# (mobile clients such as the Gmail app refuse the self-signed fallback) and
# wires it into the service — fully automatic and best-effort.
#
# TIP: VayuPress can now provision and auto-renew the mail certificate itself,
# with no certbot, via native ACME. If port 80 on mail.<domain> is reachable,
# you can skip the certbot path here and instead set in /etc/vayupress/env:
#     VAYUOS_MAIL_TLS_ACME=on
#     VAYUOS_MAIL_ACME_EMAIL=admin@<domain>
# then restart the service. This script's capability + firewall fixes (below)
# are still useful either way.
#
# Usage:  sudo bash deploy/vayumail-setup.sh
#
# Override the service/domain/TLS behaviour if needed:
#   SERVICE=vayupress MAIL_DOMAIN=mail.example.com sudo -E bash deploy/vayumail-setup.sh
#   MAIL_CERT_EMAIL=admin@example.com sudo -E bash deploy/vayumail-setup.sh
#   MAIL_TLS=off sudo -E bash deploy/vayumail-setup.sh    # skip cert provisioning

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

# ── 3) Provision a trusted TLS certificate (Let's Encrypt) ──────────────────
# Mobile clients (the Gmail app, Apple Mail) refuse VayuMail's self-signed
# fallback certificate, so a CA-signed cert for mail.<domain> is required for
# them to connect. This step is automatic and best-effort: it resolves your mail
# hostname, obtains a certificate with certbot, wires VAYUOS_MAIL_TLS_CERT/KEY
# into the service environment, and installs a renewal hook that restarts the
# service so a renewed cert is picked up. Set MAIL_TLS=off to skip entirely.
MAIL_TLS="${MAIL_TLS:-auto}"

# Locate the service's EnvironmentFile (where we read DOMAIN and write the TLS
# vars). Prefer what the unit actually loads, then common locations.
ENV_FILE="$(systemctl show "$SERVICE" -p EnvironmentFiles --value 2>/dev/null | awk '{print $1; exit}')"
if [[ -z "${ENV_FILE:-}" || ! -f "${ENV_FILE:-}" ]]; then
  for c in /etc/vayupress/env /etc/default/vayupress /etc/vayupress/vayupress.env; do
    [[ -f "$c" ]] && ENV_FILE="$c" && break
  done
fi

# Resolve the mail hostname: explicit MAIL_DOMAIN wins, else mail.<DOMAIN> where
# DOMAIN is taken from the environment or the service EnvironmentFile.
DOMAIN="${DOMAIN:-}"
if [[ -z "$DOMAIN" && -n "${ENV_FILE:-}" && -f "${ENV_FILE:-}" ]]; then
  DOMAIN="$(grep -E '^DOMAIN=' "$ENV_FILE" | tail -1 | cut -d= -f2-)"
  DOMAIN="${DOMAIN//\"/}"; DOMAIN="${DOMAIN//\'/}"; DOMAIN="${DOMAIN// /}"; DOMAIN="${DOMAIN%$'\r'}"
fi
# Fallback domain auto-detection so the script "just works" even when DOMAIN is
# not in the EnvironmentFile: (a) an existing non-mail Let's Encrypt certificate
# (your website's cert), then (b) the first real server_name in the nginx config.
if [[ -z "$DOMAIN" ]]; then
  if [[ -d /etc/letsencrypt/live ]]; then
    for d in /etc/letsencrypt/live/*/; do
      name="$(basename "$d")"
      [[ "$name" == "README" ]] && continue
      [[ "$name" == mail.* ]] && continue        # skip the mail host itself
      [[ "$name" == *.*    ]] || continue        # must look like a domain
      DOMAIN="$name"; break
    done
  fi
fi
if [[ -z "$DOMAIN" ]] && command -v nginx >/dev/null 2>&1; then
  DOMAIN="$(nginx -T 2>/dev/null | grep -hoE '^[[:space:]]*server_name[[:space:]]+[^;]+' \
    | tr ' ' '\n' | grep -E '^[a-z0-9.-]+\.[a-z]{2,}$' | grep -vE '^(mail|www)\.' | head -1 || true)"
fi
[[ -n "$DOMAIN" ]] && info "Using domain: ${DOMAIN}"

MAIL_HOST="${MAIL_DOMAIN:-}"
[[ -z "$MAIL_HOST" && -n "$DOMAIN" ]] && MAIL_HOST="mail.${DOMAIN}"

if [[ "$MAIL_TLS" == "off" ]]; then
  info "MAIL_TLS=off — skipping TLS certificate provisioning."
elif [[ -z "$MAIL_HOST" ]]; then
  warn "Could not determine your mail hostname (no DOMAIN found) — skipping TLS automation."
  warn "Re-run with:  MAIL_DOMAIN=mail.example.com sudo -E bash deploy/vayumail-setup.sh"
else
  CERT_LIVE="/etc/letsencrypt/live/${MAIL_HOST}"
  if [[ -f "${CERT_LIVE}/fullchain.pem" ]]; then
    ok "Existing Let's Encrypt certificate found for ${MAIL_HOST}."
  else
    # ── DNS preflight ───────────────────────────────────────────────────────
    # The #1 reason certbot fails (and burns a rate-limit) is mail.<domain> not
    # yet pointing at this server. Check the A record against our public IP and,
    # on a mismatch, print the exact record to add and SKIP the certbot attempt.
    SERVER_IP="$( { curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
      || curl -fsS --max-time 5 https://ifconfig.me 2>/dev/null \
      || hostname -I 2>/dev/null | awk '{print $1}'; } || true )"
    MAIL_IP="$(getent ahostsv4 "$MAIL_HOST" 2>/dev/null | awk '{print $1; exit}' || true)"
    if [[ -z "$MAIL_IP" ]] && command -v dig >/dev/null 2>&1; then
      MAIL_IP="$(dig +short A "$MAIL_HOST" 2>/dev/null | tail -1 || true)"
    fi
    if [[ -n "$SERVER_IP" && -n "$MAIL_IP" && "$MAIL_IP" != "$SERVER_IP" ]]; then
      warn "DNS for ${MAIL_HOST} points at ${MAIL_IP}, but this server is ${SERVER_IP}."
      warn "Add/fix this DNS A record, wait a few minutes, then re-run:"
      echo "       ${MAIL_HOST}.  →  ${SERVER_IP}"
      warn "Skipping the certificate request until DNS matches (avoids a Let's Encrypt rate-limit)."
      MAIL_TLS="dns-pending"
    elif [[ -z "$MAIL_IP" ]]; then
      warn "${MAIL_HOST} does not resolve yet. Add a DNS A record, wait, then re-run:"
      echo "       ${MAIL_HOST}.  →  ${SERVER_IP:-<this server public IP>}"
      warn "Skipping the certificate request until DNS resolves."
      MAIL_TLS="dns-pending"
    else
      ok "DNS check: ${MAIL_HOST} resolves to this server (${SERVER_IP:-?})."
    fi
  fi
  if [[ "$MAIL_TLS" == "dns-pending" ]]; then
    : # DNS not ready — fall through to the wiring/summary without calling certbot.
  elif [[ -f "${CERT_LIVE}/fullchain.pem" ]]; then
    : # already issued above
  else
    if ! command -v certbot >/dev/null 2>&1; then
      info "Installing certbot..."
      if command -v apt-get >/dev/null 2>&1; then
        apt-get update -y >/dev/null 2>&1 || true; apt-get install -y certbot >/dev/null 2>&1 || true
      elif command -v dnf >/dev/null 2>&1; then
        dnf install -y certbot >/dev/null 2>&1 || true
      elif command -v yum >/dev/null 2>&1; then
        yum install -y certbot >/dev/null 2>&1 || true
      fi
    fi
    if ! command -v certbot >/dev/null 2>&1; then
      warn "certbot is not installed and could not be installed automatically — skipping TLS."
      warn "Install certbot and re-run, or set the cert paths manually (see the note below)."
    else
      CERT_EMAIL="${MAIL_CERT_EMAIL:-admin@${DOMAIN:-$MAIL_HOST}}"
      WEBROOT="/var/cache/vayupress"
      info "Obtaining a Let's Encrypt certificate for ${MAIL_HOST} (contact: ${CERT_EMAIL})..."
      info "This needs inbound port 80 reachable on ${MAIL_HOST} for the HTTP-01 challenge."

      # ── Preferred path: webroot (ZERO downtime) ─────────────────────────────
      # If nginx is running we install a dedicated server block for the mail
      # host that serves the ACME challenge from the shared webroot, then issue
      # via certbot --webroot. This needs no nginx stop/start, and renewals are
      # fully automatic. Falls back to --standalone if nginx is absent or the
      # webroot challenge fails.
      ISSUED=0
      if command -v nginx >/dev/null 2>&1 && systemctl is-active --quiet nginx 2>/dev/null; then
        mkdir -p "${WEBROOT}/.well-known/acme-challenge"
        chmod -R 0755 "${WEBROOT}" 2>/dev/null || true
        VHOST_AVAIL="/etc/nginx/sites-available/${MAIL_HOST}"
        if [[ ! -d /etc/nginx/sites-available ]]; then
          # Some distros (RHEL/Alma) use conf.d instead of sites-available.
          VHOST_AVAIL="/etc/nginx/conf.d/${MAIL_HOST}.conf"
        fi
        info "Installing nginx ACME vhost for ${MAIL_HOST} at ${VHOST_AVAIL}..."
        cat > "$VHOST_AVAIL" <<NGINX
# VayuMail ACME challenge vhost for ${MAIL_HOST} (auto-generated by vayumail-setup.sh).
server {
    listen 80;
    listen [::]:80;
    server_name ${MAIL_HOST};
    location ^~ /.well-known/acme-challenge/ {
        root ${WEBROOT};
        default_type text/plain;
        try_files \$uri =404;
    }
    location / { return 301 https://${DOMAIN:-$MAIL_HOST}\$request_uri; }
}
NGINX
        if [[ -d /etc/nginx/sites-enabled && ! -e "/etc/nginx/sites-enabled/${MAIL_HOST}" ]]; then
          ln -s "$VHOST_AVAIL" "/etc/nginx/sites-enabled/${MAIL_HOST}" 2>/dev/null || true
        fi
        if nginx -t >/dev/null 2>&1; then
          systemctl reload nginx 2>/dev/null || true
          info "nginx reloaded; issuing certificate via webroot (no downtime)..."
          if certbot certonly --webroot -w "$WEBROOT" --non-interactive --agree-tos \
               -m "$CERT_EMAIL" -d "$MAIL_HOST"; then
            ISSUED=1
            ok "Certificate issued via webroot for ${MAIL_HOST}."
          else
            warn "Webroot challenge failed — see /var/log/letsencrypt/letsencrypt.log. Trying standalone next."
          fi
        else
          warn "nginx config test failed after adding the mail vhost — removing it and trying standalone."
          rm -f "$VHOST_AVAIL" "/etc/nginx/sites-enabled/${MAIL_HOST}" 2>/dev/null || true
        fi
      fi

      # ── Fallback path: standalone (brief one-time nginx stop) ───────────────
      if [[ "$ISSUED" -ne 1 ]]; then
        if command -v nginx >/dev/null 2>&1 && systemctl is-active --quiet nginx 2>/dev/null; then
          certbot certonly --standalone --non-interactive --agree-tos -m "$CERT_EMAIL" \
            -d "$MAIL_HOST" --http-01-port 80 \
            --pre-hook "systemctl stop nginx" --post-hook "systemctl start nginx" \
            || warn "certbot failed — see /var/log/letsencrypt/letsencrypt.log (is ${MAIL_HOST} DNS pointing here and port 80 open?)"
        else
          certbot certonly --standalone --non-interactive --agree-tos -m "$CERT_EMAIL" -d "$MAIL_HOST" \
            || warn "certbot failed — see /var/log/letsencrypt/letsencrypt.log (is ${MAIL_HOST} DNS pointing here and port 80 open?)"
        fi
      fi
    fi
  fi

  # Wire the cert into the service environment and install a renewal hook.
  if [[ -f "${CERT_LIVE}/fullchain.pem" ]]; then
    if [[ -z "${ENV_FILE:-}" || ! -f "${ENV_FILE:-}" ]]; then
      ENV_FILE="/etc/vayupress/env"
      mkdir -p /etc/vayupress
      touch "$ENV_FILE"
      warn "No service EnvironmentFile was found; wrote TLS vars to ${ENV_FILE}."
      warn "Make sure your unit loads it (add  EnvironmentFile=${ENV_FILE}  to the [Service] section)."
    fi
    if ! grep -q '^VAYUOS_MAIL_TLS_CERT=' "$ENV_FILE"; then
      printf 'VAYUOS_MAIL_TLS_CERT=%s/fullchain.pem\n' "$CERT_LIVE" >> "$ENV_FILE"
      printf 'VAYUOS_MAIL_TLS_KEY=%s/privkey.pem\n'   "$CERT_LIVE" >> "$ENV_FILE"
      ok "Wired VAYUOS_MAIL_TLS_CERT/KEY into ${ENV_FILE}."
    else
      ok "VAYUOS_MAIL_TLS_CERT is already set in ${ENV_FILE} — left as-is."
    fi
    HOOK_DIR="/etc/letsencrypt/renewal-hooks/deploy"
    mkdir -p "$HOOK_DIR"
    cat > "${HOOK_DIR}/restart-${SERVICE}.sh" <<EOF
#!/bin/bash
# Reload ${SERVICE} after a Let's Encrypt renewal. VayuMail now HOT-RELOADS the
# certificate from disk on the next handshake, so this hook is belt-and-braces:
# even without it, a renewed cert is picked up automatically within ~30s and
# with zero connection drops. The restart is harmless and guarantees an
# immediate swap.
systemctl restart ${SERVICE}
EOF
    chmod +x "${HOOK_DIR}/restart-${SERVICE}.sh"
    ok "Installed auto-renewal restart hook for ${SERVICE} (cert is also hot-reloaded live)."
  fi
fi

# ── 4) Restart and verify ───────────────────────────────────────────────────
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
info "One thing this script can't do for you:"
echo "   • DNS — your mail client connects to mail.<domain>. Add a DNS A record:"
echo "       mail.<your-domain>  →  this server's public IP"
echo
if [[ "${MAIL_TLS:-auto}" != "off" && -n "${MAIL_HOST:-}" && -f "/etc/letsencrypt/live/${MAIL_HOST}/fullchain.pem" ]]; then
  ok "TLS: a trusted certificate for ${MAIL_HOST} is installed and wired in — mobile mail apps should connect."
else
  warn "TLS: no trusted certificate is active yet, so VayuMail is using a self-signed fallback that mobile apps (e.g. the Gmail app) will reject."
  echo "     Once mail.<domain> DNS points here and port 80 is reachable, re-run this script, or set manually:"
  echo "       VAYUOS_MAIL_TLS_CERT=/etc/letsencrypt/live/mail.<domain>/fullchain.pem"
  echo "       VAYUOS_MAIL_TLS_KEY=/etc/letsencrypt/live/mail.<domain>/privkey.pem"
  echo "     then: systemctl restart ${SERVICE}"
fi
echo
info "Client settings are also shown in VayuOS → VayuMail → Connect (with live status)."
