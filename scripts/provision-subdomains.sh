#!/usr/bin/env bash
#
# provision-subdomains.sh — the privileged half of one-click subdomain setup.
#
# WHY THIS EXISTS
# The VayuPress service runs as www-data with NoNewPrivileges=yes, so the
# in-app updater can swap the binary but can NEVER obtain a TLS certificate or
# reload nginx — both need root. That is correct hardening, and it left a real
# hole in the product: an operator who only ever uses VayuOS → Update now got a
# current binary and no subdomains, with no indication anything was missing.
# Certificates silently absent, PGP key discovery silently dead.
#
# This script is the root-side worker that closes that hole. It is triggered by
# vayupress-provision.path when the unprivileged service creates a request flag,
# and by vayupress-provision.timer daily so a DNS record added later is picked
# up on its own.
#
# THE SECURITY PROPERTY THAT MAKES THIS SAFE
# The unprivileged side can say exactly one thing: "please provision". It cannot
# say what to provision, cannot pass an argument, and cannot influence which
# code runs. Three things enforce that, and all three matter:
#
#   1. The trigger is an EMPTY FLAG FILE. No content is read from it — ever. If
#      this script grew to parse that file, www-data would be handing arguments
#      to a root process, which is the whole vulnerability class this design
#      exists to avoid.
#   2. This script and the setup scripts it calls live in /usr/local/lib/vayupress,
#      owned root:root mode 0755. www-data cannot modify them, so it cannot
#      change what root executes. If that directory were writable by the service
#      user, this file would be a local privilege escalation rather than a
#      feature.
#   3. The systemd unit hardcodes this path. There is no lookup, no PATH
#      search, and no argument passed from the requester.
#
# It is IDEMPOTENT: every setup script it calls skips cleanly when a subdomain's
# DNS is not pointed, and re-running is how a later-added record gets picked up.

set -u

STATE_DIR=/var/lib/vayupress
LIB_DIR=/usr/local/lib/vayupress
REQUEST="${STATE_DIR}/provision.request"
RESULT="${STATE_DIR}/provision.result"
LOCK="${STATE_DIR}/provision.lock"

log() { echo "[provision] $*"; }

# Serialise: the .path unit and the .timer can fire close together, and two
# certbot runs against the same name at once is how rate limits get burned.
exec 9>"$LOCK" 2>/dev/null || true
if command -v flock >/dev/null 2>&1; then
  flock -n 9 || { log "another provisioning run is in progress — skipping"; exit 0; }
fi

# Consume the request FIRST. Removing it before the work rather than after means
# a record added while this run is in flight leaves a fresh request behind,
# which the .path unit picks up for another pass — rather than being swallowed
# by a run that had already read the world.
rm -f "$REQUEST"

started="$(date -u +%FT%TZ)"
log "starting subdomain provisioning at ${started}"

# Resolve DOMAIN here and export it, rather than letting each helper parse
# /etc/vayupress/env for itself.
#
# This is where a real failure hid. Every helper skips cleanly when DOMAIN is
# empty -- correct on its own, so a deploy is never blocked by an unconfigured
# box -- but skipping is indistinguishable from succeeding to the caller, so a
# run in which NOTHING was provisioned reported "0 problems". An operator read
# that as done and went looking for the fault somewhere else entirely.
ENV_FILE=/etc/vayupress/env
if [[ -z "${DOMAIN:-}" && -r "$ENV_FILE" ]]; then
  DOMAIN="$(sed -n -E 's/^[[:space:]]*(export[[:space:]]+)?DOMAIN=[[:space:]]*//p' "$ENV_FILE" |
            head -n1 | sed -E 's/^"(.*)"$/\1/; s/^'"'"'(.*)'"'"'$/\1/; s/[[:space:]]+$//')"
fi
export DOMAIN="${DOMAIN:-}"

