#!/bin/bash
set -euo pipefail
INSTALL_BASE="/opt/vayupress"
KEEP=3
mapfile -t OLD < <(ls -td "$INSTALL_BASE"/v* 2>/dev/null | tail -n +$((KEEP + 1)))
for old in "${OLD[@]}"; do
  rm -rf "$old" && echo "✅ removed $old"
done
echo "✅ cleanup complete"
