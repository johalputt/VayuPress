#!/bin/bash
set -euo pipefail
BUNDLE="${1:?usage: verify.sh <path/to/bundle.vayu>}"
# Verify outer checksum
sha256sum -c "${BUNDLE}.sha256"
# Extract and verify inner checksums
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT
tar -xzf "$BUNDLE" -C "$STAGING"
cd "$STAGING"
sha256sum -c SHA256SUMS
echo "✅ bundle integrity verified: $(basename "$BUNDLE")"
