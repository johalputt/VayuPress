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

# ── L1 dynamic kernel offload ─────────────────────────────────────────────────
# The app exports its live jail verdicts to <control>/banlist.txt (lines of
# "<ip> <unix-expiry>"). We reconcile a dedicated nftables table with timeout
# sets so those IPs are dropped at line rate, before a connection exists.
# Security: every line is revalidated here with a strict character whitelist —
# the agent builds nft input only from fields that pass validation, so content
# written by the (unprivileged) app can never become command syntax. The whole
# feature is gated behind Tier 2 being wanted: no Tier 2, no kernel changes.

BANLIST="${CONTROL_DIR}/banlist.txt"
DYN_TABLE="vayushield_dyn"

ensure_dyn_table() {
  nft list table inet "${DYN_TABLE}" >/dev/null 2>&1 && return 0
  nft -f - <<EOF 2>/dev/null
table inet ${DYN_TABLE} {
  set banned4 { type ipv4_addr; flags timeout; }
  set banned6 { type ipv6_addr; flags timeout; }
  chain input {
    type filter hook input priority -20; policy accept;
    ip saddr @banned4 drop
    ip6 saddr @banned6 drop
  }
}
EOF
}

remove_dyn_table() {
  nft delete table inet "${DYN_TABLE}" 2>/dev/null || true
}

# ── Address parsing ───────────────────────────────────────────────────────────
# The banlist used to be filtered with a character whitelist (`*[!0-9a-fA-F.:]*`).
# That does foreclose injection — no space, ';' or '{' survives it — but it is
# not a parser, and it admits strings that are not addresses at all: `abcd` is
# entirely hex characters, `999.999.999.999` is entirely digits and dots.
#
# Handing those to nft is not merely useless. nft treats an unparseable element
# as a HOSTNAME and calls the resolver:
#
#   Error: Could not resolve hostname: Name or service not known
#
# So a malformed line in a file written by the unprivileged app turned into a DNS
# lookup made by the root agent, on every poll — and in onion mode that is a
# clearnet callback from the most privileged process on the box. Parsing properly
# means nothing but a literal address ever reaches nft.

# _hex_groups <colon-separated list> — echo the group count, or fail if any group
# is not 1–4 hex digits. An empty list is zero groups.
_hex_groups() {
  local part n=0
  [ -n "$1" ] || { printf '0'; return 0; }
  local IFS=:
  set -f
  # shellcheck disable=SC2086 # deliberate word split on IFS=:
  set -- $1
  set +f
  for part; do
    case "$part" in ''|*[!0-9a-fA-F]*) return 1 ;; esac
    [ "${#part}" -le 4 ] || return 1
    n=$((n + 1))
  done
  printf '%s' "$n"
}

valid_ip4() {
  local s="$1" o
  case "$s" in *[!0-9.]*) return 1 ;; esac
  local IFS=.
  set -f
  # shellcheck disable=SC2086 # deliberate word split on IFS=.
  set -- $s
  set +f
  [ "$#" -eq 4 ] || return 1
  for o; do
    case "$o" in
      ''|*[!0-9]*) return 1 ;;
      # A multi-digit octet with a leading zero is a parsing differential: some
      # parsers read it as octal. Refuse rather than pick a side.
      0?*) return 1 ;;
    esac
    [ "${#o}" -le 3 ] || return 1
    [ "$o" -le 255 ] || return 1
  done
  return 0
}

valid_ip6() {
  local s="$1" head tail hn tn
  case "$s" in *[!0-9a-fA-F:]*) return 1 ;; esac
  case "$s" in *:*) ;; *) return 1 ;; esac
  # "::" is the zero-run and there can be only one. Anything else with a run of
  # colons (":::" and friends) is malformed.
  case "$s" in *:::*) return 1 ;; esac
  case "$s" in
    *::*::*) return 1 ;;
    *::*)
      head="${s%%::*}"
      tail="${s#*::}"
      hn="$(_hex_groups "$head")" || return 1
      tn="$(_hex_groups "$tail")" || return 1
      # "::" stands for at least one all-zero group, so the explicit groups on
      # either side can total at most 7.
      [ $((hn + tn)) -le 7 ] || return 1
      return 0
      ;;
    *)
      # No zero-run: a leading or trailing colon is then unbalanced by definition.
      case "$s" in :*|*:) return 1 ;; esac
      hn="$(_hex_groups "$s")" || return 1
      [ "$hn" -eq 8 ] || return 1
      return 0
      ;;
  esac
}

