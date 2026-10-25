# ADR-0060 — Modular Deploy, Full-File Migrations, SQLite Hardening (P0)

**Status:** Accepted  
**Date:** 2026-06-13  

> **As built (checked 2026-10-25).** No `internal/migrations` package or
> `internal/db/config.go` exists. Migrations run whole-file from the embedded
> set in `internal/db` (`runMigrations`, with checksums checked by
> `verifyMigrationChecksums`), and the SQLite settings this ADR asks for
> (WAL, busy_timeout, foreign keys, synchronous=NORMAL) are in the DSNs in
> `internal/db/db.go`. The decision stands; the paths below are as proposed.

## Context

Three P0 gaps:
1. Monolithic deploy script — hard to audit, no rollback, mixes privileges
2. Line-based SQL migration parsing — fails for multi-statement migrations
3. SQLite opened without WAL, busy_timeout, or integrity_check governance

## Decisions

### 1. Modular Deploy (`deploy/`)
Nine single-responsibility scripts replace the monolith. Each is idempotent and independently testable. Symlink-based atomic upgrades (`/opt/vayupress/current`). Rollback in <10 seconds.

### 2. Full-File Migration Engine (`internal/migrations/`)
Go embed (`//go:embed sql/*.sql`), full-file transaction execution (`tx.Exec(blob)`), SHA-256 checksums, `schema_migrations` tracking table, UP/DOWN support.

### 3. SQLite Hardening (`internal/db/config.go`)
WAL journal mode, PRAGMA busy_timeout (5s default), NORMAL synchronous, connection pool (10 max open), `PRAGMA integrity_check` on every Open(), WALStats() for monitoring.

## Consequences
- Deploy is auditable, rollback-safe, privilege-separated
- Migrations are atomic, checksum-verified, idempotent  
- SQLite connections are governed, monitored, and safe under concurrent load
