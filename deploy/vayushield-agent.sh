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

# The Tier 2 table and kernel drop-in, named here so the enforcement digest can
# observe the same objects vayushield-firewall.sh creates. Kept in sync with that
# script's TABLE and SYSCTL_CONF; a test pins the pair.
TABLE_MAIN="${VAYUSHIELD_TABLE:-vayushield}"
SYSCTL_CONF_PATH="${VAYUSHIELD_SYSCTL_CONF:-/etc/sysctl.d/99-vayushield.conf}"
# Digest refreshes every N polls. It shells out to nft and `nginx -T`, which is
# far too expensive to do at the reconcile cadence.
DIGEST_EVERY="${VAYUSHIELD_DIGEST_EVERY:-12}"

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

# ── Enforcement digest ────────────────────────────────────────────────────────
# A fixed-schema file recording what the agent OBSERVES to be in force, as
# distinct from what it was asked to do.
#
# This is a new agent -> app data direction and it exists because the app cannot
# get this information any other way: it runs unprivileged (ADR-0123), so
# `nft list table` needs a capability it does not have and /etc/nginx is none of
# its business. Until now the app could only read back the agent's own report of
# what it did, which is intent one step removed — and intent is exactly what was
# wrong when Tier 3 declared its rate-limit zones, applied none of them, and
# reported "Active" for as long as nobody looked.
#
# Schema rules, because a root process writes this and an unprivileged one parses
# it: one `key=value` per line, values drawn from a fixed vocabulary, and an
# OMITTED key means "not observed" — never "no". A newer app reading an older
# agent's digest must degrade to "unverified", not to "broken".
DIGEST="${CONTROL_DIR}/enforcement.digest"
DIGEST_SCHEMA=1

# tri <command...> — echo yes when the command succeeds, no when it fails.
tri() { if "$@" >/dev/null 2>&1; then printf 'yes'; else printf 'no'; fi; }

write_digest() {
  [ -d "$CONTROL_DIR" ] || return 0
  local tmp
  tmp="$(mktemp)" || return 0

  {
    printf 'schema=%s\n' "$DIGEST_SCHEMA"
    printf 'generated=%s\n' "$(date -u +%s)"

    # Tier 2 — read the live ruleset rather than the file that was loaded from.
    if command -v nft >/dev/null 2>&1; then
      local rules=""
      rules="$(nft list table inet "${TABLE_MAIN}" 2>/dev/null || true)"
      if [ -n "$rules" ]; then
        printf 'tier2_table=yes\n'
        # The v4/v6 split is checked separately on purpose. In the inet family an
        # `ip saddr` match never matches an IPv6 packet, so a ruleset with only
        # v4 meters leaves IPv6 either unlimited or dropped wholesale — and looks
        # perfectly healthy from an IPv4 client.
        printf 'tier2_meters_v4=%s\n' "$(printf '%s' "$rules" | grep -q 'meter vs_conn4' && echo yes || echo no)"
        printf 'tier2_meters_v6=%s\n' "$(printf '%s' "$rules" | grep -q 'meter vs_conn6' && echo yes || echo no)"
      else
        printf 'tier2_table=no\n'
      fi
      # Conntrack sizing is read BACK from the kernel, not from the drop-in that
      # asked for it. Those two disagreed on every fresh boot.
      local want got
      want="$(grep -E '^net\.netfilter\.nf_conntrack_max' "$SYSCTL_CONF_PATH" 2>/dev/null | tail -1 | tr -d '[:space:]')"
      want="${want#*=}"
      got="$(sysctl -n net.netfilter.nf_conntrack_max 2>/dev/null | tr -d '[:space:]')" || got=""
      if [ -n "$want" ] && [ -n "$got" ]; then
        printf 'conntrack_sized=%s\n' "$([ "$got" = "$want" ] && echo yes || echo no)"
      fi
    fi

    # Tier 3 — the contents of the INSTALLED file, not its existence. This is the
    # distinction the whole digest was added for.
    if [ -f "$NGINX_CONF_DST" ]; then
      printf 'tier3_installed=yes\n'
      # A declared limit_req_zone enforces nothing; a limit_req that is not
      # commented out does. Match on the applying directive only.
      if grep -Eq '^[[:space:]]*limit_(req|conn)[[:space:]]+zone=' "$NGINX_CONF_DST"; then
        printf 'tier3_enforcing=yes\n'
      else
        printf 'tier3_enforcing=no\n'
      fi
    else
      printf 'tier3_installed=no\n'
      printf 'tier3_enforcing=no\n'
    fi

    if command -v nginx >/dev/null 2>&1; then
      if nginx -T 2>/dev/null | grep -Eq 'listen[^;]*443[^;]*default_server'; then
        printf 'default_server_443=yes\n'
      else
        printf 'default_server_443=no\n'
      fi
      # The dedicated MCP host should expose only /mcp and /health. Anything
      # serving a bare location / there is a second front door to the whole app.
      if nginx -T 2>/dev/null | grep -q 'server_name[[:space:]]*mcp\.'; then
        if nginx -T 2>/dev/null | awk '/server_name[[:space:]]*mcp\./,/^}/' | grep -Eq '^[[:space:]]*location[[:space:]]+/[[:space:]]*\{'; then
          printf 'mcp_vhost_restricted=no\n'
        else
          printf 'mcp_vhost_restricted=yes\n'
        fi
      fi
    fi
  } >"$tmp" 2>/dev/null

  # Replace atomically: the app polls this and must never read a half-written
  # file as a set of "no" answers.
  install -m 0644 "$tmp" "$DIGEST" 2>/dev/null || cp -f "$tmp" "$DIGEST" 2>/dev/null || true
  rm -f "$tmp" 2>/dev/null || true
}