reconcile_banlist() {
  # Gated: dynamic offload only while the operator wants Tier 2 and nft exists.
  if [ ! -f "${CONTROL_DIR}/tier2.want" ] || ! command -v nft >/dev/null 2>&1; then
    remove_dyn_table
    write_state offload inactive
    return 0
  fi
  if ! ensure_dyn_table; then
    write_state offload error
    printf '%s' "could not create the ${DYN_TABLE} nftables table" >"${CONTROL_DIR}/offload.reason" 2>/dev/null || true
    return 0
  fi
  local now count=0
  now="$(date -u +%s)"
  local batch xdp_new flushonly
  batch="$(mktemp)"
  xdp_new="$(mktemp)"
  flushonly="$(mktemp)"
  # Mirror the file EXACTLY: flush both sets and re-add every valid entry in
  # the same atomic nft transaction. This honours removals (pardons — e.g. a
  # jailed visitor solved the challenge), not just additions.
  printf 'flush set inet %s banned4\nflush set inet %s banned6\n' "$DYN_TABLE" "$DYN_TABLE" >"$flushonly"
  cat "$flushonly" >>"$batch"
  local skipped=0
  if [ -f "$BANLIST" ]; then
    # Field 1 is PARSED as an address (see valid_ip4/valid_ip6 above), field 2
    # must be only digits. Parsing rather than character-filtering is what keeps
    # a malformed line from reaching nft, which would treat it as a hostname and
    # resolve it.
    while read -r ip exp _; do
      case "$ip" in ''|\#*) continue ;; esac
      case "$exp" in ''|*[!0-9]*) skipped=$((skipped + 1)); continue ;; esac
      local ttl=$((exp - now))
      [ "$ttl" -le 0 ] && continue
      [ "$ttl" -gt 86400 ] && ttl=86400
      case "$ip" in
        *:*)
          valid_ip6 "$ip" || { skipped=$((skipped + 1)); continue; }
          printf 'add element inet %s banned6 { %s timeout %ss }\n' "$DYN_TABLE" "$ip" "$ttl" >>"$batch"
          ;;
        *)
          valid_ip4 "$ip" || { skipped=$((skipped + 1)); continue; }
          printf 'add element inet %s banned4 { %s timeout %ss }\n' "$DYN_TABLE" "$ip" "$ttl" >>"$batch"
          ;;
      esac
      count=$((count + 1))
      printf '%s\n' "$ip" >>"$xdp_new"
    done <"$BANLIST"
  fi
  # Mirror into the XDP filter when the tooling is present (best-effort; drops
  # the source before the kernel network stack). Removals are honoured by
  # diffing against what we added last poll, so a pardon lifts the XDP drop too.
  if command -v xdp-filter >/dev/null 2>&1; then
    local xdp_prev="${CONTROL_DIR}/offload.xdp.state"
    while read -r ip; do
      [ -n "$ip" ] && xdp-filter ip "$ip" -m src >/dev/null 2>&1 || true
    done <"$xdp_new"
    if [ -f "$xdp_prev" ]; then
      while read -r old; do
        [ -n "$old" ] || continue
        grep -qxF "$old" "$xdp_new" || xdp-filter ip --remove "$old" -m src >/dev/null 2>&1 || true
      done <"$xdp_prev"
    fi
    cp -f "$xdp_new" "$xdp_prev" 2>/dev/null || true
  fi
  if [ -s "$batch" ]; then
    # Element adds are idempotent per element; a duplicate only refreshes its
    # timeout. Apply as one transaction; on failure surface the reason.
    if nft -f "$batch" >"${CONTROL_DIR}/offload.log" 2>&1; then
      write_state offload active
      clear_reason offload
    else
      # One transaction means one bad element takes the FLUSH down with it:
      # nothing is flushed, nothing is added, every stale ban stays in the kernel
      # and — the part that actually harms a person — every pardon fails to lift.
      # A visitor who solved the challenge stays banned until the batch happens
      # to become valid again, signalled only by offload.state=error.
      #
      # So fall back to applying the flush on its own and then each element
      # individually. One bad line then costs one ban, and pardons always lift.
      local rejected=0 applied=0 line
      if nft -f "$flushonly" >>"${CONTROL_DIR}/offload.log" 2>&1; then
        while IFS= read -r line; do
          case "$line" in "add element"*) ;; *) continue ;; esac
          if printf '%s\n' "$line" | nft -f - >>"${CONTROL_DIR}/offload.log" 2>&1; then
            applied=$((applied + 1))
          else
            rejected=$((rejected + 1))
          fi
        done <"$batch"
        count="$applied"
        if [ "$rejected" -gt 0 ]; then
          write_state offload degraded
          printf 'applied %s ban(s); %s rejected by nft. Pardons DID lift.' \
            "$applied" "$rejected" >"${CONTROL_DIR}/offload.reason" 2>/dev/null || true
        else
          write_state offload active
          clear_reason offload
        fi
      else
        # The flush itself failed, so the set is untouched and stale. This is the
        # only case where nothing at all could be reconciled.
        write_state offload error
        write_reason offload "${CONTROL_DIR}/offload.log"
      fi
    fi
  else
    write_state offload active
    clear_reason offload
  fi
  if [ "$skipped" -gt 0 ]; then
    # Not an error state: these lines never reached nft, which is the point. But
    # a banlist the app is writing and the agent is silently discarding is worth
    # a line in the log rather than nothing at all.
    printf 'skipped %s unparseable banlist line(s)\n' "$skipped" >>"${CONTROL_DIR}/offload.log" 2>/dev/null || true
  fi
  printf '%s' "$count" >"${CONTROL_DIR}/offload.count" 2>/dev/null || true
  rm -f "$batch" "$xdp_new" "$flushonly"
}

