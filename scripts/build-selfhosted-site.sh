#!/usr/bin/env bash
#
# build-selfhosted-site.sh — turn docs/site/ into a bundle a VayuPress install
# can serve, and produce the .zip the panel's website upload accepts.
#
# WHY THIS EXISTS. docs/site/ is published to GitHub Pages, where a page may pull
# whatever it likes from wherever it likes. A VayuPress install is the opposite:
# every response carries
#
#	script-src 'self' 'nonce-…'; style-src 'self'; font-src 'self'
#
# so the same files, uploaded unchanged, render as unstyled text with every
# control dead — the CSS framework, the reactive library and the typefaces are
# all refused, and nothing on screen says why. That is not a hypothetical; it is
# what happened the first time this site was copied onto a hosted domain.
#
# This script performs the only edits that difference requires: it repoints the
# three third-party subresources at the identical first-party copies the binary
# already serves. It changes no markup and no application code.
#
# ONE THING IT CANNOT FIX, and it is stated here rather than discovered later.
# The page drives its behaviour through Alpine expression strings written in
# attributes (x-show="t > i" and ~300 others). Alpine's standard build compiles
# those with new Function(), which is eval, and the baseline policy refuses it.
# So the bundle this produces needs the per-domain opt-in:
#
#	VayuOS → the domain → Website, or update_site(allow_eval: true)
#
# Without that the layout and typography are right and the animations are inert.
# With it, the page behaves exactly as it does on GitHub Pages.
#
# Usage:  bash scripts/build-selfhosted-site.sh [outdir]
set -euo pipefail

SRC="docs/site"
OUT="${1:-dist/selfhosted-site}"

[ -f "${SRC}/index.html" ] || { echo "error: ${SRC}/index.html not found — run from the repo root" >&2; exit 1; }

rm -rf "$OUT"
mkdir -p "$OUT"
cp -R "${SRC}/." "$OUT/"
rm -f "${OUT}/CNAME"   # the GitHub Pages custom domain must not travel with the bundle

python3 - "$OUT" <<'PY'
import re, sys, pathlib

out = pathlib.Path(sys.argv[1])
p = out / "index.html"
html = p.read_text(encoding="utf-8")
before = html

# 1. The utility-CSS framework arrives as a browser JIT compiler from a third
#    party. The binary serves a precompiled stylesheet built from this very page,
#    which is both first-party and dramatically smaller than compiling in the
#    browser on every visit.
html = re.sub(r'\s*<script src="https://cdn\.tailwindcss\.com"></script>',
              '\n  <link rel="stylesheet" href="/static/vayuweb/tailwind.css" />', html, count=1)

# 2. Its inline configuration block goes with it: the palette and font stacks are
#    already baked into the precompiled file, and a static bundle cannot carry the
#    per-request nonce an inline script would need.
html = re.sub(r'\s*<script>\s*tailwind\.config\s*=.*?</script>', '', html, count=1, flags=re.S)

# 3. Typefaces. Same families, served from this origin.
html = re.sub(r'\s*<link rel="preconnect"[^>]*fonts\.(googleapis|gstatic)\.com[^>]*/?>', '', html, flags=re.I)
html = re.sub(r'\s*<link[^>]*fonts\.googleapis\.com/css2[^>]*/?>',
              '\n  <link rel="stylesheet" href="assets/fonts.css" />', html, count=1, flags=re.I)

# 4. The reactive library, from the copy shipped in the binary. This is the
#    STANDARD build — see the note at the top about why it needs the opt-in.
html = re.sub(r'<script defer src="https://unpkg\.com/alpinejs[^"]*"></script>',
              '<script defer src="/static/vayuweb/alpine.min.js"></script>', html, count=1)

if html == before:
    print("warning: nothing was rewritten — has the head of index.html changed?", file=sys.stderr)

p.write_text(html, encoding="utf-8")

# The @font-face block, pointing at the allowlisted first-party woff2 files.
faces = []
for fam, slug, weights in (
    ("Space Grotesk", "space-grotesk", (400, 500, 600, 700)),
    ("Inter", "inter", (300, 400, 500, 600)),
    ("JetBrains Mono", "jetbrains-mono", (400, 500)),
):
    for w in weights:
        faces.append(
            '@font-face{font-family:"%s";'
            'src:url("/static/fonts/%s-latin-%d.woff2") format("woff2");'
            'font-weight:%d;font-style:normal;font-display:swap}' % (fam, slug, w, w)
        )
(out / "assets" / "fonts.css").write_text(
    "/* Self-hosted faces. font-src is 'self', so these are served by the binary\n"
    "   from its embedded copies rather than fetched from a font host. */\n"
    + "\n".join(faces) + "\n", encoding="utf-8")

# Refuse to ship a bundle that still reaches off-origin for a subresource: that
# is the exact failure this script exists to prevent, and it must not be
# something the operator discovers from a blank page.
leftovers = re.findall(r'<(?:script|link)[^>]*(?:src|href)="https?://[^"]+"', html)
leftovers = [l for l in leftovers if 'rel="canonical"' not in l and 'og:' not in l]
if leftovers:
    print("error: a third-party subresource survived the rewrite:", file=sys.stderr)
    for l in leftovers:
        print("   " + l, file=sys.stderr)
    raise SystemExit(1)

print("rewrote index.html and wrote assets/fonts.css")
PY

if command -v zip >/dev/null 2>&1; then
  (cd "$OUT" && zip -qr ../"$(basename "$OUT").zip" .)
  echo "✓ bundle: ${OUT}    zip: $(dirname "$OUT")/$(basename "$OUT").zip"
else
  echo "✓ bundle: ${OUT}    (zip not installed — upload the directory's contents)"
fi
echo
echo "Upload it in VayuOS → the domain → Website → Uploaded website,"
echo "and turn ON the eval opt-in for that domain or the animations stay inert."
