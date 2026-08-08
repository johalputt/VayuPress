#!/usr/bin/env bash
# Regenerate the VayuPress screenshots used by README.md and the marketing site.
#
# One source of truth: scripts/build-selfhosted-site.sh copies docs/screenshots/*.png
# into the uploaded bundle, so a page captured here appears in both places.
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
shot "$BASE_URL/pricing"                              "$OUT_DIR/member-pricing.png"

# ── VayuOS — the single control panel (auth injected by screenshot-proxy) ────
# VayuOS is the only admin (ADR-0068, ADR-0069). The editor opens with the
# seeded article so the live preview renders real content.
shot "$BASE_URL/os"                                   "$OUT_DIR/admin-os-dashboard.png"
shot "$BASE_URL/os/posts"                             "$OUT_DIR/admin-os-posts.png"
shot "$BASE_URL/os/editor/${ARTICLE_SLUG:-hello-vayupress}" "$OUT_DIR/admin-os-editor.png"
shot "$BASE_URL/os/theme"                             "$OUT_DIR/admin-os-theme.png"
shot "$BASE_URL/os/media"                             "$OUT_DIR/admin-os-media.png"
shot "$BASE_URL/os/seo"                               "$OUT_DIR/admin-os-seo.png"
shot "$BASE_URL/os/analytics"                         "$OUT_DIR/admin-os-analytics.png"
shot "$BASE_URL/os/shield"                            "$OUT_DIR/admin-os-shield.png"
shot "$BASE_URL/os/security"                          "$OUT_DIR/admin-os-security.png"
shot "$BASE_URL/os/settings"                          "$OUT_DIR/admin-os-settings.png"

# ── The ten products, each on its own page ───────────────────────────────────
# These are what "one binary, ten products" actually looks like, and every one
# of them was missing from the screenshot set.
shot "$BASE_URL/os/vayumail"                          "$OUT_DIR/admin-os-vayumail.png"
shot "$BASE_URL/os/talk"                              "$OUT_DIR/admin-os-vayutalk.png"
shot "$BASE_URL/os/spaces"                            "$OUT_DIR/admin-os-spaces.png"
shot "$BASE_URL/os/monetization"                      "$OUT_DIR/admin-os-monetization.png"
shot "$BASE_URL/os/members"                           "$OUT_DIR/admin-os-members.png"
shot "$BASE_URL/os/website"                           "$OUT_DIR/admin-os-website.png"
shot "$BASE_URL/os/domains"                           "$OUT_DIR/admin-os-domains.png"
shot "$BASE_URL/os/connector"                         "$OUT_DIR/admin-os-connector.png"

# ── Operations consoles — "operations as first-class surfaces" ───────────────
shot "$BASE_URL/os/modes"                         "$OUT_DIR/policy-modes.png"
shot "$BASE_URL/os/policy"                        "$OUT_DIR/policy-inspector.png"
shot "$BASE_URL/os/topology"                      "$OUT_DIR/runtime-topology.png"
shot "$BASE_URL/os/replay"                        "$OUT_DIR/replay-explorer.png"
shot "$BASE_URL/os/faults"                        "$OUT_DIR/fault-manager.png"
shot "$BASE_URL/os/adr"                           "$OUT_DIR/adr-registry.png"
shot "$BASE_URL/os/governance"                    "$OUT_DIR/admin-os-governance.png"
shot "$BASE_URL/os/monitoring"                    "$OUT_DIR/admin-os-monitoring.png"

echo "Done. Screenshots in $OUT_DIR"