# ── Self-upgrade ─────────────────────────────────────────────────────────────
#
# The one operation that had to be a terminal command, and no longer is.
#
# The constraint that shapes all of this: the agent runs as ROOT, and the panel
# that asks for the upgrade is an UNPRIVILEGED web app. An unprivileged process
# able to choose what a root process executes is a complete privilege
# escalation — it is precisely the separation ADR-0123 exists to create. So the
# app is never allowed to supply the code, a URL, a version, or a path.
#
# What the app supplies is ONE BIT: an empty file named agent.upgrade.want. This
# script decides everything else. The repository is hardcoded below, the asset
# name is hardcoded, and the bundle is verified before a single byte of it is
# executed.
#
# Verification is cosign, matching how every other release artefact in this
# project is signed. When cosign is not installed the upgrade is REFUSED rather
# than downgraded silently — an operator who believes their root helper only
# accepts signed code, and is actually accepting anything served over TLS, is
# worse off than one who was told to install cosign. The refusal names the fix.
UPGRADE_REPO="${VAYUSHIELD_UPGRADE_REPO:-johalputt/VayuPress}"
UPGRADE_ASSET="vayushield-agent.tar.gz"

upgrade_status() { # $1=state word, $2=detail
  [ -d "$CONTROL_DIR" ] || return 0
  printf '%s' "$1" >"${CONTROL_DIR}/agent.upgrade.state" 2>/dev/null || true
  printf '%s' "${2:0:300}" >"${CONTROL_DIR}/agent.upgrade.detail" 2>/dev/null || true
}

# reconcile_upgrade acts on the operator's request, exactly once per request.
#
# The flag is removed BEFORE the work starts, not after. A crash mid-upgrade
# would otherwise leave the flag in place and the agent would retry it on every
# poll for as long as it kept failing — a five-second loop downloading and
# re-running an installer, which is a denial of service the operator asked for
# by accident.
reconcile_upgrade() {
  local flag="${CONTROL_DIR}/agent.upgrade.want"
  [ -f "$flag" ] || return 0
  rm -f "$flag" 2>/dev/null || true
  upgrade_status "checking" "Looking for a newer signed helper bundle."
  self_upgrade
}

