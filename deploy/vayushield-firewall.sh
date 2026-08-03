#!/usr/bin/env bash
#
# vayushield-firewall.sh — Tier 2 (kernel / OS) DDoS hardening for a sovereign
# VayuPress VPS. This is the layer VayuShield's in-binary defenses (Tier 1)
# cannot cover: SYN floods and connection/packet floods are dropped by the
# kernel before they ever reach the Go process.
#
# It is idempotent (safe to re-run), pure nftables + sysctl (no third-party
# service, nothing leaves the host), and conservative by default so it does not
# lock out a legitimate visitor. Review the rates for your traffic before use.
#
# Usage (as root):   bash deploy/vayushield-firewall.sh [apply|status|remove]
#
# NOTE: run this only if VayuPress terminates traffic directly on this host. If
# you front it with the nginx edge config (deploy/nginx-vayushield.conf), apply
# both — kernel drops volumetric floods, nginx shapes L7, VayuShield classifies.
set -euo pipefail

TABLE="vayushield"
HTTP_PORTS='{ 80, 443 }'

# HTTP/3 is UDP. deploy/nginx-vayupress.conf tells operators to enable it, and
# the ruleset did not model the port at all — so the one transport with no
# handshake to exhaust, and therefore the easiest to flood, was the one transport
# with no per-source cap on it.
QUIC_PORTS="${QUIC_PORTS-{ 443 \}}"

# The same binary binds :25/:110/:143/:587/:993/:995 (cmd/vayupress/vayuos.go),
# and the allowlist comment below already notes that an MX record publishes this
# host's address to the world. Modelling only the web ports left SMTP — the
# classic connection-exhaustion target — with no per-source cap.
#
# Set to empty to opt out entirely (a dedicated relay expecting very high
# per-peer concurrency is the case that wants this). Note the ${VAR-default}
# form, without the colon: with `:-` an explicitly-empty value would fall back to
# the default, so the documented opt-out would silently do nothing.
MAIL_PORTS="${MAIL_PORTS-{ 25, 110, 143, 465, 587, 993, 995 \}}"

# The Go process's own listener. VayuPress reads PORT (internal/config/config.go)
# and onionSafeBindAddr now binds loopback unless it detects a container — but a
# bind is a runtime decision that a stray BIND_ADDR, a container misdetection or
# an operator's systemd override can reverse, and nothing outside the process
# would notice. A kernel rule is the backstop that does not depend on the app
# having got it right.
#
# Reaching this port directly bypasses nginx entirely, and with it every Tier 3
# limit, the TLS termination and the real-client-IP headers the app trusts.
APP_PORT="${APP_PORT-${PORT:-8080}}"

# --- Tunables (per source IP) -------------------------------------------------
CONN_LIMIT=64            # max concurrent connections per IP
NEW_CONN_RATE="50/second" # new-connection rate per IP
NEW_CONN_BURST=100        # burst above the rate before dropping

# Mail gets its own budget, deliberately not the web one. Two reasons, and the
# second is the important one:
#
#   • the shapes differ. A browser opens six connections and a mail peer opens
#     one or two, so a cap sized for web traffic is not a meaningful cap on SMTP.
#   • a web flood must never be able to spend the mail allowance. Sharing one
#     meter would let an HTTP attack throttle inbound mail as a side effect,
#     which is the shield causing the outage.
#
# Honest failure mode: these rules DROP, so a false positive makes a sending peer
# time out and retry. Mail is not lost — every serious sender retries for days —
# but it is delayed, so these are set well above any normal peer's behaviour.
MAIL_CONN_LIMIT=24
MAIL_CONN_RATE="10/second"
MAIL_CONN_BURST=20
# There is deliberately NO global SYN rate limit here any more.
#
# The previous ruleset carried `tcp flags & (syn|ack) == syn limit rate 25/second`
# followed by an unconditional drop. That rate was GLOBAL, not per source, so any
# attacker willing to emit more than 25 SYN/s put the chain into its drop state
# and every NEW visitor was dropped in the kernel while established connections
# carried on. The failure presents as "the site works for me and nobody new can
# load it", which is close to unfalsifiable from the inside — and it handed an
# attacker a site-wide outage for the price of a trivial packet rate.
#
# It also ran at hook priority -10, ahead of the tcp_syncookies this same script
# enables, pre-empting the correct stateless defence with a worse one.
#
# The honest trade: syncookies only engage once the accept queue overflows, so
# the limiter did bound conntrack insertion pressure in the window before that.
# The per-source caps below cover the same ground without giving anyone a lever
# on everyone else's traffic.

