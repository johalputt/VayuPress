# ADR-0052: Idempotency & Event Evolution

**Status:** Accepted  
**Date:** 2026-06-13  
**Deciders:** VayuPress core team

---

## Context

The transactional outbox relay (ADR-0051) guarantees at-least-once delivery: an event may
be dispatched more than once if the relay crashes between dispatch and marking the record
`delivered`. Subscribers must therefore be idempotent, or the relay must prevent duplicate
delivery at the infrastructure layer.

Additionally, as the system evolves, event schemas will change. Without versioned type
names, a rolling deploy or schema change can cause consumers to misinterpret events from
a different schema version.

## Decision

### 1. Event Envelope

Every payload written to `event_outbox` is now wrapped in an `events.Envelope`:

```go
type Envelope struct {
    EventID       string          `json:"event_id"`       // stable UUID per event instance
    EventType     string          `json:"event_type"`     // "article.created.v1"
    EventVersion  string          `json:"event_version"`  // "1"
    CausationID   string          `json:"causation_id"`   // write_job ID that caused this
    CorrelationID string          `json:"correlation_id"` // request-level trace ID (future)
    OccurredAt    time.Time       `json:"occurred_at"`
    Payload       json.RawMessage `json:"payload"`        // typed event struct
}
```

The envelope is written atomically with the article mutation in the same transaction by
`writeOutboxEvent` in `internal/queue/queue.go`.

### 2. Versioned Event Type Names

Event type strings now follow the `<domain>.<verb>.<version>` convention:

| Old | New |
|---|---|
| `ArticleCreated` | `article.created.v1` |
| `ArticleUpdated` | `article.updated.v1` |
| `ArticleDeleted` | `article.deleted.v1` |

The `DispatchFn` in `cmd/vayupress/main.go` unmarshals the outer `Envelope` first, then
routes on `env.EventType`. Adding a `v2` event type requires no changes to existing
consumers — they continue to handle `v1` while new consumers opt into `v2`.

### 3. Deduplication Table

Migration 007 adds:

```sql
CREATE TABLE IF NOT EXISTS delivered_events (
    event_id    TEXT PRIMARY KEY,
    event_type  TEXT NOT NULL,
    delivered_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_delivered_events_at ON delivered_events(delivered_at);
```

After a successful dispatch the relay inserts the `event_id` into `delivered_events`.
Before dispatching, it checks whether the `event_id` is already present. If so, it marks
the outbox record `delivered` and skips the dispatch entirely.

This deduplication is best-effort: the check and the dispatch are not in the same SQL
transaction (SQLite single-writer constraint). The window for a double-dispatch is the
time between the check and the `INSERT OR IGNORE` after a successful dispatch — this is
acceptable for the current workload and can be tightened with a distributed lock if needed.

### 4. Poison-Event Sentinel

`outbox.ErrPoisonEvent` is a sentinel error that a `DispatchFn` can return to signal an
unrecoverable event. The relay dead-letters it immediately without incrementing `retries`,
separating infrastructure transient failures (which retry) from semantic errors (which
should not consume retry budget).

## Consequences

- **Positive:** subscribers receive each logical event at most once under normal operation.
- **Positive:** event schemas can evolve independently; old and new consumers coexist.
- **Positive:** traceability improves — `event_id`, `causation_id`, and `occurred_at` are
  available for debugging and audit.
- **Negative:** `delivered_events` grows unboundedly; a TTL purge job will be added in a
  future milestone.
- **Negative:** legacy outbox rows (written before this change) have no `event_id` in their
  payload; they are delivered exactly as before (deduplication is skipped for them).
