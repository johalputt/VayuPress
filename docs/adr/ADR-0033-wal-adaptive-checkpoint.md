# ADR-0033: WAL Adaptive Checkpoint Strategy

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

SQLite WAL mode accumulates a WAL file that grows unbounded without checkpointing. A fixed 5-minute PASSIVE checkpoint is insufficient under high write load — the WAL file can grow past 32 MB, degrading read performance.

## Decision

1. Every 5 minutes, check WAL file size via `os.Stat(cfg.DBPath + "-wal")`.
2. If WAL size > `WAL_SIZE_THRESHOLD_MB` (default 32 MB): issue a `PRAGMA wal_checkpoint(RESTART)` and increment `metricWALAdaptiveCheckpoints`.
3. If WAL size ≤ threshold: issue `PRAGMA wal_checkpoint(PASSIVE)`.
4. After a RESTART checkpoint, skip the next scheduled tick (adaptive backoff) to let writers drain.
5. Track checkpoint duration in `metricWALCheckpointDurationMS` for observability.

## Rationale

RESTART mode blocks new writers briefly but fully checkpoints the WAL. It's appropriate only when the WAL has grown large. PASSIVE mode is safe for routine checkpoints — it does not block writers at all.

## Consequences

- Positive: WAL file stays bounded; read performance does not degrade under write load.
- Positive: Checkpoint duration is observable via Prometheus metrics.
- Negative: RESTART checkpoint causes a brief write pause. Mitigated by adaptive backoff.