if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" ]]; then
  # Report it as a failure, loudly. Silence here is what turned a one-line
  # configuration problem into a long hunt.
  log "ERROR: no usable DOMAIN found in ${ENV_FILE} — nothing can be provisioned"
  umask 022
  cat > "$RESULT" <<JSON
{
  "started_at": "${started}",
  "finished_at": "$(date -u +%FT%TZ)",
  "ran": 0,
  "failed": 1,
  "details": "no usable DOMAIN in ${ENV_FILE}"
}
JSON
  exit 0
fi
log "provisioning for domain: ${DOMAIN}"

# Repair database ownership before doing anything else.
#
# Earlier versions of the setup helpers ran `vayupress domains …` as root. That
# command opens SQLite read-write, so it created vayupress.db-wal and
# vayupress.db-shm owned by root:root in a directory owned by the service user —
# and the unprivileged service then could not write to its own database. The
# helpers no longer do this (they drop to the service user for every query), but
# an install that ran an older version still carries the root-owned files, and
# nothing else will ever put them right.
#
# This runs on the daily timer, so an affected install heals itself without
# anyone having to diagnose why writes started failing.
SERVICE_USER="${SERVICE_USER:-www-data}"
DB_PATH="${DB_PATH:-/var/lib/vayupress/vayupress.db}"
if id -u "$SERVICE_USER" >/dev/null 2>&1; then
  for f in "$DB_PATH" "${DB_PATH}-wal" "${DB_PATH}-shm"; do
    [[ -e "$f" ]] || continue
    if [[ "$(stat -c '%U' "$f" 2>/dev/null)" == "root" ]]; then
      log "repairing root-owned ${f} (left by an older provisioning run)"
      chown "${SERVICE_USER}:${SERVICE_USER}" "$f" 2>/dev/null || true
    fi
  done
fi

# Each helper is optional: an older checkout may not carry all of them, and a
# missing one must not fail the run.
ran=0
failed=0
skipped=0
declare -a details=()

for helper in setup-openpgpkey-subdomain.sh setup-talk-subdomain.sh \
              setup-mcp-subdomain.sh setup-api-subdomain.sh setup-vayudomain.sh; do
  script="${LIB_DIR}/${helper}"
  [[ -x "$script" ]] || { log "skip ${helper} (not installed)"; continue; }
  log "running ${helper}"
  before="$(wc -c <"${STATE_DIR}/provision.log" 2>/dev/null || echo 0)"
  if bash "$script" >>"${STATE_DIR}/provision.log" 2>&1; then
    # An exit status of 0 is not the same as work done: every helper exits 0
    # when it skips, by design. Read back what this run appended and record a
    # skip as a SKIP, so the result cannot claim success for a no-op.
    if tail -c "+$((before + 1))" "${STATE_DIR}/provision.log" 2>/dev/null | grep -qi "skipping"; then
      skipped=$((skipped + 1))
      details+=("${helper}=skipped")
    else
      ran=$((ran + 1))
      details+=("${helper}=ok")
    fi
  else
    # Non-fatal by design: one subdomain whose DNS is not pointed must never
    # stop the others from being provisioned.
    failed=$((failed + 1))
    details+=("${helper}=failed")
    log "${helper} reported a problem (see provision.log)"
  fi
done

finished="$(date -u +%FT%TZ)"

# Write a result the unprivileged UI can read, so VayuOS shows what happened
# instead of the operator having to read a log over SSH — which is the entire
# point of doing this from the console.
umask 022
cat > "$RESULT" <<JSON
{
  "started_at": "${started}",
  "finished_at": "${finished}",
  "ran": ${ran},
  "failed": ${failed},
  "skipped": ${skipped},
  "details": "$(IFS=,; echo "${details[*]:-none}")"
}
JSON

# Keep the log from growing without bound on a daily timer.
if [[ -f "${STATE_DIR}/provision.log" ]]; then
  tail -n 2000 "${STATE_DIR}/provision.log" > "${STATE_DIR}/provision.log.tmp" 2>/dev/null &&
    mv "${STATE_DIR}/provision.log.tmp" "${STATE_DIR}/provision.log"
fi

log "finished: ${ran} provisioned, ${skipped} skipped, ${failed} reported a problem"
exit 0
