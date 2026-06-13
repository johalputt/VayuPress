#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="$ROOT/dist"
mkdir -p "$OUT"
echo "Building VayuPress $VERSION ..."
CGO_ENABLED=1 GOTOOLCHAIN=auto go build \
  -trimpath \
  -ldflags="-s -w -X main.version=$VERSION" \
  -o "$OUT/vayupress" \
  "$ROOT/cmd/vayupress"
sha256sum "$OUT/vayupress" > "$OUT/vayupress.sha256"
echo "✅ build complete: $OUT/vayupress ($VERSION)"
cat "$OUT/vayupress.sha256"
