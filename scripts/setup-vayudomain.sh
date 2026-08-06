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

# probe_challenge <host> — does the RUNNING server answer this host's ACME
# challenge over loopback? Writes a token, asks for it back with that Host
# header, and compares.
#
# This is the only check in the script that tests the server rather than a file,
# a symlink or an exit status. Everything else can be green while this fails,
# and when it does, this is the one that is right.
probe_challenge() {
  local host="$1" probe body
  command -v curl >/dev/null 2>&1 || return 0
  probe="vayupress-preflight-$$-${RANDOM}"
  printf '%s' "$probe" > "${CACHE_DIR}/.well-known/acme-challenge/${probe}" 2>/dev/null || true
  body="$(curl -fsS --max-time 5 -H "Host: ${host}" \
    "http://127.0.0.1/.well-known/acme-challenge/${probe}" 2>/dev/null || true)"
  rm -f "${CACHE_DIR}/.well-known/acme-challenge/${probe}"
  [ "$body" = "$probe" ]
}

# probe_https <host> — does the running server complete a TLS handshake for this
# host and serve it? Used after the certificate exists, where the challenge path
# is no longer the right question because it now redirects to HTTPS.
#
# --resolve pins the connection to this machine, so it tests THIS server rather
# than whatever DNS currently points at. -k accepts the certificate: the question
# here is whether nginx loaded the new server block at all, and a name or chain
# complaint would be a different finding on a block that did load.
#
# THE QUESTION THIS ASKS, and the first version asked a different one. It used
# `curl -f`, which fails on any 4xx or 5xx — so a site with no posts yet (404) or
# one whose first request VayuShield challenges (403) was scored as NOT SERVED.
# Both are working HTTPS sites. The helper then reported "certificate issued but
# this server does not serve it", marked the host failed, and restarted nginx
# trying to fix a server block that had been correct the whole time.
#
# The question is whether a server block ANSWERS for this host over TLS, not
# whether the page is a 200. So: any HTTP status counts, and only the absence of
# one does not. `%{http_code}` is 000 when no HTTP response arrived at all —
# connection refused, TLS failure, or the catch-all default server, which closes
# without a response (444). That is precisely the state worth detecting, and it
# is the state a missing vhost produces.
#
# What this deliberately does NOT prove: that the certificate served is the right
# one. -k accepts any. A default server holding some other valid cert and
# answering with a status would pass here. On this install the default closes
# without answering, so the distinction holds; it is stated because a check is
# only worth what it actually establishes.
probe_https() {
  local host="$1" code
  command -v curl >/dev/null 2>&1 || return 0
  code="$(curl -sSk --max-time 8 --resolve "${host}:443:127.0.0.1" \
    -o /dev/null -w '%{http_code}' "https://${host}/" 2>/dev/null || true)"
  [ -n "$code" ] && [ "$code" != "000" ]
}

# probe_https_code reports what actually came back, for the message on failure.
# "no HTTP response at all" and "answered 403" are different problems and were
# being reported as the same one.
probe_https_code() {
  local host="$1"
  command -v curl >/dev/null 2>&1 || { printf 'not checked'; return 0; }
  printf '%s' "$(curl -sSk --max-time 8 --resolve "${host}:443:127.0.0.1" \
    -o /dev/null -w '%{http_code}' "https://${host}/" 2>/dev/null || echo 000)"
}

# probe_settles <verifier> <host> — run a verifier with a few seconds' grace, for
# use straight after nginx has been restarted and may not have finished binding.
probe_settles() {
  local fn="$1" host="$2" _i
  for _i in 1 2 3 4 5; do
    "$fn" "$host" && return 0
    sleep 1
  done
  return 1
}

# NGINX_RESTARTED gates the HEAVY rung to once per run. A restart interrupts
# every site on the machine; doing it once per host would multiply that by the
# number of domains, for a condition that is a property of the server and not of
# any one host. The light rung — signalling the master — costs nothing and is
# never gated.
NGINX_RESTARTED=0

