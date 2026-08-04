#!/usr/bin/env bash
#
# build-tailwind-css.sh — regenerate static/vayuweb/tailwind.css from docs/site/.
#
# WHY THIS FILE EXISTS AT ALL. The marketing pages are authored against the
# utility-CSS framework's browser JIT build, which compiles the classes it finds
# in the DOM at page load, from a third-party host. A VayuPress install serves
# every response under `style-src 'self'`, so that script is refused and the page
# renders as unstyled text. The binary therefore ships a PRECOMPILED stylesheet
# and the bundle points at that instead.
#
# WHY IT IS A SCRIPT AND NOT A CHECKED-IN BLOB NOBODY CAN REPRODUCE. The first
# precompiled stylesheet was generated once, by hand, from index.html — and then
# the pages moved on. It was missing utilities that four of the six pages
# actually use (`bg-saffron-600/15`, `bg-steel-100`, `border-b`, `pt-12`,
# `hover:-translate-y-px`, every arbitrary-value class such as `bg-[#05070d]`),
# so those pages rendered subtly and then not-so-subtly wrong, with nothing
# anywhere reporting a problem. A generated artefact with no recorded way to
# regenerate it is a file that silently goes stale.
#
# THE CONFIG BELOW IS THE UNION of the `tailwind.config` blocks the pages declare
# inline: index.html, about.html and vayumail/privacy/index.html share one, and
# vayuweb.html adds the `steel` ramp. If you add a colour or a font to a page's
# inline config, add it here too — the inline block is dropped from the bundle
# (its compiler is gone) and this file is what replaces it.
#
# Requires network access to the npm registry.
#
# Usage:  bash scripts/build-tailwind-css.sh
set -euo pipefail

VERSION="3.4.19"      # the major the pages are authored against
OUT="static/vayuweb/tailwind.css"
SITE="docs/site"

[ -f "${SITE}/index.html" ] || { echo "error: run from the repo root" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "error: npm is required to compile the stylesheet" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cat > "${WORK}/tailwind.config.js" <<'JS'
module.exports = {
  content: [],   // supplied on the command line, so this file stays path-agnostic
  theme: {
    extend: {
      colors: {
        teal:    { 300:'#5eead4',400:'#2dd4bf',500:'#14b8a6',600:'#0d9488',700:'#0f766e',800:'#115e59',900:'#042f2e' },
        saffron: { 300:'#fcd34d',400:'#fbbf24',600:'#d97706' },
        ink:     { 700:'#10172a',800:'#0a1020',900:'#070b16',950:'#04060d' },
        steel:   { 100:'#f2f5f9',200:'#dbe2ec',300:'#b8c4d4',400:'#8b9aae',500:'#64748b',600:'#475569',700:'#334155',800:'#1e293b' },
      },
      fontFamily: {
        display: ['"Space Grotesk"','Inter','system-ui','sans-serif'],
        sans:    ['Inter','system-ui','sans-serif'],
        mono:    ['"JetBrains Mono"','monospace'],
      },
      letterSpacing: { tightest:'-0.045em' },
    },
  },
};
JS

printf '@tailwind base;\n@tailwind components;\n@tailwind utilities;\n' > "${WORK}/in.css"

(cd "$WORK" && npm install --silent --no-audit --no-fund "tailwindcss@${VERSION}" >/dev/null)

# EVERY page plus assets/app.js. app.js matters because the interactive page
# assembles class strings in JavaScript, and a utility that only ever appears
# inside a template literal is still a utility the page needs.
CONTENT=""
while IFS= read -r f; do
  CONTENT="${CONTENT}${CONTENT:+,}$(cd "$(dirname "$f")" && pwd)/$(basename "$f")"
done < <(find "$SITE" -name '*.html' -o -name '*.js' | sort)

"${WORK}/node_modules/.bin/tailwindcss" \
  -c "${WORK}/tailwind.config.js" -i "${WORK}/in.css" -o "${WORK}/out.css" \
  --content "$CONTENT" --minify 2>&1 | grep -vE '^\s*$|Rebuilding|Done in|browserslist|caniuse|npx update-browserslist|Why you should' || true

[ -s "${WORK}/out.css" ] || { echo "error: the compiler produced nothing" >&2; exit 1; }

# A stylesheet that shrank dramatically means the content globs stopped matching
# — the exact failure that shipped, and one that looks like success otherwise.
if [ -f "$OUT" ]; then
  old=$(wc -c < "$OUT"); new=$(wc -c < "${WORK}/out.css")
  if [ "$new" -lt $(( old / 2 )) ]; then
    echo "error: the new stylesheet is ${new} bytes against ${old} before — the content globs" >&2
    echo "       are probably not matching the pages any more. Not overwriting." >&2
    exit 1
  fi
fi

mkdir -p "$(dirname "$OUT")"
cp "${WORK}/out.css" "$OUT"
echo "✓ ${OUT}  ($(wc -c < "$OUT") bytes, tailwindcss ${VERSION}, $(echo "$CONTENT" | tr ',' '\n' | wc -l) source files)"
echo
echo "Rebuild the site bundle so it ships against this stylesheet:"
echo "  bash scripts/build-selfhosted-site.sh"
