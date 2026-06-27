# ADR-0092: Block Editor v2 — Publishing Options, Content Cards & Low-Impact Deploys

**Status**: Accepted  
**Date**: 2026-06-27  
**Author**: @johalputt

## Context

The VayuOS block editor (ADR-0068) shipped a capable, CSP-strict, vanilla-JS
writing surface, but three gaps remained for a publisher who runs everything on
one small VPS:

1. **The writing experience had rough edges.** A long post — or a single tall
   block — could not be scrolled into view (the canvas was clipped at the
   viewport edge), there was no way to edit a post's raw HTML, and several
   content types people expect (image galleries, an HTML card, a Markdown card,
   image captions and widths) were missing.
2. **There was nowhere to set publishing metadata.** Tags were not even editable
   in the block editor, and there was no per-post excerpt, feature image, SEO
   title/description, canonical URL, social-card overrides, publish date, or a
   post-vs-page distinction. All of that was derived from content + global
   settings, so the operator could not tune how a post appeared in search
   results or social shares.
3. **Deploys wobbled the server.** A restart blocked the HTTP listener behind
   the Meilisearch readiness probe, and the post-deploy cache re-render and
   search reindex ran in tight unpaced loops while the update's `go build`
   competed for the box — on a small VPS this produced multi-second (sometimes
   longer) 502 windows.

## Decision

### 1. A richer, scrollable editor with HTML source mode

The editor canvas is rebuilt as a fixed-height grid with a correct
`min-height: 0` chain so the writing surface scrolls internally. A one-click
**HTML** mode renders the document to sanitised HTML for direct editing and
parses it back into blocks on exit; the importer re-encodes inline emphasis,
code, strike, and links as Markdown so the visual ↔ HTML round-trip is lossless
for common formatting. New content cards — **gallery**, **html**, and
**markdown** — plus image **caption** and **width** options are added. Every
card's output still passes through the same bluemonday UGC policy as every other
block, so the "no block bypasses sanitisation" posture is preserved; the HTML
and Markdown cards enrich markup within the safe allowlist but cannot introduce
scripts, handlers, or forms. Any URL assigned to an `<img>` `src` is first
resolved with the URL parser and accepted only for `http(s)` (closing a
DOM-XSS sink flagged by CodeQL).

### 2. Per-post publishing options as additive columns

A **Post settings** drawer exposes feature image, slug (with a safe,
cache-purging rename), publish date, excerpt, tags, SEO meta title/description,
canonical URL, Open Graph + Twitter overrides, and `featured` / `is_page`
flags. These are stored in new `articles` columns (migration 045) written
through a synchronous side-car update on disjoint columns, so the authoritative
queued content/title/tags write is never contended. Every field is optional and
falls back to the previously-derived default, so existing posts render
identically until something is set. The resolved values flow into the article
`<head>` (title, description, canonical, `og:*`, `twitter:*`, JSON-LD) and body
(hero feature image); pages drop the post chrome and are excluded from the home
feed, RSS, and sitemap.

### 3. Low-impact deploys and restarts

The Meilisearch readiness probe moves off the startup critical path into a
background goroutine, so the HTTP listener comes up immediately on restart and
search uses its SQLite fallback until Meili is confirmed ready. The boot
cache-warm and the full search reindex are paced (`VAYU_WARM_THROTTLE_MS`,
`VAYU_WARM_DELAY_SEC`, `VAYU_REINDEX_THROTTLE_MS`, and `VAYU_WARM_ON_BOOT=0` to
skip boot warming) so they never saturate a small VPS. The in-place updater
builds at idle CPU/IO priority with capped parallelism, runs a disk-space
preflight, and takes a timeout-bounded DB snapshot by default; the sample nginx
config adds a dual-peer upstream with a bounded `proxy_next_upstream` retry so an
in-flight idempotent request during a sub-second restart is retried instead of
returning a 502 (non-idempotent methods are never retried).

## Consequences

- The editor reaches feature parity with mainstream block editors while keeping
  the strict-CSP, vanilla-JS, server-sanitised model intact.
- Publishing metadata is now first-class and operator-controlled, improving SEO
  and social sharing without weakening the privacy posture.
- Restarts are fast and gentle; a redeploy no longer needs to take the public
  site down for seconds. All pacing has gentle defaults and is env-tunable.
- The new columns are additive and nullable-by-default, so the upgrade is
  backward compatible and requires no operator action.
