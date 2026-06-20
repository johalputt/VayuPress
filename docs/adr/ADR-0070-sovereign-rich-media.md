# ADR-0070 — Sovereign Rich Media: Server-Rendered Diagrams & Privacy-First Embeds

**Status:** Proposed
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers
**Target release:** v1.4.0

---

## Context

Operators want to put richer content into posts: **Mermaid diagrams**, **YouTube
and other video**, **third-party images**, and arbitrary **rich link embeds** —
"so a user may post anything." Every mainstream CMS satisfies this by handing the
reader's browser a third-party `<script>` or `<iframe>`, which:

- requires a loose Content-Security-Policy (`unsafe-eval`, wildcard `frame-src`,
  wildcard `img-src`),
- leaks every reader's IP and a tracking surface to a third party on page load,
- bloats the client and harms LCP/CLS,
- and, for diagrams, ships a multi-megabyte JS engine.

These directly violate VayuPress invariants: a sovereign single binary, zero CDN
dependencies, no third-party reader-side tracking, a strict CSP
(`default-src 'self'; script-src 'self' 'nonce-…'; img-src 'self' data:;
connect-src 'self'; frame-ancestors 'none'`), and `internal/blockrender`'s rule
that there is **no raw-HTML escape hatch**.

## Decision

Render all rich media **sovereignly on the server**: resolve, fetch, render,
sanitise, and cache server-side; ship the reader pure same-origin HTML/SVG; never
relax the reader's CSP for content they did not explicitly request. Rich media
becomes *faster, more private, and more secure* than the third-party approach —
because of the constraints, not despite them. Each capability is a new **typed
block**, never raw HTML.

### 1. Diagrams — pure-Go Mermaid → SVG (zero client JS)

- New `diagram` block: `{ engine:"mermaid", source, variant }`.
- New package `internal/diagram`: a from-scratch, dependency-free Mermaid→SVG
  compiler — `lexer → AST → layout → SVG emitter → bluemonday SVG allowlist`.
  No headless browser, no Node, no `eval`.
- Output is a static, themeable SVG (`currentColor` + CSS classes, reusing the
  v3 dashboard sparkline pattern) — paints instantly, prints perfectly, and
  ships **no JavaScript**, so the strict CSP is untouched.
- Rendered SVG is content-hash cached in SQLite (`diagram_cache`, migration 027);
  re-renders are free.
- Supported grammar is phased, with graceful fallback to an annotated code block
  for anything unsupported:
  - Phase 1: `flowchart` (Sugiyama layered-DAG layout) and `sequence`
    (lifeline grid) — ~80 % of real-world usage.
  - Phase 2: `class`, `state`, `pie`, `gantt`.
- The editor live preview calls a debounced server endpoint that returns the SVG;
  no client-side Mermaid library is ever loaded.

### 2. Embeds — one privacy-first framework (`embed` block + `safefetch`)

- New `embed` block: `{ provider, url, refId, meta }`.
- New package `internal/safefetch`: a single SSRF-hardened HTTP fetcher
  (private-range/loopback/link-local denial, redirect re-validation, size and
  time caps, scheme allowlist), consolidating the ad-hoc guards currently in
  `internal/social` and `internal/webhooks`.

**(a) Video — click-to-load facade.** Server resolves the provider via an
allowlist + oEmbed (YouTube, Vimeo, Spotify, …), re-hosts the **poster
thumbnail into the media library** (so `img-src 'self'` is sufficient), and
renders a lightweight poster + play button. **Nothing third-party loads until the
reader clicks.** On click, a vanilla-JS handler injects a `sandbox`-ed iframe
pointed at the privacy origin (`youtube-nocookie.com`, `player.vimeo.com`, …),
and the CSP **for that page only** carries a *narrow* `frame-src` allowlist of
exactly the embedded origins. Readers who never click never touch a third party.

**(b) Third-party images — sovereign import, not hotlink.** A remote image URL
triggers an SSRF-guarded fetch → magic-number validation → re-encode through the
existing stdlib image pipeline → store in the media library → rewrite the block
to a local `/media/…` URL. "Embed any picture" becomes "**import** any picture":
`img-src 'self'` never changes, no hotlink rot, no reader-IP leak, and SVG stays
refused as an XSS vector.

**(c) Anything else — unfurls.** Arbitrary URLs become self-hosted **link cards**
(OpenGraph/oEmbed metadata fetched server-side, thumbnail cached locally) — no
iframe, fully sanitised, lightweight.

- `embed_cache` (migration 028) stores resolved metadata + provenance.
- A per-response **CSP builder** starts from the strict baseline and *narrowly*
  extends `frame-src`/`img-src` only when a page contains an embed that requires
  it, listing exact origins. Admin and non-embed pages stay fully locked.
- **Operator sovereignty:** config switches disable embeds entirely or
  per-provider; the `frame-src` allowlist is operator-owned config, never a
  wildcard.

## Threat model

- **SSRF.** Every server-side fetch (thumbnails, images, oEmbed, OG metadata)
  routes through `internal/safefetch`: deny RFC1918/loopback/link-local/ULA,
  re-validate on each redirect hop, cap response size and time, allow only
  `http(s)`. Covered by adversarial unit tests.
- **Iframe / XSS.** Third-party frames appear only via the controlled facade,
  always `sandbox`-ed, only on click, only to allowlisted origins, and the page
  CSP enumerates those origins explicitly. The `embed`/`diagram` blocks render
  through HTML-escape + bluemonday (SVG via an allowlist policy). The
  no-raw-HTML-escape-hatch invariant is preserved.
- **Decompression / pixel bombs.** Imported images are size-capped and
  re-encoded by the existing stdlib pipeline; SVG remains refused.

## Consequences

**Positive:** rich media with zero reader-side third-party requests by default;
CSP stays strict; LCP/CLS improve; binary stays lightweight (no Chromium/Node);
diagrams render with no client JS; a single SSRF-safe fetcher hardens the whole
codebase.

**Negative / costs:** building a Mermaid subset compiler is real engineering
(mitigated by phasing + graceful fallback); the per-page CSP builder adds
response-time logic (covered by tests); oEmbed provider allowlist needs periodic
curation.

## Alternatives considered

- **Ship mermaid.js to the client** — rejected: multi-MB bundle, needs loose CSP.
- **Headless-Chromium diagram rendering in the sandbox** — rejected for v1.4.0:
  full grammar but a heavy binary/ops surface; revisit only if the pure-Go
  subset proves insufficient.
- **Direct (always-on) iframes** — rejected: leaks reader IP on load and relaxes
  `frame-src` for the whole page.
- **Hotlinking remote images** — rejected: reader-IP leak, hotlink rot,
  mixed-content, and CSP wildcards.
