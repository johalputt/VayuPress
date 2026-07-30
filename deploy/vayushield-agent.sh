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

# mcp_catchall_is_open reads an nginx config dump on stdin and succeeds when the
# dedicated MCP host serves a catch-all that PROXIES to the app.
#
# It judges what the catch-all DOES, not that one exists. The earlier version
# flagged the mere presence of a catch-all location, so once the remediation had
# narrowed that block to "return 404" the panel went on reporting the host as
# unrestricted: the hardening row said Applied and the posture report said it had
# not been, leaving the operator to work out which panel was lying. A location
# that refuses IS the desired end state; only one that proxies is a second front
# door.
#
# It is a named function, not an inline pipeline, so the test suite runs THIS
# code against its fixtures instead of a copy that can silently drift from it.
#
# The server-block tracking is deliberately IDENTICAL to reconcile_mcpsurface's:
# inserver/ismcp opened on `server {`, closed on the matching brace. The two used
# to differ — the detector left the MCP host on a bare `}` at column zero, so a
# config that indents its server blocks kept ismcp set past the closing brace and
# charged the APEX vhost's proxying catch-all to the MCP host. A detector and the
# remediation it grades must not disagree about where the block ends.
mcp_catchall_is_open() {
  awk '
    /^[[:space:]]*server[[:space:]]*\{/ { inserver=1; ismcp=0 }
    inserver && /^[[:space:]]*server_name[[:space:]]+mcp\./ { ismcp=1 }
    ismcp && /^[[:space:]]*location[[:space:]]+\/[[:space:]]*\{/ {
      if ($0 ~ /\}/) { if ($0 ~ /proxy_pass/) { print "open"; exit } ; next }
      depth=1
      while (depth > 0 && (getline line) > 0) {
        if (line ~ /proxy_pass/) { print "open"; exit }
        n=gsub(/\{/,"{",line); m=gsub(/\}/,"}",line); depth += n - m
      }
      next
    }
    /^[[:space:]]*\}/ && inserver && !depth { inserver=0; ismcp=0 }
  ' | grep -q open
}

# mcp_narrow_config rewrites an MCP vhost on stdin and prints the result.
#
# It is the REMEDIATION half of the pair above, and it applies the same rule:
# judge what a catch-all does. Kept beside the detector deliberately — the two
# disagreeing is what produced a row that said Applied next to a report that said
# it had not been. See reconcile_mcpsurface for why each branch exists.
mcp_narrow_config() {
  awk '
    /^[[:space:]]*server[[:space:]]*\{/ { inserver=1; ismcp=0; is80=0; has443=0 }
    inserver && /^[[:space:]]*server_name[[:space:]]+mcp\./ { ismcp=1 }
    inserver && /^[[:space:]]*listen[[:space:]]/ {
      if ($0 ~ /443/ || $0 ~ /ssl/) { has443=1 }
      else if ($0 ~ /(^|[^0-9])80([^0-9]|$)/) { is80=1 }
    }
    ismcp && /^[[:space:]]*location[[:space:]]+\/[[:space:]]*\{/ {
      buf=$0
      if ($0 !~ /\}/) {
        depth=1
        while (depth > 0 && (getline line) > 0) {
          buf=buf "\n" line
          n=gsub(/\{/,"{",line); m=gsub(/\}/,"}",line); depth += n - m
        }
      }
      if (is80 && !has443 && buf ~ /narrowed by vayushield-agent/ && buf ~ /return[[:space:]]+404/) {
        print "    location / { return 301 https://$host$request_uri; }"
        next
      }
      if (buf ~ /proxy_pass/) {
        print "    location / { return 404; }  # narrowed by vayushield-agent"
        next
      }
      print buf
      next
    }
    /^[[:space:]]*\}/ && inserver && !depth { inserver=0; ismcp=0; is80=0; has443=0 }
    { print }
  '
}

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
      # PROXYING a bare location / there is a second front door to the whole app.
      if nginx -T 2>/dev/null | grep -Eq '^[[:space:]]*server_name[[:space:]]+mcp\.'; then
        if nginx -T 2>/dev/null | mcp_catchall_is_open; then
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


