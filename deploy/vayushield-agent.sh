#!/usr/bin/env bash
#
# vayushield-agent.sh — the privileged reconcile agent that makes VayuShield's
# Tier 2 (kernel nftables) and Tier 3 (nginx edge) hardening toggleable from the
# VayuOS panel WITHOUT giving the web app any privilege. See ADR-0123.
#
# Privilege separation / security model:
#   • The UNPRIVILEGED VayuPress web app expresses INTENT only, by creating or
#     removing an EMPTY flag file in the control dir it owns:
#         <control>/tier2.want   <control>/tier3.want
#   • THIS agent runs as root and polls those flags. On a change it runs ONLY the
#     fixed, vetted, root-owned scripts installed at LIB_DIR. It reads NO content
#     or arguments from anything the web app writes — the flag's mere presence is
#     the whole signal — so there is no command-injection surface.
#   • The agent writes back a status file (<control>/tierN.state) and a heartbeat
#     (<control>/agent.alive) so the panel can show live state and detect that the
#     helper is installed. Those are plain status strings the panel only displays.
#
# The agent NEVER creates the control dir (that stays owned by the service user
# who writes the flags); it waits for the app to create it, then only reads flags
# and writes status/heartbeat into it (root can write into the app-owned dir).
set -uo pipefail

CONTROL_DIR="${VAYUSHIELD_CONTROL_DIR:-/var/lib/vayupress/vayushield-control}"
LIB_DIR="${VAYUSHIELD_LIB_DIR:-/usr/local/lib/vayushield}"
FIREWALL="${LIB_DIR}/vayushield-firewall.sh"
NGINX_CONF_SRC="${LIB_DIR}/nginx-vayushield.conf"
NGINX_CONF_DST="${VAYUSHIELD_NGINX_DST:-/etc/nginx/conf.d/vayushield.conf}"
POLL="${VAYUSHIELD_POLL_SECONDS:-5}"

write_state() { # $1=tier tag, $2=state word — best-effort.
  [ -d "$CONTROL_DIR" ] || return 0
  printf '%s' "$2" >"${CONTROL_DIR}/$1.state" 2>/dev/null || true
}

# write_reason extracts a short, human-readable failure reason from a captured
# log ($2) into <control>/<tag>.reason so the panel can show WHY a tier errored
# instead of a bare "check the log". Best-effort.
write_reason() { # $1=tier tag, $2=log file
  [ -d "$CONTROL_DIR" ] || return 0
  local line=""
  if [ -s "$2" ]; then
    line="$(grep -iE 'error|not installed|rejected|denied|fail|permission|no such' "$2" 2>/dev/null | tail -n 1)"
    [ -z "$line" ] && line="$(tail -n 1 "$2" 2>/dev/null)"
  fi
  printf '%s' "${line:0:200}" >"${CONTROL_DIR}/$1.reason" 2>/dev/null || true
}

clear_reason() { # $1=tier tag
  [ -d "$CONTROL_DIR" ] || return 0
  : >"${CONTROL_DIR}/$1.reason" 2>/dev/null || true
}

reconcile_tier2() {
  local want=0 active=0
  [ -f "${CONTROL_DIR}/tier2.want" ] && want=1
  nft list table inet vayushield >/dev/null 2>&1 && active=1
  if [ "$want" = 1 ] && [ "$active" = 0 ]; then
    write_state tier2 applying
    if [ -f "$FIREWALL" ]; then
      if bash "$FIREWALL" apply >"${CONTROL_DIR}/tier2.log" 2>&1; then
        write_state tier2 active
        clear_reason tier2
      else
        write_state tier2 error
        write_reason tier2 "${CONTROL_DIR}/tier2.log"
      fi
    else
      write_state tier2 error
      printf '%s' "firewall script not found at ${FIREWALL}" >"${CONTROL_DIR}/tier2.reason" 2>/dev/null || true
    fi
  elif [ "$want" = 0 ] && [ "$active" = 1 ]; then
    write_state tier2 removing
    bash "$FIREWALL" remove >/dev/null 2>&1 || true
    write_state tier2 inactive
  elif [ "$active" = 1 ]; then
    write_state tier2 active
  else
    write_state tier2 inactive
  fi
}

