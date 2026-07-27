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
# A WKD fetch is machine-to-machine: GnuPG has no JavaScript engine and cannot
# solve a CDN bot-challenge. Behind one, key discovery fails SILENTLY and
# correspondents quietly fall back to unencrypted mail — the worst failure mode
# available, because it looks exactly like nothing happened.
#
# Note this is a different case from mail.<domain>, which is unproxied already
# and unavoidably so (a CDN HTTP proxy does not carry SMTP or IMAP). Serving WKD
# there would work and still be undiscoverable: clients look at the two spec
# hostnames and nowhere else. The apex is the other spec location, and on most
# installs it IS proxied — which is exactly why this dedicated host exists.
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
# EVERY MAIL DOMAIN, NOT JUST THE PRIMARY
# WKD discovery is per-domain: a key for someone@shop.example is only findable at
# openpgpkey.shop.example. So this provisions the primary DOMAIN plus every
# mail-enabled VayuDomains secondary (`vayupress domains hosts --mail`). Covering
# only the primary would leave every secondary domain's users silently
# undiscoverable, which is the same outcome as never running this at all.
#
# It is IDEMPOTENT and NON-FATAL. It runs automatically from
# deploy-vayupress.sh and update-vayupress.sh, and by hand any time:
#     sudo bash scripts/setup-openpgpkey-subdomain.sh              # all mail domains
#     sudo bash scripts/setup-openpgpkey-subdomain.sh shop.example # just this one
#
# Config: environment, then /etc/vayupress/env, then defaults — DOMAIN, EMAIL,
# CACHE_DIR, CF_ZONE_ID, CF_API_TOKEN, VP_BIN.

set -u

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }

ENV_FILE=/etc/vayupress/env
env_get() { # $1=KEY — read a value from /etc/vayupress/env if present
  # Deliberately tolerant of how the file is actually written. The original
  # matched only a line beginning exactly "KEY=", which silently returned empty
  # for `export KEY=`, for a leading space, and returned the quotes themselves
  # for KEY="value". Empty DOMAIN makes every subdomain helper skip with "No
  # usable DOMAIN", and because skipping is deliberately non-fatal the whole run
  # then reported success while provisioning nothing at all.
  [[ -r "$ENV_FILE" ]] || return 0
  sed -n -E "s/^[[:space:]]*(export[[:space:]]+)?$1=[[:space:]]*//p" "$ENV_FILE" |
    head -n1 |
    sed -E "s/^\"(.*)\"$/\1/; s/^'(.*)'$/\1/; s/[[:space:]]+$//"
}

# ── Resolve config (env → /etc/vayupress/env → default) ───────────────────────
DOMAIN="${DOMAIN:-$(env_get DOMAIN)}"
CACHE_DIR="${CACHE_DIR:-$(env_get CACHE_DIR)}"; CACHE_DIR="${CACHE_DIR:-/var/cache/vayupress}"
EMAIL="${EMAIL:-}"; [[ -z "$EMAIL" ]] && EMAIL="postmaster@${DOMAIN}"
CF_ZONE_ID="${CF_ZONE_ID:-$(env_get CF_ZONE_ID)}"
CF_API_TOKEN="${CF_API_TOKEN:-$(env_get CF_API_TOKEN)}"
VP_BIN="${VP_BIN:-$(command -v vayupress || echo /usr/local/bin/vayupress)}"

if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" ]]; then
  warn "No usable DOMAIN — skipping WKD subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping WKD subdomain setup (run with sudo to enable)."; exit 0
fi

resolves() { getent hosts "$1" >/dev/null 2>&1; }

# ── DNS automation (Cloudflare) ───────────────────────────────────────────────
# Creates openpgpkey.<domain> as an UNPROXIED A record. Unproxied is not a
# preference — a proxied record puts a bot-challenge in front of a fetch made by
# GnuPG, which cannot answer one.
public_ip() {
  local ip u
  for u in https://api.ipify.org https://icanhazip.com https://ifconfig.me/ip; do
    ip="$(curl -4 -sS --max-time 8 "$u" 2>/dev/null | tr -d '[:space:]')"
    [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && { echo "$ip"; return 0; }
  done
  # Fall back to whatever the primary domain already resolves to — if the site is
  # serving, that is by definition the right address.
  ip="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk 'NR==1{print $1}')"
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && { echo "$ip"; return 0; }
  return 1
}

cf_create_record() { # $1=ip  $2=fqdn
  local ip="$1" host="$2"
  local api="https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records"
  local existing resp
  existing="$(curl -sS --max-time 15 -X GET "${api}?type=A&name=${host}" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" -H "Content-Type: application/json" 2>/dev/null)"
  if echo "$existing" | grep -q '"count":[1-9]'; then
    info "Cloudflare already has an A record for ${host} — leaving it alone."
    return 0
  fi
  resp="$(curl -sS --max-time 15 -X POST "$api" \
      -H "Authorization: Bearer ${CF_API_TOKEN}" -H "Content-Type: application/json" \
      --data "{\"type\":\"A\",\"name\":\"${host}\",\"content\":\"${ip}\",\"ttl\":300,\"proxied\":false}" 2>/dev/null)"
  if echo "$resp" | grep -q '"success":true'; then
    ok "Created DNS record ${host} → ${ip} (proxy OFF)."
    return 0
  fi
  warn "Cloudflare API did not create ${host}. Check that CF_API_TOKEN carries Zone:DNS:Edit for this zone."
  return 1
}

