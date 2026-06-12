# ADR-0046 — Runtime Architecture & Service Boundaries (P15)

**Status:** Accepted  
**Date:** 2026-06-12  
**Supersedes:** Parts of ADR-0045 (inline plugin pool)

## Context

P14 (ADR-0045) decomposed the monolith into 8 `internal/` packages with compiler-enforced boundaries. However, the plugin pool remained inline in `main.go` (~150 lines of concurrent infrastructure), and unit tests were absent for most internal packages.

Two problems drove this ADR:

1. **Plugin pool as highest-risk unextracted code**: The goroutine pool, panic recovery, failure counting, and auto-disable logic is complex concurrent infrastructure that belongs behind a stable API, not scattered in main.go.
2. **No unit tests for internal packages**: Without tests, regressions from future refactors are invisible until production.

## Decision

### 1. Extract plugin subsystem to `internal/plugins`

`internal/plugins` exposes:
- `Registry` — thread-safe hook registration (`Register`, `Handlers`)
- `Manager` — worker pool with context cancellation, WaitGroup drain, panic recovery, auto-disable after `failThresh` failures (`New`, `Start`, `Fire`, `Shutdown`)
- `HookFunc` — the hook function type
- `DefaultPoolSize = 4`, `DefaultQueueDepth = 32`

`main.go` reduced to two package-level vars and two thin wrapper functions:
```go
var (
    pluginRegistry = plugins.NewRegistry()
    pluginManager  = plugins.New(pluginRegistry)
)
func RegisterHook(event string, fn plugins.HookFunc) { pluginRegistry.Register(event, fn) }
func FireHook(event string, payload map[string]interface{}) {
    if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" { return }
    pluginManager.Fire(event, payload)
}
```

### 2. Package-level unit tests for all internal packages

Tests added (with `-race` passing):
- `internal/metrics` — histogram recording, percentiles, Prometheus format, cache hit ratio
- `internal/auth` — CSRF round-trip/invalid, auth lockout/reset, Argon2id, RequireAPIKey middleware
- `internal/logging` — secret redaction regex, LogInfo/LogError no-panic, ReplaceAll redaction
- `internal/config` — EnvOr, GetEnvAsInt, LoadDefaults, ConfigVersion
- `internal/plugins` — register+fire, unknown event, panic isolation, hook disable after failures, shutdown drains, registry snapshot
- `internal/health` — liveness, DB health, ready with/without workers, ethics, Meilisearch up/down, storage, queue
- `internal/queue` — queue status, replay empty, processOneJob maintenance/empty/insert, backoff cap

### 3. SQLite migration compatibility fix

Removed `IF NOT EXISTS` from `ALTER TABLE ADD COLUMN` in migrations 003 and 004. This syntax is not supported in the SQLite version present in the test environment. The existing `runMigrations()` error handler already catches "duplicate column" errors for idempotency.

## Consequences

- `internal/plugins` is independently testable and replaceable
- All 7 internal packages with logic have unit tests
- `main.go` plugin section: ~150 lines → ~15 lines
- `go test -race ./internal/...` passes cleanly
- Dependency graph unchanged: `plugins` sits above `metrics`/`logging`, imported only by `main`
