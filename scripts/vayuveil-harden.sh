#!/usr/bin/env bash
#
# vayuveil-harden.sh — write the VayuVeil hardening drop-in, restart, and revert
# if the service does not come back.
#
# ADR-0150 §5 S6, the privileged half. The panel cannot edit a systemd unit: the
# service runs unprivileged with NoNewPrivileges, which is itself one of the
# controls this script exists to install. So the panel creates an empty flag
# file, a .path unit notices it, and systemd runs this — root-owned, fixed path,
# no arguments. Nothing the requesting process supplies reaches this script, and
# the directive list below is compiled in rather than passed in, so a compromise
# of the web session can ask for hardening and cannot choose what hardening is.
#
# WHY THE RESTART IS PART OF THE JOB
# Systemd applies unit directives at exec. A drop-in written under a running
# process changes nothing about that process and will not until it starts again.
# A worker that wrote the file and stopped there would let the panel report a
# control that does not exist — the exact defect ADR-0150 is about. So this
# restarts, and the panel then verifies by reading the kernel back rather than by
# believing this script.
#
# WHY THE REVERT IS PART OF THE JOB
# A hardening button that can leave an operator locked out of their own panel is
# worse than the exposure it closes. If the unit does not come back active, the
# drop-in is removed and the service is restarted without it, and the result file
# says so loudly.

set -uo pipefail

STATE_DIR="${VAYU_DATA_DIR:-/var/lib/vayupress}"
REQUEST="${STATE_DIR}/veilharden.request"
RESULT="${STATE_DIR}/veilharden.result"
LOG="${STATE_DIR}/veilharden.log"

UNIT_NAME="vayupress"
UNIT_FILE="/etc/systemd/system/${UNIT_NAME}.service"
DROPIN_DIR="/etc/systemd/system/${UNIT_NAME}.service.d"
DROPIN="${DROPIN_DIR}/20-vayuveil-hardening.conf"

STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
WROTE=()
SKIPPED=()
REVERTED=false
FAILED=false
DETAIL=""

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG" 2>/dev/null || true; }

