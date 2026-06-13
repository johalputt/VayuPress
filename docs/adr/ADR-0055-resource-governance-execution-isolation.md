# ADR-0055 — Resource Governance & Execution Isolation

**Status:** Accepted  
**Date:** 2026-06-13

## Context

After P23, VayuPress has structured tracing and correlation. The next risk is
resource exhaustion: plugins share process memory and goroutine budget; the
write queue has no hard ceiling; plugin executions have no enforced concurrency
limit. Under adverse conditions (traffic spike, misbehaving plugin, stuck
goroutine), the process can exhaust heap or scheduler budget without any
backpressure or automatic recovery.

## Decision

### 1. `internal/resource` package

A dedicated `resource` package provides three primitives:

**`Limiter`** — semaphore-based concurrency ceiling for a named component.
`Acquire` returns `ErrAtCapacity` immediately (non-blocking) when the ceiling
is reached. `Stats()` exposes active/total/dropped counters for telemetry.
Named limiters are stored in a global registry; `AllStats()` returns a
snapshot of all of them for the resource stats API.

**`Watchdog`** — background goroutine that monitors registered in-flight
operations. Callers call `Watch(opID, opName, budget, cancelFn)` on entry and
deregister (via the returned func) on exit. If an operation exceeds its `budget`
before deregistering, the watchdog calls `cancelFn` and logs a warning.
`checkInterval` is 250 ms in production.

**`GoroutineCount()`** — thin wrapper over `runtime.NumGoroutine()` for
inclusion in span attributes and the resource stats API.

### 2. Queue hard limit — backpressure to HTTP 429

`queue.SQLiteWriter` now accepts a `hardLimit int` parameter. Before inserting
a write job it counts pending jobs; if `depth >= hardLimit` it returns
`queue.ErrQueueSaturated`. `api.HTTPStatus` maps this to `429 Too Many Requests`
and `api.ErrorCode` returns `"queue_saturated"`. Default hard limit is 1000
(configurable via `QUEUE_HARD_LIMIT`).

### 3. Plugin concurrency ceiling

`plugins.Manager.run()` acquires the `"plugin.exec"` limiter before calling
any hook. If the limiter is at capacity the job is dropped and
`MetricPluginDisabled` is incremented. The ceiling defaults to 8
(configurable via `PLUGIN_MAX_CONCURRENT`).

### 4. Configurable plugin timeout

`config.Cfg.PluginTimeoutMS` (env `PLUGIN_TIMEOUT_MS`, default 2000) replaces
the hard-coded 2 s `hookTimeout` constant. The watchdog integration is
additive and available for future use by long-running operations.

### 5. Resource-aware spans

HTTP root spans now carry `runtime.goroutines` as an attribute. Combined with
the trace recorder, operators can correlate goroutine count changes with
specific request timings.

### 6. Resource stats API

`GET /api/v1/admin/resource/stats` returns:
- `goroutines` — current `runtime.NumGoroutine()`
- `limiters` — array of `LimiterStats` (name, active, total, dropped, cap)

### 7. Lifecycle: watchdog stops at shutdown phase 3

`resource.Global.Stop()` is called in the graceful shutdown phase 3 alongside
the plugin pool, preventing the watchdog goroutine from leaking.

## Consequences

- **Backpressure**: the system now refuses new write work when saturated,
  returning HTTP 429 instead of silently growing the queue without bound.
- **Plugin isolation**: plugin executions are bounded by a semaphore; runaway
  plugin load cannot starve other operations.
- **Watchdog coverage**: operations registered with the watchdog get automatic
  cancellation on timeout, preventing goroutine leaks from stuck operations.
- **Telemetry**: goroutine count is visible in spans and the resource stats API.
- **Trade-offs**: the queue hard limit check is a DB query per enqueue; at
  high write rates this adds one `COUNT(1)` per request. The count query uses
  a covered index on `status`. Acceptable for the expected single-node write rates.
- **In-process plugins**: plugins remain in-process. The limiter and timeout
  reduce blast radius but do not eliminate the shared-heap risk. True subprocess
  or WASM isolation is a future prompt.
