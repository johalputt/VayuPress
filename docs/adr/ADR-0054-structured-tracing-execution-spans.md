# ADR-0054 — Structured Tracing & Execution Spans

**Status:** Accepted  
**Date:** 2026-06-13

## Context

After P22, every HTTP request has a correlation ID that persists through the
write queue and outbox envelope. However, there is no way to see how time was
spent inside a request — which service method was slow, whether the DB query
or the queue write dominated latency, or how deeply nested the operation tree
was. Logs show what happened; spans show where time went.

## Decision

### 1. Span model — OpenTelemetry-compatible fields

`trace.Span` carries:

| Field | Meaning |
|-------|---------|
| `TraceID` | `= correlation_id` of the originating HTTP request |
| `SpanID` | Unique per operation (crypto-random hex) |
| `ParentSpanID` | SpanID of the enclosing span, empty for root spans |
| `Operation` | Human-readable name e.g. `"ArticleService.Create"` |
| `StartTime` / `EndTime` / `DurationMS` | Timing |
| `Status` | `ok`, `error`, `unset` — matches OTEL status codes |
| `ErrorMsg` | Set via `span.SetError(err)` |
| `Attributes` | `map[string]string` — OTEL-compatible key/value |

Field names and semantics match the OpenTelemetry specification so future
export to an OTEL collector, Jaeger, or Tempo requires no structural changes.

### 2. `Recorder` — fixed-size ring buffer

`trace.Recorder` stores finished `SpanRecord` values in a ring buffer
(default 2000 entries). It is safe for concurrent use and never allocates
beyond its initial capacity. Overflow drops the oldest entries.

### 3. `Tracer` — public API

`trace.Global` is the application-wide tracer. Any package calls:

```go
ctx, span := trace.Start(ctx, "MyService.Operation")
defer span.End()
span.SetAttribute("key", "value")
span.SetError(err)
```

`Start` reads the parent span from `ctx` (if present) to build the
parent-child relationship. `TraceID` is taken from `CorrelationID(ctx)`.

### 4. Instrumented operations

- `structuredLoggerMiddleware` — root HTTP span wraps entire request lifetime
- `ArticleService.Create/Update/Delete/Get/List` — service-level spans
- `outbox.dispatch.*` — relay dispatch spans per event type

### 5. Span inspection API (admin-protected)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/admin/traces` | Recent spans from ring buffer (`?limit=`) |
| `GET` | `/api/v1/admin/traces/{trace_id}` | All spans for one trace ID |

Combined with the P22 correlation trace endpoint, operators can reconstruct
a full waterfall: HTTP handler → service → DB → queue → outbox relay → event
dispatch, all for a single originating request.

### 6. No external dependency

The entire tracing implementation is `~250 lines` of stdlib Go. There is no
dependency on OpenTelemetry SDK, Jaeger client, or any vendor library.
The model is forward-compatible: exporting to OTEL is additive, not a rewrite.

## Consequences

- **Latency decomposition**: operators can see exactly where time is spent
  across the service → DB → queue call chain.
- **Parent-child graph**: nested spans reconstruct the call tree for any
  request, including async outbox dispatch spans.
- **Zero allocation overhead at rest**: the ring buffer is pre-allocated;
  span creation only allocates when spans are actually started.
- **Test coverage**: 9 unit tests cover ring buffer semantics, parent-child
  relationships, error status, idempotent End(), and ByTraceID lookup.
- **Trade-offs**: in-memory only — spans are lost on process restart.
  A future prompt can add optional persistence or OTEL export.
