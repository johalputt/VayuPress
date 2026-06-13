# Service Level Objectives — VayuPress

**Version:** 1.0  
**Last reviewed:** 2026-06-13  
**Authority:** VayuPress Maintainers

---

## SLO Definitions

| SLO Name | Target | Window | Error Budget | Measurement |
|----------|--------|--------|-------------|-------------|
| `signing.latency.p95` | 99.9% | 30 days | 43.2 min/month | P95 of `signing.Sign()` calls ≤ 100ms |
| `plugin.invocation.success` | 99.0% | 7 days | 1.68 hr/week | Plugin `Invoke()` returns nil error |
| `federation.inbox.lag` | 95.0% | 24 hours | 72 min/day | Inbox activity processed within 60s of receipt |
| `restore.rto.10min` | 100% | 365 days | 0 min/year | Restore-from-backup completes within 10 minutes |
| `wal.recovery.success` | 99.9% | 30 days | 43.2 min/month | WAL recovery succeeds without data loss |

---

## Error Budget Policy

When an error budget reaches **≤ 20% remaining**:
- Engineering work on new features pauses.
- Focus shifts to reliability improvements.
- Incident post-mortem required within 48 hours.

When an error budget reaches **0%**:
- All feature work stops.
- P0 incident declared.
- Release gate blocked until budget recovers.

---

## SLO Instrumentation

SLOs are tracked via `internal/slo` package:

```go
tr := slo.Global.Register(slo.SLOSigningP95)
// On each signing call:
tr.Record(err == nil)
// Check budget:
if tr.BudgetExhausted() {
    logging.LogJSON(logging.LogFields{Level: "error", Component: "slo", Msg: tr.Status()})
}
```

---

## Latency Targets

| Operation | P50 target | P95 target | P99 target |
|-----------|-----------|-----------|-----------|
| `signing.Sign()` | ≤ 10ms | ≤ 100ms | ≤ 500ms |
| `signing.Verify()` | ≤ 20ms | ≤ 150ms | ≤ 500ms |
| `merkle.New(1024)` | ≤ 1ms | ≤ 5ms | ≤ 20ms |
| HTTP article GET | ≤ 50ms | ≤ 200ms | ≤ 1s |
| Plugin hook invocation | ≤ 100ms | ≤ 500ms | ≤ Manifest.Timeout |
| DB read (indexed) | ≤ 1ms | ≤ 10ms | ≤ 50ms |
| DB write (WAL) | ≤ 5ms | ≤ 50ms | ≤ 200ms |

---

## Recovery Objectives

| Scenario | RTO | RPO | Runbook |
|----------|-----|-----|---------|
| WAL corruption | 30 min | Last backup (≤ 24h) | [wal-corruption-recovery.md](../operations/wal-corruption-recovery.md) |
| Process crash | < 1 min | 0 (SQLite WAL persists) | Systemd auto-restart |
| Full server loss | < 10 min | Last backup (≤ 24h) | [backup-restore.md](../operations/backup-restore.md) |
| Plugin quarantine storm | < 5 min | 0 | [incident-response.md](../security/incident-response.md) |
| Signing key compromise | < 1 hour | 0 (re-sign from archive) | [signing-model.md](../security/signing-model.md) |

---

## Benchmark Thresholds (CI-enforced)

From `testdata/bench/`:

| Benchmark | Baseline | Max regression |
|-----------|----------|---------------|
| `BenchmarkSign` | ~24µs/op | +20% |
| `BenchmarkVerify` | ~52µs/op | +20% |
| `BenchmarkMerkleNew1024` | ~406µs/op | +30% |
| `BenchmarkMerkleProof` | ~948ns/op | +10% |

Regressions exceeding these thresholds block the release gate.
