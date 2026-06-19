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

- **Split-view live preview** — Markdown on the left, rendered HTML on the right.
- **Distraction-free mode** — toggles a body class to hide chrome.
- **Slash commands** — type `/` for a palette: image, code block, quote, table,
  callout, heading.
- **Formatting toolbar** — bold / italic / heading / link / code wrap the
  current selection.
- **Live stats** — word count, reading time, and an SEO preview (title, slug,
  first 160 characters).
- **Autosave** — debounced `PUT /api/v1/articles/{slug}` using the existing CSRF
  handshake, with a "Saving…/Saved" toast.
- **Version history** — reads `GET /api/v1/admin/articles/{slug}/versions`
  (backed by `internal/versions`).

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
