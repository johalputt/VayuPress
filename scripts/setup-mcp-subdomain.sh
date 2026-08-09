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
# update-vayupress.sh) does everything else: it issues a DEDICATED Let's Encrypt
# certificate for mcp.<domain> (validated directly at the origin, so it never
# re-validates the CDN-proxied apex — that could fail), writes the nginx vhost that
# proxies the full app so the whole OAuth sign-in + consent flow works on the
# subdomain, and reloads nginx. Point Claude at  https://mcp.<domain>/mcp .
#
# It is IDEMPOTENT and NON-FATAL: if mcp.<domain> isn't pointed yet it skips
# cleanly and the connector keeps using the main domain — so calling it from
# deploy/update never breaks anything. Run it by hand any time, too:
#     sudo bash scripts/setup-mcp-subdomain.sh
#
# Config is read from the environment, then /etc/vayupress/env, then sane
# defaults: DOMAIN, EMAIL (Let's Encrypt contact), CACHE_DIR (certbot webroot).

set -u

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $*${NC}"; }
info() { echo -e "${CYAN}ℹ  $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }

# nginx_ok runs the config test and, on failure, PRINTS what nginx said.
#
# This previously ran with output discarded, so a rejected vhost produced
# "nginx config test failed" and nothing else — the operator was told the step
# aborted and never told why, with the real message discarded a line before it
# would have solved the problem. A diagnostic that is generated and then thrown
# away is worse than one that was never produced: it costs the same to compute
# and it teaches the reader the tool does not know.
nginx_ok() {
  local out
  if out="$(nginx -t 2>&1)"; then
    return 0
  fi
  warn "nginx rejected the configuration. It said:"
  printf '%s\n' "$out" | sed 's/^/      /' >&2
  return 1
}

# nginx_baseline_ok checks whether nginx's config was ALREADY invalid before this
# script touched anything.
#
# This distinction is the difference between a five-minute fix and an afternoon.
# A single stale vhost anywhere under sites-enabled — most commonly one written
# while DOMAIN was still "localhost", pointing at a certificate Let's Encrypt will
# never issue — makes `nginx -t` fail forever. Every helper then aborts with
# "nginx config test failed", each blaming its own change, none of them at fault,
# and provisioning silently never happens again. The running nginx is unaffected
# because it loaded before the bad file appeared, so the site keeps serving and
# nothing looks wrong.
#
# Checking first means the message can name the real problem instead of pointing
# at the innocent change that happened to run next.
nginx_baseline_ok() {
  local out
  if out="$(nginx -t 2>&1)"; then
    return 0
  fi
  warn "nginx configuration is ALREADY invalid, before this script changed anything."
  warn "Nothing here can be provisioned until that is fixed. nginx said:"
  printf '%s\n' "$out" | sed 's/^/      /' >&2
  if printf '%s' "$out" | grep -q "letsencrypt/live/localhost"; then
    warn "That path is the giveaway: a vhost was written while DOMAIN was still localhost,"
    warn "referencing a certificate that cannot exist. Find and disable it with:"
    warn "    sudo grep -rln letsencrypt/live/localhost /etc/nginx/sites-enabled/"
    warn "    sudo rm -f /etc/nginx/sites-enabled/<name>   # the file stays in sites-available"
  fi
  return 1
}


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

if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" ]]; then
  warn "No usable DOMAIN — skipping VayuMCP subdomain setup."; exit 0
fi
if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping VayuMCP subdomain setup (run with sudo to enable)."; exit 0
fi

# Fail here, not later. If nginx is already broken, every write below is pointless
# and the resulting "config test failed" would blame this script for someone
# else's stale vhost — which is exactly how a one-line fix becomes a long hunt.
if ! nginx_baseline_ok; then
  exit 0
fi

MCP="mcp.${DOMAIN}"
CERT_DIR="/etc/letsencrypt/live/${MCP}"   # DEDICATED cert for the subdomain
AVAIL=/etc/nginx/sites-available/vayupress-mcp
ENABLED=/etc/nginx/sites-enabled/vayupress-mcp

# ── Helpers ───────────────────────────────────────────────────────────────────
resolves() { getent hosts "$1" >/dev/null 2>&1; }
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
write_mcp_full() { # phase B: HTTP redirect + HTTPS full-app vhost (CDN proxy OFF)
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

    # Declared once at server level; every location below inherits them, because
    # proxy_set_header only stops inheriting when a location declares its own.
    proxy_set_header Host              \$host;
    proxy_set_header X-Real-IP         \$remote_addr;
    proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_http_version 1.1;
    proxy_set_header Connection "";

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # Only what the connector flow needs. This host is provisioned with the CDN
    # proxy deliberately OFF (an edge bot challenge breaks machine-to-machine MCP
    # calls), so anything reachable here is reachable straight from the internet
    # with no edge in front of it.
    #
    # It used to proxy "/" — the whole app, admin console included — which made
    # this the widest-open vhost on the install AND one whose paths sit in
    # shieldBypassPrefixes, because an MCP client cannot solve a proof-of-work.
    # Widest-open plus shield-bypassed is not a combination to leave on a
    # wildcard. The sibling api-host script already closes with a 404 return;
    # this now matches it.
    location ^~ /.well-known/oauth-authorization-server { proxy_pass http://127.0.0.1:8080; }
    location ^~ /.well-known/oauth-protected-resource   { proxy_pass http://127.0.0.1:8080; }
    location ^~ /oauth/    { proxy_pass http://127.0.0.1:8080; }
    location ^~ /mcp       { proxy_pass http://127.0.0.1:8080; }
    # The consent page is served under /os and loads its own CSS/JS from there.
    location ^~ /os/       { proxy_pass http://127.0.0.1:8080; }
    location = /os         { proxy_pass http://127.0.0.1:8080; }
    location ^~ /static/   { proxy_pass http://127.0.0.1:8080; }
    location = /health     { proxy_pass http://127.0.0.1:8080; access_log off; }

    # Everything else — the public site, the API, the editor, media — is served on
    # the apex, where the edge and Tier 3 shaping actually apply.
    location / { return 404; }

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

# ── 1. Issue a DEDICATED cert for mcp.<domain> (never touches the apex cert) ──
if [[ ! -f "${CERT_DIR}/fullchain.pem" ]]; then
  info "Issuing a Let's Encrypt certificate for ${MCP}…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  # Phase A: HTTP-only vhost so certbot's HTTP-01 challenge validates. Because
  # mcp.<domain> is pointed straight at the origin (CDN proxy OFF), the challenge
  # reaches nginx directly and is never intercepted by a CDN.
  write_mcp_http_only
  ln -sf "$AVAIL" "$ENABLED"
  if ! nginx_ok; then warn "nginx config test failed — aborting MCP setup."; rm -f "$ENABLED"; exit 0; fi
  # The reload is the step that makes the vhost live, so its failure is
  # REPORTED. `|| true` here discarded it: nginx -t passing was treated as
  # "the new config is serving", and a reload that never happened left a
  # correct file on disk that the running server had never read — which the
  # certificate authority then reports as an unexplained connection error.
  if ! _rl="$(systemctl reload nginx 2>&1)"; then
    warn "nginx accepted the config but RELOADING it failed: ${_rl:-no output}."
    warn "  The vhost is on disk and the running nginx has not read it."
  fi

  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$MCP" \
    -d "$MCP" --email "$EMAIL" --agree-tos --non-interactive || \
    warn "certbot could not issue a certificate for ${MCP} (is its DNS pointed here with the CDN proxy OFF?)."
fi

# ── 2. Provision the full vhost, or clean up if the cert is still missing ─────
if [[ -f "${CERT_DIR}/fullchain.pem" ]]; then
  write_mcp_full
  ln -sf "$AVAIL" "$ENABLED"
  if nginx_ok; then
    # The reload is the step that makes the vhost live, so its failure is
    # REPORTED. `|| true` here discarded it: nginx -t passing was treated as
    # "the new config is serving", and a reload that never happened left a
    # correct file on disk that the running server had never read — which the
    # certificate authority then reports as an unexplained connection error.
    if ! _rl="$(systemctl reload nginx 2>&1)"; then
      warn "nginx accepted the config but RELOADING it failed: ${_rl:-no output}."
      warn "  The vhost is on disk and the running nginx has not read it."
    fi
    ok "VayuMCP subdomain live: https://${MCP}/mcp  (CDN proxy OFF — no challenge)."
    info "In Claude → Settings → Connectors → Add custom connector, use:  https://${MCP}/mcp"
  else
    warn "nginx config test failed after writing the MCP vhost — leaving it disabled."
    rm -f "$ENABLED"
  fi
else
  warn "No certificate for ${MCP} yet; the connector will use ${DOMAIN} for now."
  rm -f "$ENABLED"
fi
exit 0
