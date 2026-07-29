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

# --- Tunables (per source IP) -------------------------------------------------
CONN_LIMIT=64            # max concurrent connections per IP
NEW_CONN_RATE="50/second" # new-connection rate per IP
NEW_CONN_BURST=100        # burst above the rate before dropping
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
  while IFS= read -r line; do
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
  if ! curl -fsSL "$v4" >>"$tmp" || ! curl -fsSL "$v6" >>"$tmp"; then
    rm -f "$tmp"
    echo "error: could not fetch ${vendor} ranges; the existing allowlist is unchanged." >&2
    return 1
  fi
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

apply_sysctl() {
  echo "• applying kernel DDoS sysctls (SYN cookies, backlog, conntrack)…"
  cat >/etc/sysctl.d/99-vayushield.conf <<'EOF'
# VayuShield kernel hardening — SYN-flood and connection-flood resistance.
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 4096
net.ipv4.tcp_synack_retries = 2
net.core.somaxconn = 4096
net.ipv4.tcp_rfc1337 = 1
net.netfilter.nf_conntrack_max = 262144
net.ipv4.tcp_fin_timeout = 15
EOF
  sysctl --quiet -p /etc/sysctl.d/99-vayushield.conf 2>/dev/null || true
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
  if [ -n "$cdn4" ]; then
    cdn_rules="${cdn_rules}
    ip saddr { ${cdn4} } tcp dport ${HTTP_PORTS} accept"
  fi
  if [ -n "$cdn6" ]; then
    cdn_rules="${cdn_rules}
    ip6 saddr { ${cdn6} } tcp dport ${HTTP_PORTS} accept"
  fi
  if [ -n "$cdn_rules" ]; then
    echo "• allowlisting proxy edge ranges from ${CDN_ALLOW_FILE}"
  elif [ -e "$CDN_ALLOW_FILE" ]; then
    echo "  ! ${CDN_ALLOW_FILE} exists but yielded no usable ranges — edge traffic WILL be rate-limited" >&2
  fi
  # Per-IP concurrent connections are limited with a keyed meter (ip saddr) — the
  # portable idiom; a bare "ct count over N" is not per-IP and is rejected by some
  # nft versions. The rate limiter above already uses the same meter mechanism.
  cat >"$rules" <<EOF
table inet ${TABLE} {
  chain input {
    type filter hook input priority -10; policy accept;

    # Fast-path established/related and loopback.
    ct state established,related accept
    iif "lo" accept
    ct state invalid drop
${cdn_rules}

    # New web connections go to a REGULAR chain. This is load-bearing: in a base
    # chain a "return" verdict means the chain policy, which here is accept, so a
    # rule ending in return terminated evaluation and everything after it was
    # dead. In a regular chain a return resumes the caller, which is what lets
    # more than one meter see the same packet.
    tcp dport ${HTTP_PORTS} ct state new jump vs_web
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
}
EOF
  # Validate first so a syntax error is reported clearly and nothing is applied
  # half-way (the existing state is left intact on failure).
  if ! nft -c -f "$rules" 2>&1; then
    rm -f "$rules"
    echo "error: nftables rejected the ruleset (see the message above); no changes applied." >&2
    return 1
  fi
  nft delete table inet "${TABLE}" 2>/dev/null || true
  if ! nft -f "$rules" 2>&1; then
    rm -f "$rules"
    echo "error: failed to load the nftables ruleset." >&2
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
    ;;
  remove)
    require_root
    nft delete table inet "${TABLE}" 2>/dev/null || true
    rm -f /etc/sysctl.d/99-vayushield.conf
    # The allowlist is left in place on purpose: it is configuration, not state,
    # and removing it would mean re-fetching it the next time Tier 2 is enabled.
    echo "✓ VayuShield Tier-2 firewall removed."
    ;;
  *)
    echo "usage: $0 [apply|status|remove|cdn-allow [vendor]]" >&2
    exit 2
    ;;
esac
