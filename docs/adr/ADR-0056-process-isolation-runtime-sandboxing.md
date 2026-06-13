# ADR-0056 — Process Isolation & Runtime Sandboxing

**Status:** Accepted  
**Date:** 2026-06-13

## Context

P24 (ADR-0055) introduced concurrency limiters and watchdog timers to bound plugin
blast radius, but plugins still execute in-process: a panicking plugin can corrupt
shared heap state, and a misbehaving plugin can access any memory or file descriptor
reachable from the host process. The limiter reduces contention but does not achieve
true isolation.

## Decision

### 1. `internal/sandbox` package

A new `sandbox` package implements subprocess isolation for plugins:

**`Manifest`** — declares plugin identity and capability grants. All permissions
are deny-by-default; operators must explicitly opt in to `AllowNetwork`,
`AllowedReadPaths`, and `AllowedWritePaths`. The subprocess is started with a
minimal, sanitised environment (no parent-env inheritance); only `PATH`, `HOME`,
and `PLUGIN_NAME` are set, plus any explicitly declared `Env` entries.

**JSON-over-stdio IPC** — the host writes one `Request` JSON line to the
subprocess stdin; the subprocess writes one `Response` JSON line to stdout. Each
request carries `hook`, `payload`, tracing IDs (`correlation_id`, `causation_id`,
`trace_id`), and an echoed `Capabilities` block so the plugin can self-enforce
its permission set.

**`SubprocessPlugin`** — manages the lifecycle of a single subprocess. On crash
the subprocess is restarted (up to `Manifest.MaxRestarts`, default 3). After the
budget is exhausted the plugin is quarantined (`ErrQuarantined`) and all further
`Invoke` calls fail immediately without restarting. A timed-out invocation kills
and quarantine-counts the subprocess. All subprocess processes share no memory
with the host — a plugin crash or panic cannot affect the host process.

**`Pool`** — owns N `SubprocessPlugin` instances for one Manifest. `Invoke`
round-robins across members. `Stats()` surfaces per-member health for the admin
API.

### 2. Integration with `internal/plugins`

`plugins.RegisterSubprocess(reg, m, hookEvent, poolSize)` starts a `sandbox.Pool`
and wires a `HookFunc` adapter into the existing registry. Downstream dispatch is
unchanged — the worker pool calls `fn(ctx, payload)` and the adapter calls
`pool.Invoke(ctx, hookEvent, payload)`.

`plugins.SubprocessStats()` aggregates stats from all registered pools.
`plugins.ShutdownSubprocesses()` terminates all pools (called in graceful shutdown).

### 3. Admin stats endpoint

`GET /api/v1/admin/sandbox/stats` — returns per-worker name, running state, PID,
crash count, quarantine flag, and invocation count.

## Consequences

- **True crash isolation**: a subprocess crash never corrupts host heap; the
  sandbox contains the failure and restarts transparently up to the restart budget.
- **Capability-scoped execution**: plugins operate with an explicitly enumerated
  permission set rather than inheriting the full host environment.
- **Quarantine as circuit-breaker**: repeated crashes trigger permanent disabling
  until the host process restarts, preventing runaway restart storms.
- **Minimal IPC overhead**: JSON-over-stdio adds one `exec.Cmd` startup cost and
  ~1 µs per `json.Marshal`/`json.Unmarshal` pair per invocation — negligible for
  event-driven hooks.
- **No external dependencies**: subprocess IPC uses only the standard library
  (`os/exec`, `bufio`, `encoding/json`) — no WASM runtime or plugin framework required.
- **Trade-offs**: subprocess plugins incur higher startup latency than in-process
  hooks; pooling amortises this. Long-lived stateful plugins require careful
  protocol design (not addressed here — stateless one-shot hooks assumed).