# json_array prints a JSON array from the remaining arguments, escaping the two
# characters that can appear in a reason string and break the document. A result
# file that will not parse is read by the panel as "never run", which would hide
# a revert — the one outcome that must never be silent.
json_array() {
  local first=1 item
  printf '['
  for item in "$@"; do
    item=${item//\\/\\\\}
    item=${item//\"/\\\"}
    [[ $first -eq 1 ]] || printf ','
    printf '"%s"' "$item"
    first=0
  done
  printf ']'
}

write_result() {
  local finished; finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local tmp="${RESULT}.tmp.$$"
  {
    printf '{"started_at":"%s","finished_at":"%s","wrote":' "$STARTED_AT" "$finished"
    json_array "${WROTE[@]+"${WROTE[@]}"}"
    printf ',"skipped":'
    json_array "${SKIPPED[@]+"${SKIPPED[@]}"}"
    printf ',"reverted":%s,"failed":%s,"detail":"%s"}' \
      "$REVERTED" "$FAILED" "$(printf '%s' "$DETAIL" | sed 's/\\/\\\\/g; s/"/\\"/g')"
  } >"$tmp" 2>/dev/null || { log "could not write result"; return; }
  # Atomic, because the panel reads this file on every page load and a
  # half-written document parses as no document at all.
  mv -f "$tmp" "$RESULT" 2>/dev/null || true
  # The panel's verdict compares this file's MTIME against the service's start
  # time, so the mtime must be now rather than inherited from anything.
  touch "$RESULT" 2>/dev/null || true
  chmod 0644 "$RESULT" 2>/dev/null || true
}

# Consume the request FIRST, whatever happens next. The watcher is a .path unit
# with PathExists=, which only re-arms when the file goes away — leaving it in
# place on an early exit means no future request from the panel ever fires.
mkdir -p "$STATE_DIR" 2>/dev/null || true
rm -f "$REQUEST" 2>/dev/null || true
log "hardening requested"

# ── Refuse rather than guess ─────────────────────────────────────────────────
# Writing a drop-in for a unit that does not exist produces a directory nothing
# reads and a panel that says "written" forever.
if [[ ! -f "$UNIT_FILE" ]]; then
  FAILED=true
  DETAIL="No ${UNIT_FILE} on this host, so there is no unit to write a drop-in for. This install runs from a unit at some other path, or not from systemd at all; nothing was changed."
  log "$DETAIL"
  write_result
  exit 0
fi

# ── ProtectHome, conditionally ───────────────────────────────────────────────
# ProtectHome=yes makes /home, /root and /run/user unreadable. On an install
# whose data directory lives under one of them that is not hardening, it is an
# outage — the service comes back unable to open its own database. So the actual
# path is read and the directive is skipped, with the reason recorded, rather
# than written hopefully.
DATA_DIR="$STATE_DIR"
if [[ -r /etc/vayupress/env ]]; then
  ENV_DATA="$(sed -n 's/^VAYU_DATA_DIR=//p' /etc/vayupress/env | head -1 | tr -d "\"'")"
  [[ -n "${ENV_DATA:-}" ]] && DATA_DIR="$ENV_DATA"
fi

DIRECTIVES=("NoNewPrivileges=yes" "PrivateDevices=yes" "PrivateTmp=yes" "MemorySwapMax=0")
case "$DATA_DIR" in
  /home/*|/root/*|/home|/root)
    SKIPPED+=("ProtectHome=yes — this install's data directory is ${DATA_DIR}, which ProtectHome would make unreadable. Hardening that stops the service opening its own database is an outage, not a control.")
    ;;
  *)
    DIRECTIVES+=("ProtectHome=yes")
    ;;
esac

# ── Write the drop-in ────────────────────────────────────────────────────────
mkdir -p "$DROPIN_DIR" 2>/dev/null || true
BACKUP=""
if [[ -f "$DROPIN" ]]; then
  BACKUP="${DROPIN}.prev"
  cp -f "$DROPIN" "$BACKUP" 2>/dev/null || BACKUP=""
fi

{
  echo "# Written by vayuveil-harden.sh on request from the VayuPress panel."
  echo "# ADR-0150 section 5, step S6. Every directive here is one the service can"
  echo "# read back from the kernel afterwards; directives that cannot be verified"
  echo "# from inside the process are deliberately not written."
  echo "[Service]"
  for d in "${DIRECTIVES[@]}"; do echo "$d"; done
} >"$DROPIN" 2>/dev/null

if [[ ! -s "$DROPIN" ]]; then
  FAILED=true
  DETAIL="Could not write ${DROPIN}."
  log "$DETAIL"
  write_result
  exit 0
fi
chmod 0644 "$DROPIN" 2>/dev/null || true
WROTE=("${DIRECTIVES[@]}")

revert() {
  # Put the unit back exactly as it was: restore a previous drop-in if there was
  # one, otherwise remove ours entirely. Leaving a broken drop-in behind would
  # mean the next legitimate restart — a reboot, an in-app update — also fails,
  # long after anyone connects it to this button.
  if [[ -n "$BACKUP" && -f "$BACKUP" ]]; then
    mv -f "$BACKUP" "$DROPIN" 2>/dev/null || true
  else
    rm -f "$DROPIN" 2>/dev/null || true
  fi
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl restart "${UNIT_NAME}.service" >/dev/null 2>&1 || true
  REVERTED=true
  WROTE=()
}

if ! systemctl daemon-reload >/dev/null 2>&1; then
  FAILED=true
  DETAIL="systemctl daemon-reload failed, so the drop-in was removed rather than left to take effect at some unrelated future restart."
  revert
  log "$DETAIL"
  write_result
  exit 0
fi

# ── Restart, then check it came back ─────────────────────────────────────────
log "restarting ${UNIT_NAME}.service with $(IFS=,; echo "${DIRECTIVES[*]}")"
systemctl restart "${UNIT_NAME}.service" >/dev/null 2>&1 || true

# Sample repeatedly rather than checking once. Restart=always with RestartSec=5
# means a unit that crashes on the new directives CYCLES rather than settling,
# and a single look a moment after the restart lands inside one of the up phases
# and reads as healthy. So: a grace period for a legitimately slow start, then
# fifteen looks a second apart, and any single one of them finding the unit
# inactive is a failure. A settled service is active at all fifteen; a crash
# loop with a five-second retry cannot be.
sleep 5
DIPS=0
for _ in $(seq 1 15); do
  systemctl is-active --quiet "${UNIT_NAME}.service" || DIPS=$((DIPS + 1))
  sleep 1
done

if [[ "$DIPS" -ne 0 ]]; then
  DETAIL="The service did not come back with the drop-in in place, so it was removed and the service restarted without it. Nothing is hardened and nothing is broken. systemd's own reason: $(systemctl status "${UNIT_NAME}.service" --no-pager -n 3 2>&1 | tr '\n' ' ' | tr -d '"' | cut -c1-400)"
  revert
  log "$DETAIL"
  write_result
  exit 0
fi

DETAIL="Drop-in written to ${DROPIN} and the service restarted into it. Whether each directive is actually in force is not asserted here — the panel reads that back from the kernel."
[[ ${#SKIPPED[@]} -gt 0 ]] && DETAIL="${DETAIL} One directive was skipped; the reason is listed beside it."
log "$DETAIL"
write_result
exit 0