reconcile_tier3() {
  local want=0 active=0
  [ -f "${CONTROL_DIR}/tier3.want" ] && want=1
  [ -f "$NGINX_CONF_DST" ] && active=1
  if [ "$want" = 1 ] && [ "$active" = 0 ]; then
    write_state tier3 applying
    # Install the vetted conf, validate, then reload. If validation fails, roll
    # the file back so nginx is never left broken. Output is captured so the
    # panel can show the reason on failure.
    if [ -f "$NGINX_CONF_SRC" ] && cp -f "$NGINX_CONF_SRC" "$NGINX_CONF_DST" >"${CONTROL_DIR}/tier3.log" 2>&1 && nginx -t >>"${CONTROL_DIR}/tier3.log" 2>&1 && systemctl reload nginx >>"${CONTROL_DIR}/tier3.log" 2>&1; then
      write_state tier3 active
      clear_reason tier3
    else
      rm -f "$NGINX_CONF_DST" 2>/dev/null || true
      systemctl reload nginx >/dev/null 2>&1 || true
      write_state tier3 error
      write_reason tier3 "${CONTROL_DIR}/tier3.log"
    fi
  elif [ "$want" = 0 ] && [ "$active" = 1 ]; then
    write_state tier3 removing
    rm -f "$NGINX_CONF_DST" 2>/dev/null || true
    nginx -t >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true
    write_state tier3 inactive
  elif [ "$active" = 1 ]; then
    write_state tier3 active
  else
    write_state tier3 inactive
  fi
}

run_agent() {
  echo "vayushield-agent: watching ${CONTROL_DIR} (poll ${POLL}s)"
  while true; do
    if [ -d "$CONTROL_DIR" ]; then
      printf '%s' "$(date -u +%s)" >"${CONTROL_DIR}/agent.alive" 2>/dev/null || true
      reconcile_tier2
      reconcile_tier3
    fi
    sleep "$POLL"
  done
}

# install_agent bootstraps the helper from the deploy/ dir this script lives in:
# it copies the vetted scripts to LIB_DIR, installs the systemd unit, and starts
# it. Run once as root from your VayuPress checkout:
#   sudo bash deploy/vayushield-agent.sh install
install_agent() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "error: run as root — sudo bash $0 install" >&2
    exit 1
  fi
  local src
  src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  echo "• installing VayuShield agent from ${src}…"
  install -d -m 0755 "$LIB_DIR"
  install -m 0755 "${src}/vayushield-firewall.sh" "${LIB_DIR}/vayushield-firewall.sh"
  install -m 0755 "${src}/vayushield-agent.sh" "${LIB_DIR}/vayushield-agent.sh"
  if [ -f "${src}/nginx-vayushield.conf" ]; then
    install -m 0644 "${src}/nginx-vayushield.conf" "${LIB_DIR}/nginx-vayushield.conf"
  fi
  install -m 0644 "${src}/vayushield-agent.service" /etc/systemd/system/vayushield-agent.service
  systemctl daemon-reload
  systemctl enable --now vayushield-agent
  echo "✓ VayuShield agent installed and started."
  echo "  Toggle Tier 2/3 from VayuOS → Bot Shield → Network hardening (hard-refresh the page)."
}

uninstall_agent() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "error: run as root" >&2
    exit 1
  fi
  systemctl disable --now vayushield-agent 2>/dev/null || true
  rm -f /etc/systemd/system/vayushield-agent.service
  systemctl daemon-reload 2>/dev/null || true
  echo "✓ VayuShield agent removed. Any applied Tier 2/3 rules are left as-is — turn them off in the panel first, or run vayushield-firewall.sh remove."
}

case "${1:-run}" in
  run) run_agent ;;
  install) install_agent ;;
  uninstall) uninstall_agent ;;
  status) systemctl status vayushield-agent --no-pager 2>/dev/null || echo "agent not installed" ;;
  *) echo "usage: $0 [run|install|uninstall|status]" >&2; exit 2 ;;
esac
