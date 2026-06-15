#!/usr/bin/env bash
# Regenerate the VayuPress screenshots referenced by README.md.
#
# Requires Node.js + puppeteer-core (npm install puppeteer-core) and a system
# Chromium/Chrome on PATH. Run via CI with the screenshots workflow, or locally:
#
#   npm install puppeteer-core
#   API_KEY=... ./scripts/capture-screenshots.sh [BASE_URL]
#
# Admin pages need the API key injected — use scripts/screenshot-proxy to front
# the server so Chrome can reach authenticated /admin pages without custom headers.
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

# ── Public pages (no auth) ────────────────────────────────────────────────────
shot "$BASE_URL/"                                 "$OUT_DIR/homepage.png"
shot "$BASE_URL/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/article-page.png"
shot "$BASE_URL/health"                           "$OUT_DIR/health-observability.png"

# ── Operator console (auth injected by screenshot-proxy) ─────────────────────
shot "$BASE_URL/admin"                            "$OUT_DIR/admin-dashboard.png"
shot "$BASE_URL/admin/theme"                      "$OUT_DIR/theme-panel.png"
shot "$BASE_URL/admin/modes"                      "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/admin/policy"                     "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/admin/topology"                   "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/admin/replay"                     "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/admin/faults"                     "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/admin/adr"                        "$OUT_DIR/adr-registry.png"

# ── API / observability endpoints ────────────────────────────────────────────
shot "$BASE_URL/api/v1/admin/traces"              "$OUT_DIR/traces-metrics.png"
shot "$BASE_URL/api/v1/queue"                     "$OUT_DIR/queue-events.png"

echo "Done. Screenshots in $OUT_DIR"
