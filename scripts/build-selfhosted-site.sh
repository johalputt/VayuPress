#!/usr/bin/env bash
#
# build-selfhosted-site.sh — turn docs/site/ into a bundle a VayuPress install
# can serve, and produce the .zip the panel's website upload accepts.
#
# WHY THIS EXISTS. docs/site/ was written for GitHub Pages, where a page may pull
# whatever it likes from wherever it likes and run whatever it wants inline. A
# VayuPress install is the opposite: every response carries
#
#	script-src 'self' 'nonce-…'; style-src 'self'; font-src 'self'
#
# so the same files, uploaded unchanged, render as unstyled text with every
# control dead — the CSS framework, the reactive library, the typefaces, every
# <style> block and every inline <script> are all refused, and nothing on screen
# says why.
#
# WHAT THE FIRST VERSION GOT WRONG, because it is the reason this one exists.
# It rewrote index.html and nothing else, and then "verified" the result by
# checking index.html for surviving off-origin subresources. The bundle shipped
# with about.html, vayuweb.html, sponsors/index.html and vayumail/privacy/
# index.html still pointing at the CDN — four of six pages rendering as unstyled
# text — and the check passed, because it was reading the one file that had been
# fixed. A gate that inspects only what you already repaired is not a gate.
#
# So this script now processes EVERY .html file in the bundle, and the final
# check reads every file it produced rather than the one it started from.
#
# WHAT IT DOES to each page:
#   1. the utility-CSS framework  → the precompiled first-party stylesheet
#   2. the inline `tailwind.config` → dropped (its palette is already compiled in),
#      but any OTHER statement in that same block is kept and externalised
#   3. Google Fonts               → the same families, served from this origin
#   4. the reactive library       → the copy shipped in the binary
#   5. every <style> block        → an external stylesheet, linked IN PLACE so
#                                   the cascade order is unchanged
#   6. every inline <script>      → an external script, linked IN PLACE so the
#                                   execution order is unchanged
#      (JSON-LD is left alone: it is data, never executed, and not script-src's
#      business.)
#
# ONE THING IT CANNOT FIX, stated here rather than discovered later. index.html
# drives its behaviour through Alpine expression STRINGS written in attributes
# (x-show="t > i" and ~250 others). Alpine's standard build compiles those with
# new Function(), which is eval, and the baseline policy refuses it. So the
# bundle needs the per-domain opt-in:
#
#	VayuOS → the domain → Website → "Allow this site to use eval"
#
# Without it the layout and typography are correct and the animations are inert.
# With it, the page behaves exactly as it was written to.
#
# Usage:  bash scripts/build-selfhosted-site.sh [outdir]
set -euo pipefail

SRC="docs/site"
OUT="${1:-dist/selfhosted-site}"

[ -f "${SRC}/index.html" ] || { echo "error: ${SRC}/index.html not found — run from the repo root" >&2; exit 1; }

rm -rf "$OUT"
mkdir -p "$OUT"
cp -R "${SRC}/." "$OUT/"

# Repository housekeeping that is not part of the site, and MUST NOT travel with
# it. README.md is the worked example and the reason this line exists: a
# VayuPress deploy accepts a fixed list of static-web extensions, `.md` is not on
# it, and ONE disallowed file rejects the entire upload. So a bundle carrying a
# readme could not be deployed at all — and from the operator's side that looked
# like pressing Upload and having nothing change, with the previous site still
# live. Two days of "the clone does not match" was this.
rm -f "${OUT}/README.md" "${OUT}/readme.md" "${OUT}/.gitignore" "${OUT}/.nojekyll"

