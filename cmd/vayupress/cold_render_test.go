// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// fillColdRenderSlots takes every render slot, as a crawler burst does, and
// gives them back when the test ends.
func fillColdRenderSlots(t *testing.T) {
	t.Helper()
	coldRenderOnce.Do(func() { coldRenderSlots = make(chan struct{}, coldRenderLimit()) })
	n := 0
	for len(coldRenderSlots) < cap(coldRenderSlots) {
		coldRenderSlots <- struct{}{}
		n++
	}
	saved := coldRenderWait
	coldRenderWait = 50 * time.Millisecond
	t.Cleanup(func() {
		coldRenderWait = saved
		for ; n > 0; n-- {
			<-coldRenderSlots
		}
	})
}

// routeRequest is a request as chi hands it to a handler: the path, and the
// URL parameters the route would have extracted.
func routeRequest(path string, params ...string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rctx := chi.NewRouteContext()
	for i := 0; i+1 < len(params); i += 2 {
		rctx.URLParams.Add(params[i], params[i+1])
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// With every slot taken, each public page that renders from the database is
// answered at once with 503 and Retry-After. One row per page, so removing the
// ceiling from any one of them fails that row by name.
func TestEveryUncachedPublicPageWaitsForASlot(t *testing.T) {
	setupRelatedTestDB(t)
	render.Init(t.TempDir())
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	a := &App{siteSettings: settings.New(dbpkg.DB), articles: &api.ArticleService{Repo: repo}}
	now := time.Now()
	for i := 0; i < 2*homeFeedPageSize; i++ {
		slug := "post-" + itoaSafe(i)
		if err := repo.Create(context.Background(), dbpkg.Article{ID: slug, Title: slug, Slug: slug, Tags: []string{"go"},
			Content: "<p>x</p>", Status: "published", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := dbpkg.RunInTx(context.Background(), dbpkg.DB, func(tx *sql.Tx) error {
			return dbpkg.SyncArticleTagsByIDTx(tx, slug, now, []string{"go"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	fillColdRenderSlots(t)

	for _, c := range []struct {
		page string
		req  *http.Request
		h    http.HandlerFunc
	}{
		{"a post", routeRequest("/post-1", "slug", "post-1"), a.handleArticlePage},
		{"the home page", routeRequest("/"), a.handleHome},
		{"a deeper feed page", routeRequest("/page/2", "page", "2"), a.handleHomePaged},
		{"a tag page", routeRequest("/tags/go", "tag", "go"), a.handleTagPage},
		{"the topic index", routeRequest("/tags"), a.handleTagIndex},
		{"a search", routeRequest("/search?q=post"), a.handleSearchPage},
	} {
		rec := httptest.NewRecorder()
		start := time.Now()
		c.h(rec, c.req)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s with no render slot free got %d (Retry-After %q); want 503 with Retry-After", c.page, rec.Code, rec.Header().Get("Retry-After"))
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: the refusal took %s; it must answer within its short wait, not queue", c.page, d)
		}
	}
}

// The ceiling holds back database renders only. With every slot taken, a page
// with a cache file, the warmer's own render and an operator's preview are
// served as always.
func TestCachedPagesTheWarmerAndPreviewsNeverWaitForASlot(t *testing.T) {
	setupRelatedTestDB(t)
	render.Init(t.TempDir())
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	now := time.Now()
	for _, slug := range []string{"cached-page", "preview-page"} {
		if err := repo.Create(context.Background(), dbpkg.Article{ID: slug, Title: slug, Slug: slug,
			Content: "<p>x</p>", Status: "published", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	cached := filepath.Join(config.Cfg.CacheDir, "posts", "cached-page.html")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("<p>from the cache</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	fillColdRenderSlots(t)

	rec := httptest.NewRecorder()
	a.handleArticlePage(rec, routeRequest("/cached-page", "slug", "cached-page"))
	if rec.Code != http.StatusOK || rec.Body.String() != "<p>from the cache</p>" {
		t.Errorf("a cached page with every slot taken got %d %q; want the cache file", rec.Code, rec.Body.String())
	}

	warm := routeRequest("/")
	warm.Header.Set(warmHeader, "1")
	rec = httptest.NewRecorder()
	a.handleHome(rec, warm)
	if rec.Code == http.StatusServiceUnavailable {
		t.Error("the warmer's render of the home page was refused a slot; the warmer is paced on its own and must never be held back")
	}

	preview := routeRequest("/preview-page", "slug", "preview-page")
	preview.Header.Set("X-API-Key", config.Cfg.APIKey)
	rec = httptest.NewRecorder()
	a.handleArticlePage(rec, preview)
	if rec.Code == http.StatusServiceUnavailable {
		t.Error("an operator's preview was refused a slot; the operator is never held back by crawler load")
	}
}

// A slot is given back when its render ends, so the ceiling bounds renders at
// once rather than renders in total.
func TestARenderSlotIsGivenBack(t *testing.T) {
	coldRenderOnce.Do(func() { coldRenderSlots = make(chan struct{}, coldRenderLimit()) })
	before := len(coldRenderSlots)
	release, ok := admitColdRender(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !ok {
		t.Fatal("a free slot was refused")
	}
	if len(coldRenderSlots) != before+1 {
		t.Fatalf("taking a slot left %d in use, want %d", len(coldRenderSlots), before+1)
	}
	release()
	if len(coldRenderSlots) != before {
		t.Fatalf("a released slot was not given back: %d in use, want %d", len(coldRenderSlots), before)
	}
}