# AGENT_CAPS is what this build can do, written where the panel can read it.
#
# Without this the self-upgrade button is a trap: an older helper has no code
# that reads the request flag, so the panel would record an operator's click,
# nothing would ever act on it, and no status would ever appear. A control that
# silently does nothing is worse than one that is absent -- the operator waits,
# concludes the upgrade is slow, and eventually stops trusting the panel.
#
# Written on every start, so it also tracks correctly through a downgrade.
AGENT_CAPS="selfupgrade=1 digest=1 defaulthost=1 mcpsurface=1 cosignpin=1 rescue=1"

# AGENT_VERSION is READ from a file the release workflow stamps into the bundle,
# never typed into this script.
#
# The reason is a real dead end reached in the field. An operator pressed
# "Upgrade the helper", the page reported nothing new, and the posture report
# went on showing the same warning — and there was no way, from the panel or
# from the control dir, to tell whether the helper had upgraded and the finding
# was genuine, or the upgrade had silently not happened. AGENT_CAPS could not
# settle it: the capability string is identical across releases that change
# behaviour. An upgrade button whose effect cannot be observed is the same
# failure as a control that does nothing.
#
# Deriving it from the git tag at build time rather than adding a fourth file to
# bump by hand is deliberate: a version someone must remember to edit is a
# version that eventually lies, and a version that lies is worse than none. An
# agent installed from a checkout honestly reports "unknown".
agent_version() {
  local v=""
  v="$(cat "${LIB_DIR}/agent.version" 2>/dev/null)" || v=""
  v="$(printf '%s' "$v" | tr -cd '0-9A-Za-z.+-' | head -c 32)"
  [ -n "$v" ] || v="unknown"
  printf '%s' "$v"
}

write_caps() {
  [ -d "$CONTROL_DIR" ] || return 0
  printf '%s' "$AGENT_CAPS" >"${CONTROL_DIR}/agent.caps" 2>/dev/null || true
  printf '%s' "$(agent_version)" >"${CONTROL_DIR}/agent.version" 2>/dev/null || true
}

# ── Posture remediations ─────────────────────────────────────────────────────
#
# Two rows of the posture report used to be reported and not fixable: an
# operator was told what was wrong and then had to open a terminal, which is the
# thing this agent exists to make unnecessary.
#
# Both follow the same contract as every other action here. The app supplies ONE
# BIT — an empty flag file it owns as an unprivileged user. The agent decides
# what that means and writes the config itself, from text compiled into this
# root-owned script. Nothing the app can write becomes part of an nginx file.
#
# And both are reversible by construction: back up, apply, `nginx -t`, reload —
# and on ANY failure restore the backup and reload again, so a rejected config
# can never leave the edge down. The state file says which of those happened.

DEFAULT_HOST_CONF="${VAYUSHIELD_DEFAULT_HOST_DST:-/etc/nginx/conf.d/vayushield-default-server.conf}"

# nginx_try_reload validates and reloads, restoring $2 over $1 if either fails.
# Returns 0 only when the new config is live.
# NGINX_TRY_WHY carries nginx's OWN words about a rejection, for the panel.
#
# The first version of this discarded them with >/dev/null and reported only
# "nginx rejected the catch-all server". That is the same defect as the truncated
# cosign log: a row that says something failed but not why sends the operator to
# a terminal, which is the one thing this whole surface exists to avoid. nginx
# gives a precise, line-numbered reason; there is no excuse for withholding it.
NGINX_TRY_WHY=""