# The gallery. Its 31 images live in docs/screenshots/, OUTSIDE docs/site/, and
# the Pages workflow used to copy them into the published tree — so there the
# gallery works and nothing in docs/site/ records the dependency.
#
# This script copied docs/site/ alone, so every self-hosted bundle shipped a
# gallery of 31 broken images. Same shape as the failure in the header: the part
# that was checked was correct, and the part nobody thought to check was the
# product. The asset gate below now fails the build if a referenced file is
# missing, so this cannot regress silently again.
if [ -d docs/screenshots ]; then
  mkdir -p "${OUT}/screenshots"
  cp docs/screenshots/*.png "${OUT}/screenshots/" 2>/dev/null || true
  echo "copied $(ls -1 "${OUT}/screenshots" | wc -l | tr -d ' ') gallery image(s)"
fi

python3 - "$OUT" <<'PY'
import re, sys, pathlib

out = pathlib.Path(sys.argv[1])
pages = sorted(out.rglob("*.html"))
if not pages:
    raise SystemExit("error: no HTML in the bundle")

# Root-absolute paths for everything generated here. The pages sit at three
# different depths (/, /sponsors/, /vayumail/privacy/) and already mix relative
# and absolute references; a bundle is served from the domain root, so "/assets/…"
# is correct from all three and cannot be got wrong by counting "../".
FONTS_CSS = "/assets/fonts.css"
TAILWIND  = "/static/vayuweb/tailwind.css"
ALPINE    = "/static/vayuweb/alpine.min.js"

# A `tailwind.config = {...};` assignment configures the browser-side JIT
# compiler, which is not present any more — its palette and font stacks are baked
# into the precompiled stylesheet. Dropping the assignment while KEEPING whatever
# else shares the block matters: vayuweb.html sets a `vw-js` marker class in the
# same script, and its stylesheet hides the reveal targets when that class is
# present. Drop the marker and keep the CSS and the page is blank.
TW_CONFIG = re.compile(r'\btailwind\.config\s*=\s*\{.*?\}\s*;', re.S)

def slug(page):
    rel = page.relative_to(out).as_posix()
    return re.sub(r'[^a-z0-9]+', '-', rel.rsplit('.', 1)[0].lower()).strip('-') or "index"

total = {"style": 0, "script": 0, "pages": 0}

for page in pages:
    html = page.read_text(encoding="utf-8")
    before = html
    name = slug(page)

    # 1. The framework arrives as a browser JIT compiler from a third party. The
    #    binary serves a stylesheet precompiled from these very pages, which is
    #    both first-party and far smaller than compiling in the browser on every
    #    visit.
    html = re.sub(r'<script src="https://cdn\.tailwindcss\.com"></script>',
                  '<link rel="stylesheet" href="%s" />' % TAILWIND, html, count=1)

    # 2. Typefaces. Same families, served from this origin.
    html = re.sub(r'\s*<link rel="preconnect"[^>]*fonts\.(googleapis|gstatic)\.com[^>]*/?>', '', html, flags=re.I)
    html = re.sub(r'<link[^>]*fonts\.googleapis\.com/css2[^>]*/?>',
                  '<link rel="stylesheet" href="%s" />' % FONTS_CSS, html, count=1, flags=re.I)

    # 3. The reactive library, from the copy shipped in the binary. This is the
    #    STANDARD build — see the note at the top about why it needs the opt-in.
    html = re.sub(r'<script defer src="https://unpkg\.com/alpinejs[^"]*"></script>',
                  '<script defer src="%s"></script>' % ALPINE, html, count=1)

    # 4. Inline <style> → an external stylesheet, substituted AT THE SAME POSITION
    #    so nothing about the cascade changes.
    styles = []
    def take_style(m):
        styles.append(m.group(1))
        href = "/assets/inline-%s-%d.css" % (name, len(styles))
        return '<link rel="stylesheet" href="%s" />' % href
    html = re.sub(r'<style[^>]*>(.*?)</style>', take_style, html, flags=re.S)
    for i, css in enumerate(styles, 1):
        p = out / "assets" / ("inline-%s-%d.css" % (name, i))
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text("/* Lifted out of %s. style-src is 'self', so a <style> block\n"
                     "   in the page would be refused and the rules silently lost. */\n%s\n"
                     % (page.relative_to(out).as_posix(), css.strip()), encoding="utf-8")

    # 5. Inline <script> → an external script, again in place so execution order
    #    is untouched. JSON-LD is skipped: it is data, the browser never executes
    #    it, and script-src does not apply to it.
    scripts = []
    def take_script(m):
        attrs, body = m.group(1), m.group(2)
        if 'ld+json' in attrs or 'application/json' in attrs:
            return m.group(0)
        body = TW_CONFIG.sub('', body)
        if not body.strip():
            return ''                      # the block held nothing but the config
        scripts.append(body)
        src = "/assets/inline-%s-%d.js" % (name, len(scripts))
        keep = ' defer' if re.search(r'\bdefer\b', attrs) else ''
        return '<script%s src="%s"></script>' % (keep, src)
    html = re.sub(r'<script((?![^>]*\bsrc=)[^>]*)>(.*?)</script>', take_script, html, flags=re.S)
    for i, js in enumerate(scripts, 1):
        p = out / "assets" / ("inline-%s-%d.js" % (name, i))
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text("/* Lifted out of %s. script-src is 'self' plus a per-request\n"
                     "   nonce, and a static bundle cannot carry a nonce, so an inline\n"
                     "   block in the page would simply never run. */\n%s\n"
                     % (page.relative_to(out).as_posix(), js.strip()), encoding="utf-8")

    if html != before:
        total["pages"] += 1
    total["style"] += len(styles)
    total["script"] += len(scripts)
    page.write_text(html, encoding="utf-8")

