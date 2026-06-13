#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="$ROOT/dist/bundles"
mkdir -p "$OUT"

BUNDLE="$OUT/vayupress-${VERSION}.vayu"
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

# Collect artifacts
mkdir -p "$STAGING/bin" "$STAGING/migrations" "$STAGING/deploy"
cp "$ROOT/dist/vayupress" "$STAGING/bin/"
cp "$ROOT/internal/migrations/sql/"*.sql "$STAGING/migrations/" 2>/dev/null || true
cp "$ROOT/deploy/"*.sh "$STAGING/deploy/"

# Generate per-file checksums
find "$STAGING" -type f | sort | while read -r f; do
  rel="${f#$STAGING/}"
  sha256sum "$f" | awk "{print \$1 \"  $rel\"}"
done > "$STAGING/SHA256SUMS"

# Write manifest
cat > "$STAGING/manifest.json" << JSON
{
  "version": "$VERSION",
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "platform": "$(uname -s)-$(uname -m)"
}
JSON

# Pack
tar -czf "$BUNDLE" -C "$STAGING" .
sha256sum "$BUNDLE" > "$BUNDLE.sha256"
echo "✅ bundle: $BUNDLE"
cat "$BUNDLE.sha256"
