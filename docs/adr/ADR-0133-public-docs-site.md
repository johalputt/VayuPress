# ADR-0133: Public documentation at /docs (no docs subdomain)

- **Status**: Accepted
- **Date**: 2026-07-14
- **Deciders**: BDFL / Owner

## Context

VayuPress's guides, operations runbooks, security model and every Architecture
Decision Record live as markdown in the repository. They were referenced from the
application (API error `docs` hints, install/troubleshooting help) via a
`docs.vayupress.com` subdomain that was never actually served — a dangling host
that required its own DNS record, its own TLS certificate and its own hosting to
become real. That is at odds with the sovereignty posture: one binary, one
origin, no extra moving parts.

Meanwhile the ADRs were already embedded in the binary (ADR-0099, `DocsADRFS`) and
surfaced only in the admin-only VayuOS ADR registry — not to the public who might
want to audit how the software is built.

## Decision

Serve the documentation from the main site at **`/docs`** — a path, not a
subdomain — rendered from markdown **embedded in the binary**:

- `embedded_assets.go` gains `DocsFS`, embedding the prose docs tree (top-level
  guides plus the `architecture/`, `security/`, `operations/`, `governance/`,
  `compatibility/`, `reliability/`, `release/`, `plugins/` sub-folders). The
  image-heavy folders (`screenshots/`, `assets/`, `site/`) are excluded to keep
  the binary lean; ADRs are not re-embedded (the handler unions `DocsADRFS`).
- A public, read-only, GET-only handler (`handlers_docs.go`) renders each file
  with the same sanitising pipeline as the ADR registry (goldmark + bluemonday
  UGC), inside a self-contained, CSP-safe shell (`static/css/docs.css`, linked —
  no inline styles/scripts, so `style-src 'self'` holds).
- Routing is allowlist-based: the index is built once from the embedded files, so
  a request can only ever resolve to a real embedded doc — no path can escape the
  tree. Legacy anchor paths still used by API error hints
  (`/docs/operations/…`, `/docs/api/…`, `/docs/governance/…`) alias to the guide
  that covers them, so no hint link dead-ends.
- All in-app `docs.vayupress.com` references become site-relative `/docs/…` (so
  they resolve on whatever host serves the API, multi-domain included); doc/config
  prose links become `https://vayupress.com/docs/…`. `/doc` redirects to `/docs`.

## Consequences

- **Sovereign & atomic.** Docs ship and self-update with the binary (ADR-0099);
  no docs subdomain, DNS record, certificate or separate host to operate.
- **Auditable.** The public can read every guide and ADR of the exact running
  build at `<site>/docs` — the same bytes that shipped, not a drifting external
  copy.
- **Lean.** Only markdown (~0.7 MB) is embedded; README screenshots are not, so
  the hot request path and binary size stay controlled.
- The subdomain option remains available to anyone who wants it (point a CNAME and
  reverse-proxy `/docs`), but nothing requires it.
