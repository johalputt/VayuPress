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
SYN_RATE="25/second"      # global SYN accept rate (flood guard)
SYN_BURST=50

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "error: must run as root" >&2
    exit 1
  fi
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
  local rules
  rules="$(mktemp)"
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

    # Global SYN-flood guard.
    tcp flags & (syn|ack) == syn limit rate ${SYN_RATE} burst ${SYN_BURST} packets return
    tcp flags & (syn|ack) == syn drop

    # Per-IP new-connection rate limit to the web ports.
    tcp dport ${HTTP_PORTS} ct state new meter vs_rate { ip saddr limit rate ${NEW_CONN_RATE} burst ${NEW_CONN_BURST} packets } accept
    tcp dport ${HTTP_PORTS} ct state new drop

    # Per-IP concurrent-connection cap to the web ports.
    tcp dport ${HTTP_PORTS} ct state new meter vs_conn { ip saddr ct count over ${CONN_LIMIT} } drop
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
  status)
    nft list table inet "${TABLE}" 2>/dev/null || echo "table '${TABLE}' not installed"
    ;;
  remove)
    require_root
    nft delete table inet "${TABLE}" 2>/dev/null || true
    rm -f /etc/sysctl.d/99-vayushield.conf
    echo "✓ VayuShield Tier-2 firewall removed."
    ;;
  *)
    echo "usage: $0 [apply|status|remove]" >&2
    exit 2
    ;;
esac
