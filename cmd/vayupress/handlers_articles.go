package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sony/gobreaker"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
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
	res, err := a.articles.Create(req.Title, req.Slug, req.Content, req.Tags)
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
	res, err := a.articles.BulkCreate(items)
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
	art, err := a.articles.Update(slug, req.Title, req.Content, req.Tags)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	dbpkg.AuditLog("article.update", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "slug": art.Slug})
}

func (a *App) handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	art, err := a.articles.Delete(slug)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+art.ID)
	writeJSON(w, r, 200, map[string]string{"status": "queued", "slug": slug})
}

func (a *App) handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	art, err := a.articles.Get(slug)
	if err != nil {
		writeAPIError(w, r, api.HTTPStatus(err), api.ErrorCode(err), err.Error(), docsArticles)
		return
	}
	writeJSON(w, r, 200, art)
}

func (a *App) handleListArticles(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tag := r.URL.Query().Get("tag")
	res, err := a.articles.List(page, limit, tag)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "database error", docsErrors)
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"articles": res.Articles, "page": res.Page, "limit": res.Limit, "total": res.Total, "pages": res.Pages})
}

func (a *App) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.articles.ListTags()
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "", docsErrors)
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"tags": tags, "total": len(tags)})
}

// =============================================================================
// Search — Meilisearch with SQLite fallback
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
	if a.meiliCB == nil || a.meiliCB.State() != gobreaker.StateClosed {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	body, _ := json.Marshal(map[string]interface{}{"q": q, "limit": limit, "attributesToRetrieve": []string{"title", "slug", "tags", "created_at"}})
	req, err := http.NewRequestWithContext(context.Background(), "POST", config.Cfg.MeiliHost+"/indexes/articles/search", bytes.NewReader(body))
	if err != nil {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.Copy(w, resp.Body)
}

func (a *App) handleSearchFallback(w http.ResponseWriter, r *http.Request, q string, limit int) {
	pattern := "%" + q + "%"
	rows, err := dbpkg.DB.Query(`SELECT title,slug,tags,created_at FROM articles WHERE title LIKE ? OR content LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`, pattern, pattern, pattern, limit)
	if err != nil {
		writeAPIError(w, r, 500, "search_error", "search unavailable", docsSearch)
		return
	}
	defer rows.Close()
	type hit struct {
		Title, Slug string
		Tags        []string
		CreatedAt   interface{}
	}
	var hits []hit
	for rows.Next() {
		var h hit
		var tagsStr string
		rows.Scan(&h.Title, &h.Slug, &tagsStr, &h.CreatedAt)
		h.Tags = api.SplitTags(tagsStr)
		hits = append(hits, h)
	}
	if hits == nil {
		hits = []hit{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"hits": hits, "query": q, "fallback": true})
}
