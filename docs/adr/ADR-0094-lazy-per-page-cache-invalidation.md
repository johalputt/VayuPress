# ADR-0094: Lazy, Per-Page Cache Invalidation

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt

## Context

VayuPress pre-renders public pages (`home/index.html`, `tags/*.html`,
`posts/*.html`) to disk and serves them as static files. The cache had two
"invalidate everything" paths:

1. **`ReconcileCacheVersion`** ran on startup. If the renderer fingerprint (CSS
   content hashes + `cacheSchema`) differed from the stamp on disk, it
   `os.RemoveAll`-ed the whole cache so pages would regenerate with the new
   templates/CSS.
2. **`CachePurgeAll`** ran on every global change — a theme save, identity edit,
   ad-settings change, etc. — and deleted every cached HTML fragment.

Both worked fine for a small blog, but they are exactly the wrong shape for the
product's core promise: running 1M+ posts on a small VPS with smooth,
incremental updates. Deleting the whole cache means the next wave of visitor
traffic re-renders hundreds of thousands of pages at once — a thundering herd
that saturates CPU and stalls the server. This is the "rebuild the whole site on
update" behaviour VayuPress was built to escape.

## Decision

Replace whole-cache deletion with a **lazy, per-page** scheme based on a single
persisted *staleness cutoff*.

- A package-level cutoff instant (`staleBeforeNano`, read atomically) records the
  moment the renderer/theme last changed. A cached file is **fresh** iff its
  modification time is at or after the cutoff (`CacheEntryFresh`).
- The three serve paths (home, article, tag page) treat a stale file as a cache
  miss: they re-render it, which rewrites the file and advances its mtime past
  the cutoff — so each page refreshes exactly once, on its next request.
- `ReconcileCacheVersion` no longer deletes anything. If the fingerprint matches
  the stamp it restores the previous cutoff (a plain restart, or a deploy that
  doesn't touch templates/CSS, invalidates nothing). If it changed, it advances
  the cutoff to now and persists `<fingerprint>\n<cutoff>` to `.render-stamp`.
- `CachePurgeAll` becomes O(1): it advances and persists the cutoff instead of
  walking and unlinking the cache. Persisting matters because a settings save
  does not change the renderer fingerprint, so a restart must still see the
  pages as stale.
- The stamp is backward compatible: a legacy single-line stamp (fingerprint
  only) parses with a zero cutoff, which keeps an existing valid cache intact on
  the first upgrade rather than rebuilding it.
- `WarmCache` refreshes only stale pages (paced in the background), so a deploy's
  proactive warm-up no longer re-renders already-current pages.

## Consequences

- A renderer or theme change refreshes pages **lazily, one per request**, instead
  of wiping the cache and forcing a site-wide re-render herd — the update cost is
  proportional to the pages actually visited, not to the catalog size.
- Stale files are left on disk until their next request overwrites them; this
  costs a little space transiently but never serves stale content (a stale file
  is re-rendered before serving).
- A per-page concurrent miss can still re-render the same page a few times under
  a burst; this is the same bounded, per-page behaviour as any cache miss today
  and is vastly smaller than the previous global herd. A single-flight guard can
  be layered on later if needed.
