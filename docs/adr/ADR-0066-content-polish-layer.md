# ADR-0066 — Content Polish Layer: CSP-Safe Highlighting, Related Posts, PWA

**Status:** Accepted
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers
**Owner:** Core / Rendering

---

## Context

The public reading experience lagged the authoring experience. Operators asked
for the table-stakes polish modern publishing platforms ship: syntax-highlighted
code, "related articles," visible reading time, document (PDF) uploads, an email
nudge when a held comment is approved, and an installable/offline reading mode.

Every one of these had to be added **without** weakening the two invariants that
define VayuPress:

1. **Strict CSP (ADR-0036).** `style-src 'self'`, `script-src 'self' 'nonce-…'`,
   no `unsafe-inline`, no external origins. This rules out the usual client-side
   highlighters (which inject inline styles) and any CDN-hosted theme CSS.
2. **Server-sanitised HTML.** All article HTML passes through the bluemonday UGC
   policy before it reaches a reader. Nothing may bypass that sanitiser.

## Decision

Ship a **content polish layer** rendered entirely server-side, sovereign, and
CSP-clean.

### Syntax highlighting — highlight *before* sanitising, re-inject *after*

Highlighting is done at render time by **chroma** (`WithClasses(true)`,
`github-dark` theme) and served from a same-origin `/static/chroma.css` under
`style-src 'self'` — chroma emits only `class` attributes, never inline styles.

The ordering problem is subtle and was the source of a real bug caught in live
testing: bluemonday strips the `class="language-go"` hint we need to choose a
lexer, *and* would strip chroma's own `<span class="…">` output. So a naïve
"sanitise then highlight" pipeline produces no highlighting at all.

The resolution (`render.renderContentHTML`):

1. Extract each fenced code block from the **raw** content (language hint intact).
2. Highlight it with chroma into trusted, fully-escaped, class-only HTML.
3. Replace the block with an **unguessable per-render placeholder** token
   (`VAYUCODE<random-nonce><index>ENDVAYUCODE`).
4. Run bluemonday over the surrounding prose (placeholders survive as inert text).
5. Substitute the trusted chroma HTML back in for each placeholder.

Because the nonce is `crypto/rand`-derived per render and never derived from
content, article authors cannot forge a placeholder to smuggle unsanitised HTML
past the policy — the final substitution only replaces tokens the renderer itself
generated. This is covered by a placeholder-forgery regression test.

### Related articles — precise, comma-token matching

`relatedArticles` selects recent posts sharing ≥1 tag. A coarse SQL `LIKE`
pre-filter (with LIKE metacharacters escaped) narrows candidates; exact,
comma-delimited token matching then runs in Go so `go` never matches `golang`
and a tag containing `%`/`_` cannot act as a wildcard.

### Reading time, related, PWA in one template pass

Reading time (200 wpm) was already computed in the template. The article and
home templates now also emit `<link rel="manifest">`. A new `/manifest.json`
(name/icons from site settings) and `/sw.js` (cache-first for `/static` &
`/media`, stale-while-revalidate for navigations, **never** caching `/admin`)
make the public site installable and offline-capable — no build step, no
third-party service worker library.

### PDF/document uploads

`handleMediaUpload` accepts `application/pdf` (validated by the `%PDF` magic
number, up to 32 MB) alongside the existing raster formats. SVG remains refused
(it is an XSS vector when served same-origin). Content-addressed storage and the
strict serve-name regex are unchanged.

### Comment-approval email

When a held comment is moderated to `approved`, a best-effort goroutine emails
the commenter (if they supplied an address) via the existing sovereign SMTP
sender. A nil mailer / unconfigured SMTP makes this a safe no-op.

## Consequences

- **Positive:** Highlighted code, related posts, reading time, PDFs, and an
  installable offline site — with zero CDNs, zero client-side JS frameworks, and
  the strict CSP and sanitiser invariants fully intact.
- **Positive:** One new dependency (`alecthomas/chroma/v2`, pure Go, vendored).
- **Negative:** Highlighting adds render-time CPU; mitigated by the existing
  rendered-page disk cache (highlight cost is paid once per publish, not per hit).
- **Negative:** The placeholder pipeline is more intricate than a single
  sanitise call; documented here and guarded by regression tests.

## Alternatives considered

- **Client-side highlighter (Prism/Shiki/highlight.js):** rejected — injects
  inline styles (breaks `style-src 'self'`) and ships reader-facing JS.
- **Allow `class` on `<span>` in bluemonday and highlight in place:** rejected —
  widens the sanitiser's allowlist for *all* content, a larger attack surface
  than injecting trusted, self-generated HTML behind a random placeholder.
