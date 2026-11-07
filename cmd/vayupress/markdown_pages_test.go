// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// markdownApp is the hosted-blog install with a members store and its own
// cache directory.
func markdownApp(t *testing.T) (*App, domain.Domain) {
	t.Helper()
	a, _, blog := hostedBlogApp(t)
	a.members = members.New(dbpkg.DB)
	prevCache, prevDomain := config.Cfg.CacheDir, config.Cfg.Domain
	config.Cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	config.Cfg.Domain = "example.com"
	t.Cleanup(func() { config.Cfg.CacheDir, config.Cfg.Domain = prevCache, prevDomain })
	return a, blog
}

func mdPost(t *testing.T, slug, title, content, status, site, created string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,created_at,updated_at,status,is_page,domain_id,excerpt) VALUES(?,?,?,?,?,?,?,0,?,?)`,
		slug, title, slug, content, created, created, status, site, "About "+title); err != nil {
		t.Fatal(err)
	}
}

// getMarkdown asks for path the way the router does, on d's host or the
// install's own when d is nil.
func getMarkdown(a *App, path string, d *domain.Domain, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if d != nil {
		r = requestFor(path, *d)
	}
	if host != "" {
		r.Host = host
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add("slug", strings.TrimPrefix(path, "/"))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	a.handleArticlePage(w, r)
	return w
}

// TestAPostHasMarkdownForAgents — a published public post answers /<slug>.md
// with Markdown made from its own HTML, under its site's own address.
func TestAPostHasMarkdownForAgents(t *testing.T) {
	a, _ := markdownApp(t)
	mdPost(t, "telemetry", "Telemetry in Python", `<style>.x{}</style><h2>Core</h2><p>Use <strong>OTel</strong>.</p>`, "published", "", "2026-04-25 12:00:00")
	w := getMarkdown(a, "/telemetry.md", nil, "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("/telemetry.md: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	for _, want := range []string{"# Telemetry in Python", "- URL: https://example.com/telemetry", "## Core", "Use **OTel**."} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, ".x{}") {
		t.Errorf("the post's style sheet reached the Markdown:\n%s", body)
	}
}

// TestOnlyWhatAReaderMayReadHasMarkdown — a members' post, a draft and
// another site's post have no Markdown here; one seed each.
func TestOnlyWhatAReaderMayReadHasMarkdown(t *testing.T) {
	a, blog := markdownApp(t)
	mdPost(t, "for-members", "For members", "<p>secret words</p>", "published", "", "2026-04-25 12:00:00")
	if err := a.members.SetAccess(context.Background(), "for-members", members.AccessMembers); err != nil {
		t.Fatal(err)
	}
	mdPost(t, "a-draft", "A draft", "<p>draft words</p>", "draft", "", "2026-04-25 12:00:00")
	mdPost(t, "blog-post", "Blog post", "<p>blog words</p>", "published", blog.ID, "2026-04-25 12:00:00")
	for _, c := range []struct {
		name, path, never string
		d                 *domain.Domain
	}{
		{"a members' post", "/for-members.md", "secret words", nil},
		{"a draft", "/a-draft.md", "draft words", nil},
		{"another site's post", "/blog-post.md", "blog words", nil},
	} {
		w := getMarkdown(a, c.path, c.d, "")
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), c.never) {
			t.Errorf("%s: %d, and its words shown=%v; want 404 and nothing of it", c.name, w.Code, strings.Contains(w.Body.String(), c.never))
		}
	}
	if w := getMarkdown(a, "/blog-post.md", &blog, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "https://"+blog.Host+"/blog-post") {
		t.Errorf("the blog's own post on its own host: %d\n%s", w.Code, w.Body.String())
	}
}

// TestTheMarkdownNamesTheSitesOwnAddress — it is cached, so a forged Host
// header must not put another address in every reader's copy.
func TestTheMarkdownNamesTheSitesOwnAddress(t *testing.T) {
	a, _ := markdownApp(t)
	mdPost(t, "addressed", "Addressed", "<p>x</p>", "published", "", "2026-04-25 12:00:00")
	_ = getMarkdown(a, "/addressed.md", nil, "evil.example")
	w := getMarkdown(a, "/addressed.md", nil, "")
	if strings.Contains(w.Body.String(), "evil.example") || !strings.Contains(w.Body.String(), "https://example.com/addressed") {
		t.Errorf("the cached Markdown took the request's Host:\n%s", w.Body.String())
	}
}

// TestTheMarkdownIsPurgedWithThePost — an edit purges the post's page and
// its Markdown together.
func TestTheMarkdownIsPurgedWithThePost(t *testing.T) {
	a, _ := markdownApp(t)
	mdPost(t, "edited", "Before", "<p>x</p>", "published", "", "2026-04-25 12:00:00")
	_ = getMarkdown(a, "/edited.md", nil, "")
	if _, err := dbpkg.DB.Exec(`UPDATE articles SET title='After' WHERE slug='edited'`); err != nil {
		t.Fatal(err)
	}
	render.CachePurgePost("edited")
	if w := getMarkdown(a, "/edited.md", nil, ""); !strings.Contains(w.Body.String(), "# After") {
		t.Errorf("the Markdown outlived the post's purge:\n%s", w.Body.String())
	}
}

// TestTheSiteListsItsPostsInMarkdown — /index.md lists the site's own public
// posts, newest first, and nothing a reader may not read.
func TestTheSiteListsItsPostsInMarkdown(t *testing.T) {
	a, blog := markdownApp(t)
	mdPost(t, "older", "Older", "<p>x</p>", "published", "", "2026-04-01 12:00:00")
	mdPost(t, "newer", "Newer", "<p>x</p>", "published", "", "2026-04-02 12:00:00")
	mdPost(t, "members-only", "Members only", "<p>x</p>", "published", "", "2026-04-03 12:00:00")
	if err := a.members.SetAccess(context.Background(), "members-only", members.AccessMembers); err != nil {
		t.Fatal(err)
	}
	mdPost(t, "elsewhere", "Elsewhere", "<p>x</p>", "published", blog.ID, "2026-04-04 12:00:00")
	w := getMarkdown(a, "/index.md", nil, "")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "## [Newer](https://example.com/newer)") || strings.Index(body, "Newer") > strings.Index(body, "Older") {
		t.Fatalf("the listing does not name the site's posts newest first: %d\n%s", w.Code, body)
	}
	for _, never := range []string{"Members only", "Elsewhere"} {
		if strings.Contains(body, never) {
			t.Errorf("the listing names %q:\n%s", never, body)
		}
	}
}

// TestAnInstallWidePurgeRendersTheMarkdownAgain — CachePurgeAll marks every
// cached page stale rather than deleting it; the Markdown honours the mark.
func TestAnInstallWidePurgeRendersTheMarkdownAgain(t *testing.T) {
	a, _ := markdownApp(t)
	mdPost(t, "marked", "Before", "<p>x</p>", "published", "", "2026-04-25 12:00:00")
	_ = getMarkdown(a, "/marked.md", nil, "")
	if _, err := dbpkg.DB.Exec(`UPDATE articles SET title='After' WHERE slug='marked'`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // the mark is later than the file by more than a clock tick
	render.CachePurgeAll()
	if w := getMarkdown(a, "/marked.md", nil, ""); !strings.Contains(w.Body.String(), "# After") {
		t.Errorf("the Markdown outlived the install-wide purge:\n%s", w.Body.String())
	}
}
