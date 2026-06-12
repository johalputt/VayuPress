//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/plugins"
)

// directEnqueue inserts/updates/deletes directly into the articles table so
// integration tests can read back results without running the queue worker.
func directEnqueue(db interface{ Exec(string, ...interface{}) (interface{ RowsAffected() (int64, error) }, error) }) func(art dbpkg.Article, op string) error {
	return func(art dbpkg.Article, op string) error {
		tagsCSV := ""
		for i, t := range art.Tags {
			if i > 0 {
				tagsCSV += ","
			}
			tagsCSV += t
		}
		switch op {
		case "insert":
			_, err := dbpkg.DB.Exec(
				`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
				art.ID, art.Title, art.Slug, art.Content, tagsCSV,
				art.CreatedAt.Format(time.RFC3339), art.UpdatedAt.Format(time.RFC3339),
			)
			return err
		case "update":
			_, err := dbpkg.DB.Exec(
				`UPDATE articles SET title=?,content=?,tags=?,updated_at=? WHERE slug=?`,
				art.Title, art.Content, tagsCSV,
				art.UpdatedAt.Format(time.RFC3339), art.Slug,
			)
			return err
		case "delete":
			_, err := dbpkg.DB.Exec(`DELETE FROM articles WHERE slug=?`, art.Slug)
			return err
		}
		return fmt.Errorf("unknown op: %s", op)
	}
}

// newTestHarness spins up a full HTTP test server backed by a temp SQLite DB.
// Callers must call the returned cleanup func when the test ends.
func newTestHarness(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()

	os.Setenv("DB_PATH", filepath.Join(dir, "test.db"))
	os.Setenv("API_KEY", "test-key")
	os.Setenv("DOMAIN", "localhost")
	os.Setenv("PORT", "0")
	os.Setenv("CACHE_DIR", dir)
	os.Setenv("STORAGE_QUOTA_GB", "10")
	config.Load()

	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { dbpkg.DB.Close() })

	auth.InitCSRFSecret()

	a := &App{
		policy:         bluemonday.UGCPolicy(),
		outboundClient: &http.Client{Timeout: 5 * time.Second},
		pluginRegistry: plugins.NewRegistry(),
		articles: &api.ArticleService{
			DB:      dbpkg.DB,
			Enqueue: directEnqueue(nil), // nil arg unused — closure captures dbpkg.DB
		},
	}
	a.pluginManager = plugins.New(a.pluginRegistry)
	a.initMeilisearchCB()

	r := chi.NewRouter()
	a.registerRoutes(r, dir)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv, "test-key"
}

func doRequest(t *testing.T, srv *httptest.Server, method, path string, apiKey string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m
}

// =============================================================================
// Tests
// =============================================================================

func TestIntegration_CreateArticle_Returns202(t *testing.T) {
	srv, key := newTestHarness(t)
	resp := doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
		"title": "Hello World", "slug": "hello-world",
		"content": "Integration test content.", "tags": []string{"test"},
	})
	if resp.StatusCode != 202 {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["slug"] != "hello-world" {
		t.Errorf("want slug hello-world, got %v", body["slug"])
	}
	if body["status"] != "queued" {
		t.Errorf("want status queued, got %v", body["status"])
	}
}

func TestIntegration_GetArticle_AfterCreate(t *testing.T) {
	srv, key := newTestHarness(t)
	doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
		"title": "Readable", "slug": "readable-slug",
		"content": "Some content.", "tags": []string{},
	})
	resp := doRequest(t, srv, "GET", "/api/v1/articles/readable-slug", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["title"] != "Readable" {
		t.Errorf("want title Readable, got %v", body["title"])
	}
}

func TestIntegration_GetArticle_NotFound(t *testing.T) {
	srv, _ := newTestHarness(t)
	resp := doRequest(t, srv, "GET", "/api/v1/articles/no-such-slug", "", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	errMap, _ := body["error"].(map[string]interface{})
	if errMap["code"] != "not_found" {
		t.Errorf("want code not_found, got %v", errMap["code"])
	}
}

func TestIntegration_CreateArticle_SlugConflict(t *testing.T) {
	srv, key := newTestHarness(t)
	payload := map[string]interface{}{
		"title": "Dup", "slug": "dup-slug", "content": "x.", "tags": []string{},
	}
	doRequest(t, srv, "POST", "/api/v1/articles", key, payload)
	resp := doRequest(t, srv, "POST", "/api/v1/articles", key, payload)
	if resp.StatusCode != 409 {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	errMap, _ := body["error"].(map[string]interface{})
	if errMap["code"] != "slug_conflict" {
		t.Errorf("want slug_conflict, got %v", errMap["code"])
	}
}

func TestIntegration_ListArticles_Empty(t *testing.T) {
	srv, _ := newTestHarness(t)
	resp := doRequest(t, srv, "GET", "/api/v1/articles", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	articles, _ := body["articles"].([]interface{})
	if len(articles) != 0 {
		t.Errorf("want empty list, got %d items", len(articles))
	}
}

func TestIntegration_DeleteArticle(t *testing.T) {
	srv, key := newTestHarness(t)
	doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
		"title": "To Delete", "slug": "to-delete", "content": "bye.", "tags": []string{},
	})
	resp := doRequest(t, srv, "DELETE", "/api/v1/articles/to-delete", key, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	get := doRequest(t, srv, "GET", "/api/v1/articles/to-delete", "", nil)
	if get.StatusCode != 404 {
		t.Fatalf("want 404 after delete, got %d", get.StatusCode)
	}
}

func TestIntegration_RequiresAPIKey(t *testing.T) {
	srv, _ := newTestHarness(t)
	resp := doRequest(t, srv, "POST", "/api/v1/articles", "", map[string]interface{}{
		"title": "Unauthorized", "slug": "unauth", "content": "x.", "tags": []string{},
	})
	if resp.StatusCode != 401 {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}
