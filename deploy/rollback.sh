#!/bin/bash
set -euo pipefail
INSTALL_BASE="/opt/vayupress"
CURRENT="$INSTALL_BASE/current"
PREV=$(ls -td "$INSTALL_BASE"/v* 2>/dev/null | sed -n '2p')
[ -n "$PREV" ] || { echo "❌ no previous version to rollback to"; exit 1; }
ln -snf "$PREV" "$CURRENT"
systemctl restart vayupress && echo "✅ rolled back to $(basename "$PREV")"
