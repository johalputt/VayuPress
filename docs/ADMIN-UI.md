# Admin UI

VayuPress ships two admin experiences during the redesign transition:

| Path | Status | Description |
|------|--------|-------------|
| `/admin` | Stable | The original server-rendered console (untouched) |
| `/admin/v2` | New | Modern, editor-first redesign on a CSP-compliant vendored stack |

`/admin/v2` is **additive and non-breaking** — the legacy panel keeps working
while the new one matures. See
[ADR-0065](adr/ADR-0065-admin-ui-csp-compliant-stack.md) for the design rationale.

---

## Design constraints (why it's built the way it is)

The admin runs under a strict Content-Security-Policy (ADR-0036):

```
default-src 'self'; style-src 'self'; script-src 'self' 'nonce-<nonce>'; img-src 'self' data:
```

This forbids external CDNs, `unsafe-eval`, and `unsafe-inline`. So:

- **Tailwind** is *precompiled* to `static/css/admin-v2.css` (no CDN, no inline
  `<style>`, no `style="…"` attributes).
- **Alpine.js** uses the *CSP build* (`@alpinejs/csp`), vendored locally —
  components register as JS objects, never eval'd from attribute strings.
- Every inline `<script>` carries the per-request CSP nonce.
- Fonts (Space Grotesk, Inter) are self-hosted woff2 under `font-src 'self'`,
  with a system-font fallback.

If you re-theme, keep these invariants or the CSP will (correctly) block your
assets.

---

## Design system

| Token | Value |
|-------|-------|
| Background | `#0f172a` |
| Surface | `#1e293b` |
| Text | `#f1f5f9` |
| Primary (teal) | `#0d9488` / `#2dd4bf` |
| Accent (saffron) | `#f59e0b` |
| Headings | Space Grotesk |
| Body | Inter |

---

## Pages

| Route | Purpose |
|-------|---------|
| `/admin/v2` | Dashboard — stats, recent activity, quick actions |
| `/admin/v2/login` | Sign-in |
| `/admin/v2/posts` | Posts list with search & filters |
| `/admin/v2/editor` | New post editor |
| `/admin/v2/editor/{slug}` | Edit existing post |
| `/admin/v2/settings` | Settings + read-only update checker |

---

## The editor

The editor is the centrepiece. All interactivity is CSP-safe (no `eval`):

- **Multi-format authoring** — a segmented switch toggles between **Markdown**
  and **raw HTML**. Whichever you choose, the editor renders the same live
  preview, and the *published* content is always HTML — Markdown is converted on
  save, raw HTML is passed straight through. Every save is sanitised by the
  server (bluemonday) before it reaches the public renderer, so neither mode can
  introduce an XSS vector. The chosen format and your editable source are
  persisted in a side-car (`article_sources`) so reopening a post restores the
  exact text you wrote, not a lossy round-trip. Legacy posts (no side-car yet)
  open as HTML seeded from their stored content.
- **Split-view live preview** — source on the left, rendered HTML on the right.
  Toggle the preview off (`Ctrl/⌘+P`) to write full-width.
- **Distraction-free / focus mode** (`Ctrl/⌘+.`) — hides all chrome and switches
  to a calmer, larger measure for long-form writing.
- **Slash commands** — type `/` for a **filterable, keyboard-navigable** palette
  (↑/↓ to move, Enter to insert, Esc to dismiss): headings, image, code block,
  quote, callout, lists, table, divider. Typing after the `/` filters the list,
  and the typed query is replaced by the inserted block.
- **Formatting toolbar + keyboard shortcuts** — bold (`Ctrl/⌘+B`), italic
  (`Ctrl/⌘+I`), link (`Ctrl/⌘+K`), plus heading / quote / list / code. `Tab`
  indents instead of leaving the field.
- **Inline image upload** — click the image button, **drag-&-drop** an image onto
  the editor, or **paste** an image from the clipboard. Images upload to the
  sovereign, same-origin endpoint `POST /api/v1/admin/media` (magic-number
  validated, content-addressed) and the Markdown is inserted automatically.
- **Live stats** — word count, character count, reading time.
- **SEO preview + readiness meter** — title / slug / 160-char description preview
  plus a 0–100 score that reacts to title length, word count, slug, headings,
  and images, with an actionable hint.
- **Autosave** — debounced and triggered on edit or `Ctrl/⌘+S`, with a live
  "Saving…/Saved" status. Each save writes two CSRF-protected requests in
  parallel: the editable source + format to
  `PUT /api/v1/admin/articles/{slug}/source`, and the rendered, publishable HTML
  to `PUT /api/v1/articles/{slug}`. On a brand-new post the first save `POST`s to
  `/api/v1/articles` and redirects to the permanent editor URL so autosave can
  continue. A `beforeunload` guard warns before leaving with unsaved edits, and
  the slug auto-derives from the title until you edit it.
- **Version history** — reads `GET /api/v1/admin/articles/{slug}/versions`
  (backed by `internal/versions`).

### Media storage

Editor image uploads are written to `MEDIA_DIR` (env var, default
`/var/lib/vayupress/media`) and served same-origin from `/media/{file}`. Only
PNG / JPEG / GIF / WebP are accepted — SVG is refused because it can carry inline
script. Files are content-addressed (`<hash>.<ext>`), so duplicate uploads
collapse to one file and the path is never attacker-influenced.

---

## Assets

| File | Role |
|------|------|
| `static/css/admin-v2.css` | Precompiled styles (the only stylesheet) |
| `static/js/admin-v2.js` | CSP-safe interactivity (DOM APIs, no eval) |
| `static/js/alpine-csp.min.js` | *(operator-vendored)* Alpine CSP build |
| `static/fonts/*.woff2` | *(operator-droppable)* self-hosted fonts |

To vendor Alpine's CSP build for production, download `@alpinejs/csp` and place
the minified file at `static/js/alpine-csp.min.js`. The bundled `admin-v2.js`
works without it; Alpine enhances it.

---

## Promoting v2 to default

Once feature parity is confirmed, `/admin/v2` can be promoted to `/admin` by
swapping the route registration. Until then both coexist with no shared state.
