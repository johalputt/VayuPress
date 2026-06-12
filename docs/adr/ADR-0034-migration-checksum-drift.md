# ADR-0034: Migration Checksum Drift Verification

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

Historical schema migrations are immutable by design — changing an already-applied migration is equivalent to tampering with the database schema contract. Without verification, a developer could silently alter a migration's SQL (e.g., to change a column type) and the error would only manifest at runtime as data corruption.

## Decision

1. Every migration has a SHA-256 checksum of its `Up` SQL, stored in `schema_migrations.checksum`.
2. At startup, `verifyMigrationChecksums()` queries all applied migrations and compares stored checksums against the compiled-in values.
3. If any checksum differs: log `CHECKSUM DRIFT` at error level, increment `metricMigrationDriftDetected`, and **halt startup** with a fatal error.
4. Checksums are computed at compile time (`checksumSQL()`), not at runtime.

## Rationale

A startup halt is the correct response to tampered migrations. Silent continuation risks data corruption that may be discovered only after significant data loss. The halt forces a human to investigate.

## Consequences

- Positive: Tampered migrations are detected immediately at startup, before any data is read or written.
- Positive: Provides a security signal if a supply-chain attack modifies migration files.
- Negative: A legitimate migration SQL change (e.g., whitespace fix) will trigger a drift alert. Developers must update both the SQL and the checksum constant together.
