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

# ── VayuOS — the single Admin v3 (auth injected by screenshot-proxy) ─────────
# As of v1.5.0 the v1/v2 admin surfaces are consolidated into Admin v3 (ADR-0068,
# ADR-0069): cohesive design system, dashboard intelligence, block editor (with
# AI-assist and version-history diff), native Theme Studio, media library,
# members, security (2FA), SEO and analytics. The editor opens with the seeded
# article so the live preview renders real content.
shot "$BASE_URL/os"                                   "$OUT_DIR/admin-v3-dashboard.png"
shot "$BASE_URL/os/posts"                             "$OUT_DIR/admin-v3-posts.png"
shot "$BASE_URL/os/editor/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/admin-v3-editor.png"
shot "$BASE_URL/os/theme"                             "$OUT_DIR/admin-v3-theme.png"
shot "$BASE_URL/os/media"                             "$OUT_DIR/admin-v3-media.png"
shot "$BASE_URL/os/seo"                               "$OUT_DIR/admin-v3-seo.png"
shot "$BASE_URL/os/analytics"                         "$OUT_DIR/admin-v3-analytics.png"
shot "$BASE_URL/os/security"                          "$OUT_DIR/admin-v3-security.png"
shot "$BASE_URL/os/settings"                          "$OUT_DIR/admin-v3-settings.png"

# ── Operator console (auth injected by screenshot-proxy) ─────────────────────
shot "$BASE_URL/admin/modes"                      "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/admin/policy"                     "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/admin/topology"                   "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/admin/replay"                     "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/admin/faults"                     "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/admin/adr"                        "$OUT_DIR/adr-registry.png"

echo "Done. Screenshots in $OUT_DIR"
