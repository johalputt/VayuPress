#!/usr/bin/env bash
#
# setup-openpgpkey-subdomain.sh — provision the VayuPGP Web Key Directory on its
# own CDN-proxy-OFF host (openpgpkey.<domain>), fully automatically.
#
# WHY THIS EXISTS
# VayuPress already serves WKD at /.well-known/openpgpkey/ (routes.go), but PGP
# clients do not look there. The Web Key Directory spec
# (draft-koch-openpgp-webkey-service) defines exactly two discovery locations,
# and the "advanced" one — which every current client prefers — is a DEDICATED
# hostname:
#
#     https://openpgpkey.<domain>/.well-known/openpgpkey/<domain>/hu/<hash>
#
# Without that hostname the keys are served and nobody finds them. With it,
# GnuPG, Thunderbird, Enigmail and the VayuMail mobile app discover a
# correspondent's public key automatically, with no key exchange step at all —
# which is the whole promise of "privacy by architecture": encryption that does
# not require the user to do anything.
#
# WHY THE CDN PROXY MUST BE OFF
# Same reason as api.<domain> and mcp.<domain>. A WKD fetch is machine-to-machine
# — GnuPG has no JavaScript engine and cannot solve a CDN bot-challenge. Behind
# one, key discovery silently fails and users quietly fall back to unencrypted
# mail. Point this record straight at the origin.
#
# WHY IT IS SAFE TO EXPOSE DIRECTLY
# This vhost is the tightest in the project: it proxies ONLY
# /.well-known/openpgpkey/ and returns 404 for absolutely everything else. No
# /os, no login, no API, no static assets. And what it does serve is *public
# keys* — material whose entire purpose is to be handed to strangers. There is
# nothing here to authenticate and nothing to leak.
#
# AUTOMATION
# If CF_ZONE_ID and CF_API_TOKEN are set (they already exist in the VayuPress
# config for cache purging), this script CREATES THE DNS RECORD ITSELF, with the
# proxy off, and waits for it to resolve. Nothing is left for the operator to do.
# Without those credentials it degrades to the same behaviour as the other
# subdomain helpers: it prints the one record to add and exits cleanly.
#
# It is IDEMPOTENT and NON-FATAL. It runs automatically from
# deploy-vayupress.sh and update-vayupress.sh, and by hand any time:
#     sudo bash scripts/setup-openpgpkey-subdomain.sh
#
# Config: environment, then /etc/vayupress/env, then defaults — DOMAIN, EMAIL,
# CACHE_DIR, CF_ZONE_ID, CF_API_TOKEN.

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
CF_ZONE_ID="${CF_ZONE_ID:-$(env_get CF_ZONE_ID)}"
CF_API_TOKEN="${CF_API_TOKEN:-$(env_get CF_API_TOKEN)}"

if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" ]]; then
  warn "No usable DOMAIN — skipping WKD subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping WKD subdomain setup (run with sudo to enable)."; exit 0
fi

WKD="openpgpkey.${DOMAIN}"
CERT_DIR="/etc/letsencrypt/live/${WKD}"   # DEDICATED cert for the subdomain
AVAIL=/etc/nginx/sites-available/vayupress-openpgpkey
ENABLED=/etc/nginx/sites-enabled/vayupress-openpgpkey

resolves() { getent hosts "$1" >/dev/null 2>&1; }

# ── DNS automation (Cloudflare) ───────────────────────────────────────────────
# Creates openpgpkey.<domain> as an UNPROXIED A record. Unproxied is not a
# preference here — a proxied record puts a bot-challenge in front of a fetch
# performed by GnuPG, which cannot answer one.
public_ip() {
  local ip
  for u in https://api.ipify.org https://icanhazip.com https://ifconfig.me/ip; do
    ip="$(curl -4 -sS --max-time 8 "$u" 2>/dev/null | tr -d '[:space:]')"
    [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && { echo "$ip"; return 0; }
  done
  # Fall back to whatever the apex already resolves to — if the site is serving,
  # this is by definition the right address.
  ip="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk 'NR==1{print $1}')"
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && { echo "$ip"; return 0; }
  return 1
}

cf_create_record() {
  local ip="$1" api="https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records"
  local existing
  existing="$(curl -sS --max-time 15 -X GET "${api}?type=A&name=${WKD}" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" -H "Content-Type: application/json" 2>/dev/null)"
  if echo "$existing" | grep -q '"count":[1-9]'; then
    info "Cloudflare already has an A record for ${WKD} — leaving it alone."
    return 0
  fi
  local resp
  resp="$(curl -sS --max-time 15 -X POST "$api" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" -H "Content-Type: application/json" \
      --data "{\"type\":\"A\",\"name\":\"${WKD}\",\"content\":\"${ip}\",\"ttl\":300,\"proxied\":false}" 2>/dev/null)"
  if echo "$resp" | grep -q '"success":true'; then
    ok "Created DNS record ${WKD} → ${ip} (proxy OFF)."
    return 0
  fi
  warn "Cloudflare API did not create ${WKD}. Check that CF_API_TOKEN has Zone:DNS:Edit on this zone."
  return 1
}

