# ADR-0051 — Transactional Consistency & Event Reliability

**Status:** Accepted  
**Date:** 2026-06-13

## Context

Prior to this ADR, article mutations (insert/update/delete) and their downstream side effects (search indexing, cache purge, CDN invalidation) were coupled by direct in-process calls. If the process crashed between a DB write and the event dispatch, the side effects were silently lost. There was no durable record of events, no replay path, and no delivery guarantee.

## Decision

### 1. Transactional Outbox

Every article mutation in the queue worker is wrapped in a single SQLite transaction that atomically writes both the article row change and an `event_outbox` record. Either both succeed or neither does — the system never reaches a state where an article is mutated but its event is missing.

```sql
BEGIN;
  INSERT INTO articles ...;           -- or UPDATE / DELETE
  INSERT INTO event_outbox ...;       -- durable event record
COMMIT;
```

### 2. Outbox Relay

A dedicated `outbox.Relay` goroutine polls `event_outbox` for `pending` records at 200 ms intervals and dispatches them by calling a caller-provided `DispatchFn`. On success the record is marked `delivered`. On failure it is retried with exponential backoff (base 5 s, max 120 s) up to 3 times, then dead-lettered.

The relay reuses `queue.DoneCh` for shutdown; on receive it drains remaining events before exiting.

### 3. queue.Writer Interface

`ArticleService` depends on the `queue.Writer` interface (defined in `internal/queue`) rather than a concrete function type. `queue.NewSQLiteWriter` provides the production implementation; tests supply `directWriter` (synchronous) or any other stub satisfying the interface.

### 4. Lifecycle Manager

`lifecycle.Manager` (in `internal/lifecycle`) provides ordered startup and reverse-order shutdown for named service components. In `main.go`, queue workers and the outbox relay are registered as named lifecycle services, so their start sequence is explicit and auditable.

## Consequences

- **Durability:** Article mutations and their events are always consistent — no silent event loss on crash.
- **Replay:** Dead-lettered outbox events can be retried independently of the write-jobs queue.
- **Testability:** `queue.Writer` interface enables synchronous test doubles without coupling tests to the real queue.
- **Ordering:** Lifecycle registration makes startup dependencies explicit and shutdown reversal automatic.
- **Trade-offs:** Adds one extra row per mutation to `event_outbox`; SQLite single-writer serialisation means the relay and queue workers share the same write lock — acceptable for the expected write rates.
