package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sony/gobreaker"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// =============================================================================
// Article API handlers
// =============================================================================

func (a *App) handleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title   string   `json:"title"`
		Slug    string   `json:"slug"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := readJSONDirect(r, &in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if err := validateArticleInput(in.Title, in.Slug, in.Content, in.Tags); err != nil {
		writeAPIError(w, r, 400, "validation_error", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if dbpkg.StorageUsedBytes() >= dbpkg.StorageQuotaBytes() {
		writeAPIError(w, r, 413, "storage_quota_exceeded", fmt.Sprintf("quota %dGB exceeded", config.Cfg.StorageQuotaGB), "https://docs.vayupress.com/api/articles")
		return
	}
	var count int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
	if count > 0 {
		writeAPIError(w, r, 409, "slug_conflict", "slug already exists", "https://docs.vayupress.com/api/articles")
		return
	}
	art := dbpkg.Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(art)
	if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		writeAPIError(w, r, 500, "queue_error", err.Error(), "https://docs.vayupress.com/api/errors")
		return
	}
	dbpkg.AuditLog("article.create", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "id": art.ID, "slug": art.Slug})
}

func (a *App) handleBulkCreateArticles(w http.ResponseWriter, r *http.Request) {
	var articles []struct {
		Title, Slug, Content string
		Tags                 []string `json:"tags"`
	}
	if err := readJSONDirect(r, &articles); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if len(articles) > 1000 {
		writeAPIError(w, r, 400, "too_many_articles", "max 1000", "https://docs.vayupress.com/api/articles")
		return
	}
	queued, skipped := 0, 0
	var skipReasons []string
	for _, in := range articles {
		if err := validateArticleInput(in.Title, in.Slug, in.Content, in.Tags); err != nil {
			skipped++
			skipReasons = append(skipReasons, in.Slug+": "+err.Error())
			continue
		}
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
		if count > 0 {
			skipped++
			skipReasons = append(skipReasons, in.Slug+": duplicate slug")
			continue
		}
		a := dbpkg.Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload)
		queued++
	}
	writeJSON(w, r, 202, map[string]interface{}{"status": "queued", "queued": queued, "skipped": skipped, "skip_reasons": skipReasons})
}

func (a *App) handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	var in struct {
		Title   *string  `json:"title"`
		Content *string  `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := readJSONDirect(r, &in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", "", "https://docs.vayupress.com/api/articles")
		return
	}
	if in.Title != nil {
		art.Title = *in.Title
	}
	if in.Content != nil {
		art.Content = *in.Content
	}
	if in.Tags != nil {
		art.Tags = in.Tags
	}
	art.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(art)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'update')`, payload)
	dbpkg.AuditLog("article.update", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "slug": art.Slug})
}

func (a *App) handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	payload, _ := json.Marshal(art)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+art.ID)
	writeJSON(w, r, 200, map[string]string{"status": "queued", "slug": slug})
}

func (a *App) handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/articles")
		return
	}
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	writeJSON(w, r, 200, art)
}

func (a *App) handleListArticles(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tag := r.URL.Query().Get("tag")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit
	type row struct {
		ID, Title, Slug      string
		Tags                 []string
		CreatedAt, UpdatedAt time.Time
	}
	var (
		rows_ *sql.Rows
		err   error
		total int
	)
	if tag != "" {
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE tags LIKE ?`, "%"+tag+"%").Scan(&total)
		rows_, err = dbpkg.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, "%"+tag+"%", limit, offset)
	} else {
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total)
		rows_, err = dbpkg.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "database error", "https://docs.vayupress.com/api/errors")
		return
	}
	defer rows_.Close()
	var result []row
	for rows_.Next() {
		var rr row
		var tagsStr string
		rows_.Scan(&rr.ID, &rr.Title, &rr.Slug, &tagsStr, &rr.CreatedAt, &rr.UpdatedAt)
		rr.Tags = splitTags(tagsStr)
		result = append(result, rr)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"articles": result, "page": page, "limit": limit, "total": total, "pages": (total + limit - 1) / limit})
}

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
		writeAPIError(w, r, 500, "search_error", "search unavailable", "https://docs.vayupress.com/api/search")
		return
	}
	defer rows.Close()
	type hit struct {
		Title, Slug string
		Tags        []string
		CreatedAt   time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		var tagsStr string
		rows.Scan(&h.Title, &h.Slug, &tagsStr, &h.CreatedAt)
		h.Tags = splitTags(tagsStr)
		hits = append(hits, h)
	}
	if hits == nil {
		hits = []hit{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"hits": hits, "query": q, "fallback": true})
}

func (a *App) handleListTags(w http.ResponseWriter, r *http.Request) {
	rows, err := dbpkg.DB.Query(`SELECT tags FROM articles WHERE tags != ''`)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "", "https://docs.vayupress.com/api/errors")
		return
	}
	defer rows.Close()
	tagCount := make(map[string]int)
	for rows.Next() {
		var tagsStr string
		rows.Scan(&tagsStr)
		for _, t := range splitTags(tagsStr) {
			if t != "" {
				tagCount[t]++
			}
		}
	}
	type tagRow struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	result := make([]tagRow, 0, len(tagCount))
	for t, c := range tagCount {
		result = append(result, tagRow{t, c})
	}
	writeJSON(w, r, 200, map[string]interface{}{"tags": result, "total": len(result)})
}
