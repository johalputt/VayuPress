#!/bin/bash
# =============================================================================
#  sync-source.sh — VayuPress Source Integrity Check (ADR-0045 / P13–P20)
# -----------------------------------------------------------------------------
#  Architecture evolution note (P14–P20):
#  The application was refactored from a single embedded Go heredoc in the
#  deploy script into a proper multi-package Go module (cmd/vayupress + internal/*).
#  The "heredoc sync" model is superseded by standard Go module tooling.
#
#  This script now verifies structural integrity of the Go source tree:
#    --check   Verify required source files exist and the binary builds cleanly.
#    (default) Same as --check (no write mode needed; source is canonical).
#
#  CI passes when:
#    1. cmd/vayupress/main.go exists and is non-empty.
#    2. All required internal packages are present.
#    3. go build ./... succeeds (binary compiles).
#
#  Usage:
#    scripts/sync-source.sh           # verify source integrity
#    scripts/sync-source.sh --check   # same (CI mode, same exit semantics)
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FAILED=0

# ── 1. Required entry-point files ────────────────────────────────────────────
REQUIRED_FILES=(
  "cmd/vayupress/main.go"
  "cmd/vayupress/app.go"
  "cmd/vayupress/routes.go"
  "cmd/vayupress/handlers_articles.go"
  "cmd/vayupress/handlers_infra.go"
  "cmd/vayupress/handlers_admin.go"
  "cmd/vayupress/middleware.go"
)

for f in "${REQUIRED_FILES[@]}"; do
  if [ ! -s "$f" ]; then
    echo "❌ MISSING or EMPTY: $f"
    FAILED=1
  else
    echo "✅ $f"
  fi
done

# ── 2. Required internal packages ─────────────────────────────────────────────
REQUIRED_PKGS=(
  "internal/api"
  "internal/db"
  "internal/queue"
  "internal/outbox"
  "internal/lifecycle"
  "internal/trace"
  "internal/resource"
  "internal/events"
  "internal/search"
  "internal/httputil"
  "internal/logging"
  "internal/metrics"
  "internal/config"
  "internal/auth"
  "internal/health"
  "internal/plugins"
  "internal/render"
)

for pkg in "${REQUIRED_PKGS[@]}"; do
  if ls "$pkg"/*.go >/dev/null 2>&1; then
    echo "✅ $pkg"
  else
    echo "❌ MISSING package: $pkg"
    FAILED=1
  fi
done

# ── 3. Binary builds cleanly ──────────────────────────────────────────────────
echo "Running: go build ./..."
if go build ./... 2>&1; then
  echo "✅ go build ./... passed"
else
  echo "❌ go build ./... FAILED"
  FAILED=1
fi

# ── Result ────────────────────────────────────────────────────────────────────
if [ $FAILED -ne 0 ]; then
  echo ""
  echo "❌ Source integrity check FAILED — see above for details."
  exit 1
fi

echo ""
echo "✅ Source integrity verified — multi-package Go module structure intact."
