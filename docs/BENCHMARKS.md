# VayuPress Benchmarks

All numbers below are **reproducible on commodity hardware** and were measured on:

- **CPU:** 4-vCPU Intel Xeon @ 2.80 GHz
- **RAM:** 16 GB
- **Storage:** NVMe-backed container filesystem
- **DB:** SQLite in WAL mode (`go-sqlite3`, CGO)
- **Go:** the toolchain pinned in `go.mod` at the time (now go1.27.2)

> Honesty note: the figures in this file were produced by *actually running* the
> harnesses described below. The cross-engine comparison table (vs.
> WordPress/Ghost/Hugo) is **intentionally omitted** until those engines are run
> on identical hardware — publishing unverified competitor numbers would be
> marketing, not measurement. Run `make bench` and the commands below to
> reproduce everything here.

---

## 0. In production: johal.in

The numbers that matter most are the ones a live install serves. johal.in runs
**234,727 posts**, fourteen sites and its mail server on one Contabo VPS 10
(read from its API, 16 October 2026).

| Measure | Figure | Measured |
|---------|-------:|----------|
| PageSpeed, home: Performance · Accessibility · Best Practices · SEO | mobile 98 · 100 · 100 · 100; desktop 100 · 100 · 100 · 100 | 27 Sep 2026 |
| Largest Contentful Paint | mobile 1.5–1.6 s, desktop 0.3 s | 27 Sep 2026 |
| Total Blocking Time | 0 ms | 27 Sep 2026 |
| Time to first byte, Iowa → the VPS | 156–161 ms | Cloudflare Synthetic Monitoring, 27–28 Jul 2026 |
| HTTP p95 over 15 minutes | 52–63 ms | the install's own `get_performance`, 16 Oct 2026 |

## 0.1 A large archive, cold

A topic page not yet cached, on a local install of 235,000 posts with real post
bodies (16 October 2026):

