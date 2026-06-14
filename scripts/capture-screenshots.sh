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
  local url="$1" out="$2"
  echo "Capturing $url -> $(basename "$out")"
  "$BROWSER" --headless=new --disable-gpu --hide-scrollbars \
    --window-size=1440,1024 --screenshot="$out" "$url" \
    || echo "  WARN: failed to capture $url (auth required?)" >&2
}

# Public pages (no auth).
shot "$BASE_URL/"                 "$OUT_DIR/homepage.png"
# Article page: pass a real slug as ARTICLE_SLUG, else the first listed article.
shot "$BASE_URL/${ARTICLE_SLUG:-}" "$OUT_DIR/article-page.png"

# Operator console (auth required — see header note).
shot "$BASE_URL/admin"            "$OUT_DIR/admin-dashboard.png"
shot "$BASE_URL/admin/modes"      "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/admin/topology"   "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/admin/replay"     "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/admin/policy"     "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/admin/theme"      "$OUT_DIR/theme-panel.png"

echo "Done. Screenshots in $OUT_DIR"
