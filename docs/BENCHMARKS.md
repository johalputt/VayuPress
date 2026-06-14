# VayuPress Benchmarks

All numbers below are **reproducible on commodity hardware** and were measured on:

- **CPU:** 4-vCPU Intel Xeon @ 2.80 GHz
- **RAM:** 16 GB
- **Storage:** NVMe-backed container filesystem
- **DB:** SQLite in WAL mode (`go-sqlite3`, CGO)
- **Go:** 1.25 toolchain (module targets 1.23+)

> Honesty note: the figures in this file were produced by *actually running* the
> harnesses described below. The cross-engine comparison table (vs.
> WordPress/Ghost/Hugo) is **intentionally omitted** until those engines are run
> on identical hardware — publishing unverified competitor numbers would be
> marketing, not measurement. Run `make bench` and the commands below to
> reproduce everything here.

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
| Ed25519 sign | `internal/signing` | 28,423 | 688 | 7 |
| Ed25519 verify | `internal/signing` | 64,133 | 432 | 4 |
| Article input validation | `internal/api` | 234 | 0 | 0 |
| Tag split | `internal/api` | 1,575 | 1,112 | 9 |
| Slug validation | `internal/api` | 384 | 0 | 0 |
| Migration apply (full) | `internal/migrations` | 142,151 | 4,688 | 102 |
| Event schema validate | `internal/events/schema` | 196 | 0 | 0 |
| Merkle build (1024 leaves) | `internal/merkle` | 943,875 | 372,489 | 5,201 |
| Merkle proof generation | `internal/merkle` | 1,403 | 1,264 | 20 |
| Histogram record | `internal/metrics` | 18.3 | 0 | 0 |
| Histogram percentile | `internal/metrics` | 27.6 | 0 | 0 |
| Cache hit-ratio read | `internal/metrics` | 0.46 | 0 | 0 |

**Zero-allocation hot paths:** input validation, slug validation, event-schema
validation, and all metrics recording allocate nothing per call — they impose no
GC pressure under sustained load.

---

## 3. Reproducing

```bash
# Micro-benchmarks (committed baselines live in testdata/bench/)
make bench

# Full sweep with allocation accounting
go test -bench=. -benchmem -run=^$ ./...

# Live end-to-end load against a running server
API_KEY=... DB_PATH=/tmp/bench.db ./vayupress &
curl -X POST -H "X-API-Key: $API_KEY" -H "X-CSRF-Token: $CSRF" \
     http://localhost:8080/admin/benchmark
```

---

## 4. Methodology & caveats

- Numbers are single-run on a shared container host; expect ±15% variance.
- The end-to-end read path is served from the in-memory render cache, matching
  production where Nginx serves the same cached output as static files.
- Micro-benchmarks use Go's `testing.B` with `-benchmem`; each is run to a stable
  iteration count by the framework.
- Signing/verification dominate the cryptographic cost and are deliberately *not*
  on the article read hot path (verification happens at publish time).
