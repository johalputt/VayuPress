# ADR-0115: Website at Root with the Blog under /blog (single-domain)

**Status**: Accepted  
**Date**: 2026-07-07  
**Author**: @johalputt  
**Relates to**: [ADR-0114](ADR-0114-custom-website-zip-deploy.md) (custom website deploy), the business-site modes (VayuOS → Website)

## Context

VayuPress could serve either the blog at the root, or a business site at the
root with the blog moved to `blog.<domain>` (a subdomain, needing its own DNS
record and TLS certificate). Operators wanted a middle option: a business (or
custom) website at the domain root with the blog on the **same** domain under
`/blog`, **without changing any existing post URL** and without provisioning a
subdomain or extra certificate.

The hard constraint was URL stability: a live blog's posts already live at
`https://<domain>/<slug>` and must keep exactly those URLs — a migration that
moved posts under `/blog/<slug>` would break every existing link, feed item and
search-engine result.

## Decision

Add a third site mode, **`business_subpath`**: the website owns `/`, the blog
homepage moves to **`/blog`** (`/blog/page/N` for pagination), and **posts stay
at `/<slug>`**.

- Routing: `/blog` and `/blog/page/{n}` render the blog feed; `/{slug}` is
  unchanged, so every existing post URL is preserved. In other modes `/blog` is
  served as an ordinary slug (so a post actually slugged `blog` still resolves).
  `/page/N` 301-redirects to `/blog/page/N` in this mode to keep one canonical
  set.
- A `render`-level blog base path (`SetBlogBase`/`BlogBase`, default `/`, `/blog`
  in this mode) drives the blog canonical, `rel=prev/next`, pager and nav-brand
  URLs so the blog index is correct under `/blog` while the historic modes stay
  byte-identical. It is set at boot and on a Website-settings save.
- The custom-website mode (ADR-0114) reuses this same routing, so a custom site
  at `/` also keeps the blog at `/blog` and posts at `/<slug>`.

## Consequences

- Positive: a single-domain website + blog with **zero change to existing post
  URLs**, and no subdomain, DNS record, or extra TLS certificate. Simpler to
  operate than the subdomain mode.
- The mode is explicit and never changed by an update; existing installs keep
  their current behaviour.
- Known minor follow-up: the sitemap/RSS **homepage** entry still lists `/`
  regardless of mode (cosmetic — the blog index canonical is already correct via
  the render blog base); to be folded in later.
