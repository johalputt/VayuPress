#!/usr/bin/env bash
#
# setup-talk-subdomain.sh — provision the VayuTalk relay on a dedicated,
# CDN-proxy-OFF subdomain (talk.<domain>), fully automatically.
#
# WHY THIS EXISTS
# VayuTalk delivers messages over a long-lived Server-Sent-Events stream. A CDN
# bot-challenge (e.g. Cloudflare) in front of the site challenges that stream, and
# the mobile app — a non-browser client — cannot solve a JS challenge, so it can
# SEND (a short POST slips through) but never RECEIVE. The fix is a dedicated
# subdomain the operator points STRAIGHT at the origin with the CDN proxy OFF, so
# the stream reaches the origin unbuffered and unchallenged while the main domain
# keeps full CDN protection.
#
# THE OPERATOR'S ONLY STEP is one DNS record:
#     talk.<domain>   A/AAAA  ->  this server's IP    (CDN/proxy OFF — "DNS only")
# After that, this script (invoked automatically by deploy-vayupress.sh AND
# update-vayupress.sh) does everything else: it adds the TLS SAN, writes the nginx
# vhost, and sets VAYUOS_TALK_HOST so autoconfig advertises the host and the app
# routes its chat stream there on its own.
#
# It is IDEMPOTENT and NON-FATAL: if talk.<domain> isn't pointed yet it skips
# cleanly and the app keeps using the main domain — so calling it from deploy/
# update never breaks anything. Run it by hand any time, too:
#     sudo bash scripts/setup-talk-subdomain.sh
#
# Config is read from the environment, then /etc/vayupress/env, then sane
# defaults: DOMAIN, EMAIL (Let's Encrypt contact), CACHE_DIR (certbot webroot).

set -u

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }

ENV_FILE=/etc/vayupress/env
env_get() { # $1=KEY — read a value from /etc/vayupress/env if present
  [[ -r "$ENV_FILE" ]] && sed -n "s/^$1=//p" "$ENV_FILE" | head -n1
}

# ── Resolve config (env → /etc/vayupress/env → default) ───────────────────────
DOMAIN="${DOMAIN:-$(env_get DOMAIN)}"
CACHE_DIR="${CACHE_DIR:-$(env_get CACHE_DIR)}"; CACHE_DIR="${CACHE_DIR:-/var/cache/vayupress}"
EMAIL="${EMAIL:-}"; [[ -z "$EMAIL" ]] && EMAIL="postmaster@${DOMAIN}"

if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" ]]; then
  warn "No usable DOMAIN — skipping VayuTalk subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping VayuTalk subdomain setup (run with sudo to enable)."; exit 0
fi

TALK="talk.${DOMAIN}"
CERT_DIR="/etc/letsencrypt/live/${DOMAIN}"
AVAIL=/etc/nginx/sites-available/vayupress-talk
ENABLED=/etc/nginx/sites-enabled/vayupress-talk

# ── Helpers ───────────────────────────────────────────────────────────────────
resolves()   { getent hosts "$1" >/dev/null 2>&1; }
cert_covers() { # $1=hostname — is it a SAN on the main cert?
  [[ -f "${CERT_DIR}/fullchain.pem" ]] || return 1
  openssl x509 -in "${CERT_DIR}/fullchain.pem" -noout -text 2>/dev/null \
    | grep -oE 'DNS:[^,]+' | sed 's/DNS://' | grep -qx "$1"
}
cert_sans() { # print current SANs on the main cert, one per line
  [[ -f "${CERT_DIR}/fullchain.pem" ]] || return 0
  openssl x509 -in "${CERT_DIR}/fullchain.pem" -noout -text 2>/dev/null \
    | grep -oE 'DNS:[^,]+' | sed 's/DNS://'
}
set_env_var() { # $1=KEY $2=VALUE — upsert in /etc/vayupress/env
  touch "$ENV_FILE"
  if grep -q "^$1=" "$ENV_FILE"; then sed -i "s|^$1=.*|$1=$2|" "$ENV_FILE"
  else printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"; fi
}
write_talk_http_only() { # phase A: HTTP vhost so the ACME challenge validates
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${TALK};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}
write_talk_full() { # phase B: HTTP redirect + HTTPS relay-only vhost
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${TALK};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name ${TALK};
    ssl_certificate     ${CERT_DIR}/fullchain.pem;
    ssl_certificate_key ${CERT_DIR}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
    add_header X-Content-Type-Options    "nosniff" always;

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # The relay, direct to the origin, never buffered/gzipped (long-lived SSE).
    location /api/v1/talk/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off; proxy_cache off; gzip off;
        chunked_transfer_encoding on;
        proxy_read_timeout 3600s; proxy_send_timeout 3600s;
    }
    location = /health { proxy_pass http://127.0.0.1:8080; access_log off; }
    location = /.well-known/vayumail/autoconfig.json { proxy_pass http://127.0.0.1:8080; }
    location / { return 404; }

    access_log /var/log/nginx/vayupress-talk-access.log;
    error_log  /var/log/nginx/vayupress-talk-error.log warn;
}
NGINX
}

