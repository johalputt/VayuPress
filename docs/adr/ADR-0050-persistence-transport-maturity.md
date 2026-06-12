# ADR-0050 — Persistence & Transport Maturity (P19)

**Status:** Accepted  
**Date:** 2026-06-12  
**Supersedes:** Direct `*sql.DB` field on `ArticleService`, string-keyed `FireHookFn`, embedded Meilisearch logic in `App`

## Context

After P18, the article service owned business invariants but still accessed SQLite
directly via `s.DB`. The queue worker published string-keyed hooks instead of typed
domain events. Meilisearch search logic lived inside `App` as a loose collection of
methods. Response encoding was duplicated inline. There were no package-level
transaction or persistence abstractions.

## Decision

### 1. Repository pattern — `internal/api/repository.go` + `internal/db/article_repo.go`

`ArticleRepository` interface (consumer-owned in `internal/api`):
```go
type ArticleRepository interface {
    SlugExists(ctx, slug) (bool, error)
    Create(ctx, art) error
    Get(ctx, slug) (Article, error)
    Update(ctx, art) error
    Delete(ctx, slug) error
    List(ctx, page, limit int, tag string) ([]Article, int, error)
    AllTagCSVs(ctx) ([]string, error)
}
```

`sqliteArticleRepo` in `internal/db` satisfies the interface via duck typing —
no import of `internal/api` required. Wired in `main.go`:
```go
a.articles = &api.ArticleService{
    Repo: dbpkg.NewArticleRepo(dbpkg.DB),
    ...
}
```

`ArticleService` no longer holds `*sql.DB`; all persistence is delegated to `Repo`.

### 2. Transaction helper — `internal/db/tx.go`

```go
func RunInTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error
```

Handles `BeginTx`, commit, rollback on error, and panic recovery. Available for
any package that needs atomic multi-step writes.

### 3. Context propagation

All `ArticleService` methods now accept `context.Context` as the first parameter,
propagated from `r.Context()` in handlers and from `context.Background()` in the
repository internals. Repository methods use `*Context` variants of all DB calls.

### 4. Typed domain events — `internal/events/events.go`

String-keyed `FireHookFn func(string, map[string]interface{})` replaced with a
typed `Bus` and three domain event structs:

```go
type ArticleCreated struct{ ID, Slug string; Tags []string }
type ArticleUpdated struct{ ID, Slug string; Tags []string }
type ArticleDeleted struct{ ID, Slug string }
```

`Bus.Subscribe(sample, handler)` uses Go reflection on the concrete type; `Publish`
dispatches to all matching handlers with panic recovery per handler. Queue workers
now call `EventBus.Publish(ctx, events.ArticleCreated{...})` instead of a string
callback. `App.registerEventHandlers()` wires indexing, cache purge, IndexNow, and
plugin forwarding as typed subscribers.

### 5. Search service — `internal/search/search.go`

`Service` interface:
```go
type Service interface {
    Search(ctx, q string, limit int) (Result, error)
    Index(ctx, id, title, slug, content string, tags []string, createdAt int64) error
    Delete(ctx, id string) error
    Ping(ctx) error
}
```

`NewMeiliService(client, db)` returns a concrete implementation with:
- Meilisearch primary path
- SQLite LIKE fallback (automatic, no handler branching)
- Internal circuit breaker (moved from `App.meiliCB`)
- `WaitReady(ctx, svc, maxAttempts)` and `ConfigureIndex(ctx, svc)` package-level helpers

`App.meiliCB`, `App.meiliDo`, `App.configureMeilisearch`, and `App.indexArticle`
all removed. `handleSearch` now calls `a.search.Search(r.Context(), q, limit)`.
Health check wired as: `health.MeiliDoFn = func(...) error { return a.search.Ping(ctx) }`.

### 6. `internal/httputil` — canonical response helpers

`WriteJSON`, `WriteError`, `DecodeJSON` extracted to a shared package with no
application state dependency. Handler-layer wrappers in `main.go` delegate to
`httputil.*`, eliminating inline encoding logic.

## Consequences

- `ArticleService` has zero direct SQL — all persistence through `ArticleRepository`.
- Meilisearch circuit breaker, retry, and fallback logic centralised in `internal/search`.
- Queue workers emit typed events; subscribers are registered once at startup, not
  embedded in a callback closure.
- Context flows end-to-end from HTTP request through service into repository.
- `RunInTx` provides a reusable transaction primitive for future multi-step writes.
- `internal/httputil.ErrorBody` is the single canonical JSON error shape.
- Dependency graph: `httputil → (none)`, `events → logging`, `search → config/metrics`,
  `db/article_repo → db`, `api → db/events`, `queue → events`, `cmd/vayupress → all`.
