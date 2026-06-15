#!/usr/bin/env bash
# Regenerate the VayuPress screenshots referenced by README.md.
#
# Usage (CI — proxy injects X-API-Key for admin pages):
#   API_KEY=... ./scripts/capture-screenshots.sh [PROXY_BASE_URL]
#
# All URLs are routed through the screenshot-proxy so admin pages get auth.
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/docs/screenshots"
SCRIPT="$ROOT/scripts/screenshot.mjs"
mkdir -p "$OUT_DIR"

shot() {
  local url="$1" out="$2" width="${3:-1440}" height="${4:-1024}"
  echo "Capturing $url -> $(basename "$out")"
  node "$SCRIPT" "$url" "$out" "$width" "$height" \
    || echo "  WARN: failed to capture $url" >&2
}

# ── Public pages ──────────────────────────────────────────────────────────────
shot "$BASE_URL/"                                 "$OUT_DIR/homepage.png"
shot "$BASE_URL/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/article-page.png"

# ── Operator console (auth injected by screenshot-proxy) ─────────────────────
shot "$BASE_URL/admin"                            "$OUT_DIR/admin-dashboard.png"
shot "$BASE_URL/admin/theme"                      "$OUT_DIR/theme-panel.png"
shot "$BASE_URL/admin/modes"                      "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/admin/policy"                     "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/admin/topology"                   "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/admin/replay"                     "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/admin/faults"                     "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/admin/adr"                        "$OUT_DIR/adr-registry.png"

echo "Done. Screenshots in $OUT_DIR"