# force_apply <verifier> <host> — the vhost is on disk, nginx accepts it, the reload
# reported SUCCESS, and the server still does not answer for this host.
#
# THE CASE THIS EXISTS FOR, measured on a live install rather than reasoned
# about: `nginx -t` passed, `systemctl reload nginx` exited 0, systemd's
# MainPID, /run/nginx.pid and the running master all named the same process —
# and nginx's workers were still five days old, so the running server had never
# read the file. A reload that reports success and does not happen is invisible
# to every check that trusts the exit status, which is every check there was.
#
# The answer is not to test the exit status harder. It is to stop believing it
# and ask the server. probe_challenge is that question, and this function is
# what to do when the answer is no: try mechanisms that do not share a failure
# mode with the one that just lied, cheapest first, re-asking the server after
# each.
force_apply() {
  local verify="$1" host="$2" out

  # AUDIT FINDING against this function, and it is the one worth stating.
  #
  # Both probes return SUCCESS when curl is absent — deliberately, because "no
  # tool" must never be read as "no", or a box without curl would be refused
  # every certificate by a check meant to protect issuance. Correct there, and
  # quietly wrong here: the ladder verifies by probing, so with no probe every
  # rung "succeeds" and this function reports a server repaired that it never
  # asked. It would restart nothing, fix nothing and say it worked.
  #
  # Escalation needs a verifier that can actually answer. Without one, refuse.
  if ! command -v curl >/dev/null 2>&1; then
    warn "  curl is not installed, so this server cannot be asked whether it answers."
    warn "  Not escalating: every rung would report success on a check that never ran."
    return 1
  fi

  # Never escalate onto a configuration nginx rejects. `nginx -t` passing is what
  # makes the restart below safe: nginx comes back on a config it has already
  # validated. Without this the heavy rung could take the whole machine down to
  # fix one host.
  nginx_ok || { warn "  not escalating: nginx rejects the configuration on disk."; return 1; }

  # RUNG 1 — signal the master directly, via its pid file, with systemd out of
  # the path entirely. reload_ok only reaches for this when systemctl FAILS, so
  # on this install it had never been tried: systemctl kept returning 0. Same
  # outcome, genuinely different mechanism, and no interruption.
  if out="$(nginx -s reload 2>&1)"; then
    info "  signalled the running master directly (nginx -s reload)."
  else
    warn "  nginx -s reload failed: ${out:-no output}"
  fi
  if probe_settles "$verify" "$host"; then
    ok "  the direct signal took effect — ${host} is being served now."
    return 0
  fi

  # RUNG 2 — replace the master instead of asking it to re-read. A reload is a
  # request to a process that may itself be the broken thing: started before the
  # current unit file, holding a config path that has since moved, or ignoring
  # the signal. A restart does not ask.
  if [ "$NGINX_RESTARTED" = "1" ]; then
    warn "  nginx has already been restarted once in this run; not restarting again."
    return 1
  fi
  NGINX_RESTARTED=1
  warn "  the reload still has not taken effect; restarting nginx."
  warn "  (brief interruption to every site on this machine, on a configuration"
  warn "   nginx has already validated above.)"
  if ! out="$(systemctl restart nginx 2>&1)"; then
    warn "  systemctl restart nginx failed: ${out:-no output}"
  fi

  # A restart that leaves nginx DOWN is worse than the problem it was sent to
  # fix — it takes every other site with it. Never leave this function without
  # confirming the server came back, and try once to bring it up if not.
  if ! systemctl is-active --quiet nginx 2>/dev/null; then
    warn "  nginx is NOT running after the restart — starting it."
    systemctl start nginx >/dev/null 2>&1 || nginx >/dev/null 2>&1 || true
    sleep 1
  fi
  if ! systemctl is-active --quiet nginx 2>/dev/null && ! pgrep -o -x nginx >/dev/null 2>&1; then
    warn "  nginx is still not running. This is now more serious than one missing"
    warn "  certificate: no site on this machine is being served. The configuration"
    warn "  passed its own test, so the cause is in the service rather than the file."
    return 1
  fi

  if probe_settles "$verify" "$host"; then
    ok "  the restart took effect — ${host} is being served now."
    return 0
  fi
  return 1
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
  local out out2
  if out="$(systemctl reload nginx 2>&1)"; then
    return 0
  fi
  # SECOND MECHANISM, and it is the point of this block rather than a nicety.
  #
  # `systemctl reload nginx` fails whenever systemd is not the thing supervising
  # nginx — a master started by hand, a unit that was never installed, a unit in
  # a failed state — and on an install where that was true, nginx went FIVE DAYS
  # without reading a new configuration while every run reported success.
  #
  # `nginx -s reload` does not involve systemd at all: it reads the master's pid
  # file and signals it directly. It is a genuinely different path to the same
  # outcome, so trying it is not a retry of something that just failed. Every
  # repair this product offers ended in the systemctl call, which meant every
  # repair inherited the one failure it was meant to fix.
  if out2="$(nginx -s reload 2>&1)"; then
    info "systemd could not reload nginx (${out:-no output}); signalled the master directly instead."
    return 0
  fi
  if true; then
    warn "nginx accepted the configuration but RELOADING it failed BOTH ways:"
    warn "  systemctl: ${out:-no output}"
    warn "  nginx -s reload: ${out2:-no output}"
    warn "  The new vhost is on disk and the running nginx has never read it, so nothing"
    warn "  serves this host yet. Not proceeding to certbot: a challenge cannot be answered"
    warn "  by configuration that is not loaded, and the attempt would spend a rate-limited"
    warn "  validation to discover that."
    return 1
  fi
  return 0
}