nginx_try_reload() { # $1=file just written, $2=backup path or "" if newly created
  local log
  log="$(mktemp)" || log=/dev/null
  if nginx -t >"$log" 2>&1 && systemctl reload nginx >>"$log" 2>&1; then
    [ "$log" = /dev/null ] || rm -f "$log"
    NGINX_TRY_WHY=""
    return 0
  fi
  # Capture BEFORE rolling back: once the file is restored, `nginx -t` passes and
  # the reason is gone.
  NGINX_TRY_WHY="$(grep -iE 'emerg|unknown directive|duplicate|not allowed|cannot|failed' "$log" 2>/dev/null | head -n 2 | tr '\n' ' ')"
  [ -n "$NGINX_TRY_WHY" ] || NGINX_TRY_WHY="$(tail -n 2 "$log" 2>/dev/null | tr '\n' ' ')"
  [ "$log" = /dev/null ] || rm -f "$log"
  if [ -n "$2" ] && [ -f "$2" ]; then
    cp -f "$2" "$1" 2>/dev/null || true
  else
    rm -f "$1" 2>/dev/null || true
  fi
  nginx -t >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true
  return 1
}

# nginx_has_reject_handshake reports whether this nginx understands
# ssl_reject_handshake (1.19.4+). The catch-all depends on it: it is what lets a
# TLS server block exist with NO certificate, which is the whole reason the
# catch-all needs no key material of its own. On an older nginx the directive is
# an "unknown directive" hard error, so checking first turns a rejected config
# into a sentence the operator can act on.
# nginx_first_certificate echoes "<cert> <key>" for the first pair this nginx
# already serves and can read, or nothing.
#
# It exists so the catch-all works on nginx older than 1.19.4. Without
# ssl_reject_handshake a TLS server block must present SOME certificate, and the
# alternative — telling an operator on a perfectly healthy LTS release to upgrade
# their web server — is not an answer, it is the problem restated.
#
# Borrowing a certificate the host already serves adds no key material and no new
# exposure: it is the same file nginx has open for another vhost. The client gets
# a name mismatch, which is exactly right for a request naming a host this
# install does not serve, and then 444 closes the connection.
nginx_first_certificate() {
  local dump certs keys i c k
  dump="$(nginx -T 2>/dev/null)" || return 1
  # The trailing space in the pattern matters: without it, ssl_certificate also
  # matches ssl_certificate_key and the two lists shift out of alignment.
  certs="$(printf '%s\n' "$dump" | sed -n 's/^[[:space:]]*ssl_certificate[[:space:]]\{1,\}\([^;]*\);.*/\1/p' | tr -d '"'"'"'')"
  keys="$(printf '%s\n' "$dump" | sed -n 's/^[[:space:]]*ssl_certificate_key[[:space:]]\{1,\}\([^;]*\);.*/\1/p' | tr -d '"'"'"'')"
  i=1
  while [ "$i" -le 50 ]; do
    c="$(printf '%s\n' "$certs" | sed -n "${i}p")"
    k="$(printf '%s\n' "$keys" | sed -n "${i}p")"
    [ -n "$c" ] && [ -n "$k" ] || return 1
    if [ -r "$c" ] && [ -r "$k" ]; then
      printf '%s %s' "$c" "$k"
      return 0
    fi
    i=$((i + 1))
  done
  return 1
}

nginx_has_reject_handshake() {
  local v
  v="$(nginx -v 2>&1 | sed -n 's#.*nginx/\([0-9][0-9.]*\).*#\1#p')"
  [ -n "$v" ] || return 1
  awk -v v="$v" 'BEGIN{
    n=split(v,a,".");
    maj=a[1]+0; min=(n>1?a[2]+0:0); pat=(n>2?a[3]+0:0);
    if (maj>1) exit 0;
    if (maj==1 && min>19) exit 0;
    if (maj==1 && min==19 && pat>=4) exit 0;
    exit 1}'
}