# --- CDN / reverse-proxy allowlist --------------------------------------------
# Every limit above keys on the SOURCE IP, and the kernel has no idea what an
# HTTP header is. So when a CDN proxies the site, this layer sees a handful of
# edge addresses instead of thousands of readers, and measures the whole audience
# as if it were a few extremely busy clients: a per-visitor connection cap of 64
# is nothing for one edge node. Connections then get dropped in the kernel, which
# surfaces as intermittent, unreproducible failures — no log line, no error page,
# just requests that sometimes do not arrive.
#
# No in-app setting can fix this. "Behind Cloudflare / a CDN" in VayuOS teaches
# the *application* to read the real visitor from CF-Connecting-IP; it cannot
# teach the kernel, which runs long before that header is parsed.
#
# So the edge ranges are allowlisted here instead, ahead of every limiter — which
# also sharpens the firewall rather than weakening it. Traffic arriving from
# anywhere OTHER than the CDN is, by definition, someone who found the origin
# address and skipped the edge, and that traffic still meets the full ruleset.
# (Worth knowing: if this host also runs mail, its address is already public in
# DNS via the MX record, so direct-to-origin traffic is not hypothetical.)
#
# One CIDR per line; blank lines and '#' comments ignored. Populate it with
#   vayushield-firewall.sh cdn-allow cloudflare
# which is a deliberate, explicit fetch — apply never reaches the network by
# itself, so an offline or onion-only host is never made to call out.
CDN_ALLOW_FILE="${CDN_ALLOW_FILE:-/etc/vayushield/cdn-allow.conf}"

# Where the kernel-tunable drop-in lands. Overridable so the apply-and-verify
# path can be exercised by tests against a temporary file instead of /etc.
SYSCTL_CONF="${SYSCTL_CONF:-/etc/sysctl.d/99-vayushield.conf}"

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "error: must run as root" >&2
    exit 1
  fi
}

# read_cdn_allow <4|6> — print a comma-separated nft set body of allowlisted
# CIDRs for the given family, or nothing when the file is absent or has no
# entries of that family.
#
# Every line is validated against a strict pattern before it is allowed anywhere
# near the ruleset. The file is root-owned, but it is still operator-edited text
# being spliced into a rule set that is loaded as root: one stray line ending in
# a semicolon would otherwise become an nft statement rather than an address.
# Anything that does not look exactly like a CIDR is skipped with a warning
# rather than silently dropped, because a typo that quietly removes an allowlist
# entry reintroduces the throttling this exists to prevent.
read_cdn_allow() {
  local family="$1" line out=""
  [ -r "$CDN_ALLOW_FILE" ] || return 0
  # ANOTHER SILENT DROP, from the same root cause. `read` returns non-zero
  # on a final line with no trailing newline, so the loop body never runs for
  # it and the LAST range in the file disappears — from the nginx config and
  # from the kernel set alike. The fetch now guarantees the newline, but this
  # file is explicitly documented as hand-writable ("For any other proxy,
  # write its ranges to ${CDN_ALLOW_FILE}, one CIDR per line"), and an editor
  # that does not add a final newline is ordinary.
  #
  # Worse, the count reported after a fetch uses grep, which DOES count an
  # unterminated final line — so the panel reported one more range than
  # either consumer actually applied.
  #
  # `|| [ -n "$line" ]` runs the body once more when read failed but left
  # content in the variable, which is exactly that case.
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%%#*}"
    line="$(printf '%s' "$line" | tr -d '[:space:]')"
    [ -n "$line" ] || continue
    if [ "$family" = 4 ]; then
      printf '%s' "$line" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$' || {
        printf '%s' "$line" | grep -q ':' || echo "  ! skipping malformed allowlist entry: $line" >&2
        continue
      }
    else
      printf '%s' "$line" | grep -Eq '^[0-9a-fA-F:]+/[0-9]{1,3}$' || continue
    fi
    out="${out:+$out, }$line"
  done <"$CDN_ALLOW_FILE"
  printf '%s' "$out"
}

