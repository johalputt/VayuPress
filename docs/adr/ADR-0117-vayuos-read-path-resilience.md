# ADR-0117: VayuOS Read-Path Resilience — Dedicated Admin DB Lane, Recycled Read Pools, PASSIVE Checkpointing & Off-Request Dashboard Computation

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0001](ADR-0001-sqlite-first.md) (SQLite-first), [ADR-0033](ADR-0033-wal-adaptive-checkpoint.md) (WAL adaptive checkpoint — amended here), [ADR-0111](ADR-0111-vayushield-bot-protection-and-analytics.md) (VayuShield + VayuAnalytics), [ADR-0113](ADR-0113-memory-footprint-budget-and-search-index-windowing.md) (memory budget)

## Context

A recurring production symptom was reported: the public blog stayed fast, but
**VayuOS (the admin console) intermittently returned 502** — sometimes right
after an update, sometimes after hours of normal operation, and — after the
analytics/bot-shield features shipped — most visibly on the **Analytics** and
**Bot Shield & Analytics** tabs. A run of releases (v3.8.9–v3.9.3) treated the
symptoms by moving individual reads off the single SQLite writer, but the
problem kept coming back. A deeper audit found three distinct, compounding
root causes, all on the **read path**:

1. **Unbounded WAL growth from checkpoint starvation.** The read pool was opened
   with `SetConnMaxLifetime(0)` — connections were never recycled. In WAL mode a
   checkpoint can only reset/truncate the `-wal` file when *no* connection holds
   an older read snapshot; with a pool of permanent readers under continuous
   traffic there was almost always a live reader, so the periodic `PASSIVE`
   checkpoint could never reclaim the WAL. It grew without bound, every read got
   slower (scanning an ever-larger WAL), and the dynamic admin eventually
   exceeded its timeout. (Ironically, each earlier "move another read to the
   pool" release made this *worse* by adding readers that starved the
   checkpoint.)

2. **Read-pool starvation under load.** The pool was sized to `NumCPU` (e.g. 6).
   Because the bottleneck under load is read *concurrency*, not CPU, a burst of
   uncached requests — a bot/crawler mix, or a cold cache after a restart —
   drained those few connections, and **every** uncached read then queued behind
   them, including the per-request VayuOS auth gate, which timed out into 502
   while the cache-served public site stayed fast.

3. **Heavy dashboards computed on the request goroutine.** The Analytics and Bot
   Shield panels run ~a dozen aggregate scans (`COUNT(DISTINCT)`, `GROUP BY`,
   `AVG`) over the ever-growing `analytics_daily` / `analytics_pageviews` /
   `vayuanalytics_sessions` tables, **synchronously on the request**. On a store
   with millions of analytics rows the combined scan time exceeded the 30s
   server write timeout → 502. Resource isolation alone cannot fix a query that
   is simply slow — the work has to leave the request path.

The design goal: **VayuOS must stay responsive under any load** — bot flood,
cold-cache render storm, or a very large analytics store — without regressing
the public site, features, or data correctness.

## Decision

Adopt a layered read-path resilience architecture (shipped across v3.9.3–v3.9.5):

- **Recycle read-pool connections (WAL reclamation).** The public read pool now
  sets a bounded `ConnMaxLifetime`/`ConnMaxIdleTime`, guaranteeing recurring
  moments with no live reader so the checkpoint can fully reset the WAL. This
  amends [ADR-0033](ADR-0033-wal-adaptive-checkpoint.md).

- **Always-`PASSIVE` background checkpoint.** The periodic checkpoint runs
  `PASSIVE` (never an exclusive lock), so it can never stall readers/writer.
  Connection recycling plus `journal_size_limit` keep the WAL bounded; the old
  escalation to `TRUNCATE`/`RESTART` on a timer (which stalled every reader for
  up to `busy_timeout` each tick) is removed.

- **Enlarged, tunable public read pool.** Defaults to `NumCPU × 4` (min 24, max
  64) with a smaller per-connection page cache (8 MB) to offset memory; tunable
  via `VAYU_READ_POOL_SIZE`. Read concurrency — not CPU — is the scaling axis.

- **Dedicated admin DB lane (the isolation guarantee).** A separate, reserved
  WAL read pool (`AdminReader()`/`ARDB`), physically distinct from the public
  pool, serves the `/os` auth gate and the operator dashboards. However
  saturated the public pool gets, the control plane keeps guaranteed capacity
  and always authenticates and loads. Small by design (default `max(8, NumCPU)`,
  cap 16), tunable via `VAYU_ADMIN_POOL_SIZE`. `/os` is also exempt from
  VayuShield challenges, so the admin surface is doubly isolated.

- **Off-request-path dashboard computation.** Heavy admin dashboard sections are
  produced by a single-flighted, TTL-cached, background fragment builder with a
  bounded compute deadline; a startup warmer keeps the default window hot. A
  request returns the last-known fragment instantly (or a light "assembling…"
  placeholder only in the brief cold window after a restart) and **never** waits
  on a scan. This generalises the background-snapshot pattern already used by the
  Monitoring tab. Writes stay on the writer; WAL preserves read-your-writes.

- **Load-shedding that protects by default.** When VayuShield load-shedding is
  enabled without an explicit cap, it derives a capacity-based in-flight ceiling
  (`max(256, NumCPU×64)`) instead of leaving the gate disabled, so a saturating
  flood is shed with a cheap 503 before the render/SQLite path collapses.

## Consequences

- Positive: VayuOS stays responsive under bot floods, cold-cache storms, and
  large analytics stores; the Analytics/Bot Shield tabs can no longer block a
  request into a 502; the WAL stays bounded (a reproduction that drove it to
  multiple GB now stays at a few dozen MB); and the public site is unaffected.
- Trade-off: admin dashboard reports are now bounded by a short cache TTL
  (default 90s, kept hot by the warmer) — a report tolerates being a few seconds
  stale, and this collapses repeated tab opens to at most one heavy scan per
  window per TTL. The Bot Shield status/settings/signatures/review-queue stay
  fully synchronous, so operator controls remain instant.
- Correctness: routing dashboard reads to a read pool is safe because those code
  paths use no explicit transactions or `RETURNING`; every routed call is a
  plain SELECT, and WAL gives cross-connection read-your-writes for sequential
  autocommit operations. Writes remain on the single writer connection.
- Memory: the dedicated admin pool and larger public pool add bounded,
  lazily-allocated page cache (8 MB/conn) — well within the memory budget of
  [ADR-0113](ADR-0113-memory-footprint-budget-and-search-index-windowing.md);
  both pool sizes are env-tunable for constrained hosts.
- All knobs are reversible via environment variables, and the changes are
  covered by build/vet/`-race`/lint gates.
