# ADR-0053 — Observability & Correlation Architecture

**Status:** Accepted  
**Date:** 2026-06-13

## Context

After P21, VayuPress has durable, idempotent event delivery. The next gap is
observability: when something goes wrong, there is no way to trace an HTTP
request through the write queue, outbox, and event dispatch without manually
correlating scattered log lines by timestamp. Operators cannot answer "what
happened to this request?" or "which events did this article creation produce?"

## Decision

### 1. `internal/trace` package — context-scoped IDs

A dedicated `trace` package owns correlation context propagation.  
`trace.WithCorrelationID` / `trace.CorrelationID` get/set a `correlation_id`
in `context.Context`. `trace.WithCausationID` / `trace.CausationID` do the
same for causation chains. `trace.NewID()` generates crypto-random IDs.

### 2. Correlation ID injection in HTTP middleware

`requestIDMiddleware` now reads `X-Correlation-ID` from the incoming request
(caller-supplied) or falls back to the request ID. The ID is:

- stored in request context via `trace.WithCorrelationID`
- echoed back in the `X-Correlation-ID` response header
- included in every structured access log line

### 3. Correlation ID propagation through the write queue

`queue.SQLiteWriter.Enqueue` reads `trace.CorrelationID(ctx)` and persists it
in the `write_jobs.correlation_id` column (migration 008). The queue worker
reads the column when processing a job and passes it into `writeOutboxEvent`.

### 4. Correlation ID in event envelopes

`writeOutboxEvent` (already wrapping payloads in `events.Envelope`) now also
sets `CorrelationID` from the job row. Every delivered event is therefore
traceable back to its originating HTTP request.

### 5. Correlation ID in outbox relay dispatch

The relay's `DispatchFn` in `main.go` unmarshals the envelope, extracts
`CorrelationID` and `CausationID`, injects them into the dispatch context, and
emits a structured log line with both IDs before publishing to the event bus.

### 6. Structured log fields

`logging.LogFields` gains `CorrelationID` and `CausationID` fields (both
`omitempty`). All relay dispatch logs, unknown-event warnings, and
outbox errors include them.

### 7. Observability API endpoints (admin-protected)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/admin/outbox/stats` | Aggregated counts: pending/delivered/dead_letter, dedup table size, oldest pending |
| `GET` | `/api/v1/admin/outbox/events` | List recent outbox records with parsed envelope (`?status=&limit=`) |
| `GET` | `/api/v1/admin/outbox/events/{id}` | Single event record + full envelope |
| `GET` | `/api/v1/admin/trace/{correlation_id}` | All outbox events for a given correlation ID via `json_extract` |

The correlation trace endpoint enables operators to reconstruct the full
event timeline for any HTTP request by its correlation ID.

## Consequences

- **Full request-to-event traceability**: every log line, queue job, outbox
  record, and event envelope shares a `correlation_id` from the HTTP request.
- **Operational debugging**: operators can call `/api/v1/admin/trace/{id}` to
  see every event a request produced, their delivery status, and retry state.
- **No external dependency**: tracing is implemented with `context.Context` and
  SQLite `json_extract` — no OpenTelemetry, Jaeger, or external collector required.
- **Forward-compatible**: `CorrelationID` and `CausationID` fields in envelopes
  are already present; future structured tracing or OTEL export can read them
  without schema changes.
- **Trade-offs**: `json_extract` on the payload column for trace lookup is a
  full-table scan on large datasets. A future migration can add a generated
  column index if needed.