# reconcile_defaulthost installs a catch-all 443 server so a request carrying a
# Host this install does not serve is refused, instead of being handed to
# whichever vhost happens to be listed first.
#
# 444 (nginx's "close without a response") rather than a redirect or an error
# page: an unknown Host is either a direct-to-IP scan or a misconfiguration, and
# neither deserves a reply that confirms something is listening.
reconcile_defaulthost() {
  [ -f "${CONTROL_DIR}/defaulthost.want" ] || return 0
  command -v nginx >/dev/null 2>&1 || {
    write_state defaulthost error
    printf '%s' "nginx is not installed on this host" >"${CONTROL_DIR}/defaulthost.reason" 2>/dev/null || true
    rm -f "${CONTROL_DIR}/defaulthost.want" 2>/dev/null || true
    return 0
  }
  # Already satisfied — by us or by the operator's own config. Either way there
  # is nothing to do, and adding a second default_server would be a hard error.
  if nginx -T 2>/dev/null | grep -Eq 'listen[^;]*443[^;]*default_server'; then
    write_state defaulthost active
    clear_reason defaulthost
    rm -f "${CONTROL_DIR}/defaulthost.want" 2>/dev/null || true
    return 0
  fi
  # Two shapes of the same server block. ssl_reject_handshake (nginx 1.19.4+) is
  # cleaner — no certificate at all — but an operator on an LTS nginx should not
  # be told to upgrade their web server to close a scanning surface, so where the
  # directive is missing the block borrows a certificate the host already serves.
  local tls_stanza=""
  if nginx_has_reject_handshake; then
    tls_stanza="    ssl_reject_handshake on;"
  else
    local pair cert key
    pair="$(nginx_first_certificate)" || pair=""
    if [ -z "$pair" ]; then
      write_state defaulthost error
      printf '%s' "This nginx ($(nginx -v 2>&1 | sed -n 's#.*nginx/##p')) predates ssl_reject_handshake (1.19.4), so the catch-all needs a certificate — and no readable certificate was found in the running config to borrow. Nothing was changed." \
        >"${CONTROL_DIR}/defaulthost.reason" 2>/dev/null || true
      rm -f "${CONTROL_DIR}/defaulthost.want" 2>/dev/null || true
      return 0
    fi
    cert="${pair%% *}"
    key="${pair##* }"
    tls_stanza="    ssl_certificate ${cert};
    ssl_certificate_key ${key};"
  fi
  write_state defaulthost applying
  local bak=""
  if [ -f "$DEFAULT_HOST_CONF" ]; then
    bak="${DEFAULT_HOST_CONF}.vayushield.bak"
    cp -f "$DEFAULT_HOST_CONF" "$bak" 2>/dev/null || true
  fi
  # The v6 listener is emitted only on a host that has IPv6. On a v4-only box
  # `listen [::]:443` makes nginx -t fail outright with EAFNOSUPPORT, so writing
  # it unconditionally would turn this button into a guaranteed rollback and an
  # error the operator cannot act on. Where IPv6 exists it is required, not
  # optional: a default_server bound only to v4 leaves an unknown-Host request
  # over v6 landing on the first vhost, which is the whole problem.
  local v6=""
  if [ -f /proc/net/if_inet6 ]; then
    v6="    listen [::]:443 ssl default_server;"
  fi
  cat >"$DEFAULT_HOST_CONF" <<NGINX_DEFAULT_HOST
# Written by vayushield-agent. A request whose Host header names no vhost this
# install serves lands here and is closed without a response (444). Without it,
# nginx hands such a request to the first server block that happens to match,
# which is rarely the one intended and leaks whichever site that is to a scan.
server {
    listen 443 ssl default_server;
${v6}
    server_name _;
${tls_stanza}
    return 444;
}
NGINX_DEFAULT_HOST
  if nginx_try_reload "$DEFAULT_HOST_CONF" "$bak"; then
    write_state defaulthost active
    clear_reason defaulthost
    # Refresh the enforcement digest NOW rather than waiting for the periodic
    # rebuild. The posture report reads that digest, and it is rebuilt about once
    # a minute because it shells out to nft and `nginx -T`. So an operator who
    # pressed a fix, saw it report Applied, and then looked at the posture report
    # was shown the PREVIOUS state for up to a minute — with nothing on the page
    # saying the two panels disagreed because one of them was stale. The obvious
    # reading is that the fix did not work.
    write_digest
  else
    write_state defaulthost error
    printf '%s' "nginx rejected the catch-all server; the previous config was restored. ${NGINX_TRY_WHY:0:240}" \
      >"${CONTROL_DIR}/defaulthost.reason" 2>/dev/null || true
  fi
  rm -f "${CONTROL_DIR}/defaulthost.want" 2>/dev/null || true
}

