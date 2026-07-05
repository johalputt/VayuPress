# ADR-0110: Incremental Search-Index Persistence (No Full Rebuild on Restart)

**Status**: Accepted  
**Date**: 2026-07-05  
**Author**: @johalputt  
**Relates to**: [ADR-0101](ADR-0101-builtin-search-vayufind.md) (VayuFind built-in search)

## Context

VayuFind keeps its index in memory. It was **rebuilt from scratch on every boot**
by `search.Load`, which scans every published article. On a large store (a real
12.7 GB deployment) that scan took **minutes**. Two follow-on problems fell out
of that on the same deployment:

1. It ran **synchronously before the HTTP listener bound**, so the site 502'd for
   the whole load (fixed in v2.9.7 by backgrounding it).
2. It ran on the **single writer connection**, blocking the startup thread's next
   write (fixed in v2.9.8 by reading from the read pool).

Even after those fixes the index is still *fully* rebuilt in the background on
every restart/update — wasteful, and contrary to the principle that an update
should process only what is new, never rebuild derived data from zero.

## Decision

**Persist the search index and, on start, restore it and reconcile only what
changed.**

- `SaveIndex(path)` writes the in-memory index to a gob snapshot
  (`$CACHE_DIR/search-index.gob`), atomically (temp + rename). It stamps each
  document with its article's `updated_at`, read via a cheap **id+timestamp scan**
  (no content), so a later load can tell what changed.
- `LoadIndex(path)` restores the snapshot, then runs a cheap **id+updated_at delta
  scan** of the current published set and:
  - re-fetches content and re-indexes only **new or modified** articles,
  - drops documents that are **no longer published** (deleted/unpublished).
  It never re-reads the content of unchanged articles.
- Startup prefers `LoadIndex`; a **full `Load` runs only when there is no usable
  snapshot**. The snapshot is refreshed periodically (`VAYU_SEARCH_SAVE_MIN`,
  default 10 min) and on shutdown; the in-memory index stays current between saves
  via the existing article event handlers.

This mirrors the rest of the update path, which is already incremental:
migrations apply only *new* migrations, the render cache invalidates lazily
per-page (ADR-0094), and the Go build recompiles only changed packages.

## Consequences

- Positive: a restart or version update on a large site no longer rescans the
  whole article store — it restores the index and touches only changed rows, so
  search is complete almost immediately and startup does far less work.
- **Safety by construction:** the snapshot is a pure optimisation. A missing,
  unreadable, or wrong-version snapshot, or any error in the delta scan, makes
  `LoadIndex` report "no snapshot" and the caller performs the exact same full
  `Load` as before — persistence can never make search worse than a rebuild. The
  snapshot format carries a version so a schema change simply forces one rebuild.
- Trade-off: the snapshot holds only excerpts (not full content) and is small for
  typical sites; on a very large corpus it is a few hundred MB, still far cheaper
  to read than rescanning gigabytes of article content.
