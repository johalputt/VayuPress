#!/bin/bash
# VayuPress v1.7.0 — VPS kernel tuning for 200k-post / high-concurrency workload
# Run as root: sudo bash vps-kernel-tuning.sh

set -euo pipefail

echo "Applying VayuPress VPS kernel tuning..."

cat > /etc/sysctl.d/99-vayupress.conf << 'EOF'
# ── File descriptors ──────────────────────────────────────────────────────────
fs.file-max = 200000

# ── TCP tuning ────────────────────────────────────────────────────────────────
# Increase listen backlog (default 128 drops connections under burst)
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535

# Reduce TIME_WAIT socket lifetime
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1

# TCP keepalive — detect dead connections faster
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 3

# Increase TCP buffer sizes for high-throughput
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216

# ── Memory ────────────────────────────────────────────────────────────────────
# Reduce swap pressure (SQLite mmap benefits from page cache staying hot)
vm.swappiness = 10
vm.dirty_ratio = 15
vm.dirty_background_ratio = 5

# Overcommit — Go runtime pre-allocates large virtual address spaces
vm.overcommit_memory = 1
EOF

sysctl -p /etc/sysctl.d/99-vayupress.conf

# ── System-wide file descriptor limits ───────────────────────────────────────
cat > /etc/security/limits.d/99-vayupress.conf << 'EOF'
vayupress soft nofile 65536
vayupress hard nofile 65536
vayupress soft nproc  8192
vayupress hard nproc  8192
EOF

echo "Kernel tuning applied. Verify with:"
echo "  sysctl fs.file-max"
echo "  sysctl net.core.somaxconn"
echo "  ss -s   (socket statistics)"