| Listing cards | Cold topic page |
|---------------|----------------:|
| Missing (built from each post's body on first view) | 65–125 ms |
| Stored (the warmer builds them ahead) | 6–16 ms |

A cached page is served from disk in single-digit milliseconds. The difference
is why the warmer builds cards before visitors ask (`fillMissingCards`).

---

## 1. End-to-end load (built-in harness)

The server ships a real load benchmark at `POST /admin/benchmark`: it writes a
batch of articles through the async write queue, then drives concurrent reads
against the cached render path and records a latency histogram.

```bash
curl -X POST -H "X-API-Key: $API_KEY" -H "X-CSRF-Token: $CSRF" \
     http://localhost:8080/admin/benchmark
```

| Metric | Measured |
|--------|---------:|
| Articles written | 50 |
| Read requests | 200 @ concurrency 20 |
| Read p50 | 16 ms |
| Read p95 | 16 ms |
| Read p99 | 16 ms |
| Read mean | 8.2 ms |
| Read max | 14 ms |
| Throughput | ~8,734 RPS |
| Gate (p95 < 50 ms, p99 < 50 ms) | **PASS** |

---

## 2. Micro-benchmarks (`go test -bench`)

```bash
go test -bench=. -benchmem -run=^$ ./...
```

| Operation | Package | ns/op | B/op | allocs/op |
|-----------|---------|------:|-----:|----------:|
| Article input validation | `internal/api` | 234 | 0 | 0 |
| Tag split | `internal/api` | 1,575 | 1,112 | 9 |
| Slug validation | `internal/api` | 384 | 0 | 0 |
| Histogram record | `internal/metrics` | 18.3 | 0 | 0 |
| Histogram percentile | `internal/metrics` | 27.6 | 0 | 0 |
| Cache hit-ratio read | `internal/metrics` | 0.46 | 0 | 0 |

**Zero-allocation hot paths:** input validation, slug validation
and all metrics recording allocate nothing per call — they impose no
GC pressure under sustained load.

## 2.1 Analytics at scale

`internal/analytics/bench_test.go` writes a store through the ingest path
itself and times the reads the console makes of it: fourteen sites over
thirty days, a few sites taking most of the traffic and a few pages most of
each site's views, visits of one to six views, one event in ten a custom
event (24 October 2026, 4 vCPU Xeon @ 2.10 GHz).

| Read | 1,000,000 events | 3,000,000 events |
|------|-----------------:|-----------------:|
| Ingest, one event (batch path) | 139 µs (7,182/s) | 108 µs (9,255/s) |
| One site's 30-day overview | 54 ms | 182 ms |
| The busiest site's 30-day overview | 305 ms | 1.28 s |
| Every site's 30-day overview | 1.28 s | 4.83 s |
| One site's top pages, 30 days | 136 ms | 407 ms |
| Realtime (last five minutes) | 0.66 ms | 0.76 ms |

A million events in thirty days is about ten times johal.in's rate. The
overview reads grow a little faster than the events in their range (3.4 to
4.2 times for three times the events), most of it the exact count of
distinct visitors; realtime reads only its five minutes and does not grow. This is
why the Analytics pages are counted in a background cache and never on the
request: the page is served from the last count while the next one runs.
Ten million events was not run (the store would not fit the disk of the
host measured on); growing as from 1M to 3M, it would put one site's
overview near 0.7 s and every site's near 18 s, both in the background.
That is an extrapolation, not a measurement.

## 2.2 VayuShield under load

`internal/vayushield/load_test.go` drives the shield's middleware at a fixed
rate with recording on, rate limits, the jail and twenty operator rules,
over a mix of readers on two thousand addresses, crawlers, scripts,
scanners and a few flooding addresses. The time is the middleware's own,
per request.

| Rate | Sent | Late | p50 | p95 | p99 | CPU | Heap growth | Events dropped / lost |
|------|-----:|-----:|----:|----:|----:|----:|------------:|----------------------:|
| 1,000 req/s for 20 s | 20,000 | 0 | 26 µs | 56 µs | 80 µs | 8% of a core | 13 MiB | 0 / 0 |
| 5,000 req/s for 20 s | 100,000 | 0 | 17 µs | 51 µs | 73 µs | 19% of a core | 63 MiB | 0 / 0 |
| 20,000 req/s for 5 s | 100,000 | 0 | 14 µs | 40 µs | 61 µs | 41% of a core | 64 MiB | 0 / 0 |

The costliest rule set the shield accepts, 100 rules of 16 conditions with
every condition tested and none matching, costs 15 µs a request
(`BenchmarkMatchTheCostliestSetThatMisses`, `internal/vayushield/oprules`).

---

## 3. Reproducing

```bash
# Micro-benchmarks
make bench

# Full sweep with allocation accounting
go test -bench=. -benchmem -run=^$ ./...

# Analytics at a million events (VP_BENCH_EVENTS, default 200,000)
VP_BENCH_EVENTS=1000000 go test -run '^$' -bench . -benchtime 20x ./internal/analytics/

# The shield under load (VP_SHIELD_RATES and VP_SHIELD_SECONDS to change the runs)
VP_SHIELD_LOAD=1 go test -run TestTheShieldUnderLoad -v ./internal/vayushield/

# Live end-to-end load against a running server
API_KEY=... DB_PATH=/tmp/bench.db ./vayupress &
curl -X POST -H "X-API-Key: $API_KEY" -H "X-CSRF-Token: $CSRF" \
     http://localhost:8080/admin/benchmark
```

---

## 4. Methodology & caveats

- Numbers are single-run on a shared container host; expect ±15% variance.
- The end-to-end read path is the application's cached render path; in
  production nginx proxies to the same path.
- Micro-benchmarks use Go's `testing.B` with `-benchmem`; each is run to a stable
  iteration count by the framework.
- Nothing cryptographic is on the article read path: pages are served from the
  page cache, and TLS is nginx's.
