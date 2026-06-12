# ADR-0043: Integration Test Suite (8 Files)

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

Unit tests verify individual functions. Integration tests verify that the entire VayuPress stack — HTTP layer, business logic, database, queue, health endpoints — works correctly together. Without integration tests, subtle interactions between components (e.g., WAL checkpoint during a write burst, migration on a non-empty DB) go untested.

## Decision

The integration test suite consists of 8 test files, each covering a distinct subsystem:

| File | Coverage |
|------|---------|
| `integration_test.go` | Core HTTP routes: GET /posts, POST /api/v1/posts, pagination |
| `auth_integration_test.go` | API key auth, lockout after 10 failures, lockout reset |
| `queue_integration_test.go` | Write queue ordering, dead-letter queue, poison job quarantine (ADR-0035) |
| `migration_integration_test.go` | Forward migration, checksum verification (ADR-0034), drift detection |
| `health_integration_test.go` | All 6 health endpoints, dependency status, readiness during startup |
| `media_integration_test.go` | Upload, path traversal rejection, magic number verification, cleanup |
| `wal_integration_test.go` | WAL checkpoint trigger, RESTART vs PASSIVE selection (ADR-0033) |
| `backup_integration_test.go` | Backup creation, checksum registry, restore validation (ADR-0042) |

Rules:
1. Integration tests use `TestMain` to spin up a real VayuPress instance with an in-memory SQLite DB.
2. Each test file is tagged `//go:build integration` and runs with `go test -tags=integration ./...`.
3. Integration tests do NOT mock the database — they use a real SQLite file in `t.TempDir()`.
4. Integration tests run in CI with the race detector: `go test -race -tags=integration ./...`.
5. Each test file cleans up after itself; no shared state between test files.

## Rationale

Integration tests with a real SQLite instance catch issues that mocked DB tests miss: WAL behavior, migration sequencing, concurrent write safety under the race detector. 8 files covering 8 subsystems gives comprehensive coverage without creating a monolithic test file.

## Consequences

- Positive: Regressions in system-level behavior are caught before release.
- Positive: The race detector on integration tests catches data races in goroutine-heavy paths (queue, checkpoint, plugin pool).
- Negative: Integration tests are slower than unit tests (~5-10s per run). Mitigated by running unit tests and integration tests as separate CI steps.
- Negative: Each new subsystem needs a corresponding integration test file. This is a feature, not a bug — it enforces coverage discipline.
