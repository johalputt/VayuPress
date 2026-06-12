# ADR-0048 — Route Domains & Service Extraction (P17)

**Status:** Accepted  
**Date:** 2026-06-12  
**Supersedes:** Inline validation + article logic in `cmd/vayupress/main.go`

## Context

After P16, `main.go` was 1,824 lines of `package main`. While all handlers were methods on `*App` and state was explicit, the file was still a composition monolith: article CRUD logic, admin handlers, metrics exposition, search, middleware, and route registration were all co-located with `func main()`. This made navigation, review, and future extraction harder than necessary.

Simultaneously, business logic (validation, DB queries, queue dispatch) was duplicated between `handleCreateArticle` and `handleBulkCreateArticles`, and fully embedded in handler bodies rather than in a testable service layer.

## Decision

### 1. `internal/api` package — `ArticleService`

Introduced `ArticleService` struct:

```go
type ArticleService struct {
    DB      *sql.DB
    Enqueue func(art db.Article, op string) error
}
```

Methods: `Create`, `BulkCreate`, `Update`, `Delete`, `Get`, `List`, `ListTags`.

Extracted from handlers:
- Validation (`ValidateArticleInput`, `IsValidSlug`, `SplitTags`) — now in `internal/api/validate.go`
- Slug conflict detection, quota checks, DB queries, queue dispatch

`MakeEnqueueFn(db)` provides a concrete `Enqueue` implementation; handlers can inject a stub for unit tests.

### 2. `internal/api` validation helpers exported

`ValidateArticleInput`, `IsValidSlug`, `SplitTags` are exported from `internal/api` and used by both the service and the `cmd/vayupress` handlers. Eliminates the duplicate `validateArticleInput`/`splitTags` in `main.go`.

### 3. `cmd/vayupress/main.go` split into domain files

Same `package main`, multiple files:

| File | Content |
|------|---------|
| `main.go` | `func main()`, version, immutable regexes, response helpers, feed generators |
| `app.go` | `App` struct, constructor, meilisearch methods, plugin hook methods, admin metrics collectors |
| `middleware.go` | SSRF helpers, HTTP middleware, security headers |
| `handlers_articles.go` | Article CRUD + search + tags handlers |
| `handlers_infra.go` | Stats, Prometheus metrics, queue status/replay handlers |
| `handlers_admin.go` | Vacuum, pprof, backup, cache-purge, article page, smoke test, ADR, benchmark, admin dashboard |
| `routes.go` | `(a *App).registerRoutes(r, staticDir)` — all route registration |

`main()` calls `a.registerRoutes(r, staticDir)` — route wiring is no longer inline in bootstrap.

### 4. Package benchmarks

Added `go test -bench=.` benchmarks:
- `internal/metrics`: `BenchmarkHistogramRecord`, `BenchmarkHistogramPercentile`, `BenchmarkCacheHitRatio`
- `internal/api`: `BenchmarkValidateArticleInput`, `BenchmarkSplitTags`, `BenchmarkIsValidSlug`

Results (Intel Xeon 2.8GHz):
- Histogram record: ~20 ns/op
- Histogram P95: ~37 ns/op
- ValidateArticleInput: ~282 ns/op
- SplitTags (10 tags): ~1723 ns/op

## Consequences

- `ArticleService` provides an independently testable business logic layer
- `main.go` reduced from 1,824 lines to ~500 lines
- Route registration is a single, reviewable function
- Validation logic has one canonical location (`internal/api`)
- Handlers call service methods; duplication between create and bulk-create eliminated
- Dependency chain: `logging → config → metrics → db → auth → render → queue → health → api → plugins → main`