# The @font-face block, pointing at the allowlisted first-party woff2 files.
faces = []
for fam, s, weights in (
    ("Space Grotesk", "space-grotesk", (400, 500, 600, 700)),
    ("Inter", "inter", (300, 400, 500, 600)),
    ("JetBrains Mono", "jetbrains-mono", (400, 500)),
):
    for w in weights:
        faces.append('@font-face{font-family:"%s";'
                     'src:url("/static/fonts/%s-latin-%d.woff2") format("woff2");'
                     'font-weight:%d;font-style:normal;font-display:swap}' % (fam, s, w, w))
(out / "assets" / "fonts.css").write_text(
    "/* Self-hosted faces. font-src is 'self', so these are served by the binary\n"
    "   from its embedded copies rather than fetched from a font host. */\n"
    + "\n".join(faces) + "\n", encoding="utf-8")

# ── The gate. It reads EVERY file this script produced, because the version that
#    read only the file it had just fixed is how four broken pages shipped. ──────
problems = []
for page in sorted(out.rglob("*.html")):
    rel = page.relative_to(out).as_posix()
    s = page.read_text(encoding="utf-8")

    for m in re.finditer(r'<(?:script|link)[^>]*(?:src|href)="https?://[^"]+"', s):
        tag = m.group(0)
        if 'rel="canonical"' in tag or 'rel="alternate"' in tag:
            continue     # a link to a URL, not a subresource fetched by the browser
        problems.append("%s: off-origin subresource — %s" % (rel, tag))

    if re.search(r'<style[^>]*>', s):
        problems.append("%s: a <style> block survived; style-src 'self' refuses it "
                        "and the rules are silently lost" % rel)

    for m in re.finditer(r'<script((?![^>]*\bsrc=)[^>]*)>(.*?)</script>', s, re.S):
        if 'ld+json' in m.group(1) or 'application/json' in m.group(1):
            continue
        if m.group(2).strip():
            problems.append("%s: an inline <script> survived; it can never run "
                            "without a nonce this bundle cannot carry" % rel)

# ── Every same-origin asset a page asks for must BE in the bundle ────────────
#
# The checks above prove nothing is fetched off-origin. They say nothing about
# whether what IS referenced exists, and that is how a gallery of 31 broken
# images shipped: the files sat in docs/screenshots/, which only the Pages
# workflow ever copied. A 404 and a policy refusal look identical to a visitor —
# a gap where a picture should be — and neither announces itself.
#
# app.js is read too, not just the markup: the gallery is a list of paths in a
# JavaScript array, so a check confined to HTML would have missed all 31.
referenced = set()
# The lookbehind is load-bearing: `:href="l.href"` and `x-bind:src="s.src"` are
# Alpine EXPRESSIONS, not URLs, and a bare (src|href)= pattern matches the tail
# of both and then reports `l.href` as a missing file.
REF = re.compile(r'(?<![:\w-])(?:src|href)="([^"]+)"')
for page in sorted(out.rglob("*.html")):
    referenced.update((page.relative_to(out).parent, u) for u in REF.findall(page.read_text(encoding="utf-8")))