HOST_FAILURES=0

# PENDING_SERVE collects hosts whose certificate was issued in this run but whose
# vhost the running server has not picked up. Settled once, at the end.
PENDING_SERVE=()
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
  if ! probe_challenge "$HOST"; then
    warn "pre-flight failed for ${HOST}: this server does not serve its own ACME challenge over loopback."
    # WHY THIS DUMP EXISTS. The reload immediately above reported SUCCESS and the
    # configuration still is not live — nginx's workers predate the vhost by
    # days. `systemctl reload nginx` returning 0 while nginx does not reload is
    # the state that cannot be diagnosed from outside, and every remote guess at
    # it so far has been wrong.
    #
    # So the helper records what systemd actually thinks it is managing. Whether
    # the unit is active, which PID it believes is the master, and which PID the
    # pid file names, together answer it: if they disagree, systemd is reloading
    # something that is not the running nginx, and its success is true for the unit
    # and meaningless for the server.
    warn "  reload diagnostics — systemd's view of nginx:"
    warn "    is-active: $(systemctl is-active nginx 2>&1 | head -n1)"
    warn "    unit MainPID: $(systemctl show nginx -p MainPID --value 2>&1 | head -n1)"
    warn "    pid file: $(cat /run/nginx.pid 2>/dev/null || echo 'unreadable')"
    warn "    master process: $(pgrep -o -x nginx 2>/dev/null || echo 'none found')"
    warn "  If the unit's MainPID and the running master disagree, systemd is reloading"
    warn "  a process that is not this nginx, and its success says nothing about the server."
    warn "  nginx accepted the vhost but a request carrying Host: ${HOST} is not reaching it —"
    warn "  most often no enabled server block names this host, so the default server answers."

    # ESCALATE RATHER THAN REPORT. Every repair this product offered ended in a
    # reload, so when the reload was the thing failing, every repair inherited
    # the failure and the operator was handed the same diagnosis again. The
    # helper is already root, already holds the evidence, and is the only thing
    # positioned to act on it.
    warn "  reload reported success and did not take effect — escalating."
    if force_apply probe_challenge "$HOST"; then
      ok "pre-flight passes for ${HOST} after escalation; continuing to certbot."
    else
      warn "  escalation did not make this server answer for ${HOST}."
      warn "  certbot was NOT run: a validation that cannot succeed only spends the retry budget."
      set_tls "$HOST" failed
      HOST_FAILURES=$((HOST_FAILURES + 1))
      continue
    fi
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
      # The SAME blind spot closes here. reload_ok reports what the reload
      # COMMAND said; on the install this was built for, that was 0 while nginx
      # went on serving a five-day-old configuration. A certificate that exists
      # on disk behind a server that never loaded the vhost referencing it is
      # indistinguishable, from the panel, from no certificate at all — and it is
      # worse, because the registry would say active.
      if probe_https "$HOST"; then
        set_tls "$HOST" active
        ok "VayuDomain live: https://${HOST}"
      else
        # DEFERRED, not restarted here. See settle_pending_hosts: a restart per
        # host multiplies with the host count, and refusing one strands a
        # certificate that was just paid for. One apply at the end does both jobs.
        PENDING_SERVE+=("$HOST")
        info "certificate issued for ${HOST}; the running server has not loaded its vhost yet."
        info "  Deferred to a single apply at the end of this run."
      fi
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

# NO RESTART. This is ADR-0155 P1, and the deletion is the feature.
#
# What used to be here restarted VayuPress at the end of every run and then
# polled /health for up to sixty seconds, twice over. nginx has no queue in front
# of :8080, so every one of those seconds was a 502 for every visitor — a full
# outage on a live site, to publish a certificate.
#
# It was never needed. This helper's only lasting effect on the RUNNING app is
# the registry row written by `vayupress domains set-tls`, from a separate CLI
# process, into SQLite. The server re-reads that table on a thirty-second TTL,
# and the constant carries its own reason (internal/domain/domain.go):
#
#   "writes invalidate the cache immediately, and the TTL only bounds staleness
#    from an out-of-band DB edit."
#
# A CLI process writing the registry IS that out-of-band edit. The domain starts
# resolving within thirty seconds, by a mechanism that already ships and is
# already tested, and the restart bought nothing but the outage.
#
# The health poll goes with it rather than being kept "just in case". It existed
# to watch a restart that no longer happens, and a check that watches nothing
# reports success forever. Whether the app is healthy is the panel's job, and the
# panel already does it.

