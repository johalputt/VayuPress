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

# ── Auth surfaces ────────────────────────────────────────────────────────────
# The VayuOS sign-in page and the public reader/member signup page.
shot "$BASE_URL/os/login"                             "$OUT_DIR/os-login.png"
shot "$BASE_URL/signup"                               "$OUT_DIR/member-signup.png"

# ── VayuOS — the single control panel (auth injected by screenshot-proxy) ────
# VayuOS is the only admin (ADR-0068, ADR-0069): cohesive design system,
# dashboard intelligence, block editor (with AI-assist and version-history
# diff), native Theme Studio, media library, members, security (2FA), SEO and
# analytics. The editor opens with the seeded article so the live preview
# renders real content.
shot "$BASE_URL/os"                                   "$OUT_DIR/admin-os-dashboard.png"
shot "$BASE_URL/os/posts"                             "$OUT_DIR/admin-os-posts.png"
shot "$BASE_URL/os/editor/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/admin-os-editor.png"
shot "$BASE_URL/os/theme"                             "$OUT_DIR/admin-os-theme.png"
shot "$BASE_URL/os/media"                             "$OUT_DIR/admin-os-media.png"
shot "$BASE_URL/os/seo"                               "$OUT_DIR/admin-os-seo.png"
shot "$BASE_URL/os/analytics"                         "$OUT_DIR/admin-os-analytics.png"
shot "$BASE_URL/os/security"                          "$OUT_DIR/admin-os-security.png"
shot "$BASE_URL/os/monitoring"                        "$OUT_DIR/admin-os-monitoring.png"
shot "$BASE_URL/os/governance"                        "$OUT_DIR/admin-os-governance.png"
shot "$BASE_URL/os/tools"                             "$OUT_DIR/admin-os-tools.png"
shot "$BASE_URL/os/settings"                          "$OUT_DIR/admin-os-settings.png"

# ── Operator consoles — now inside the VayuOS shell at /os/* ─────────────────
shot "$BASE_URL/os/modes"                         "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/os/policy"                        "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/os/topology"                      "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/os/replay"                        "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/os/faults"                        "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/os/adr"                           "$OUT_DIR/adr-registry.png"

echo "Done. Screenshots in $OUT_DIR"
