# ADR-0113: Memory-Footprint Budget & Search-Index Windowing

**Status**: Accepted  
**Date**: 2026-07-07  
**Author**: @johalputt  
**Relates to**: [ADR-0001](ADR-0001-sqlite-first.md) (SQLite-first), [ADR-0101](ADR-0101-builtin-search-vayufind.md) (VayuFind built-in search), [ADR-0110](ADR-0110-incremental-search-index-persistence.md) (incremental index persistence)

## Context

After a run of feature work the resident memory (RSS) of the single binary grew
well beyond its historical ~190 MB baseline on the reference deployment
(234k+ posts). A profiling audit found the growth was **not a leak** — it was
two design assumptions that stopped holding at scale, plus one config knob that
multiplied:

1. **SQLite page cache is per-connection.** `cache_size` was `-65536` (64 MB) on
   both the writer and every reader connection, and the read pool opens one
   connection per CPU. On a many-core host that multiplied to 256–512 MB of
   private page cache — largely redundant with the 512 MB `mmap_size`, which
   already keeps hot database pages resident in the shared OS page cache.
2. **The in-memory search index scales O(posts).** Each indexed document kept a
   `title + tags + excerpt` lowercase haystack *in addition to* the separate
   lowercase title and tags — a redundant third copy of the largest field.
3. **The client search index shipped every post.** VayuFind's downloadable
   instant-search index (ADR-0101) included the entire corpus, so both the
   browser download and the memoised server-side snapshot were tens of MB and
   grew without bound — even though a reader almost always wants recent content.

The design goal was to cut RSS **without regressing any of**: features,
reliability, search results/ranking, UI, or measured latency.

## Decision

Adopt an explicit **memory-footprint budget** for the binary and implement four
bounded, reversible measures (shipped across v3.8.2–v3.8.3):

- **Right-size the SQLite page cache.** Writer `cache_size` → 32 MB, each reader
  → 16 MB; `mmap_size` (512 MB) unchanged. Hot pages stay resident via mmap, so
  query latency is unaffected while the per-connection caches no longer
  multiply.
- **Trim the search index per-document.** Store only a lowercase *excerpt*
  instead of a `title+tags+excerpt` concatenation. The excerpt fallback in the
  scorer runs only after title and tags have already been checked, so ranking is
  byte-for-byte identical.
- **Single-pass client snapshot.** Compute the snapshot's content version with a
  streaming hash while assembling the payload, instead of serialising the whole
  index to JSON a second time purely to hash it — removing a large short-lived
  allocation from every rebuild. The version stays fully content-sensitive.
- **Window the client search index.** Ship only the most-recent
  `clientSnapshotMax` (5000) posts to the browser. Instant, zero-latency,
  in-browser search covers that window; when the corpus is larger the modal
  offers a one-click "search the full archive" row that jumps to the existing
  server-rendered `/search` page, which searches **every** post. Server-side
  `Search`/`DocCount` remain uncapped.
- **Bound the runtime.** Set a Go soft memory limit at 90% of the detected
  cgroup memory limit (honouring an explicit `GOMEMLIMIT`; a no-op on bare
  hosts), and hand the boot-time index-scan working set back to the OS once the
  live index is in place.

## Consequences

- Positive: steady RSS on a large store drops by roughly 200–400 MB (core-count
  dependent) with no change to features, ranking, UI, or measured latency; the
  client search download shrinks from tens of MB to a few MB; and the runtime
  now targets the container's real memory instead of overshooting toward an
  OOM-kill.
- **No lost coverage:** the full archive stays searchable server-side, so no post
  becomes unreachable; the windowed client index is a pure client-side
  optimisation surfaced by an explicit escalation affordance.
- Trade-off: the in-browser instant modal auto-completes only the recent window;
  older posts are found via the server `/search` page (one round-trip, only when
  the visitor chooses it). The server-side index remains O(posts) in memory —
  a full move off the in-memory engine (e.g. SQLite FTS5) is deliberately **not**
  taken here and is left for a future ADR should corpus growth demand it.
- Every measure is reversible via a single constant/PRAGMA and is covered by
  tests (identical-ranking, version content-sensitivity, client cap + retained
  server coverage, cgroup-limit parsing).
