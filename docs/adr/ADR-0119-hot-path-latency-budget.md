# ADR-0119: Hot-Path Latency Budget & Tail-Latency Posture

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0054](ADR-0054-structured-tracing-execution-spans.md) (structured tracing), [ADR-0113](ADR-0113-memory-footprint-budget-and-search-index-windowing.md) (memory budget), [ADR-0117](ADR-0117-vayuos-read-path-resilience.md) (read-path resilience), [ADR-0036](ADR-0036-csp-nonce-centralized.md) (CSP nonce)

## Context

After the read-path resilience work (ADR-0117) closed the VayuOS 502 saga, the
remaining question was pure latency calibration: the public request path reports
a **P95/P99 around 25 ms**, which those percentiles now only rarely reach. The
goal was to make the system faster and keep the tail tight **without
compromising any service** — no dropped logs, no weakened bot protection, no
reduced observability.

An honest audit matters more than an impressive number here, so this ADR records
both what was changed and — just as important — the bigger levers that were
**deliberately not pulled**, with the reasoning, so a future maintainer does not
mistake the restraint for an oversight.

### What the hot path actually does

The reported latency is measured server-side as the root HTTP span, which wraps
the middleware chain (request-ID → structured logging/tracing → security headers
→ VayuShield → CORS) and the handler. For the common case — a cached public page
— the handler does **no per-request database work** (it serves a pre-rendered
file), so the fixed per-request cost is dominated by the middleware chain, not
by I/O. A benchmark of the request-ID + tracing/logging middleware measured
**~26 allocations/op**, confirming the path was already lean. The dominant
contributors to the *rare* tail spikes are therefore not the middleware but:

1. **Garbage-collection jitter** — a GC cycle adds assist/scheduling latency to
   in-flight requests. This scales with allocation *rate*, so reducing
   per-request allocations tightens the tail.
2. **Cold-cache renders** — a freshly published/edited post (or a cold cache
   after restart) renders synchronously; this is inherently tens of
   milliseconds and is already mitigated by the SWR cache + warmer (ADR earlier
   in the 3.8.x line), not by anything in this release.
3. **Slow/abusive clients** — a slow-loris client holding the header phase open
   ties up a server goroutine and can inflate the tail for everyone.

## Decision

Make three safe, measurable changes and stop there — do not chase micro-optim
placebo or take on risk that would compromise a service.

1. **Lean per-request tracing.** Remove the per-request `runtime.NumGoroutine()`
   probe (which briefly locks the scheduler) and the reflection-based
   `fmt.Sprintf` attribute formatting from the always-on root HTTP span; format
   status with `strconv.Itoa`. Live goroutine count remains available as a gauge
   at `/api/v1/admin/resource/stats`, so no observability is lost. Measured:
   **~4 210 → ~4 020 ns/op (~4–5%)** on the middleware, with lower allocation
   pressure.

2. **Server-level slow-client hardening.** Set `ReadHeaderTimeout` (10s) and
   `MaxHeaderBytes` (1 MB) on `http.Server`. This closes the slow-loris /
   oversized-header vector that inflates the tail under attack, with no effect on
   real clients.

3. **A permanent benchmark regression guard.**
   `BenchmarkObservabilityMiddleware` pins the fixed per-request overhead with a
   reusable, allocation-free `ResponseWriter`, so future changes that reintroduce
   an expensive probe or per-request allocation on the hot path surface as a
   measurable regression instead of silent tail-latency creep.

## Levers deliberately NOT pulled (and why)

- **In-memory hot-page LRU cache in front of the file cache.** This is the one
  lever that could shave real tail latency (eliminating the `stat`+`open`+`read`
  syscalls and any OS-page-cache-miss stall for hot pages). It was **rejected for
  now** because it largely duplicates the OS page cache (hot files are already
  resident in RAM), adds an invalidation surface that must stay perfectly in
  lockstep with the existing file-cache invalidation, and spends memory — a
  compromise this platform's memory budget (ADR-0113) treats as significant. It
  remains the recommended *next* step **if** measurement ever shows file-serve
  I/O as the dominant tail, and should be introduced with the same
  single-flight/invalidation discipline as the render cache.
- **Access-log sampling / asynchronous logging.** Dropping or sampling the
  per-request access log would cut CPU under extreme load, but it **reduces
  observability** — explicitly out of bounds for this work.
- **`GOGC` tuning.** Raising `GOGC` reduces GC frequency (fewer tail spikes) but
  trades resident memory. Left as an operator tunable rather than a forced
  default, bounded by the existing `GOMEMLIMIT` (ADR-0113).
- **Switching request-ID / span-ID / CSP-nonce generation off `crypto/rand`.**
  Modern Go's `crypto/rand` is per-P buffered, so the per-request reads are
  already cheap; the CSP nonce is security-sensitive. The negligible gain did not
  justify the risk.

## Consequences

- The per-request observability overhead is lower and guarded against
  regression; the server is hardened against a real slow-client DoS vector.
- The change is honest about scope: a ~4–5% middleware improvement plus
  hardening, **not** a dramatic mean-latency reduction — because the path was
  already fast. The remaining tail is dominated by GC jitter and cold-cache
  renders, and the concrete next lever (in-memory hot-page cache) is documented
  here with its tradeoff for a future, measurement-driven decision.
- No service was weakened: full access logging, full tracing (minus a redundant
  per-request goroutine probe), and full VayuShield protection are retained.
