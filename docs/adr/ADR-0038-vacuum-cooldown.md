# ADR-0038: VACUUM Cooldown + Write-Threshold Guard

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

SQLite's `VACUUM` command rewrites the entire database file, reclaiming deleted pages. Running `VACUUM` under active write load causes contention — it holds an exclusive lock for the entire duration, blocking all readers and writers. Without a guard, a scheduled VACUUM during peak hours would cause a noticeable write pause.

## Decision

1. `VACUUM` is only triggered by an explicit admin API call (`POST /api/v1/admin/vacuum`) — never scheduled automatically.
2. Before executing `VACUUM`, the write queue depth is checked: if `queueDepth > VACUUM_WRITE_THRESHOLD` (default: 50 pending writes), `VACUUM` is rejected with `503 Service Unavailable` and a `Retry-After` header.
3. After a successful `VACUUM`, a cooldown timer is set: subsequent `VACUUM` requests within `VACUUM_COOLDOWN_MINUTES` (default: 60 minutes) are rejected with `429 Too Many Requests`.
4. `VACUUM` is always preceded by `PRAGMA wal_checkpoint(TRUNCATE)` to reduce the WAL before the full rewrite.
5. `VACUUM` duration is emitted as a log event and recorded in `metricVacuumDurationMS`.

## Rationale

Unguarded VACUUM under load causes write timeouts that surface as user-facing errors. The write-threshold guard ensures VACUUM only runs during low-load periods. The cooldown prevents repeated VACUUM calls that would cause sustained I/O pressure.

## Consequences

- Positive: No write contention from VACUUM under normal load.
- Positive: Predictable VACUUM behavior; operators know when it runs.
- Negative: Database fragmentation accumulates if operators never trigger VACUUM. Mitigated by nightly backup (which implicitly measures DB size) and the disk usage runbook.
