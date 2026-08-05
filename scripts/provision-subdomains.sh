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

# ── Self-upgrade ─────────────────────────────────────────────────────────────
#
# WHY THIS EXISTS, and it is the whole point: these helpers run as ROOT out of
# /usr/local/lib/vayupress, which the unprivileged web app cannot write. That
# boundary is correct — a process that can replace what root executes is a full
# privilege escalation — but it had a consequence nobody had stated: the in-app
# updater swaps the BINARY ONLY, so a fix to these scripts reached nobody.
#
# A real defect proved it. The nginx reload step read
#
#   systemctl reload nginx 2>/dev/null || true
#
# which discarded the reload's exit status and reported success regardless, so a
# reload that never happened looked like one that did and every certificate on
# the install failed with an unexplained connection error. The fix was one line
# and it could not reach a single existing install, because the only delivery
# mechanism was an operator with a shell — for a product whose premise is that
# you never need one.
#
# So the worker upgrades itself, by exactly the mechanism the VayuShield agent
# already uses (ADR-0123): fetch the signed bundle from the pinned repository,
# verify it with cosign BEFORE unpacking, and install. Nothing the web app can
# write becomes part of what root runs; the app's only power remains the single
# empty flag file that asks for a run.
UPGRADE_REPO="${UPGRADE_REPO:-johalputt/VayuPress}"
UPGRADE_ASSET="vayuprovision-helpers.tar.gz"
HELPERS_VERSION_STAMP="dev"