# reconcile_cdnallow — one-click population of the Tier 2 proxy allowlist.
#
# Behind a CDN, Tier 2's per-IP limits key on edge addresses because the kernel
# cannot see an HTTP header, so a busy edge node trips a per-visitor cap and its
# traffic is dropped with no log line anywhere. The remedy is a range allowlist,
# and asking an operator to run a fetch by hand is exactly the terminal step this
# agent exists to remove.
#
# The panel writes an EMPTY flag file; it never supplies a vendor name, a URL or
# any other content. The vendor is read from the flag's NAME (cdnallow.<vendor>)
# and matched against a fixed list here, so nothing the unprivileged web app can
# write ever reaches a command line. Anything unrecognised is refused outright.
reconcile_cdnallow() {
  local flag vendor
  for flag in "${CONTROL_DIR}"/cdnallow.*.want; do
    [ -e "$flag" ] || continue
    vendor="${flag##*/cdnallow.}"
    vendor="${vendor%.want}"
    # Fixed allowlist of vendors. Never interpolate the flag name into a command
    # without passing it through this gate first.
    case "$vendor" in
      cloudflare) ;;
      *)
        write_state cdnallow error
        printf 'unknown vendor %s' "$vendor" >"${CONTROL_DIR}/cdnallow.reason" 2>/dev/null || true
        rm -f "$flag" 2>/dev/null || true
        continue
        ;;
    esac
    write_state cdnallow applying
    if [ -f "$FIREWALL" ] && bash "$FIREWALL" cdn-allow "$vendor" >"${CONTROL_DIR}/cdnallow.log" 2>&1; then
      # Re-apply so the kernel picks the ranges up now rather than at next boot.
      # A failure here matters more than the fetch: the file is on disk but the
      # rules are stale, which looks like success from the panel.
      if bash "$FIREWALL" apply >>"${CONTROL_DIR}/cdnallow.log" 2>&1; then
        write_state cdnallow active
        clear_reason cdnallow
      else
        write_state cdnallow error
        write_reason cdnallow "${CONTROL_DIR}/cdnallow.log"
      fi
    else
      write_state cdnallow error
      write_reason cdnallow "${CONTROL_DIR}/cdnallow.log"
    fi
    # One-shot: the flag is intent to fetch ONCE, not a state to hold. Leaving it
    # would re-fetch on every poll and hammer the vendor's endpoint.
    rm -f "$flag" 2>/dev/null || true
  done
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
  local want=0 active=0 drift=0
  [ -f "${CONTROL_DIR}/tier3.want" ] && want=1
  [ -f "$NGINX_CONF_DST" ] && active=1

  # DRIFT: the installed conf differs from the vetted source, i.e. an upgrade
  # shipped a new one. Without this check nothing ever re-copies: "active" was
  # computed purely from the file EXISTING, and the copy lived only in the
  # want=1 && active=0 branch. So every fix to the Tier 3 conf reached exactly
  # zero already-enabled installs, silently — the agent kept reporting "active"
  # against whatever was installed the first time, however stale.
  #
  # That is not a hypothetical: the release that made Tier 3 stop reporting
  # "Active" while enforcing nothing changes this very file, and without drift
  # detection it would have landed only on installs that had never switched
  # Tier 3 on.
  if [ "$want" = 1 ] && [ "$active" = 1 ] && [ -f "$NGINX_CONF_SRC" ] \
     && ! cmp -s "$NGINX_CONF_SRC" "$NGINX_CONF_DST"; then
    drift=1
  fi

  if [ "$want" = 1 ] && { [ "$active" = 0 ] || [ "$drift" = 1 ]; }; then
    write_state tier3 applying
    # Keep the current conf so a failed validation restores exactly what was
    # working. The old code rm'd the destination on failure, which is right for
    # a first install (there was nothing before) and wrong for a drift re-copy
    # (it would delete a working config because a NEW one failed to parse).
    local backup=""
    if [ "$active" = 1 ]; then
      backup="${NGINX_CONF_DST}.vayushield-prev"
      cp -f "$NGINX_CONF_DST" "$backup" 2>/dev/null || backup=""
    fi
    if [ -f "$NGINX_CONF_SRC" ] && cp -f "$NGINX_CONF_SRC" "$NGINX_CONF_DST" >"${CONTROL_DIR}/tier3.log" 2>&1 && nginx -t >>"${CONTROL_DIR}/tier3.log" 2>&1 && systemctl reload nginx >>"${CONTROL_DIR}/tier3.log" 2>&1; then
      rm -f "$backup" 2>/dev/null || true
      write_state tier3 active
      clear_reason tier3
    else
      if [ -n "$backup" ] && [ -f "$backup" ]; then
        mv -f "$backup" "$NGINX_CONF_DST" 2>/dev/null || true
      else
        rm -f "$NGINX_CONF_DST" 2>/dev/null || true
      fi
      nginx -t >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true
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
      reconcile_cdnallow
      reconcile_tier2
      reconcile_tier3
      reconcile_banlist
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
