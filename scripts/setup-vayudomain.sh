#!/usr/bin/env bash
#
# setup-vayudomain.sh — provision TLS + nginx for VayuDomains SECONDARY domains
# that the operator has APPROVED for sync (VayuDomains P4 + P5, ADR-0132).
#
# WHY THIS EXISTS
# VayuPress runs UNPRIVILEGED on :8080 behind nginx + certbot, so the server
# process can never run certbot or reload nginx itself. This root helper is the
# out-of-process actor the ADR calls for: it reads the SYNC-APPROVED secondary
# domains from the binary (`vayupress domains hosts` — domains on manual hold
# are never listed, so adding a domain in VayuOS provisions NOTHING until the
# operator presses "Sync now" there or runs `vayupress domains sync <host>`).
# For each approved domain it obtains its OWN Let's Encrypt certificate
# (--cert-name <host>, a SEPARATE lineage per domain — never expanding the
# primary cert, so the 100-SAN cap can never be hit) and writes a reverse-proxy
# vhost to the origin. It then records the result back into the registry via
# `vayupress domains set-tls`.
#
# THE OPERATOR'S STEPS per domain: point DNS, then approve the sync.
#     <domain>       A/AAAA -> this server's IP
#     www.<domain>   A/AAAA -> this server's IP        (optional)
#     mail.<domain>  A/AAAA -> this server's IP        (only if mail is enabled)
# Approve in VayuOS → Domains ("Sync now"), or:  vayupress domains sync <host>
# Then run (or let deploy/update run):  sudo bash scripts/setup-vayudomain.sh
#
# It is IDEMPOTENT and NON-FATAL: a domain whose DNS isn't pointed yet is skipped
# cleanly, so calling it from deploy/update never breaks anything. Pass explicit
# hosts to provision just those (an explicit host is treated as operator intent
# and approves that domain's sync):  sudo bash scripts/setup-vayudomain.sh shop.example
#
# Config: env → /etc/vayupress/env → defaults (DOMAIN, EMAIL, CACHE_DIR, VP_BIN).

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
env_get() { [[ -r "$ENV_FILE" ]] && sed -n "s/^$1=//p" "$ENV_FILE" | head -n1; }

DOMAIN="${DOMAIN:-$(env_get DOMAIN)}"
CACHE_DIR="${CACHE_DIR:-$(env_get CACHE_DIR)}"; CACHE_DIR="${CACHE_DIR:-/var/cache/vayupress}"
EMAIL="${EMAIL:-}"; [[ -z "$EMAIL" ]] && EMAIL="postmaster@${DOMAIN}"
VP_BIN="${VP_BIN:-$(command -v vayupress || echo /usr/local/bin/vayupress)}"

if [[ $EUID -ne 0 ]]; then
  warn "Not root — skipping VayuDomains TLS/nginx setup (run with sudo to enable)."; exit 0
fi

# Fail here, not later. If nginx is already broken, every write below is pointless
# and the resulting "config test failed" would blame this script for someone
# else's stale vhost — which is exactly how a one-line fix becomes a long hunt.
if ! nginx_baseline_ok; then
  exit 0
fi

resolves() { getent hosts "$1" >/dev/null 2>&1; }
cert_covers() { # $1=cert-name host $2=san — is $2 a SAN on that lineage's cert?
  local f="/etc/letsencrypt/live/$1/fullchain.pem"
  [[ -f "$f" ]] || return 1
  openssl x509 -in "$f" -noout -text 2>/dev/null | grep -oE 'DNS:[^,]+' | sed 's/DNS://' | grep -qx "$2"
}
set_tls() { # $1=host $2=state — best-effort record back into the registry
  if [[ -x "$VP_BIN" ]]; then "$VP_BIN" domains set-tls "$1" "$2" >/dev/null 2>&1 || true; fi
}
mail_enabled() { # $1=host — is it a mail_enabled secondary?
  [[ -x "$VP_BIN" ]] || return 1
  "$VP_BIN" domains hosts --mail 2>/dev/null | grep -qx "$1"
}

