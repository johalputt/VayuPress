#!/usr/bin/env bash
# Regenerate the VayuPress screenshots referenced by README.md.
#
# Requires a headless Chromium/Chrome on PATH (chromium, chromium-browser,
# google-chrome, or chrome) — none is bundled with the repo, so run this on a
# machine or CI image that has a browser. Output PNGs overwrite the canonical
# images in docs/screenshots/ so the README always reflects the current UI.
#
# Usage:
#   API_KEY=... ./scripts/capture-screenshots.sh [BASE_URL]
#
# BASE_URL defaults to http://localhost:8080 and must point at a running
# VayuPress instance with at least one published article. The /admin pages need
# the API key; capture them from an authenticated session or front the instance
# with a proxy that injects the key for the screenshot host (Chrome headless
# cannot set custom request headers from the CLI).
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/docs/screenshots"
mkdir -p "$OUT_DIR"

find_browser() {
  for b in chromium chromium-browser google-chrome google-chrome-stable chrome; do
    if command -v "$b" >/dev/null 2>&1; then echo "$b"; return 0; fi
  done
  return 1
}

BROWSER="$(find_browser)" || {
  echo "ERROR: no Chromium/Chrome binary found on PATH." >&2
  echo "Install one (e.g. 'apt-get install chromium') and re-run." >&2
  exit 1
}

shot() {
  local url="$1" out="$2" width="${3:-1440}" height="${4:-1024}"
  echo "Capturing $url -> $(basename "$out")"
  "$BROWSER" --headless=new --disable-gpu --hide-scrollbars \
    --window-size="${width},${height}" --screenshot="$out" "$url" \
    || echo "  WARN: failed to capture $url" >&2
}

# ── Public pages (no auth) ────────────────────────────────────────────────────
shot "$BASE_URL/"                                 "$OUT_DIR/homepage.png"
shot "$BASE_URL/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/article-page.png"
shot "$BASE_URL/health"                           "$OUT_DIR/health-observability.png"

# ── Operator console (auth injected by screenshot-proxy or session) ───────────
shot "$BASE_URL/admin"                            "$OUT_DIR/admin-dashboard.png"
shot "$BASE_URL/admin/theme"                      "$OUT_DIR/theme-panel.png"
shot "$BASE_URL/admin/modes"                      "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/admin/policy"                     "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/admin/topology"                   "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/admin/replay"                     "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/admin/faults"                     "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/admin/adr"                        "$OUT_DIR/adr-registry.png"

# ── API / observability endpoints (JSON, shown in browser) ───────────────────
shot "$BASE_URL/api/v1/admin/traces"              "$OUT_DIR/traces-metrics.png"
shot "$BASE_URL/api/v1/queue"                     "$OUT_DIR/queue-events.png"

echo "Done. Screenshots in $OUT_DIR"