self_upgrade_helpers() {
  # Every failure here is a SKIP, never a stop. This runs before the work, and a
  # host that cannot reach GitHub must still provision with the helpers it has —
  # an upgrade path that can prevent the thing it exists to improve is worse than
  # no upgrade path.
  command -v curl >/dev/null 2>&1 || { log "self-upgrade skipped: curl is not installed"; return 0; }
  local cosign_bin
  cosign_bin="$(command -v cosign 2>/dev/null)" || {
    log "self-upgrade skipped: cosign is not installed, and unverified code is never installed as root"
    return 0
  }

  local tmp
  tmp="$(mktemp -d)" || return 0
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN

  local base="https://github.com/${UPGRADE_REPO}/releases/latest/download"
  curl -fsSL --max-time 120 -o "${tmp}/${UPGRADE_ASSET}" "${base}/${UPGRADE_ASSET}" 2>/dev/null || {
    log "self-upgrade skipped: could not download the helper bundle"; return 0; }
  curl -fsSL --max-time 60 -o "${tmp}/bundle.json" "${base}/${UPGRADE_ASSET}.cosign.bundle" 2>/dev/null || {
    log "self-upgrade skipped: the release carries no signature for the helper bundle"; return 0; }

  # Verified BEFORE unpacking. Unpacking an unverified archive as root is already
  # the compromise, whatever is checked afterwards.
  if ! "$cosign_bin" verify-blob "${tmp}/${UPGRADE_ASSET}" \
        --bundle "${tmp}/bundle.json" \
        --certificate-identity-regexp "^https://github.com/${UPGRADE_REPO}/" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        >"${tmp}/verify.log" 2>&1; then
    # Not reported as "someone tampered with it". Keyless verification needs
    # Sigstore as well as GitHub, so a host that cannot reach sigstore.dev fails
    # here having established NOTHING — and telling an operator they are under
    # attack when their egress is filtered is a false accusation of exactly the
    # kind this project's posture report exists to avoid.
    log "self-upgrade skipped: the bundle's signature could not be verified (this proves nothing about the bundle if this host cannot reach sigstore.dev): $(tail -n 2 "${tmp}/verify.log" | tr '\n' ' ')"
    return 0
  fi

  mkdir -p "${tmp}/x" && tar -C "${tmp}/x" -xzf "${tmp}/${UPGRADE_ASSET}" 2>/dev/null || {
    log "self-upgrade skipped: the verified bundle could not be unpacked"; return 0; }
  local n=0 f
  for f in "${tmp}"/x/*.sh; do
    [ -f "$f" ] || continue
    bash -n "$f" 2>/dev/null || { log "self-upgrade ABORTED: $(basename "$f") does not parse"; return 0; }
    n=$((n + 1))
  done
  [ "$n" -gt 0 ] || { log "self-upgrade skipped: the bundle contained no helpers"; return 0; }

  install -m 0755 -o root -g root "${tmp}"/x/*.sh "$LIB_DIR"/ 2>/dev/null || {
    log "self-upgrade skipped: could not install into ${LIB_DIR}"; return 0; }
  [ -f "${tmp}/x/helpers.version" ] && install -m 0644 "${tmp}/x/helpers.version" "$LIB_DIR"/ 2>/dev/null
  log "helpers upgraded to $(cat "${LIB_DIR}/helpers.version" 2>/dev/null || echo unknown) (${n} script(s), signature verified)"
  # The helpers this run is about to execute are the NEW ones — the driver reads
  # them from disk per helper. This driver itself is replaced for the next run.
  return 0
}

self_upgrade_helpers

# ensure_veilharden_units writes the VayuVeil hardening watcher if it is absent.
#
# WHY THIS LIVES HERE, in a script about certificates.
#
# self_upgrade_helpers delivers new root-side SCRIPTS to every install on the
# daily sweep, with no terminal. It does not write systemd UNITS — and a helper
# with no unit watching for its request is a helper nothing can ever run. The
# VayuVeil hardening worker shipped that way: the script would arrive on every
# install and the panel would go on showing a copyable curl-to-root command
# forever, because the watcher it also needs is only written by the one-time
# installer. That is the shape this project has a standing rule about: a repair
# that reaches only the operators willing to open a shell has reached nobody.
#
# So the daily sweep closes it. Fixed content, root-owned, no input from the web
# app — the privilege boundary is exactly the one already in force here.
#
# It touches ONLY the veilharden units. Rewriting the provisioning units while
# running from them is a different and much worse idea.
ensure_veilharden_units() {
  [[ -x "${LIB_DIR}/vayuveil-harden.sh" ]] || return 0
  command -v systemctl >/dev/null 2>&1 || return 0
  # Idempotent: present and enabled is the common case, and rewriting the files
  # on every daily run would churn systemd for nothing.
  if [[ -f /etc/systemd/system/vayupress-veilharden.path ]] &&
     [[ -f /etc/systemd/system/vayupress-veilharden.service ]] &&
     systemctl is-enabled --quiet vayupress-veilharden.path 2>/dev/null; then
    return 0
  fi
  log "installing the VayuVeil hardening watcher (absent or not enabled)"

  cat > /etc/systemd/system/vayupress-veilharden.service <<'SYSTEMD'
[Unit]
Description=VayuPress — apply the VayuVeil hardening drop-in and verify the service came back
Documentation=https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0150-vayuveil-endpoint-observation-control.md

[Service]
Type=oneshot
EnvironmentFile=-/etc/vayupress/env
ExecStart=/usr/local/lib/vayupress/vayuveil-harden.sh
TimeoutStartSec=300
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vayupress-veilharden
SYSTEMD

  cat > /etc/systemd/system/vayupress-veilharden.path <<'SYSTEMD'
[Unit]
Description=VayuPress — watch for a VayuVeil hardening request

[Path]
# The unprivileged service creates this file to request a run. Its CONTENTS are
# never read: the request is the file's existence and nothing more.
PathExists=/var/lib/vayupress/veilharden.request
Unit=vayupress-veilharden.service

[Install]
WantedBy=multi-user.target
SYSTEMD

  systemctl daemon-reload >/dev/null 2>&1 || true
  # REPORTED, not discarded. A watcher written and not enabled is the dead-button
  # state: the panel's request is created, nothing consumes it, and the card says
  # "requested" until it times out.
  if enable_out="$(systemctl enable --now vayupress-veilharden.path 2>&1)"; then
    log "VayuVeil hardening watcher installed and enabled"
  else
    log "WARNING: the VayuVeil hardening watcher was written but could NOT be enabled: ${enable_out:-no output}"
  fi
}

ensure_veilharden_units

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
    # when it skips, by design. Read back what this run appended and classify it,
    # so the result cannot claim success for a no-op.
    #
    # There are THREE ways a helper exits 0 having done nothing, and matching
    # only "skipping" caught one. The one that mattered most was missed: every
    # helper aborts early when `nginx -t` was ALREADY failing — one stale vhost
    # anywhere under sites-enabled does it — and that path prints no "skipping".
    # All five helpers were therefore counted as having run, and the panel
    # reported "5 provisioned, 0 skipped, 0 reported a problem" for an install
    # where nothing was provisioned and nothing would be until someone found the
    # bad file. A report that reads clean is worse than no report: it ends the
    # investigation.
    #
    # The nginx abort is recorded as a PROBLEM rather than a skip. It needs the
    # operator; a subdomain whose DNS is simply not pointed yet does not, and
    # putting them in one column loses the difference.
    appended="$(tail -c "+$((before + 1))" "${STATE_DIR}/provision.log" 2>/dev/null || true)"
    if printf '%s' "$appended" | grep -qi "ALREADY invalid"; then
      failed=$((failed + 1))
      details+=("${helper}=nginx-config-broken")
      log "${helper} aborted: nginx configuration was already invalid before it ran"
    elif printf '%s' "$appended" | grep -qiE "skipping|nothing to do|nothing to provision"; then
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