# ── Preconditions ─────────────────────────────────────────────────────────────
if ! resolves "$TALK"; then
  warn "${TALK} has no DNS record yet — VayuTalk will use ${DOMAIN}."
  warn "To enable the seamless relay: add  ${TALK}  A/AAAA -> this server (CDN proxy OFF), then re-run."
  exit 0
fi
if [[ ! -f "${CERT_DIR}/fullchain.pem" ]]; then
  warn "No base certificate at ${CERT_DIR} yet — run deploy-vayupress.sh first. Skipping talk setup."
  exit 0
fi

# ── 1. Ensure the certificate covers talk.<domain> ───────────────────────────
if ! cert_covers "$TALK"; then
  info "Adding ${TALK} to the TLS certificate…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  # Phase A: HTTP-only vhost so certbot's HTTP-01 challenge for talk validates.
  write_talk_http_only
  ln -sf "$AVAIL" "$ENABLED"
  if ! nginx -t >/dev/null 2>&1; then warn "nginx config test failed — aborting talk setup."; rm -f "$ENABLED"; exit 0; fi
  systemctl reload nginx 2>/dev/null || true

  # Re-issue the SAME lineage with its CURRENT SANs (so mail/site keep their
  # coverage) plus talk — but only those that still resolve, so a stale record
  # (e.g. a removed blog subdomain) can't fail the whole renewal.
  DARGS=()
  seen=" "
  while IFS= read -r h; do
    [[ -z "$h" ]] && continue
    resolves "$h" || { warn "  (dropping ${h} from cert — no DNS)"; continue; }
    case "$seen" in *" $h "*) ;; *) DARGS+=(-d "$h"); seen="${seen}${h} ";; esac
  done < <(printf '%s\n%s\n' "$(cert_sans)" "$TALK")
  # Guarantee the primary + talk are present even if the cert had no SANs listed.
  case "$seen" in *" $DOMAIN "*) ;; *) DARGS=(-d "$DOMAIN" "${DARGS[@]}");; esac
  case "$seen" in *" $TALK "*) ;; *) DARGS+=(-d "$TALK");; esac

  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$DOMAIN" --expand \
    "${DARGS[@]}" --email "$EMAIL" --agree-tos --non-interactive || \
  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$DOMAIN" --expand \
    -d "$DOMAIN" -d "mail.${DOMAIN}" -d "$TALK" --email "$EMAIL" --agree-tos --non-interactive || \
  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$DOMAIN" --expand \
    -d "$DOMAIN" -d "$TALK" --email "$EMAIL" --agree-tos --non-interactive || \
    warn "certbot could not add ${TALK} (is its DNS pointed here with the CDN proxy OFF?)."
fi

# ── 2. Provision the vhost + advertise, or clean up if the cert still lacks it ─
if cert_covers "$TALK"; then
  write_talk_full
  ln -sf "$AVAIL" "$ENABLED"
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    set_env_var VAYUOS_TALK_HOST "$TALK"
    # Apply the new advertisement without forcing a start on a fresh install
    # (try-restart is a no-op when the service isn't running yet).
    systemctl try-restart vayupress 2>/dev/null || true
    ok "VayuTalk subdomain live: https://${TALK}  (advertised to the app via autoconfig)."
  else
    warn "nginx config test failed after writing the talk vhost — leaving it disabled."
    rm -f "$ENABLED"
  fi
else
  warn "Certificate still does not cover ${TALK}; the app will use ${DOMAIN} for now."
  rm -f "$ENABLED"
fi
exit 0
