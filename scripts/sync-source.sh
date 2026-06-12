#!/bin/bash
# =============================================================================
#  sync-source.sh — VayuPress P13 Source Synchronization
# -----------------------------------------------------------------------------
#  The canonical Go application source lives EMBEDDED in the heredoc inside
#  scripts/deploy-vayupress.sh (between the 'cat > main.go << GOEOF' marker and
#  the closing 'GOEOF'). This keeps the "curl -sSL ... | bash" single-file
#  install model intact (Constitution P5: Operational Simplicity).
#
#  To gain real Go tooling (IDE indexing, go build/vet/test, golangci-lint,
#  govulncheck) we mirror that exact source to cmd/vayupress/main.go.
#
#  This script extracts the heredoc body and writes it to cmd/vayupress/main.go.
#  CI runs it with --check to fail the build if the two ever drift (P10:
#  Automated Governance — no silent divergence).
#
#  Usage:
#    scripts/sync-source.sh           # regenerate cmd/vayupress/main.go
#    scripts/sync-source.sh --check   # exit 1 if out of sync (CI mode)
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY="${ROOT}/scripts/deploy-vayupress.sh"
TARGET="${ROOT}/cmd/vayupress/main.go"
MODE="${1:-write}"

if [ ! -f "$DEPLOY" ]; then
  echo "FATAL: ${DEPLOY} not found" >&2
  exit 2
fi

# Extract the heredoc body: everything strictly between the opening marker
# 'cat > main.go << '\''GOEOF'\''' and the closing 'GOEOF' line.
EXTRACTED="$(awk '
  /^cat > main\.go << '\''GOEOF'\''$/ { capture=1; next }
  /^GOEOF$/ { if (capture) { capture=0 } ; next }
  capture { print }
' "$DEPLOY")"

if [ -z "$EXTRACTED" ]; then
  echo "FATAL: could not extract Go source heredoc from ${DEPLOY}" >&2
  echo "       (expected markers: 'cat > main.go << GOEOF' ... 'GOEOF')" >&2
  exit 2
fi

mkdir -p "$(dirname "$TARGET")"

if [ "$MODE" = "--check" ]; then
  if [ ! -f "$TARGET" ]; then
    echo "❌ ${TARGET} is missing — run scripts/sync-source.sh to generate it." >&2
    exit 1
  fi
  if ! diff -q <(printf '%s\n' "$EXTRACTED") "$TARGET" >/dev/null 2>&1; then
    echo "❌ DRIFT: cmd/vayupress/main.go does not match the deploy script heredoc." >&2
    echo "   The deploy script is canonical. Run: scripts/sync-source.sh" >&2
    echo "   Then commit the regenerated cmd/vayupress/main.go." >&2
    diff <(printf '%s\n' "$EXTRACTED") "$TARGET" | head -40 >&2 || true
    exit 1
  fi
  echo "✅ cmd/vayupress/main.go is in sync with the deploy script heredoc."
  exit 0
fi

printf '%s\n' "$EXTRACTED" > "$TARGET"
echo "✅ Wrote $(wc -l < "$TARGET") lines to ${TARGET} from the deploy script heredoc."
