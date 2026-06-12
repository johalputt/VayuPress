# ADR-0049 — Thin Handlers & Service Boundaries (P18)

**Status:** Accepted  
**Date:** 2026-06-12  
**Supersedes:** Direct DB access inside `cmd/vayupress/handlers_articles.go`

## Context

After P17, `ArticleService` existed in `internal/api` but the handlers in
`cmd/vayupress/handlers_articles.go` still performed their own validation,
DB queries, quota checks, and queue dispatch — duplicating logic the service
was created to own. The transport layer and the business logic layer were mixed.

Additionally:
- Sentinel errors were inline `fmt.Errorf` literals with no systematic HTTP
  mapping, so each handler hard-coded its own status codes.
- Request structs were anonymous, defined inline per handler.
- `splitTags`, `validateArticleInput`, and `isValidSlug` were duplicated
  between `main.go` and `internal/api`.
- No integration test harness existed; the only tests were unit tests against
  individual packages.

## Decision

### 1. `internal/api/errors.go` — canonical sentinel errors

All service error conditions are declared once:

```go
var (
    ErrNotFound     = errors.New("not found")
    ErrSlugConflict = errors.New("slug already exists")
    ErrInvalidSlug  = errors.New("invalid slug")
    ErrStorageQuota = errors.New("storage quota exceeded")
    ErrBulkLimit    = errors.New("max 1000 articles per request")
)
```

`HTTPStatus(err) int` and `ErrorCode(err) string` provide the HTTP translation.
Handlers call these helpers rather than hard-coding status literals.

### 2. `internal/api/dto.go` — request DTOs

`CreateArticleRequest` and `UpdateArticleRequest` replace anonymous inline
structs in handlers, giving the transport contract a stable, named type.

### 3. `ArticleService.StorageCheckFn` — injectable quota check

```go
StorageCheckFn func() (used, quota int64)
```

The production wiring passes `dbpkg.StorageUsedBytes/QuotaBytes`. Test harnesses
pass nil (quota skipped) or a stub. The check moved from the handler into
`ArticleService.Create`, removing the last business invariant from transport code.

### 4. Thin handlers

Every article handler is now:

```go
func (a *App) handleCreateArticle(w http.ResponseWriter, r *http.Request) {
    var req api.CreateArticleRequest
    if err := readJSONDirect(r, &req); err != nil { ... }
    res, err := a.articles.Create(req.Title, req.Slug, req.Content, req.Tags)
    if err != nil {
        writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
        return
    }
    dbpkg.AuditLog(...)
    writeJSON(w, r, 202, ...)
}
```

Transport responsibilities: decode → call service → respond. Nothing else.

### 5. Dead code removed from `main.go`

`splitTags`, `validateArticleInput`, and `isValidSlug` (duplicates of
`api.SplitTags`, `api.ValidateArticleInput`, `api.IsValidSlug`) removed from
`main.go`. All call sites updated to use the canonical `api.*` versions.
`slugRe` removed alongside `isValidSlug`.

### 6. Integration test harness — `cmd/vayupress/integration_test.go`

Seven tests behind `//go:build integration`, exercising the full HTTP stack
against a real temp-file SQLite DB:

- `TestIntegration_CreateArticle_Returns202`
- `TestIntegration_GetArticle_AfterCreate`
- `TestIntegration_GetArticle_NotFound`
- `TestIntegration_CreateArticle_SlugConflict`
- `TestIntegration_ListArticles_Empty`
- `TestIntegration_DeleteArticle`
- `TestIntegration_RequiresAPIKey`

`directEnqueue` stub writes synchronously to the `articles` table so reads work
immediately without running queue workers. Run with:

```
go test -tags integration ./cmd/vayupress/
```

## Consequences

- Handlers are now pure transport: decode → service call → respond.
- All error → HTTP mapping has one canonical location (`internal/api/errors.go`).
- Storage quota enforcement lives in the service, not the handler.
- Request types are named and importable; anonymous inline structs eliminated.
- Duplicate validation helpers removed from `main.go`; one canonical source.
- Integration test suite provides confidence across the full request path with
  real SQLite, real middleware (auth, CORS, rate limiting), and real routing.
- Dependency graph unchanged: `api` remains below `cmd/vayupress` in import order.
