# ADR-0101: VayuFind — Built-in Search, Replacing External Meilisearch

**Status**: Accepted
**Date**: 2026-06-29
**Author**: @johalputt
**Owner**: Core
**Supersedes**: the Meilisearch backend introduced in [ADR-0050](ADR-0050-persistence-transport-maturity.md)
**Relates to**: [ADR-0001](ADR-0001-sqlite-first.md), [ADR-0050](ADR-0050-persistence-transport-maturity.md)

## Context

Public-site search was served by an optional external **Meilisearch** process,
with a SQLite `LIKE` query as the fallback. Two problems made this the wrong fit
for a sovereign single-binary engine:

1. **Operational weight & sovereignty.** Meilisearch is a second long-running
   service to install, secure, version, monitor, and back up. It contradicts the
   single-binary, zero-external-dependency posture of the rest of VayuPress, and
   in practice most deployments ran on the SQLite fallback — whose `LIKE` ranking
   is poor (no field weighting, no relevance ordering).

2. **Result quality.** Operators reported the search experience lagged behind
   what readers expect from a modern blog: no instant overlay, no typo-friendly
   ranking, and results that felt arbitrary.

## Decision

Replace the external backend with **VayuFind**, a built-in, dependency-free
search engine living entirely inside the binary.

### Engine (`internal/search`)

- **In-memory index.** A compact document set (id, title, slug, short excerpt,
  tags, created-at) held in a map. For a blog this is a few MB for thousands of
  posts.
- **Incremental maintenance.** The index is mutated in place by the existing
  article event handlers — `Index` on publish/update, `Delete` on removal. It is
  **never rebuilt on a content change**. A full `Load` from the article store
  runs only once at boot, plus the periodic reconciler as a drift safety net.
- **Field-weighted scorer.** Query terms are tokenised; every term must match
  somewhere (AND semantics) for precision, scored with title ≫ tags ≫ excerpt
  weighting plus prefix and whole-word boosts, then ordered by score with
  recency as the tie-breaker. The same ranking is mirrored in the client widget.
- **No external process, no `cgo`, no network calls.**

### Client experience — the instant search modal

- A **Ghost-style overlay** opens from the nav search box, `Ctrl`/`⌘`-`K`, or
  `/`. It dims and blurs the page behind a centred panel and filters results as
  the visitor types, with keyboard navigation and match highlighting.
- The browser downloads **one compact JSON index** (`/api/search-index.json`)
  the first time the overlay opens, then filters entirely client-side — **zero
  server work per keystroke**. The payload carries a content-hash version served
  as a strong `ETag`, so a browser/CDN re-downloads it only when the published
  set actually changes.
- **Progressive enhancement:** the nav `<form>` still submits to the
  server-rendered `/search` page when JavaScript is unavailable, so search is
  crawlable and degrades gracefully.
- **Strict CSP preserved:** the widget is a same-origin, content-versioned
  script (`/static/js/search.js`), all styling lives in the stylesheet (no inline
  styles), and every result node is built with `createElement`/`textContent`
  (no `innerHTML` of untrusted data, no `eval`).

### Operator control

A single **Search** switch in Tools & Plugins (`feature.search`, default on)
turns VayuFind on or off; when off, the engine returns no results, the nav box
and modal are hidden, and `/search` returns 404. The legacy `feature.meili`
flag is deprecated and no longer surfaced.

## Consequences

- **Positive:** one fewer service to run; sovereign, instant, accurate search
  with a modern overlay; predictable, cache-friendly resource use; no
  per-keystroke server load.
- **Trade-off:** the in-memory index and the client payload scale with the
  number of published posts. This is appropriate for the blog/newsletter
  workloads VayuPress targets; a future ADR can add sharding or a server-side
  query path should very large catalogues need it.
- **Migration:** no operator action required. The `MEILI_HOST` /
  `MEILI_MASTER_KEY` settings and any external Meilisearch service are now
  ignored and can be removed. The `vayupress_meili_errors_total` metric name is
  retained for dashboard backward-compatibility and now counts built-in index
  load errors.
