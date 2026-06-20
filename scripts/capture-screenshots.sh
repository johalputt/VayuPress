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

# ── Modern admin UI — Admin v2 (auth injected by screenshot-proxy) ───────────
# The editor-first redesign lives under /admin/v2 (ADR-0065). Capture the
# dashboard, posts list, and the editor (loaded with the seeded article so the
# split-view live preview renders real content).
shot "$BASE_URL/admin/v2"                                   "$OUT_DIR/admin-v2-dashboard.png"
shot "$BASE_URL/admin/v2/posts"                             "$OUT_DIR/admin-v2-posts.png"
shot "$BASE_URL/admin/v2/editor/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/admin-v2-editor.png"
shot "$BASE_URL/admin/v2/seo"                               "$OUT_DIR/admin-v2-seo.png"
shot "$BASE_URL/admin/v2/settings"                          "$OUT_DIR/admin-v2-settings.png"

# ── Next-generation admin UI — Admin v3 (auth injected by screenshot-proxy) ──
# The ground-up redesign lives under /admin/v3 (ADR-0068): design system,
# dashboard intelligence, block editor, media library, members, security (2FA),
# SEO, and analytics. The editor opens with the seeded article.
shot "$BASE_URL/admin/v3"                                   "$OUT_DIR/admin-v3-dashboard.png"
shot "$BASE_URL/admin/v3/posts"                             "$OUT_DIR/admin-v3-posts.png"
shot "$BASE_URL/admin/v3/editor/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/admin-v3-editor.png"
shot "$BASE_URL/admin/v3/media"                             "$OUT_DIR/admin-v3-media.png"
shot "$BASE_URL/admin/v3/seo"                               "$OUT_DIR/admin-v3-seo.png"
shot "$BASE_URL/admin/v3/analytics"                         "$OUT_DIR/admin-v3-analytics.png"
shot "$BASE_URL/admin/v3/security"                          "$OUT_DIR/admin-v3-security.png"
shot "$BASE_URL/admin/v3/settings"                          "$OUT_DIR/admin-v3-settings.png"

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
