#!/usr/bin/env bash
#
# setup-mcp-subdomain.sh — provision the VayuMCP connector on a dedicated,
# CDN-proxy-OFF subdomain (mcp.<domain>), fully automatically.
#
# WHY THIS EXISTS
# Claude (and any MCP client) connects to the /mcp + /oauth endpoints
# MACHINE-TO-MACHINE — there is no browser in the loop for those requests. A CDN
# bot-challenge (e.g. Cloudflare Bot Fight Mode / a managed challenge) in front of
# the site answers those requests with a JavaScript "Just a moment…" page, which a
# server cannot solve, so dynamic client registration fails with
#   "Couldn't register with <domain>'s sign-in service."
# On Cloudflare's FREE plan that challenge runs at a layer a WAF "Skip" custom rule
# CANNOT override, so the only reliable fix is a subdomain pointed STRAIGHT at the
# origin with the CDN proxy OFF. The main domain keeps full CDN protection; the MCP
# subdomain is direct and unchallenged (VayuShield still guards the origin, and
# /mcp + /oauth are already in its bypass so they are never challenged there).
#
# THE OPERATOR'S ONLY STEP is one DNS record:
#     mcp.<domain>   A/AAAA  ->  this server's IP    (CDN/proxy OFF — "DNS only")
# After that, this script (invoked automatically by deploy-vayupress.sh AND
# update-vayupress.sh) does everything else: it adds the TLS SAN, writes the nginx
# vhost that proxies the full app so the whole OAuth sign-in + consent flow works
# on the subdomain, and reloads nginx. Point Claude at  https://mcp.<domain>/mcp .
#
# It is IDEMPOTENT and NON-FATAL: if mcp.<domain> isn't pointed yet it skips
# cleanly and the connector keeps working on the main domain (when that domain is
# reachable) — so calling it from deploy/update never breaks anything. Run it by
# hand any time, too:
#     sudo bash scripts/setup-mcp-subdomain.sh
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
  warn "No usable DOMAIN — skipping VayuMCP subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping VayuMCP subdomain setup (run with sudo to enable)."; exit 0
fi

MCP="mcp.${DOMAIN}"
CERT_DIR="/etc/letsencrypt/live/${DOMAIN}"
AVAIL=/etc/nginx/sites-available/vayupress-mcp
ENABLED=/etc/nginx/sites-enabled/vayupress-mcp

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
write_mcp_http_only() { # phase A: HTTP vhost so the ACME challenge validates
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${MCP};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}
write_mcp_full() { # phase B: HTTP redirect + HTTPS full-app vhost (proxy off at the CDN)
  cat > "$AVAIL" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name ${MCP};
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name ${MCP};
    ssl_certificate     ${CERT_DIR}/fullchain.pem;
    ssl_certificate_key ${CERT_DIR}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
    add_header X-Content-Type-Options    "nosniff" always;

    client_max_body_size 50M;
    proxy_pass_header X-CSRF-Token;

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # The whole app, straight to the origin. The full OAuth sign-in + consent flow
    # (/.well-known/oauth-*, /oauth/*, /mcp, and the /os login + static assets the
    # consent page loads) must all be reachable on this host, so proxy everything.
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }

    access_log /var/log/nginx/vayupress-mcp-access.log;
    error_log  /var/log/nginx/vayupress-mcp-error.log warn;
}
NGINX
}

# ── Preconditions ─────────────────────────────────────────────────────────────
if ! resolves "$MCP"; then
  warn "${MCP} has no DNS record yet — the MCP connector will use ${DOMAIN}."
  warn "To enable the unchallenged connector: add  ${MCP}  A/AAAA -> this server (CDN proxy OFF / 'DNS only'), then re-run."
  exit 0
fi
if [[ ! -f "${CERT_DIR}/fullchain.pem" ]]; then
  warn "No base certificate at ${CERT_DIR} yet — run deploy-vayupress.sh first. Skipping MCP setup."
  exit 0
fi

# ── 1. Ensure the certificate covers mcp.<domain> ────────────────────────────
if ! cert_covers "$MCP"; then
  info "Adding ${MCP} to the TLS certificate…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  # Phase A: HTTP-only vhost so certbot's HTTP-01 challenge for mcp validates.
  write_mcp_http_only
  ln -sf "$AVAIL" "$ENABLED"
  if ! nginx -t >/dev/null 2>&1; then warn "nginx config test failed — aborting MCP setup."; rm -f "$ENABLED"; exit 0; fi
  systemctl reload nginx 2>/dev/null || true

  # Re-issue the SAME lineage with its CURRENT SANs (so the site/mail/talk keep
  # their coverage) plus mcp — but only those that still resolve, so a stale
  # record can't fail the whole renewal.
  DARGS=()
  seen=" "
  while IFS= read -r h; do
    [[ -z "$h" ]] && continue
    resolves "$h" || { warn "  (dropping ${h} from cert — no DNS)"; continue; }
    case "$seen" in *" $h "*) ;; *) DARGS+=(-d "$h"); seen="${seen}${h} ";; esac
  done < <(printf '%s\n%s\n' "$(cert_sans)" "$MCP")
  # Guarantee the primary + mcp are present even if the cert had no SANs listed.
  case "$seen" in *" $DOMAIN "*) ;; *) DARGS=(-d "$DOMAIN" "${DARGS[@]}");; esac
  case "$seen" in *" $MCP "*) ;; *) DARGS+=(-d "$MCP");; esac

  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$DOMAIN" --expand \
    "${DARGS[@]}" --email "$EMAIL" --agree-tos --non-interactive || \
  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$DOMAIN" --expand \
    -d "$DOMAIN" -d "$MCP" --email "$EMAIL" --agree-tos --non-interactive || \
    warn "certbot could not add ${MCP} (is its DNS pointed here with the CDN proxy OFF?)."
fi

# ── 2. Provision the vhost, or clean up if the cert still lacks it ────────────
if cert_covers "$MCP"; then
  write_mcp_full
  ln -sf "$AVAIL" "$ENABLED"
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    ok "VayuMCP subdomain live: https://${MCP}/mcp  (CDN proxy OFF — no challenge)."
    info "In Claude → Settings → Connectors → Add custom connector, use:  https://${MCP}/mcp"
  else
    warn "nginx config test failed after writing the MCP vhost — leaving it disabled."
    rm -f "$ENABLED"
  fi
else
  warn "Certificate still does not cover ${MCP}; the connector will use ${DOMAIN} for now."
  rm -f "$ENABLED"
fi
exit 0