self_upgrade() {
  if ! command -v curl >/dev/null 2>&1; then
    upgrade_status "error" "curl is not installed, so the helper cannot fetch its own upgrade."
    return 1
  fi
  if ! command -v cosign >/dev/null 2>&1; then
    # Named as a refusal with a fix, not as a failure. This is the one dependency
    # the design genuinely needs, and it is a single static binary.
    upgrade_status "unverifiable" \
      "cosign is not installed, so the bundle's signature cannot be checked — refusing to install unverified code as root. Install cosign (https://github.com/sigstore/cosign/releases) and press the button again."
    return 1
  fi

  local tmp
  tmp="$(mktemp -d)" || { upgrade_status "error" "Could not create a working directory."; return 1; }
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN

  local base="https://github.com/${UPGRADE_REPO}/releases/latest/download"
  if ! curl -fsSL --max-time 120 -o "${tmp}/${UPGRADE_ASSET}" "${base}/${UPGRADE_ASSET}"; then
    upgrade_status "error" "Could not download the helper bundle from the release."
    return 1
  fi
  if ! curl -fsSL --max-time 60 -o "${tmp}/bundle.json" "${base}/${UPGRADE_ASSET}.cosign.bundle"; then
    upgrade_status "error" "The release carries no signature bundle for the helper — refusing to install it."
    return 1
  fi

  # Verify BEFORE unpacking. Unpacking an unverified archive as root is already
  # the compromise, whatever is checked afterwards.
  if ! cosign verify-blob "${tmp}/${UPGRADE_ASSET}" \
        --bundle "${tmp}/bundle.json" \
        --certificate-identity-regexp "^https://github.com/${UPGRADE_REPO}/" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        >"${tmp}/verify.log" 2>&1; then
    upgrade_status "error" "Signature verification FAILED — the bundle was not signed by this project's release workflow. Nothing was installed. $(tail -n 1 "${tmp}/verify.log" 2>/dev/null)"
    return 1
  fi

  mkdir -p "${tmp}/x"
  if ! tar -xzf "${tmp}/${UPGRADE_ASSET}" -C "${tmp}/x" 2>/dev/null; then
    upgrade_status "error" "The verified bundle could not be unpacked."
    return 1
  fi
  # Install only the four files this agent is made of, by name. Extracting a
  # verified archive still does not mean copying whatever it happens to contain.
  local f
  for f in vayushield-agent.sh vayushield-firewall.sh vayushield-agent.service nginx-vayushield.conf; do
    if [ ! -f "${tmp}/x/${f}" ]; then
      upgrade_status "error" "The bundle is missing ${f} — refusing a partial install."
      return 1
    fi
  done

  install -d -m 0755 "$LIB_DIR"
  install -m 0755 "${tmp}/x/vayushield-firewall.sh" "${LIB_DIR}/vayushield-firewall.sh"
  install -m 0644 "${tmp}/x/nginx-vayushield.conf"  "${LIB_DIR}/nginx-vayushield.conf"
  install -m 0644 "${tmp}/x/vayushield-agent.service" /etc/systemd/system/vayushield-agent.service
  # The running script is replaced last. bash reads a script incrementally, so
  # overwriting it in place under a live interpreter can execute a spliced
  # half-old, half-new file; install(1) writes to a temp file and renames, which
  # leaves the running process on the old inode until it restarts.
  install -m 0755 "${tmp}/x/vayushield-agent.sh" "${LIB_DIR}/vayushield-agent.sh"

  upgrade_status "restarting" "Signature verified. Installed and restarting the helper."
  systemctl daemon-reload 2>/dev/null || true
  # --no-block matters. `systemctl restart` on the unit you are RUNNING INSIDE
  # waits for the job to finish, and the job cannot finish until this process
  # exits — systemd is waiting for the script and the script is waiting for
  # systemd. --no-block queues the job and returns, so the stop signal arrives
  # normally and the restart completes.
  #
  # The status above is written first because nothing after this line is
  # guaranteed to run.
  systemctl restart --no-block vayushield-agent 2>/dev/null || true
  return 0
}

# resolve_pending_upgrade closes out an upgrade whose last act was to kill the
# process that was reporting on it.
#
# self_upgrade writes "restarting" and then restarts the unit, so the process
# that would have written the final state no longer exists. Without this the
# panel shows "installing and restarting" forever — a permanent in-progress
# spinner over an operation that finished successfully, which is worse than no
# status at all because it reads as a hang.
#
# Reaching this line IS the completion: it only runs in a freshly started agent.
resolve_pending_upgrade() {
  [ -d "$CONTROL_DIR" ] || return 0
  local st=""
  [ -f "${CONTROL_DIR}/agent.upgrade.state" ] && st="$(cat "${CONTROL_DIR}/agent.upgrade.state" 2>/dev/null)"
  [ "$st" = "restarting" ] || return 0
  upgrade_status "done" "Upgraded and running. The helper restarted into the new build."
}

run_agent() {
  echo "vayushield-agent: watching ${CONTROL_DIR} (poll ${POLL}s)"
  resolve_pending_upgrade
  local ticks=0
  while true; do
    if [ -d "$CONTROL_DIR" ]; then
      printf '%s' "$(date -u +%s)" >"${CONTROL_DIR}/agent.alive" 2>/dev/null || true
      reconcile_upgrade
      reconcile_cdnallow
      reconcile_tier2
      reconcile_tier3
      reconcile_banlist
      # The digest shells out to nft and nginx -T, which is far too expensive for
      # a 5-second poll. Refresh it about once a minute and on the first tick, so
      # a freshly-started agent does not leave the panel unverified for a minute.
      if [ "$ticks" = 0 ] || [ $((ticks % DIGEST_EVERY)) = 0 ]; then
        write_digest
      fi
      ticks=$((ticks + 1))
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
  selfupdate) self_upgrade ;;
  uninstall) uninstall_agent ;;
  status) systemctl status vayushield-agent --no-pager 2>/dev/null || echo "agent not installed" ;;
  *) echo "usage: $0 [run|install|selfupdate|uninstall|status]" >&2; exit 2 ;;
esac
