# ADR-0001: SQLite as Primary Database

**Status**: Accepted  
**Date**: 2024-01-01  
**Author**: @johalputt

## Context

VayuPress needs a primary database. Options considered: PostgreSQL, MySQL, SQLite.

The target deployment is a single VPS (12 GB RAM / 6 vCPU / 250 GB NVMe). Operational simplicity is a primary design goal.

## Decision

SQLite in WAL mode is the default and preferred database.

PostgreSQL is permitted only as an escape hatch for deployments exceeding 10M articles, with a backward-compatible migration path.

## Rationale

- Zero separate process — no daemon to manage.
- WAL mode provides concurrent reads + single-writer without blocking.
- Embedded in the Go binary via go-sqlite3.
- LiteStream enables read replicas if needed (1M–10M articles range).
- Sufficient for 99% of VayuPress deployments.

## Alternatives Considered

- **PostgreSQL as default**: Adds operational complexity (separate process, connection pooling, backup tooling). Rejected for violating the Operational Simplicity Doctrine.
- **MySQL/MariaDB**: Same objections as PostgreSQL. Rejected.

## Consequences

- Positive: Single binary, zero external DB dependency, simple backups (file copy + integrity check).
- Negative: Write serialization (single writer). Mitigated by write queue design.
- Negative: Not suitable for multi-region without LiteStream or custom replication.

## Ethical Implications

None. SQLite is MIT-licensed and fully open source.
