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

# load_vayupress_env exports the service's configuration into this process.
#
# THIS IS WHY NOTHING WAS PROVISIONED. The systemd worker carried no
# EnvironmentFile, and this script never loaded one, so every `vayupress …` call
# it made ran with a bare environment and died immediately:
#
#     {"level":"fatal","component":"config","msg":"required env not set","key":"API_KEY"}
#
# vp() discarded stderr and nobody checked the exit status, so that fatal became
# an empty host list and the run logged "No sync-approved secondary domains —
# nothing to do". A configuration error read as a clean no-op, daily, for a week.
#
# Parsed rather than sourced: this file is a systemd EnvironmentFile (KEY=VALUE),
# not a shell script, and sourcing it would execute whatever a stray backtick or
# $(...) happened to be in a value. Exported into the environment rather than
# passed on a command line, because API_KEY is a secret and `env KEY=… cmd` puts
# it in /proc/<pid>/cmdline for every user on the box to read.
load_vayupress_env() {
  local f="${ENV_FILE:-/etc/vayupress/env}" line key val
  [[ -r "$f" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"          # ltrim
    [[ -z "$line" || "${line:0:1}" == "#" ]] && continue
    line="${line#export }"
    key="${line%%=*}"; val="${line#*=}"
    [[ "$key" == "$line" ]] && continue              # no "=" on the line
    [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
    val="${val%\"}"; val="${val#\"}"                 # strip one layer of quotes
    val="${val%\'}"; val="${val#\'}"
    export "$key=$val"
  done < "$f"
}
load_vayupress_env

DOMAIN="${DOMAIN:-$(env_get DOMAIN)}"
CACHE_DIR="${CACHE_DIR:-$(env_get CACHE_DIR)}"; CACHE_DIR="${CACHE_DIR:-/var/cache/vayupress}"
EMAIL="${EMAIL:-}"; [[ -z "$EMAIL" ]] && EMAIL="postmaster@${DOMAIN}"
VP_BIN="${VP_BIN:-$(command -v vayupress || echo /usr/local/bin/vayupress)}"
SERVICE_USER="${SERVICE_USER:-www-data}"
DB_PATH="${DB_PATH:-$(env_get DB_PATH)}"; [[ -n "$DB_PATH" ]] && export DB_PATH

# vp — run the VayuPress CLI as the SERVICE USER, never as root.
#
# The CLI opens the SQLite database read-write, sets WAL mode and runs
# migrations. Invoked as root -- which is how this script always runs, because
# certbot needs it -- SQLite creates vayupress.db-wal and vayupress.db-shm owned
# by root:root inside a directory owned by www-data. From that moment the
# unprivileged service cannot write to its own database. Nothing fails here, so
# provisioning reports success; the site starts failing writes later, with
# nothing linking the two.
vp() {
  [[ -x "$VP_BIN" ]] || return 1
  if [[ $EUID -eq 0 ]] && id -u "$SERVICE_USER" >/dev/null 2>&1; then
    if command -v runuser >/dev/null 2>&1; then
      runuser -u "$SERVICE_USER" -- "$VP_BIN" "$@" 2>/dev/null
      return
    fi
    su -s /bin/sh "$SERVICE_USER" -c "$(printf '%q ' "$VP_BIN" "$@")" 2>/dev/null
    return
  fi
  "$VP_BIN" "$@" 2>/dev/null
}

# vp_checked runs the CLI like vp() but KEEPS stderr and the exit status.
#
# Written because the difference was invisible. `mapfile -t HOSTS < <(vp domains
# hosts)` discarded stderr and never checked the status, so a registry that could
# not be read produced the same empty array as a registry with nothing approved —
# and the run then logged "No sync-approved secondary domains — nothing to do",
# which is a reassuring sentence for a failure. An operator watching their
# certificate never appear had no way to tell the two apart, and neither did this
# script.
vp_checked() {
  [[ -x "$VP_BIN" ]] || return 127
  if [[ $EUID -eq 0 ]] && id -u "$SERVICE_USER" >/dev/null 2>&1; then
    if command -v runuser >/dev/null 2>&1; then
      runuser -u "$SERVICE_USER" -- "$VP_BIN" "$@"
      return
    fi
    su -s /bin/sh "$SERVICE_USER" -c "$(printf '%q ' "$VP_BIN" "$@")"
    return
  fi
  "$VP_BIN" "$@"
}

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
  vp domains set-tls "$1" "$2" >/dev/null 2>&1 || true
}
mail_enabled() { # $1=host — is it a mail_enabled secondary?
  vp domains hosts --mail | grep -qx "$1"
}

# ── Resolve the host list: explicit args, else the registry's APPROVED ────────
# secondaries. `domains hosts` lists only sync-approved domains (P5): a domain
# the operator has not approved is invisible here, so deploy/update runs can
# never provision it behind their back. Explicit args are operator intent —
# record the approval so future runs keep maintaining those domains too.
HOSTS=("$@")
if [[ ${#HOSTS[@]} -gt 0 ]]; then
  for H in "${HOSTS[@]}"; do
    vp domains sync "$H" >/dev/null 2>&1 || true
  done
fi
if [[ ${#HOSTS[@]} -eq 0 ]]; then
  if ! HOSTS_OUT="$(vp_checked domains hosts 2>&1)"; then
    warn "Could not read the domain registry, so NOTHING was provisioned. This is not the same"
    warn "as having no domains to provision, and it is why nothing appeared. The CLI said:"
    printf '%s\n' "$HOSTS_OUT" | sed 's/^/      /' >&2
    warn "Most often: DB_PATH missing from /etc/vayupress/env, so the CLI opens a different"
    warn "database from the one the service writes to. Check with:"
    warn "    grep -E '^(DB_PATH|DOMAIN)=' /etc/vayupress/env"
    warn "    sudo -u ${SERVICE_USER} ${VP_BIN} domains list"
    exit 1
  fi
  mapfile -t HOSTS < <(printf '%s\n' "$HOSTS_OUT" | grep -v '^[[:space:]]*$')
fi

# Surface (never act on) domains parked on manual hold, so an operator reading
# the deploy/update log knows exactly why a registered domain wasn't touched.
if [[ -x "$VP_BIN" ]]; then
  HELD="$(vp domains hosts --hold | tr '\n' ' ')"
  if [[ -n "${HELD// /}" ]]; then
    info "On manual hold (not provisioned): ${HELD}— approve in VayuOS → Domains or run: vayupress domains sync <host>"
  fi
fi

if [[ ${#HOSTS[@]} -eq 0 ]]; then
  # The registry READ SUCCEEDED and returned nothing. Say so, and say what to
  # check — the previous wording was true and told an operator nothing they
  # could act on, which is how this line got read past four times.
  info "The registry was read successfully and lists no sync-approved secondary domain, so"
  info "there is nothing to provision. If you expected one here, it is registered but not"
  info "APPROVED: open it in VayuOS → Sites → Lifecycle, or run: vayupress domains sync <host>"
  info "Registered but on hold:${HELD:- (none)}"
  exit 0
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

# reload_ok tests the config AND applies it, reporting either failure.
#
# THE BUG THIS REPLACES, in one line:
#
#   reload_ok() { nginx_ok && { systemctl reload nginx 2>/dev/null || true; return 0; }; ... }
#
# `|| true` discarded the reload's exit status and the function returned success
# regardless. So `nginx -t` passing was treated as "the new vhost is live", and
# when the reload did not actually happen — a failed unit, nginx not under
# systemd's control, a refused signal — the script carried on to certbot against
# a server still running configuration that had never contained the vhost.
#
# The visible result is a certificate that can never be issued, reported by the
# authority as "Type: connection", because the running nginx has no server block
# for that name and the default server closes the connection. Every layer above
# was correct: the file on disk, the symlink, the DNS, the webroot. The one step
# that makes any of it take effect was the only step whose failure was thrown
# away, and it was thrown away deliberately by a `|| true` nobody revisited.
reload_ok() {
  nginx_ok || return 1
  local out
  if ! out="$(systemctl reload nginx 2>&1)"; then
    warn "nginx accepted the configuration but RELOADING it failed:"
    warn "  ${out:-systemctl gave no output}"
    warn "  The new vhost is on disk and the running nginx has never read it, so nothing"
    warn "  serves this host yet. Not proceeding to certbot: a challenge cannot be answered"
    warn "  by configuration that is not loaded, and the attempt would spend a rate-limited"
    warn "  validation to discover that."
    return 1
  fi
  return 0
}

HOST_FAILURES=0
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
  if ! reload_ok; then warn "nginx test failed for ${HOST} — skipping."; rm -f "${ENABLED_DIR}/vayupress-dom-${HOST}"; set_tls "$HOST" failed; HOST_FAILURES=$((HOST_FAILURES + 1)); continue; fi

  # PRE-FLIGHT: prove this server answers its own challenge before spending a
  # validation attempt on it.
  #
  # Without this, a host whose vhost is not actually being matched — because the
  # symlink is missing, because the reload did not take, or because the default
  # server is answering first — goes straight to certbot, which returns
  # "Type: connection" naming an IP and nothing else. That error is
  # indistinguishable from a firewall problem, and it cost this project several
  # releases of guessing at DNS while the cause was one server block.
  #
  # Failed validations are also rate-limited per hostname, so burning attempts on
  # a host that provably cannot answer locally is the worst of both: no
  # certificate, and less budget to retry once it is fixed.
  # A pre-flight that cannot RUN must never be read as a pre-flight that FAILED.
  # Without this guard, a box without curl would skip certbot for every host and
  # issue no certificates at all — a check added to protect issuance becoming the
  # thing that stops it. No tool, no claim, no blocking.
  if ! command -v curl >/dev/null 2>&1; then
    info "curl is not installed; skipping the loopback pre-flight for ${HOST} (certbot still runs)."
  else
  PROBE="vayupress-preflight-$$-${RANDOM}"
  printf '%s' "$PROBE" > "${CACHE_DIR}/.well-known/acme-challenge/${PROBE}" 2>/dev/null || true
  PROBE_BODY="$(curl -fsS --max-time 5 -H "Host: ${HOST}" \
    "http://127.0.0.1/.well-known/acme-challenge/${PROBE}" 2>/dev/null || true)"
  rm -f "${CACHE_DIR}/.well-known/acme-challenge/${PROBE}"
  if [ "$PROBE_BODY" != "$PROBE" ]; then
    warn "pre-flight failed for ${HOST}: this server does not serve its own ACME challenge over loopback."
    warn "  nginx accepted the vhost but a request carrying Host: ${HOST} is not reaching it —"
    warn "  most often no enabled server block names this host, so the default server answers."
    warn "  certbot was NOT run for ${HOST}: a validation that cannot succeed only spends the retry budget."
    set_tls "$HOST" failed
    HOST_FAILURES=$((HOST_FAILURES + 1))
    continue
  fi
  fi

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
    { warn "certbot could not issue a cert for ${HOST}."; set_tls "$HOST" failed; HOST_FAILURES=$((HOST_FAILURES + 1)); continue; }

  if cert_covers "$HOST" "$HOST"; then
    write_full "$HOST"
    if reload_ok; then
      set_tls "$HOST" active
      ok "VayuDomain live: https://${HOST}"
    else
      warn "nginx test failed after writing ${HOST} vhost — leaving it disabled."
      rm -f "${ENABLED_DIR}/vayupress-dom-${HOST}"; set_tls "$HOST" failed
      HOST_FAILURES=$((HOST_FAILURES + 1))
    fi
  else
    warn "Certificate for ${HOST} did not materialise; leaving it on HTTP redirect."
    set_tls "$HOST" failed
    HOST_FAILURES=$((HOST_FAILURES + 1))
  fi
done

systemctl try-restart vayupress 2>/dev/null || true

# Exit non-zero when ANY host failed.
#
# This loop uses `continue` on failure by design -- one domain whose DNS is not
# pointed must never stop the others -- and it then exited 0 regardless. The
# driver classifies a helper by its exit status and its output, so a run that
# could not issue a certificate for a host was recorded as `setup-vayudomain.sh=ok`
# and the panel reported "0 reported a problem" beside a log that plainly said
# certbot had been refused. Continuing past a failure and REPORTING success are
# two different decisions, and only the first one was ever intended.
if (( HOST_FAILURES > 0 )); then
  warn "${HOST_FAILURES} host(s) could not be provisioned -- reporting this run as failed."
  exit 1
fi
exit 0