# reconcile_mcpsurface narrows the dedicated MCP host to the endpoints it needs.
#
# A bare `location /` in that server block is a second front door to the whole
# application on a hostname whose entire purpose is to expose one endpoint. The
# fix rewrites that block to `return 404` rather than deleting it, because a
# server block with no `location /` still falls through to nginx's default
# handling — being explicit is what makes the posture row verifiable.
reconcile_mcpsurface() {
  [ -f "${CONTROL_DIR}/mcpsurface.want" ] || return 0
  command -v nginx >/dev/null 2>&1 || {
    write_state mcpsurface error
    printf '%s' "nginx is not installed on this host" >"${CONTROL_DIR}/mcpsurface.reason" 2>/dev/null || true
    rm -f "${CONTROL_DIR}/mcpsurface.want" 2>/dev/null || true
    return 0
  }
  # Locate the file that declares the MCP vhost. -l on the real config tree, not
  # nginx -T, because -T prints the merged config and gives no path to edit.
  local f found=""
  for f in /etc/nginx/conf.d/*.conf /etc/nginx/sites-enabled/*; do
    [ -f "$f" ] || continue
    if grep -Eq '^[[:space:]]*server_name[[:space:]]+mcp\.' "$f"; then found="$f"; break; fi
  done
  if [ -z "$found" ]; then
    write_state mcpsurface error
    printf '%s' "no MCP vhost found; nothing to narrow" >"${CONTROL_DIR}/mcpsurface.reason" 2>/dev/null || true
    rm -f "${CONTROL_DIR}/mcpsurface.want" 2>/dev/null || true
    return 0
  fi
  write_state mcpsurface applying
  local bak="${found}.vayushield.bak"
  cp -f "$found" "$bak" 2>/dev/null || true
  # Rewrite only a bare `location / { ... }` on ONE line or spanning lines, and
  # only inside a server block whose server_name starts with mcp. — awk tracks
  # both so a `location /` belonging to the apex vhost in the same file is left
  # alone. That containment is the whole reason this is not a sed one-liner.
  #
  # It narrows a catch-all that PROXIES, and nothing else. The first version
  # rewrote every catch-all it found on the host, which was wrong twice over:
  #
  #   • The :443 block written by setup-mcp-subdomain.sh already ends in
  #     `location / { return 404; }`. Rewriting a refusal into the same refusal
  #     is noise, and it made "Applied" mean nothing.
  #   • The :80 block redirects to HTTPS — `location / { return 301 … }`. That
  #     is not a front door to the app, it is how a plain-HTTP client reaches
  #     the secure one, and replacing it with `return 404` broke the redirect on
  #     every install that pressed this button. (Certificate renewal survived it:
  #     the ACME location carries `^~`, which nginx prefers over a plain `/`.)
  #
  # So the same rule the detector uses applies here — judge what the block DOES.
  # A catch-all with no proxy_pass is passed through untouched.
  #
  # The 301 branch REPAIRS that damage, and is deliberately narrow: it fires only
  # on a line carrying this agent's own marker comment, only when it refuses, and
  # only inside a plain-:80 block. It restores nothing it did not itself write.
  mcp_narrow_config <"$bak" >"$found" 2>/dev/null || cp -f "$bak" "$found" 2>/dev/null
  if nginx_try_reload "$found" "$bak"; then
    write_state mcpsurface active
    clear_reason mcpsurface
    # Refresh the enforcement digest NOW rather than waiting for the periodic
    # rebuild. The posture report reads that digest, and it is rebuilt about once
    # a minute because it shells out to nft and `nginx -T`. So an operator who
    # pressed a fix, saw it report Applied, and then looked at the posture report
    # was shown the PREVIOUS state for up to a minute — with nothing on the page
    # saying the two panels disagreed because one of them was stale. The obvious
    # reading is that the fix did not work.
    write_digest
  else
    write_state mcpsurface error
    printf '%s' "nginx rejected the narrowed MCP vhost; the previous config was restored. ${NGINX_TRY_WHY:0:240}" \
      >"${CONTROL_DIR}/mcpsurface.reason" 2>/dev/null || true
  fi
  rm -f "${CONTROL_DIR}/mcpsurface.want" 2>/dev/null || true
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

# ── cosign bootstrap ─────────────────────────────────────────────────────────
#
# Verification used to require an operator with a shell. cosign is a single
# static binary, and refusing to act without it is correct — but "correct and
# stuck" is how one permission bug turned into an afternoon of terminal
# commands for something this panel exists to avoid.
#
# So the agent fetches cosign itself, and checks it against a checksum PINNED AT
# RELEASE BUILD TIME. The pin travels inside this bundle, which is itself
# cosign-signed, so the chain closes: a verified agent carries the fingerprint of
# the tool that verifies the next one. CI does the pinning because CI has the
# network and is a trusted build environment; hand-maintaining a hash means a
# stale pin that silently stops working.
#
# What this deliberately does NOT do is fall back to an unverified cosign, or to
# a plain checksum of the agent bundle. A checksum published beside the artifact
# it describes catches corruption, not substitution — and the panel would still
# say "signature verified", which would be a lie.
COSIGN_PIN="${LIB_DIR}/cosign.pin"
COSIGN="cosign"
COSIGN_BOOTSTRAP_WHY=""

ensure_cosign() {
  if command -v cosign >/dev/null 2>&1; then COSIGN="cosign"; return 0; fi
  if [ -x "${LIB_DIR}/cosign" ]; then COSIGN="${LIB_DIR}/cosign"; return 0; fi
  if [ ! -r "$COSIGN_PIN" ]; then
    COSIGN_BOOTSTRAP_WHY="This helper carries no pinned cosign checksum, so it will not download one unverified. Upgrade the helper from a release that pins it, or install cosign (https://github.com/sigstore/cosign/releases)."
    return 1
  fi
  local arch pin_ver pin_sum asset
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) COSIGN_BOOTSTRAP_WHY="No cosign pin for this CPU architecture ($(uname -m))."; return 1 ;;
  esac
  # Format, one key=value per line: version=v2.x.y / amd64=<sha256> / arm64=<sha256>
  pin_ver="$(awk -F= '$1=="version"{print $2}' "$COSIGN_PIN" 2>/dev/null | tr -d '[:space:]')"
  pin_sum="$(awk -F= -v a="$arch" '$1==a{print $2}' "$COSIGN_PIN" 2>/dev/null | tr -d '[:space:]')"
  if [ -z "$pin_ver" ] || [ -z "$pin_sum" ]; then
    COSIGN_BOOTSTRAP_WHY="The pinned cosign checksum is unreadable, so nothing was downloaded."
    return 1
  fi
  asset="cosign-linux-${arch}"
  local tmp
  tmp="$(mktemp -d)" || { COSIGN_BOOTSTRAP_WHY="Could not create a working directory."; return 1; }
  if ! curl -fsSL --max-time 180 -o "${tmp}/cosign" \
      "https://github.com/sigstore/cosign/releases/download/${pin_ver}/${asset}"; then
    rm -rf "$tmp"
    COSIGN_BOOTSTRAP_WHY="Could not download cosign ${pin_ver} for ${arch}; check egress to github.com."
    return 1
  fi
  local got
  got="$(sha256sum "${tmp}/cosign" 2>/dev/null | awk '{print $1}')"
  if [ "$got" != "$pin_sum" ]; then
    rm -rf "$tmp"
    # Not a download hiccup. The bytes GitHub served do not match what this
    # project's release workflow recorded, and installing them would defeat the
    # verification they exist to perform.
    COSIGN_BOOTSTRAP_WHY="The downloaded cosign does not match the pinned checksum — refusing to use it. Expected ${pin_sum:0:16}…, got ${got:0:16}…"
    return 1
  fi
  install -m 0755 "${tmp}/cosign" "${LIB_DIR}/cosign" 2>/dev/null || {
    rm -rf "$tmp"; COSIGN_BOOTSTRAP_WHY="Verified cosign, but could not install it into ${LIB_DIR}."; return 1; }
  rm -rf "$tmp"
  COSIGN="${LIB_DIR}/cosign"
  return 0
}

self_upgrade() {
  if ! command -v curl >/dev/null 2>&1; then
    upgrade_status "error" "curl is not installed, so the helper cannot fetch its own upgrade."
    return 1
  fi
  if ! ensure_cosign; then
    # Named as a refusal with a fix, not as a failure. Verification is the one
    # thing this path will not skip: what the bundle contains runs as root.
    upgrade_status "unverifiable" \
      "cosign is not available and could not be bootstrapped, so the bundle's signature cannot be checked — refusing to install unverified code as root. ${COSIGN_BOOTSTRAP_WHY}"
    return 1
  fi

  # Give cosign somewhere to write before it needs it. The unit sets these too,
  # but an agent running under an older unit file inherits HOME=/root while
  # ProtectHome=yes masks it — and cosign, unable to cache the Sigstore TUF trust
  # root, then fails with an error that reads like a network fault. Setting it
  # here means the script is correct on its own, without depending on which
  # version of the unit happens to be installed.
  # Written with := defaults rather than an if/else. The first version guarded
  # with `[ -z "${TUF_ROOT:-}" ] || [ ! -w "${HOME:-/root}" ]` and then used
  # "$HOME" unguarded — so a set TUF_ROOT plus an UNSET HOME skipped the branch
  # and hit an unbound variable, which under `set -u` does not fail the upgrade,
  # it kills the agent, and Restart=always then loops it. systemd always sets
  # HOME for User=root, so it would not have fired here; a script that survives
  # only because of its caller's environment is not one to leave in place.
  : "${HOME:=/var/lib/vayushield}"
  [ -w "$HOME" ] || HOME=/var/lib/vayushield
  : "${TUF_ROOT:=${HOME}/tuf}"
  export HOME TUF_ROOT
  mkdir -p "$HOME" "$TUF_ROOT" 2>/dev/null || true

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
  if ! "$COSIGN" verify-blob "${tmp}/${UPGRADE_ASSET}" \
        --bundle "${tmp}/bundle.json" \
        --certificate-identity-regexp "^https://github.com/${UPGRADE_REPO}/" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        >"${tmp}/verify.log" 2>&1; then
    # WHY it failed decides what to tell the operator, and the two answers are
    # opposites. Keyless verification needs Sigstore's TUF trust root and the
    # transparency log, so a host that can reach GitHub but not sigstore.dev --
    # a restrictive egress firewall, an outage, a Tor Space -- fails here having
    # determined NOTHING about the signature.
    #
    # Reporting that as "the bundle was not signed by this project" is a false
    # accusation of an attack, and it is the exact defect class this project's
    # posture report exists to avoid: a claim that overstates what was actually
    # established. An operator told they are under attack acts very differently
    # from one told their firewall blocks a CDN.
    local why
    why="$(tail -n 3 "${tmp}/verify.log" 2>/dev/null | tr '\n' ' ')"
    if printf '%s' "$why" | grep -qiE 'tuf|rekor|sigstore\.dev|timeout|dial|no such host|connection refused|forbidden|network'; then
      upgrade_status "unverifiable" \
        "Could not REACH the signature infrastructure, so the bundle was neither proved nor disproved — nothing was installed. Keyless verification needs sigstore.dev as well as GitHub; check egress from this host. ${why:0:160}"
      return 1
    fi
    upgrade_status "error" "Signature verification FAILED — the bundle is not signed by this project's release workflow. Nothing was installed. ${why:0:160}"
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
  # Optional by design: absent from bundles older than the release that added
  # them, and a missing extra must never fail an upgrade that is otherwise fine.
  #
  # The rescue units were being DROPPED here. install_agent laid them down, this
  # path did not, so every helper that reached its current version by pressing
  # "Upgrade the helper" — the path the panel steers everyone to — ended up
  # without the watcher that repairs a broken agent. That is the deadlock the
  # rescue path exists to break, quietly absent on exactly the installs most
  # likely to need it.
  [ -f "${tmp}/x/agent.version" ] && install -m 0644 "${tmp}/x/agent.version" "${LIB_DIR}/agent.version"
  for f in vayushield-rescue.path vayushield-rescue.service; do
    [ -f "${tmp}/x/${f}" ] && install -m 0644 "${tmp}/x/${f}" "/etc/systemd/system/${f}"
  done
  systemctl enable --now vayushield-rescue.path >/dev/null 2>&1 || true
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
  write_caps
  resolve_pending_upgrade
  local ticks=0
  while true; do
    if [ -d "$CONTROL_DIR" ]; then
      printf '%s' "$(date -u +%s)" >"${CONTROL_DIR}/agent.alive" 2>/dev/null || true
      write_caps
      reconcile_upgrade
      reconcile_cdnallow
      reconcile_tier2
      reconcile_tier3
      reconcile_defaulthost
      reconcile_mcpsurface
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
  # The cosign pin travels with the agent: without it the next self-upgrade has
  # no verified way to obtain the tool that verifies it.
  if [ -f "${src}/cosign.pin" ]; then
    install -m 0644 "${src}/cosign.pin" "${LIB_DIR}/cosign.pin"
  fi
  # Stamped by the release workflow from the git tag; absent in a checkout, where
  # the agent then reports its version as "unknown" rather than inventing one.
  if [ -f "${src}/agent.version" ]; then
    install -m 0644 "${src}/agent.version" "${LIB_DIR}/agent.version"
  fi
  install -m 0644 "${src}/vayushield-agent.service" /etc/systemd/system/vayushield-agent.service
  # The rescue path: a root-side watcher that can repair this agent WITHOUT this
  # agent. Installed here rather than only by the updater, because the install
  # that most needs it is one whose helper is already broken.
  if [ -f "${src}/vayushield-rescue.path" ] && [ -f "${src}/vayushield-rescue.service" ]; then
    install -m 0644 "${src}/vayushield-rescue.service" /etc/systemd/system/vayushield-rescue.service
    install -m 0644 "${src}/vayushield-rescue.path"    /etc/systemd/system/vayushield-rescue.path
    systemctl daemon-reload
    systemctl enable --now vayushield-rescue.path 2>/dev/null || true
  fi
  systemctl daemon-reload
  systemctl enable vayushield-agent
  # restart, NOT `enable --now`.
  #
  # `--now` starts a unit that is stopped and does nothing at all to one that is
  # already running. So re-installing over a running agent copied the new script
  # to disk while the OLD process carried on executing the old one from memory —
  # and printed "installed and started" while nothing whatsoever had changed.
  #
  # That is the worst shape a bug can take in an installer: repeated, confident
  # success with no effect. An operator upgrading a stale helper ran this three
  # times, saw three successes, and still had the stale helper. bash also reads
  # scripts incrementally, so leaving the old process running on a replaced file
  # is not merely stale, it is undefined.
  systemctl restart vayushield-agent
  echo "✓ VayuShield agent installed and (re)started."
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
