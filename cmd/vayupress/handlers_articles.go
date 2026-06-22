package main

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

const (
	docsArticles = "https://docs.vayupress.com/api/articles"
	docsErrors   = "https://docs.vayupress.com/api/errors"
	docsSearch   = "https://docs.vayupress.com/api/search"
)

// =============================================================================
// Article API handlers — thin transport layer delegating to a.articles
// =============================================================================

func (a *App) handleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var req api.CreateArticleRequest
	if err := readJSONDirect(r, &req); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), docsArticles)
		return
	}
	res, err := a.articles.Create(r.Context(), req.Title, req.Slug, req.Content, req.Tags)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	dbpkg.AuditLog("article.create", dbpkg.AuditActor(r), res.Slug, "id="+res.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "id": res.ID, "slug": res.Slug})
}

func (a *App) handleBulkCreateArticles(w http.ResponseWriter, r *http.Request) {
	var items []api.BulkCreateItem
	if err := readJSONDirect(r, &items); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), docsArticles)
		return
	}
	res, err := a.articles.BulkCreate(r.Context(), items)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	writeJSON(w, r, 202, map[string]interface{}{"status": "queued", "queued": res.Queued, "skipped": res.Skipped, "skip_reasons": res.SkipReasons})
}

func (a *App) handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var req api.UpdateArticleRequest
	if err := readJSONDirect(r, &req); err != nil {
		writeAPIError(w, r, 400, "invalid_json", "", docsArticles)
		return
	}
	art, err := a.articles.Update(r.Context(), slug, req.Title, req.Content, req.Tags)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	dbpkg.AuditLog("article.update", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "slug": art.Slug})
}

func (a *App) handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	art, err := a.articles.Delete(r.Context(), slug)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+art.ID)
	writeJSON(w, r, 200, map[string]string{"status": "queued", "slug": slug})
}

func (a *App) handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	art, err := a.articles.Get(r.Context(), slug)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	// Drafts are visible only to authenticated operators; to anyone else a draft
	// must be indistinguishable from a non-existent article (no content leak).
	if art.Status == "draft" && !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "article not found", docsArticles)
		return
	}
	writeJSON(w, r, 200, art)
}

func (a *App) handleListArticles(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tag := r.URL.Query().Get("tag")
	res, err := a.articles.List(r.Context(), page, limit, tag)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "database error", docsErrors)
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"articles": res.Articles, "page": res.Page, "limit": res.Limit, "total": res.Total, "pages": res.Pages})
}

func (a *App) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.articles.ListTags(r.Context())
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "", docsErrors)
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"tags": tags, "total": len(tags)})
}

// =============================================================================
// Search — delegates to a.search (Meilisearch + SQLite fallback)
// =============================================================================

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if q == "" {
		writeJSON(w, r, 200, map[string]interface{}{"hits": []interface{}{}, "query": ""})
		return
	}
	res, err := a.search.Search(r.Context(), q, limit)
	if err != nil {
		writeAPIError(w, r, 500, "search_error", "search unavailable", docsSearch)
		return
	}
	writeJSON(w, r, 200, res)
}
