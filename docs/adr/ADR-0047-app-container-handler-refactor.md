# ADR-0047 — App Container & Handler Refactor (P16)

**Status:** Accepted  
**Date:** 2026-06-12  
**Supersedes:** Inline globals in `cmd/vayupress/main.go`

## Context

After P15, `main.go` still had 10+ package-level mutable globals (`policy`, `meiliCB`, `outboundClient`, `vacuumMu`, `vacuumLastRun`, `smokeTestMutex`, `metricsSnapshot`, `lastBenchmark`, `lastBenchmarkMu`, `benchmarkRunning`) alongside 28 free handler functions that reached into those globals implicitly. This pattern makes state ownership invisible, complicates testing, and precludes future parallelism.

## Decision

### 1. `App` struct (explicit ownership)

Introduced `type App struct` in `main.go` that owns all mutable runtime state:

```go
type App struct {
    outboundClient  *http.Client
    policy          *bluemonday.Policy
    meiliCB         *gobreaker.CircuitBreaker
    pluginRegistry  *plugins.Registry
    pluginManager   *plugins.Manager
    vacuumMu        sync.Mutex
    vacuumLastRun   time.Time
    smokeTestMutex  sync.Mutex
    metricsSnapshot atomic.Value
    lastBenchmark   *benchmarkResult
    lastBenchmarkMu sync.Mutex
    benchmarkRunning int32
}
```

`main()` creates `a := &App{...}` and wires its fields before starting the server.

### 2. Handlers as `*App` methods

All 28 handler/helper functions converted to methods on `*App`:

- `handleCreateArticle`, `handleUpdateArticle`, `handleDeleteArticle`, `handleGetArticle`, `handleListArticles`, `handleSearch`, `handleSearchFallback`, `handleListTags`, `handleStats`, `handleQueueStatus`, `handleQueueReplay`, `handleMetrics`, `handleAdminVacuum`, `handleAdminBackupValidate`, `handleAdminCachePurge`, `handleArticlePage`, `handleSmokeTest`, `handleAdminADR`, `handleHealthBenchmarks`, `handleRunBenchmark`, `handleAdminDashboard`
- `meiliDo`, `configureMeilisearch`, `initMeilisearchCB`, `indexArticle`, `purgeCloudflare`, `pingIndexNow`
- `RegisterHook`, `FireHook`
- `collectAdminMetrics`, `startMetricsSnapshotCollector`, `getAdminSnapshot`
- `pprofHandler`

Route registration uses `a.handleXxx` method values.

### 3. Filesystem migrations (`internal/db/migrations/`)

Extracted embedded SQL strings from `db.go` `init()` into `internal/db/migrations/*.{up,down}.sql`. Loaded at compile time via `//go:embed`. Checksums are identical to the prior inline strings (verified). File naming: `NNN-name.{up,down}.sql`, lexicographically sorted.

### 4. Static analysis: `staticcheck`

Added `staticcheck` step to the `go-native` CI job. Two issues found (`ST1013` — numeric HTTP status literals) and fixed on introduction. CI now enforces staticcheck clean on every push.

## Consequences

- 10 package-level mutable globals eliminated
- 28 functions → methods: state ownership is explicit and greppable
- Filesystem migrations: SQL is now auditable in git diff, reviewable as plain text
- `staticcheck` catches subtle issues (unused code, API misuse, style) on every CI run
- `main()` is now: create `App` → wire → serve → shutdown
- Handlers are independently testable by constructing a test `App` instance