# ── Resolve the host list: explicit args, else the registry's APPROVED ────────
# secondaries. `domains hosts` lists only sync-approved domains (P5): a domain
# the operator has not approved is invisible here, so deploy/update runs can
# never provision it behind their back. Explicit args are operator intent —
# record the approval so future runs keep maintaining those domains too.
HOSTS=("$@")
if [[ ${#HOSTS[@]} -gt 0 && -x "$VP_BIN" ]]; then
  for H in "${HOSTS[@]}"; do
    "$VP_BIN" domains sync "$H" >/dev/null 2>&1 || true
  done
fi
if [[ ${#HOSTS[@]} -eq 0 ]]; then
  if [[ -x "$VP_BIN" ]]; then
    mapfile -t HOSTS < <("$VP_BIN" domains hosts 2>/dev/null)
  fi
fi

# Surface (never act on) domains parked on manual hold, so an operator reading
# the deploy/update log knows exactly why a registered domain wasn't touched.
if [[ -x "$VP_BIN" ]]; then
  HELD="$("$VP_BIN" domains hosts --hold 2>/dev/null | tr '\n' ' ')"
  if [[ -n "${HELD// /}" ]]; then
    info "On manual hold (not provisioned): ${HELD}— approve in VayuOS → Domains or run: vayupress domains sync <host>"
  fi
fi

if [[ ${#HOSTS[@]} -eq 0 ]]; then
  info "No sync-approved secondary domains — nothing to do."; exit 0
fi

AVAIL_DIR=/etc/nginx/sites-available
ENABLED_DIR=/etc/nginx/sites-enabled

write_http_only() { # $1=host — phase A: HTTP vhost so the ACME challenge validates
  cat > "${AVAIL_DIR}/vayupress-dom-$1" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name $1 www.$1;
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
NGINX
}

write_full() { # $1=host — phase B: HTTP redirect + HTTPS reverse-proxy to the origin
  local cert="/etc/letsencrypt/live/$1"
  cat > "${AVAIL_DIR}/vayupress-dom-$1" <<NGINX
server {
    listen 80; listen [::]:80;
    server_name $1 www.$1;
    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name $1 www.$1;
    ssl_certificate     ${cert}/fullchain.pem;
    ssl_certificate_key ${cert}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;
    add_header X-Content-Type-Options    "nosniff" always;
    client_max_body_size 64m;

    location ^~ /.well-known/acme-challenge/ { root ${CACHE_DIR}; default_type text/plain; try_files \$uri =404; }

    # Reverse-proxy the whole site to the origin. Host resolution in the binary
    # scopes every response to this domain (VayuDomains Stage 2/3).
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
    }

    access_log /var/log/nginx/vayupress-dom-$1-access.log;
    error_log  /var/log/nginx/vayupress-dom-$1-error.log warn;
}
NGINX
}

reload_ok() { nginx_ok && { systemctl reload nginx 2>/dev/null || true; return 0; }; return 1; }

for HOST in "${HOSTS[@]}"; do
  HOST="${HOST//[[:space:]]/}"
  [[ -z "$HOST" || "$HOST" == "$DOMAIN" ]] && continue
  if ! resolves "$HOST"; then
    warn "${HOST} has no DNS record pointing here yet — skipping (add A/AAAA and re-run)."
    set_tls "$HOST" pending
    continue
  fi

  info "Provisioning ${HOST}…"
  mkdir -p "${CACHE_DIR}/.well-known/acme-challenge"
  chown -R www-data:www-data "$CACHE_DIR" 2>/dev/null || true

  # Phase A: HTTP-only vhost so certbot's HTTP-01 challenge validates.
  write_http_only "$HOST"
  ln -sf "${AVAIL_DIR}/vayupress-dom-${HOST}" "${ENABLED_DIR}/vayupress-dom-${HOST}"
  if ! reload_ok; then warn "nginx test failed for ${HOST} — skipping."; rm -f "${ENABLED_DIR}/vayupress-dom-${HOST}"; set_tls "$HOST" failed; continue; fi

  # Its OWN certificate lineage (--cert-name <host>): never expands the primary
  # cert, so the 100-SAN cap is structurally impossible to hit. Include www and,
  # when this domain carries mail, mail.<host> so its clients get a valid cert.
  DARGS=(-d "$HOST")
  resolves "www.${HOST}" && DARGS+=(-d "www.${HOST}")
  if mail_enabled "$HOST" && resolves "mail.${HOST}"; then DARGS+=(-d "mail.${HOST}"); fi
  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$HOST" \
    "${DARGS[@]}" --email "$EMAIL" --agree-tos --non-interactive || \
  certbot certonly --webroot -w "$CACHE_DIR" --cert-name "$HOST" \
    -d "$HOST" --email "$EMAIL" --agree-tos --non-interactive || \
    { warn "certbot could not issue a cert for ${HOST}."; set_tls "$HOST" failed; continue; }

  if cert_covers "$HOST" "$HOST"; then
    write_full "$HOST"
    if reload_ok; then
      set_tls "$HOST" active
      ok "VayuDomain live: https://${HOST}"
    else
      warn "nginx test failed after writing ${HOST} vhost — leaving it disabled."
      rm -f "${ENABLED_DIR}/vayupress-dom-${HOST}"; set_tls "$HOST" failed
    fi
  else
    warn "Certificate for ${HOST} did not materialise; leaving it on HTTP redirect."
    set_tls "$HOST" failed
  fi
done

systemctl try-restart vayupress 2>/dev/null || true
exit 0