for js in sorted(out.rglob("*.js")):
    text = js.read_text(encoding="utf-8")
    for u in re.findall(r"['\"]((?:/|\./)?(?:assets|screenshots)/[A-Za-z0-9._/-]+\.[a-z0-9]{2,5})['\"]", text):
        referenced.add((pathlib.PurePosixPath("."), u))

for base, url in sorted(referenced):
    if re.match(r'^(?:https?:)?//|^(?:data|mailto|tel|javascript):|^#', url):
        continue
    clean = url.split("?", 1)[0].split("#", 1)[0]
    if not clean:
        continue
    # A root-absolute path the BINARY serves (/static/…, /media/…, /docs/…) is
    # not this bundle's to carry, and asserting on it would fail every build.
    if clean.startswith(("/static/", "/media/", "/docs/", "/blog", "/api/")):
        continue
    target = (out / clean.lstrip("/")) if clean.startswith("/") else (out / base / clean)
    try:
        exists = target.resolve().is_file() or (target / "index.html").is_file()
    except OSError:
        exists = False
    if not exists:
        problems.append("missing asset: %s is referenced but not in the bundle" % clean)

# ── Every utility class a page uses must EXIST in the compiled stylesheet ────
#
# Unbundled, the framework is a browser-side JIT: it reads the markup and
# generates whatever class it finds, so any class an author writes simply works.
# The bundle swaps that for a stylesheet compiled once from a fixed set. A class
# the compiler never saw therefore does nothing at all — and does it silently, on
# one breakpoint, in one component, which is close to unfindable by looking.
#
# Found the hard way: changing a button from `sm:inline-flex` to `lg:inline-flex`
# — a class the compiled sheet does not carry — left it `hidden` at every width,
# so the Sponsor button vanished from the bundle and from nothing else.
#
# Project classes live in assets/style.css, so both files count as coverage.
tw_path = pathlib.Path("static/vayuweb/tailwind.css")
local_css = "\n".join(p.read_text(encoding="utf-8") for p in (out / "assets").rglob("*.css"))
if tw_path.is_file():
    have = tw_path.read_text(encoding="utf-8") + "\n" + local_css

    def escape_class(c):
        return re.sub(r'([:\/\[\]\.\(\)%,#])', r'\\\1', c)

    seen = set()
    for page in sorted(out.rglob("*.html")):
        # Plain class="…" only. `:class="a ? 'x' : 'y'"` is an expression, and
        # treating its fragments as class names reports quotes and operators.
        for m in re.finditer(r'(?<![:\w-])class="([^"]*)"', page.read_text(encoding="utf-8")):
            for c in m.group(1).split():
                if re.fullmatch(r'[a-z][a-zA-Z0-9:_\/\.\[\]\(\)%,#-]*', c):
                    seen.add(c)
    absent = sorted(c for c in seen if ("." + escape_class(c)) not in have)
    if absent:
        problems.append(
            "%d class(es) used in the markup have no rule in the compiled stylesheet or "
            "assets/style.css, so they do nothing: %s" % (len(absent), ", ".join(absent)))

# Every file must carry an extension the deploy accepts, or the upload is
# refused in full and the operator is left with their previous site and no idea
# why. Checked here, at build time, where it is a one-line fix rather than a
# mystery on someone's server.
ALLOWED = {
    ".html", ".htm", ".css", ".js", ".mjs", ".json", ".txt", ".xml", ".map",
    ".webmanifest", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif",
    ".ico", ".bmp", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".pdf", ".mp4",
    ".webm", ".mp3", ".ogg", ".csv",
}
for f in sorted(out.rglob("*")):
    if not f.is_file():
        continue
    if f.suffix.lower() not in ALLOWED:
        problems.append("%s: extension %r is not one a VayuPress deploy accepts — "
                        "ONE such file rejects the whole upload"
                        % (f.relative_to(out).as_posix(), f.suffix or "(none)"))

if problems:
    print("error: the bundle would not render correctly under the install's policy:", file=sys.stderr)
    for p in problems:
        print("   " + p, file=sys.stderr)
    raise SystemExit(1)

print("rewrote %d page(s); externalised %d <style> block(s) and %d inline script(s); wrote assets/fonts.css"
      % (total["pages"], total["style"], total["script"]))
print("checked all %d page(s): no off-origin subresource, no <style> block, no inline script"
      % len(list(out.rglob("*.html"))))
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