if ! resolves "$WKD"; then
  if [[ -n "$CF_ZONE_ID" && -n "$CF_API_TOKEN" ]]; then
    info "${WKD} does not resolve yet — creating it via the Cloudflare API…"
    if IP="$(public_ip)"; then
      if cf_create_record "$IP"; then
        # Give the record a moment to become visible to this host's resolver.
        for _ in $(seq 1 20); do resolves "$WKD" && break; sleep 3; done
      fi
    else
      warn "Could not determine this server's public IPv4 address — skipping DNS creation."
    fi
  fi
fi

if ! resolves "$WKD"; then
  warn "${WKD} has no DNS record yet — PGP clients cannot auto-discover keys for ${DOMAIN}."
  warn "Add:  ${WKD}  A/AAAA -> this server's IP   (CDN proxy OFF / 'DNS only'), then re-run."
  warn "Or set CF_ZONE_ID and CF_API_TOKEN in ${ENV_FILE} and this script will create it for you."
  exit 0
fi

# ── nginx vhosts ──────────────────────────────────────────────────────────────
write_wkd_http_only() { # phase A: HTTP vhost so the ACME challenge validates
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${WKD};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}

write_wkd_full() { # phase B: HTTP redirect + HTTPS vhost exposing ONLY the WKD
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${WKD};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name ${WKD};
    ssl_certificate     ${CERT_DIR}/fullchain.pem;
    ssl_certificate_key ${CERT_DIR}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
    add_header X-Content-Type-Options    "nosniff" always;

    # A key fetch is tiny; anything larger is not a WKD request.
    client_max_body_size 16k;

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # The tightest vhost in the project: ONLY the Web Key Directory is reachable
    # here. No /os, no login, no API, no static assets. What it serves is public
    # key material whose purpose is to be handed to strangers, so there is
    # nothing to authenticate and nothing to leak.
    location ^~ /.well-known/openpgpkey/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }
    location / { return 404; }

    access_log /var/log/nginx/vayupress-openpgpkey-access.log;
    error_log  /var/log/nginx/vayupress-openpgpkey-error.log warn;
}
NGINX
}

# ── 1. Issue a DEDICATED cert (never touches the apex cert) ───────────────────
if [[ ! -f "${CERT_DIR}/fullchain.pem" ]]; then
  info "Issuing a Let's Encrypt certificate for ${WKD}…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  write_wkd_http_only
  ln -sf "$AVAIL" "$ENABLED"
  if ! nginx -t >/dev/null 2>&1; then warn "nginx config test failed — aborting WKD setup."; rm -f "$ENABLED"; exit 0; fi
  systemctl reload nginx 2>/dev/null || true

  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$WKD" \
    -d "$WKD" --email "$EMAIL" --agree-tos --non-interactive || \
    warn "certbot could not issue a certificate for ${WKD} (is its DNS pointed here with the CDN proxy OFF?)."
fi

# ── 2. Provision the vhost, or clean up if the cert is still missing ──────────
if [[ -f "${CERT_DIR}/fullchain.pem" ]]; then
  write_wkd_full
  ln -sf "$AVAIL" "$ENABLED"
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true

    # Self-verify. The policy file's presence is what signals WKD support for a
    # domain, so if it does not answer, discovery is broken however green the
    # rest of this run looked.
    POLICY="https://${WKD}/.well-known/openpgpkey/${DOMAIN}/policy"
    CODE="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "$POLICY" 2>/dev/null || echo 000)"
    if [[ "$CODE" == "200" ]]; then
      ok "VayuPGP Web Key Directory live at https://${WKD}/ — PGP clients now discover keys for ${DOMAIN} automatically."
      info "Verify any address with:  gpg --locate-keys someone@${DOMAIN}"
    else
      warn "Vhost is up but the WKD policy file returned HTTP ${CODE}."
      warn "Check that VayuPGP is enabled (VayuOS → VayuPGP) — WKD 404s when the engine is off."
    fi
  else
    warn "nginx config test failed after writing the WKD vhost — leaving it disabled."
    rm -f "$ENABLED"
  fi
else
  warn "No certificate for ${WKD} yet; WKD stays reachable only on ${DOMAIN}."
  rm -f "$ENABLED"
fi
exit 0
