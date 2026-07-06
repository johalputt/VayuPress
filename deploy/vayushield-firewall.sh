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
  echo "• installing nftables table '${TABLE}'…"
  nft delete table inet "${TABLE}" 2>/dev/null || true
  nft -f - <<EOF
table inet ${TABLE} {
  # Per-source-IP concurrent connection counter.
  set conncount {
    type ipv4_addr
    flags dynamic
  }

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
    tcp dport ${HTTP_PORTS} ct state new ct count over ${CONN_LIMIT} drop
  }
}
EOF
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
