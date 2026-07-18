#!/usr/bin/env bash
#
# setup-api-subdomain.sh — provision the VayuAPI on a dedicated, CDN-proxy-OFF
# subdomain (api.<domain>), fully automatically and LOCKED DOWN to the API.
#
# WHY THIS EXISTS
# Scripts, CI jobs and AI agents call the REST API (/api/v1/…) MACHINE-TO-MACHINE
# — there is no browser to solve a CDN bot-challenge (Cloudflare Bot Fight Mode /
# a managed challenge / Under-Attack mode). Behind such a challenge those calls
# get a JavaScript "Just a moment…" page and fail. On Cloudflare's FREE plan the
# challenge runs at a layer a WAF "Skip" rule cannot override per-path, so the
# reliable fix is a subdomain pointed STRAIGHT at the origin with the CDN proxy
# OFF. The apex keeps full CDN protection for human/browser traffic; this API
# host is direct and unchallenged.
#
# WHY IT IS SAFE TO EXPOSE DIRECTLY
# Unlike mcp.<domain> (which must proxy the whole app so the OAuth sign-in +
# consent flow works on the subdomain), this vhost proxies ONLY the REST API and
# /health and returns 404 for EVERYTHING ELSE — no /os admin, no login, no
# static admin assets are reachable on the direct host. Every /api call is
# key-authenticated, per-key rate-limited and WORM-audited, and VayuShield still
# guards the origin, so an unauthenticated bot flood only earns cheap 401s.
#
# THE OPERATOR'S ONLY STEP is one DNS record:
#     api.<domain>   A/AAAA  ->  this server's IP    (CDN/proxy OFF — "DNS only")
# After that, this script (invoked automatically by deploy-vayupress.sh AND
# update-vayupress.sh) issues a DEDICATED Let's Encrypt certificate for
# api.<domain> (validated directly at the origin, so it never re-validates the
# CDN-proxied apex), writes the hardened nginx vhost, and reloads nginx. Point
# API clients at  https://api.<domain>/api/v1/… .
#
# It is IDEMPOTENT and NON-FATAL: if api.<domain> isn't pointed yet it skips
# cleanly and the API keeps working on the main domain. Run it by hand any time:
#     sudo bash scripts/setup-api-subdomain.sh
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
  warn "No usable DOMAIN — skipping VayuAPI subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping VayuAPI subdomain setup (run with sudo to enable)."; exit 0
fi

API="api.${DOMAIN}"
CERT_DIR="/etc/letsencrypt/live/${API}"   # DEDICATED cert for the subdomain
AVAIL=/etc/nginx/sites-available/vayupress-api
ENABLED=/etc/nginx/sites-enabled/vayupress-api

# ── Helpers ───────────────────────────────────────────────────────────────────
resolves() { getent hosts "$1" >/dev/null 2>&1; }
write_api_http_only() { # phase A: HTTP vhost so the ACME challenge validates
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${API};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}
write_api_full() { # phase B: HTTP redirect + HARDENED HTTPS vhost (API-only, CDN proxy OFF)
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${API};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name ${API};
    ssl_certificate     ${CERT_DIR}/fullchain.pem;
    ssl_certificate_key ${CERT_DIR}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
    add_header X-Content-Type-Options    "nosniff" always;

    client_max_body_size 50M;

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # HARDENED: this direct host exposes ONLY the key-authenticated REST API and a
    # health probe. The admin console (/os), login, and the OAuth/MCP endpoints are
    # deliberately NOT reachable here — they stay on the CDN-proxied apex (and, for
    # MCP, on mcp.<domain>). Everything outside /api and /health returns 404.
    location = /health { proxy_pass http://127.0.0.1:8080; proxy_set_header Host \$host; proxy_set_header X-Real-IP \$remote_addr; proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for; proxy_set_header X-Forwarded-Proto \$scheme; }
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }
    location / { return 404; }

    access_log /var/log/nginx/vayupress-api-access.log;
    error_log  /var/log/nginx/vayupress-api-error.log warn;
}
NGINX
}

# ── Preconditions ─────────────────────────────────────────────────────────────
if ! resolves "$API"; then
  warn "${API} has no DNS record yet — the REST API stays available on ${DOMAIN}."
  warn "To enable the unchallenged API host: add  ${API}  A/AAAA -> this server (CDN proxy OFF / 'DNS only'), then re-run."
  exit 0
fi

# ── 1. Issue a DEDICATED cert for api.<domain> (never touches the apex cert) ──
if [[ ! -f "${CERT_DIR}/fullchain.pem" ]]; then
  info "Issuing a Let's Encrypt certificate for ${API}…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  # Phase A: HTTP-only vhost so certbot's HTTP-01 challenge validates. Because
  # api.<domain> is pointed straight at the origin (CDN proxy OFF), the challenge
  # reaches nginx directly and is never intercepted by a CDN.
  write_api_http_only
  ln -sf "$AVAIL" "$ENABLED"
  if ! nginx -t >/dev/null 2>&1; then warn "nginx config test failed — aborting API setup."; rm -f "$ENABLED"; exit 0; fi
  systemctl reload nginx 2>/dev/null || true

  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$API" \
    -d "$API" --email "$EMAIL" --agree-tos --non-interactive || \
    warn "certbot could not issue a certificate for ${API} (is its DNS pointed here with the CDN proxy OFF?)."
fi

# ── 2. Provision the hardened vhost, or clean up if the cert is still missing ─
if [[ -f "${CERT_DIR}/fullchain.pem" ]]; then
  write_api_full
  ln -sf "$AVAIL" "$ENABLED"
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    ok "VayuAPI subdomain live: https://${API}/api/v1/  (CDN proxy OFF — no challenge; API-only, /os not exposed)."
    info "Point scripts / CI / agents at:  https://${API}/api/v1/…  with  Authorization: Bearer <key>"
  else
    warn "nginx config test failed after writing the API vhost — leaving it disabled."
    rm -f "$ENABLED"
  fi
else
  warn "No certificate for ${API} yet; the REST API stays on ${DOMAIN} for now."
  rm -f "$ENABLED"
fi
exit 0
