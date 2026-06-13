#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)}"
INSTALL_BASE="/opt/vayupress"
VERSIONED="$INSTALL_BASE/$VERSION"
CURRENT="$INSTALL_BASE/current"
mkdir -p "$VERSIONED"
cp "$ROOT/dist/vayupress" "$VERSIONED/"
cp "$ROOT/dist/vayupress.sha256" "$VERSIONED/"
# Verify checksum
cd "$VERSIONED" && sha256sum -c vayupress.sha256 && echo "✅ checksum OK"
# Atomic symlink update
ln -snf "$VERSIONED" "$CURRENT"
echo "✅ installed $VERSION -> $CURRENT"
