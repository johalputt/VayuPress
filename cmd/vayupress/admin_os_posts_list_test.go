// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

func seedListPost(t *testing.T, slug, title, status string, featured, isPage int, created string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,featured,is_page,created_at,updated_at)
		 VALUES(?,?,?,'<p>body</p>','notes',?,?,?,?,?)`, "id-"+slug, title, slug, status, featured, isPage, created, created); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
}

func postsPage(t *testing.T, a *App, target string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleOSPosts(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", target, rec.Code)
	}
	return rec.Body.String()
}

// TestThePostsPageIsAList holds Posts to render 01: a list page whose rows
// each open the post in the inspector, with each action on a post drawn once,
// in the inspector, and states as a dot and a word rather than badges.
func TestThePostsPageIsAList(t *testing.T) {
	openMigratedDB(t)
	seedListPost(t, "newest", "Newest", "published", 0, 0, "2026-09-26 10:00:00")
	seedListPost(t, "a-draft", "A draft", "draft", 0, 0, "2026-09-25 10:00:00")
	seedListPost(t, "pinned", "Pinned one", "published", 1, 0, "2026-09-24 10:00:00")
	seedListPost(t, "about", "About", "published", 0, 1, "2026-09-27 10:00:00")
	body := postsPage(t, &App{siteSettings: settings.New(dbpkg.DB), analytics: analytics.New(dbpkg.DB)}, "/os/posts")

	if !strings.Contains(body, `data-page-kind="list"`) {
		t.Error("the Posts page does not say it is a list")
	}
	if n := strings.Count(body, "data-list-row"); n != 3 {
		t.Errorf("%d rows, want the 3 posts and no page", n)
	}
	if !strings.Contains(body, `data-list-src="/os/posts/inspector/a-draft"`) {
		t.Error("a row does not say where its inspector comes from")
	}
	if n := strings.Count(body, `aria-selected="true"`); n != 1 {
		t.Errorf("%d rows selected on load, want the first", n)
	}
	// One copy of each action: the inspector of the selected post. The card
	// faces carried a second, which is what the page no longer has.
	if n := strings.Count(body, `id="post-pub-`); n != 1 {
		t.Errorf("%d publish toggles on the page, want 1", n)
	}
	if !strings.Contains(body, `id="post-pub-newest"`) {
		t.Error("the inspector is not the first post's")
	}
	for _, badge := range []string{"status-pill", `class="chip`, "post-acc", "stat-card"} {
		if strings.Contains(body, badge) {
			t.Errorf("the page still draws %q", badge)
		}
	}
	if !strings.Contains(body, `id="ppin-pinned"><span class="post-pin" role="img" aria-label="Pinned"`) {
		t.Error("the pinned post carries no pin mark")
	}
	row := body[strings.Index(body, `data-list-src="/os/posts/inspector/a-draft"`):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, "Draft") || !strings.HasSuffix(row, `<td class="post-row__num">—</td>`) {
		t.Errorf("a draft's row must read Draft with no views: %s", row)
	}
	// Beside it a published post with no views yet reads 0, so the dash is the
	// draft's, not a missing analytics store's.
	pub := body[strings.Index(body, `data-list-src="/os/posts/inspector/newest"`):]
	if pub = pub[:strings.Index(pub, "</tr>")]; !strings.HasSuffix(pub, `<td class="post-row__num">0</td>`) {
		t.Errorf("a published post's views must be counted: %s", pub)
	}
}

// TestThePostInspectorAnswersOnlyForAPost — the fragment a row loads: a
// post's inspector, and nothing for an address that is not one.
func TestThePostInspectorAnswersOnlyForAPost(t *testing.T) {
	openMigratedDB(t)
	seedListPost(t, "hello", "Hello", "draft", 0, 0, "2026-09-26 10:00:00")
	seedListPost(t, "about", "About", "published", 0, 1, "2026-09-27 10:00:00")
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	get := func(slug string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/os/posts/inspector/"+slug, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("slug", slug)
		rec := httptest.NewRecorder()
		a.handleOSPostInspector(rec, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
		return rec
	}
	rec := get("hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("a post: %d", rec.Code)
	}
	for _, want := range []string{`href="/os/editor/hello"`, `id="post-istate-hello"`, `id="post-pub-hello"`, `id="post-pin-hello"`, "Not public yet"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the inspector lacks %q:\n%s", want, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), `href="/hello"`) {
		t.Error("a draft offers View, which has nothing public to show")
	}
	for slug, code := range map[string]int{"about": http.StatusNotFound, "nothing-here": http.StatusNotFound, "../etc": http.StatusBadRequest} {
		if got := get(slug).Code; got != code {
			t.Errorf("%s: %d, want %d", slug, got, code)
		}
	}
}