# cdn_allow_fetch <vendor> — write CDN_ALLOW_FILE from a vendor's published
# ranges. Explicit by design: this is the ONLY code path here that touches the
# network, so `apply` stays offline-safe and an onion-only install never makes a
# clearnet call as a side effect of enabling a firewall.
cdn_allow_fetch() {
  local vendor="${1:-cloudflare}" tmp v4 v6
  command -v curl >/dev/null 2>&1 || { echo "error: curl is required to fetch ranges" >&2; return 1; }
  case "$vendor" in
    cloudflare)
      v4="https://www.cloudflare.com/ips-v4"
      v6="https://www.cloudflare.com/ips-v6"
      ;;
    *)
      echo "error: unknown vendor '$vendor' (known: cloudflare)" >&2
      echo "  For any other proxy, write its ranges to ${CDN_ALLOW_FILE}, one CIDR per line." >&2
      return 1
      ;;
  esac
  tmp="$(mktemp)"
  {
    echo "# VayuShield CDN/proxy allowlist — ranges fetched from ${vendor}."
    echo "# Regenerate with: vayushield-firewall.sh cdn-allow ${vendor}"
    echo "# Re-apply afterwards so the kernel picks it up: vayushield-firewall.sh apply"
  } >"$tmp"
  # ROOT CAUSE OF A LIVE OUTAGE, fixed here rather than downstream.
  #
  # These two bodies used to be appended straight into the same file:
  #
  #   if ! curl -fsSL "$v4" >>"$tmp" || ! curl -fsSL "$v6" >>"$tmp"; then
  #
  # The published IPv4 list ends WITHOUT a trailing newline, so the first byte of
  # the IPv6 list landed on the same physical line as the last IPv4 CIDR:
  #
  #   192.0.2.0/24  +  2001:db8::/32  ->  192.0.2.0/242001:db8::/32
  #
  # That token then reached nginx as `set_real_ip_from`, which refused the whole
  # configuration — and because the file sits in conf.d, "the whole
  # configuration" is every vhost on the machine, not one site.
  #
  # Each body is now captured separately and normalised: CR stripped, a trailing
  # newline guaranteed, blank lines dropped. Concatenating two documents without
  # asserting the boundary between them is the defect, and it does not stop being
  # one because the far end usually happens to send a newline.
  v4body="$(curl -fsSL "$v4")" || {
    rm -f "$tmp"
    echo "error: could not fetch ${vendor} IPv4 ranges; the existing allowlist is unchanged." >&2
    return 1
  }
  v6body="$(curl -fsSL "$v6")" || {
    rm -f "$tmp"
    echo "error: could not fetch ${vendor} IPv6 ranges; the existing allowlist is unchanged." >&2
    return 1
  }
  printf '%s\n%s\n' "$v4body" "$v6body" | tr -d '\r' | grep -v '^[[:space:]]*$' >>"$tmp"
  # Refuse to install an empty or obviously-wrong file. Truncating the allowlist
  # to nothing would silently restore the throttling of every edge node.
  if ! grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$' "$tmp"; then
    rm -f "$tmp"
    echo "error: the fetched ranges contained no usable IPv4 CIDR; nothing written." >&2
    return 1
  fi
  mkdir -p "$(dirname "$CDN_ALLOW_FILE")"
  install -o root -g root -m 0644 "$tmp" "$CDN_ALLOW_FILE"
  rm -f "$tmp"
  echo "✓ wrote $(grep -Ec '^[^#]' "$CDN_ALLOW_FILE") ${vendor} range(s) to ${CDN_ALLOW_FILE}"
  echo "  Now run: $0 apply"
}

# sysctl_pairs <file> — print "key value" for every assignment in a sysctl
# drop-in, ignoring comments and blank lines.
sysctl_pairs() {
  [ -r "$1" ] || return 0
  awk -F= '
    /^[[:space:]]*[#;]/ { next }
    NF < 2 { next }
    {
      k = $1; gsub(/[[:space:]]/, "", k)
      v = $2; for (i = 3; i <= NF; i++) v = v "=" $i
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
      if (k != "" && v != "") print k, v
    }' "$1"
}

# sysctl_sort_key <path> — the name sysctl.d actually orders by. systemd treats
# /etc/sysctl.conf as though it were /etc/sysctl.d/99-sysctl.conf, so comparing
# raw paths would get the boot order wrong for the one file most likely to
# contain an operator's own overrides.
sysctl_sort_key() {
  case "$1" in
    /etc/sysctl.conf) echo "99-sysctl.conf" ;;
    *) basename "$1" ;;
  esac
}

