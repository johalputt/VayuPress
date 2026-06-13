#!/bin/bash
set -euo pipefail
# Check dependencies
for dep in go sqlite3 systemctl nginx; do
  command -v "$dep" >/dev/null 2>&1 || { echo "❌ missing: $dep"; exit 1; }
done
echo "✅ dependencies OK"
# Directories
for d in /var/lib/vayupress /var/log/vayupress /opt/vayupress /etc/vayupress; do
  mkdir -p "$d" && echo "✅ created $d"
done
# User
id vayupress >/dev/null 2>&1 || useradd --system --no-create-home --shell /bin/false vayupress
chown -R vayupress:vayupress /var/lib/vayupress /var/log/vayupress
echo "✅ bootstrap complete"