# REGISTRY_TTL_SECONDS mirrors internal/domain.cacheTTL. Stated rather than
# rounded to "a moment", because an operator watching a domain go live needs to
# know whether to wait or to start looking for a fault.
REGISTRY_TTL_SECONDS=30

# announce_settle_delay says what actually happens now. "Live shortly" and "live
# now" are different promises and only one of them is true.
announce_settle_delay() {
  info "No restart was needed. The running server picks up a newly certified domain"
  info "  from its own registry within ${REGISTRY_TTL_SECONDS}s; nothing was interrupted."
}


# settle_pending_hosts makes every certificate issued in this run actually serve.
#
# THE DEFECT THIS REPLACES, measured on a live install rather than imagined. The
# escalation ladder restarted nginx during the pre-flight, which spent the run's
# single restart allowance. certbot then issued the certificate successfully --
# and the post-issuance step found the reload had once again not taken effect,
# asked for a restart, and was REFUSED because one had already happened:
#
#   Congratulations! Your certificate and chain have been saved at: ...
#   nginx has already been restarted once in this run; not restarting again.
#   certificate issued for test.johal.in, but this server does not serve it yet.
#
# So the run ended with a valid certificate on disk that nothing served, and
# recorded the host as failed. That is a worse outcome than the brief
# interruption the allowance existed to prevent: the operator sat through the
# whole issuance and got nothing for it, and the next run would do the same.
#
# The allowance was the right instinct at the wrong granularity. A restart PER
# HOST multiplies with the number of hosts; a restart per PHASE does not. Every
# host still waiting is settled here with ONE apply for the whole run, however
# many there are -- so the cost is bounded at two restarts per run regardless of
# how many domains this install carries.
settle_pending_hosts() {
  [ "${#PENDING_SERVE[@]}" -gt 0 ] || return 0
  local host out
  info "${#PENDING_SERVE[@]} host(s) hold a certificate the running server has not loaded."

  if ! nginx_ok; then
    warn "  not applying: nginx rejects the configuration on disk."
    for host in "${PENDING_SERVE[@]}"; do
      set_tls "$host" failed
      HOST_FAILURES=$((HOST_FAILURES + 1))
    done
    return 1
  fi

  # Cheapest first, exactly as in force_apply. On the install this was written
  # for the signal never worked -- not on the original master and not on the
  # fresh one after a restart -- but a box where it does work should not be
  # interrupted for nothing.
  if out="$(nginx -s reload 2>&1)"; then
    info "  signalled the running master directly (nginx -s reload)."
  else
    warn "  nginx -s reload failed: ${out:-no output}"
  fi

  local still=()
  for host in "${PENDING_SERVE[@]}"; do
    probe_https "$host" || still+=("$host")
  done
  if [ "${#still[@]}" -eq 0 ]; then
    for host in "${PENDING_SERVE[@]}"; do
      set_tls "$host" active
      ok "VayuDomain live: https://${host}"
    done
    return 0
  fi

  warn "  the reload did not take effect; restarting nginx once for all of them."
  if ! out="$(systemctl restart nginx 2>&1)"; then
    warn "  systemctl restart nginx failed: ${out:-no output}"
  fi
  if ! systemctl is-active --quiet nginx 2>/dev/null; then
    warn "  nginx is NOT running after the restart -- starting it."
    systemctl start nginx >/dev/null 2>&1 || nginx >/dev/null 2>&1 || true
    sleep 1
  fi
  if ! systemctl is-active --quiet nginx 2>/dev/null && ! pgrep -o -x nginx >/dev/null 2>&1; then
    warn "  nginx is still not running. No site on this machine is being served, which is"
    warn "  now more serious than any certificate."
  fi

  for host in "${PENDING_SERVE[@]}"; do
    if probe_settles probe_https "$host"; then
      set_tls "$host" active
      ok "VayuDomain live: https://${host}"
    else
      local code
      code="$(probe_https_code "$host")"
      warn "certificate issued for ${host}, but this server still does not serve it."
      warn "  A request to https://${host}/ on this machine returned status ${code}."
      warn "  000 means no HTTP response arrived at all, which is what the catch-all default"
      warn "  server produces when no block names this host. The certificate is valid and"
      warn "  kept; what has not taken effect is the vhost that uses it."
      set_tls "$host" failed
      HOST_FAILURES=$((HOST_FAILURES + 1))
    fi
  done
  return 0
}

settle_pending_hosts

announce_settle_delay

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