# sysctl_collisions — report keys this drop-in shares with another one.
#
# sysctl.d applies lexicographically and the last writer wins, so two files can
# each look correct on their own and still produce a value neither author
# intended. This is not theoretical: 99-vayushield.conf sorts after
# 99-vayupress.conf, so the hardening file's accept queues silently replaced the
# performance file's — and nothing anywhere said so.
#
# Two directions, and only one of them is ours to fix:
#   • a file sorting AFTER this one overrides a hardening key, so at the next
#     boot the shield's setting is not what is in effect. The read-back in
#     apply_sysctl cannot catch this — `sysctl -p` applies one file immediately,
#     which is not the boot order.
#   • this file overrides someone else's key. Reported for the record.
sysctl_collisions() {
  local ours base mine f fk theirs k v ok ov
  ours="$SYSCTL_CONF"
  [ -r "$ours" ] || return 0
  base="$(sysctl_sort_key "$ours")"
  mine="$(sysctl_pairs "$ours")"
  [ -n "$mine" ] || return 0

  for f in /etc/sysctl.d/*.conf /etc/sysctl.conf; do
    [ -r "$f" ] || continue
    [ "$f" != "$ours" ] || continue
    fk="$(sysctl_sort_key "$f")"
    [ "$fk" != "$base" ] || continue
    theirs="$(sysctl_pairs "$f")"
    [ -n "$theirs" ] || continue
    while read -r k v; do
      [ -n "$k" ] || continue
      while read -r ok ov; do
        [ "$ok" = "$k" ] || continue
        [ "$ov" != "$v" ] || continue
        if [[ "$fk" > "$base" ]]; then
          echo "  ! ${k}: ${f} sets ${ov} and loads AFTER this file — at the next boot the shield's ${v} is NOT in effect" >&2
        else
          echo "  · ${k}: overriding ${ov} from ${f} with ${v}" >&2
        fi
      done <<<"$theirs"
    done <<<"$mine"
  done
  return 0
}

apply_sysctl() {
  echo "• applying kernel DDoS sysctls (SYN cookies, backlog, conntrack)…"

  # nf_conntrack must be loaded BEFORE its keys exist. Every rule in this table
  # is ct-stateful, so the connection table is the shield's own dependency — and
  # the conntrack settings below silently did nothing on a fresh boot, because
  # net.netfilter.* keys do not exist until the module loads and the whole sysctl
  # run had its stderr discarded and its exit status forced to success.
  #
  # Exhaustion of that table does not surface as a shield event. It surfaces as
  # "nf_conntrack: table full, dropping packet" in dmesg and as unattributable
  # packet loss for every visitor — the shield being disarmed by the same flood
  # it exists to absorb, with nothing in the panel to say so.
  modprobe nf_conntrack 2>/dev/null || true

  cat >"$SYSCTL_CONF" <<'EOF'
# VayuShield kernel hardening — SYN-flood and connection-flood resistance.
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_synack_retries = 2
net.ipv4.tcp_rfc1337 = 1
net.ipv4.tcp_fin_timeout = 15

# ── Accept queues ────────────────────────────────────────────────────────────
# These two are deliberately 65535 and NOT a smaller "hardening" number.
#
# deploy/vps-kernel-tuning.sh writes 99-vayupress.conf with both at 65535 for a
# high-concurrency workload, and deploy/vayupress.service asks for
# LimitNOFILE=65536 citing 10k+ concurrent connections. sysctl.d applies
# lexicographically and 'p' < 's', so this file loads LAST — which meant turning
# on the DDoS firewall quietly cut both backlogs 16x, on the exact box that had
# just been tuned for the opposite. Slower under normal load, and worse under
# attack: a short queue reaches syncookie fallback sooner, and syncookies drop
# window scaling and SACK for every connection that takes that path.
#
# A deep accept queue is the correct partner to tcp_syncookies, not its rival.
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535

# ── Connection tracking ──────────────────────────────────────────────────────
# Sized so a flood exhausts an attacker's per-source caps long before it
# exhausts the table every rule here depends on.
net.netfilter.nf_conntrack_max = 262144
# Buckets default to max/8 on many kernels, which turns each bucket into a long
# chain under load. 1:4 keeps lookups short at a modest memory cost.
net.netfilter.nf_conntrack_buckets = 65536
# A half-open connection held 60s is a slot an attacker gets for one SYN.
net.netfilter.nf_conntrack_tcp_timeout_syn_recv = 10
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 30
# The default ESTABLISHED timeout is FIVE DAYS. On a web server that means an
# abandoned connection holds a slot for the rest of the week.
net.netfilter.nf_conntrack_tcp_timeout_established = 3600
# Refuse to create state from a packet that is not a valid handshake opener, so
# a spray of mid-stream packets cannot allocate table entries.
net.netfilter.nf_conntrack_tcp_loose = 0
EOF

  # Apply, then VERIFY. The previous version piped stderr to /dev/null and ended
  # in `|| true`, so a failed write was indistinguishable from a successful one —
  # which is how the conntrack sizing came to be inert on every fresh boot.
  local failed=""
  sysctl -p "$SYSCTL_CONF" >/dev/null 2>&1 || true
  local want got key
  # ANOTHER SILENT DROP, from the same root cause. `read` returns non-zero
  # on a final line with no trailing newline, so the loop body never runs for
  # it and the LAST range in the file disappears — from the nginx config and
  # from the kernel set alike. The fetch now guarantees the newline, but this
  # file is explicitly documented as hand-writable ("For any other proxy,
  # write its ranges to ${CDN_ALLOW_FILE}, one CIDR per line"), and an editor
  # that does not add a final newline is ordinary.
  #
  # Worse, the count reported after a fetch uses grep, which DOES count an
  # unterminated final line — so the panel reported one more range than
  # either consumer actually applied.
  #
  # `|| [ -n "$line" ]` runs the body once more when read failed but left
  # content in the variable, which is exactly that case.
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|\#*) continue ;; esac
    key="${line%%=*}"; key="$(printf '%s' "$key" | tr -d '[:space:]')"
    want="${line#*=}"; want="$(printf '%s' "$want" | tr -d '[:space:]')"
    # `|| true` is load-bearing: this file runs under `set -euo pipefail`, and a
    # missing key makes `sysctl -n` exit 1, which fails the pipeline and aborts
    # the assignment — taking the whole script with it. A read-back that kills
    # `apply` on the exact fresh-boot case it was added to report would be worse
    # than the silent failure it replaced.
    got="$(sysctl -n "$key" 2>/dev/null | tr -d '[:space:]')" || true
    if [ "$got" != "$want" ]; then
      failed="${failed:+$failed, }${key}(want ${want}, got ${got:-unset})"
    fi
  done <"$SYSCTL_CONF"

  # A key can be correct right now and wrong after a reboot, if a drop-in that
  # sorts later sets it too. `sysctl -p` on one file is not the boot order, so
  # the read-back above cannot see that; only comparing the files can.
  sysctl_collisions

  if [ -n "$failed" ]; then
    # Not fatal: a container without CAP_SYS_ADMIN, or a kernel without the
    # module, still benefits from the nftables rules. But say so, rather than
    # reporting a hardening level that was never applied.
    echo "  ! some kernel settings did not take: ${failed}" >&2
    echo "  ! the nftables rules still apply; conntrack sizing may be the kernel default" >&2
    return 0
  fi
  echo "• kernel settings verified."
}

# report_ports <t|u> <nft port set> <label> — list the ports this ruleset models
# and mark the ones that actually have a listener.
#
# The point is service-awareness in both directions: a modelled port with no
# listener is dead weight, and a service running on a port NOT listed here has no
# per-source cap at all. Reading that off `nft list table` alone means knowing
# which of these numbers the binary happens to bind.
report_ports() {
  local proto="$1" set="$2" label="$3" p out=""
  if [ -z "$set" ]; then
    echo "  ${label}: not modelled (opted out) — no per-source cap on these ports"
    return 0
  fi
  for p in $(printf '%s' "$set" | tr -d '{}' | tr ',' ' '); do
    if ! command -v ss >/dev/null 2>&1; then
      out="${out:+$out }${p}"
    elif ss -H -ln "-${proto}" "sport = :${p}" 2>/dev/null | grep -q .; then
      out="${out:+$out }${p}(listening)"
    else
      out="${out:+$out }${p}(no listener)"
    fi
  done
  echo "  ${label}: ${out}"
}

apply_nft() {
  # Tier 2 inherently needs nftables. If it's missing, install it automatically
  # (best-effort, across the common package managers) so enabling Tier 2 is truly
  # one action; fall back to a clear manual message if that isn't possible.
  if ! command -v nft >/dev/null 2>&1; then
    echo "• nftables ('nft') not found — attempting to install it…"
    if command -v apt-get >/dev/null 2>&1; then
      DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || true
      DEBIAN_FRONTEND=noninteractive apt-get install -y nftables >/dev/null 2>&1 || true
    elif command -v dnf >/dev/null 2>&1; then
      dnf install -y nftables >/dev/null 2>&1 || true
    elif command -v yum >/dev/null 2>&1; then
      yum install -y nftables >/dev/null 2>&1 || true
    elif command -v apk >/dev/null 2>&1; then
      apk add --no-cache nftables >/dev/null 2>&1 || true
    elif command -v zypper >/dev/null 2>&1; then
      zypper --non-interactive install nftables >/dev/null 2>&1 || true
    fi
  fi
  if ! command -v nft >/dev/null 2>&1; then
    echo "error: nftables ('nft') is not installed and could not be installed automatically." >&2
    echo "  Install it manually, then retry (from the panel or CLI):" >&2
    echo "    Debian/Ubuntu:  apt-get install -y nftables" >&2
    echo "    RHEL/Fedora:    dnf install -y nftables" >&2
    return 1
  fi
  echo "• installing nftables table '${TABLE}'…"
  local rules cdn4 cdn6 cdn_rules=""
  rules="$(mktemp)"
  cdn4="$(read_cdn_allow 4)"
  cdn6="$(read_cdn_allow 6)"
  # Placed ahead of the SYN guard as well as the per-IP limiters. The SYN guard
  # is a GLOBAL rate (not per-IP), so behind a proxy — where every connection on
  # the site arrives from the edge — it would cap the whole site's new
  # connections, not an attacker's. Letting the edge past it keeps the guard
  # aimed at what it is for: traffic that reached this address directly.
  # The allowlist covers UDP/443 as well as TCP. A proxy that fronts the site
  # over HTTP/3 arrives on the UDP port, and an accept that only names tcp would
  # allowlist the edge for half its traffic and rate-limit it for the other half
  # — which presents as HTTP/3 being intermittently broken while HTTP/2 is fine,
  # about the least diagnosable failure this file could produce.
  if [ -n "$cdn4" ]; then
    cdn_rules="${cdn_rules}
    ip saddr { ${cdn4} } tcp dport ${HTTP_PORTS} accept
    ip saddr { ${cdn4} } udp dport ${QUIC_PORTS} accept"
  fi
  if [ -n "$cdn6" ]; then
    cdn_rules="${cdn_rules}
    ip6 saddr { ${cdn6} } tcp dport ${HTTP_PORTS} accept
    ip6 saddr { ${cdn6} } udp dport ${QUIC_PORTS} accept"
  fi
  # Defence in depth for the app's own port. Emitted only when it is safe to do
  # so, and skipped loudly rather than quietly when it is not — a firewall that
  # silently drops the port the site is actually served on is an outage, and this
  # script is re-run automatically by the reconcile agent, so it would be an
  # outage that reinstates itself on every boot.
  #
  # Refused when APP_PORT collides with a port this ruleset already models: an
  # operator terminating TLS in the Go binary on :443, or running the app on a
  # mail port, would otherwise lose the service outright.
  local app_rule="" app_note=""
  if [ -n "$APP_PORT" ]; then
    case " $(printf '%s %s %s' "$HTTP_PORTS" "$QUIC_PORTS" "$MAIL_PORTS" | tr -d '{}' | tr ',' ' ') " in
      *" ${APP_PORT} "*)
        app_note="  · app port ${APP_PORT} is already a modelled service port — not adding a direct-access drop"
        ;;
      *)
        # `iif != "lo"` and `tcp dport` are both family-neutral, so one rule
        # covers v4 and v6. Loopback is accepted higher up regardless, which is
        # what keeps nginx -> 127.0.0.1:APP_PORT working.
        app_rule="
    iif != \"lo\" tcp dport ${APP_PORT} drop"
        app_note="  · dropping direct external access to app port ${APP_PORT} (nginx reaches it over loopback)"
        ;;
    esac
  fi

  # Mail is deliberately NOT allowlisted for the CDN ranges: no CDN proxies SMTP,
  # so an edge address opening a mail connection is not the edge doing its job.
  local mail_rules="" mail_chains=""
  if [ -n "$MAIL_PORTS" ]; then
    mail_rules="
    tcp dport ${MAIL_PORTS} ct state new jump vs_mail"
    mail_chains="
  # Mail keeps its own meters. Sharing the web ones would let an HTTP flood spend
  # the mail budget, so an attack on the website would throttle inbound mail as a
  # side effect — the shield causing the outage.
  chain vs_mail {
    meta nfproto ipv4 jump vs_mail4
    meta nfproto ipv6 jump vs_mail6
  }

  chain vs_mail4 {
    meter vs_mconn4 { ip saddr ct count over ${MAIL_CONN_LIMIT} } drop
    meter vs_mrate4 { ip saddr limit rate ${MAIL_CONN_RATE} burst ${MAIL_CONN_BURST} packets } return
    drop
  }

  chain vs_mail6 {
    meter vs_mconn6 { ip6 saddr ct count over ${MAIL_CONN_LIMIT} } drop
    meter vs_mrate6 { ip6 saddr limit rate ${MAIL_CONN_RATE} burst ${MAIL_CONN_BURST} packets } return
    drop
  }
"
  fi
  if [ -n "$cdn_rules" ]; then
    echo "• allowlisting proxy edge ranges from ${CDN_ALLOW_FILE}"
  elif [ -e "$CDN_ALLOW_FILE" ]; then
    echo "  ! ${CDN_ALLOW_FILE} exists but yielded no usable ranges — edge traffic WILL be rate-limited" >&2
  fi
  [ -z "$app_note" ] || echo "$app_note"
  # Per-IP concurrent connections are limited with a keyed meter (ip saddr) — the
  # portable idiom; a bare "ct count over N" is not per-IP and is rejected by some
  # nft versions. The rate limiter above already uses the same meter mechanism.
  # The delete lives INSIDE the ruleset, and that is the whole point.
  #
  # It used to be a separate `nft delete table` run AFTER the `nft -c` dry run,
  # which made re-applying impossible. `nft -c` validates against the LIVE
  # kernel, so on a machine where this table already exists it was asked to add
  # meters that were already there and answered "Device or resource busy" — for
  # every meter, every time. The first apply on a clean box worked; every apply
  # after it failed, which is precisely the moment an operator presses
  # "Allowlist the edge ranges" or re-runs the firewall.
  #
  # Declaring the table before deleting it is the standard nftables idiom for an
  # idempotent delete: the bare `table` line creates it if absent so the delete
  # cannot fail on a clean box. Both then live in the same transaction, so the
  # dry run evaluates exactly what the apply will do.
  #
  # It also closes a real gap. The old order deleted the table and THEN loaded
  # the new one as a separate command; if that second step failed, the machine
  # was left with NO firewall while the script reported a load failure. One
  # transaction is atomic — it applies completely or changes nothing, which is
  # what the error message already claimed.
  cat >"$rules" <<EOF
table inet ${TABLE}
delete table inet ${TABLE}

table inet ${TABLE} {
  chain input {
    type filter hook input priority -10; policy accept;

    # Fast-path established/related and loopback.
    ct state established,related accept
    iif "lo" accept
    ct state invalid drop
${cdn_rules}${app_rule}

    # New web connections go to a REGULAR chain. This is load-bearing: in a base
    # chain a "return" verdict means the chain policy, which here is accept, so a
    # rule ending in return terminated evaluation and everything after it was
    # dead. In a regular chain a return resumes the caller, which is what lets
    # more than one meter see the same packet.
    tcp dport ${HTTP_PORTS} ct state new jump vs_web
    # HTTP/3. QUIC has no handshake to make an attacker pay for state, so the
    # per-source caps matter more here than on TCP, not less.
    udp dport ${QUIC_PORTS} ct state new jump vs_web${mail_rules}
  }

  # vs_web splits by address family before touching a source address. In the
  # inet family an "ip saddr" match carries an implicit IPv4-only dependency, so
  # a v4-keyed meter simply never matches a v6 packet — and a bare drop beside it
  # carries no such dependency and matches everything. Mixing the two in one
  # chain is how you drop 100% of IPv6 while the IPv4 rules look correct.
  chain vs_web {
    meta nfproto ipv4 jump vs_web4
    meta nfproto ipv6 jump vs_web6
  }

  chain vs_web4 {
    # Concurrent connections per source. Checked first: a host already holding
    # the cap should be refused regardless of how slowly it opens the next one.
    meter vs_conn4 { ip saddr ct count over ${CONN_LIMIT} } drop
    # New-connection rate per source. Under the limit -> return to the caller and
    # on to the chain policy; over it -> fall through to the drop below.
    meter vs_rate4 { ip saddr limit rate ${NEW_CONN_RATE} burst ${NEW_CONN_BURST} packets } return
    drop
  }

  chain vs_web6 {
    meter vs_conn6 { ip6 saddr ct count over ${CONN_LIMIT} } drop
    meter vs_rate6 { ip6 saddr limit rate ${NEW_CONN_RATE} burst ${NEW_CONN_BURST} packets } return
    drop
  }
${mail_chains}}
EOF
  # Validate first so a syntax error is reported clearly and nothing is applied
  # half-way (the existing state is left intact on failure).
  if ! nft -c -f "$rules" 2>&1; then
    rm -f "$rules"
    echo "error: nftables rejected the ruleset (see the message above); no changes applied." >&2
    return 1
  fi
  # No separate delete: the ruleset carries its own, atomically.
  if ! nft -f "$rules" 2>&1; then
    rm -f "$rules"
    echo "error: failed to load the nftables ruleset; the previous rules are still in force." >&2
    return 1
  fi
  rm -f "$rules"
  echo "• nftables rules installed."
}

case "${1:-apply}" in
  apply)
    require_root
    apply_sysctl
    apply_nft
    echo "✓ VayuShield Tier-2 firewall applied. Verify with: $0 status"
    ;;
  cdn-allow)
    require_root
    cdn_allow_fetch "${2:-cloudflare}"
    ;;
  status)
    nft list table inet "${TABLE}" 2>/dev/null || echo "table '${TABLE}' not installed"
    echo
    if [ -r "$CDN_ALLOW_FILE" ]; then
      echo "proxy allowlist: $(grep -Ec '^[^#[:space:]]' "$CDN_ALLOW_FILE" 2>/dev/null || echo 0) range(s) in ${CDN_ALLOW_FILE}"
    else
      # Not an error on a direct-to-origin host, but the single most likely
      # reason a proxied site sees traffic disappear, so it is always stated.
      echo "proxy allowlist: none (${CDN_ALLOW_FILE} absent)"
      echo "  If a CDN proxies this site, its edge nodes are being rate-limited as"
      echo "  if each were one very busy visitor. Fix: $0 cdn-allow cloudflare && $0 apply"
    fi
    echo
    echo "modelled ports:"
    report_ports t "$HTTP_PORTS" "web        "
    report_ports u "$QUIC_PORTS" "http/3     "
    report_ports t "$MAIL_PORTS" "mail       "
    echo
    echo "kernel tunables: ${SYSCTL_CONF}"
    sysctl_collisions
    ;;
  remove)
    require_root
    nft delete table inet "${TABLE}" 2>/dev/null || true
    rm -f "$SYSCTL_CONF"
    # The allowlist is left in place on purpose: it is configuration, not state,
    # and removing it would mean re-fetching it the next time Tier 2 is enabled.
    echo "✓ VayuShield Tier-2 firewall removed."
    ;;
  *)
    echo "usage: $0 [apply|status|remove|cdn-allow [vendor]]" >&2
    exit 2
    ;;
esac