# ── nginx vhosts ──────────────────────────────────────────────────────────────
# Kept at top level so the here-documents are not indented: an indented
# terminator does not close a plain here-doc, and the nginx body must be emitted
# verbatim.
write_wkd_http_only() { # $1=avail-path  $2=fqdn — phase A: HTTP so ACME validates
  cat > "$1" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name $2;
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}

write_wkd_full() { # $1=avail-path  $2=fqdn  $3=cert-dir — phase B: WKD-only HTTPS
  cat > "$1" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name $2;
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name $2;
    ssl_certificate     $3/fullchain.pem;
    ssl_certificate_key $3/privkey.pem;
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

# ── Provision one domain ──────────────────────────────────────────────────────
provision_wkd() {
  local dom="$1"
  local wkd="openpgpkey.${dom}"
  local cert_dir="/etc/letsencrypt/live/${wkd}"
  local avail="/etc/nginx/sites-available/vayupress-openpgpkey-${dom}"
  local enabled="/etc/nginx/sites-enabled/vayupress-openpgpkey-${dom}"
  local ip code policy

  if ! resolves "$wkd" && [[ -n "$CF_ZONE_ID" && -n "$CF_API_TOKEN" ]]; then
    info "${wkd} does not resolve yet — creating it via the Cloudflare API…"
    if ip="$(public_ip)"; then
      if cf_create_record "$ip" "$wkd"; then
        # Give the record a moment to become visible to this host's resolver.
        for _ in $(seq 1 20); do resolves "$wkd" && break; sleep 3; done
      fi
    else
      warn "Could not determine this server's public IPv4 address — skipping DNS creation for ${dom}."
    fi
  fi

  if ! resolves "$wkd"; then
    warn "${wkd} has no DNS record — PGP clients cannot auto-discover keys for ${dom}."
    warn "Add:  ${wkd}  A/AAAA -> this server's IP   (CDN proxy OFF / 'DNS only'), then re-run."
    warn "Or set CF_ZONE_ID and CF_API_TOKEN in ${ENV_FILE} and this script will create it for you."
    return 0
  fi

  # 1. Issue a DEDICATED cert (a separate lineage per host, so the primary
  #    certificate is never expanded and its SAN cap can never be hit).
  if [[ ! -f "${cert_dir}/fullchain.pem" ]]; then
    info "Issuing a Let's Encrypt certificate for ${wkd}…"
    mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
    chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

    write_wkd_http_only "$avail" "$wkd"
    ln -sf "$avail" "$enabled"
    if ! nginx -t >/dev/null 2>&1; then
      warn "nginx config test failed — aborting WKD setup for ${dom}."
      rm -f "$enabled"
      return 0
    fi
    systemctl reload nginx 2>/dev/null || true

    certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$wkd" \
      -d "$wkd" --email "$EMAIL" --agree-tos --non-interactive || \
      warn "certbot could not issue a certificate for ${wkd} (is its DNS pointed here with the CDN proxy OFF?)."
  fi

  # 2. Provision the vhost, or clean up if the cert is still missing.
  if [[ ! -f "${cert_dir}/fullchain.pem" ]]; then
    warn "No certificate for ${wkd} yet; keys for ${dom} stay undiscoverable."
    rm -f "$enabled"
    return 0
  fi

  write_wkd_full "$avail" "$wkd" "$cert_dir"
  ln -sf "$avail" "$enabled"
  if ! nginx -t >/dev/null 2>&1; then
    warn "nginx config test failed after writing the WKD vhost for ${dom} — leaving it disabled."
    rm -f "$enabled"
    return 0
  fi
  systemctl reload nginx 2>/dev/null || true

  # 3. Self-verify. The policy file's presence is what signals WKD support for a
  #    domain, so if it does not answer, discovery is broken however green the
  #    rest of this run looked.
  policy="https://${wkd}/.well-known/openpgpkey/${dom}/policy"
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "$policy" 2>/dev/null || echo 000)"
  if [[ "$code" == "200" ]]; then
    ok "WKD live for ${dom} at https://${wkd}/ — PGP clients now discover its keys automatically."
    info "Verify with:  gpg --locate-keys someone@${dom}"
  else
    warn "${wkd} is up but its WKD policy file returned HTTP ${code}."
    warn "Check that VayuPGP is enabled (VayuOS → VayuPGP) — WKD 404s when the engine is off."
  fi
  return 0
}

# ── Which domains carry mail ──────────────────────────────────────────────────
# The primary always; plus every mail-enabled VayuDomains secondary, because
# covering only the primary leaves those users silently undiscoverable — the same
# outcome as never running this at all. Explicit arguments override both.
DOMAINS=()
if [[ $# -gt 0 ]]; then
  DOMAINS=("$@")
else
  DOMAINS=("$DOMAIN")
  if [[ -x "$VP_BIN" ]]; then
    while IFS= read -r h; do
      [[ -n "$h" && "$h" != "$DOMAIN" ]] && DOMAINS+=("$h")
    done < <("$VP_BIN" domains hosts --mail 2>/dev/null)
  fi
fi

info "Provisioning the Web Key Directory for: ${DOMAINS[*]}"
for d in "${DOMAINS[@]}"; do
  provision_wkd "$d"
done
exit 0
