//go:build integration

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/versions"
)

// pathReadings are the ways one page's path can be written that a handler
// which cleans its path serves as the page itself, for page p.
func pathReadings(p string) []string {
	out := []string{p, p + "/", "/" + p, "/." + p, "/x/.." + p}
	for _, pre := range []string{"/os", "/api", "/static", "/health", "/feed", "/mcp", "/.well-known"} {
		out = append(out, pre+"/.."+p)
	}
	return out
}

// shieldedSite is the real router with the shield on and the operator's
// rules set, serving an uploaded site (with /private.html) and a post.
func shieldedSite(t *testing.T, rules string) (*App, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DB_PATH", filepath.Join(dir, "test.db"))
	t.Setenv("API_KEY", "test-key")
	t.Setenv("DOMAIN", "localhost")
	t.Setenv("CACHE_DIR", dir)
	t.Setenv("MEDIA_DIR", filepath.Join(dir, "media"))
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); dbpkg.DB.Close() })
	t.Cleanup(render.WaitForPurges)
	auth.InitCSRFSecret()
	a := &App{policy: bluemonday.UGCPolicy(), outboundClient: &http.Client{Timeout: 5 * time.Second},
		pluginRegistry: plugins.NewRegistry(), eventBus: events.NewBus(),
		articles: &api.ArticleService{Repo: dbpkg.NewArticleRepo(dbpkg.DB), Queue: directWriter{}},
		search:   &noopSearch{}, versionStore: versions.New(dbpkg.DB), siteSettings: settings.New(dbpkg.DB)}
	a.pluginManager = plugins.New(a.pluginRegistry)
	t.Cleanup(a.bgWG.Wait)
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeySiteMode: "custom"}); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(customSiteRoot(), "current")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<p>home</p>", "private.html": "<p>the private body</p>"} {
		if err := os.WriteFile(filepath.Join(cur, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.vayuShield = vayushield.New(vayushield.Config{Enabled: true, BypassPrefixes: shieldBypassPrefixes, ClientIP: auth.ClientIP})
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, Rules: rules})
	r := chi.NewRouter()
	a.registerRoutes(r, dir)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp := doRequest(t, srv, "POST", "/api/v1/articles", "test-key", map[string]any{
		"title": "Private post", "slug": "private-post", "content": "<p>the private body</p>", "tags": []string{"x"}})
	resp.Body.Close()
	return a, srv
}

// fetch reports the status of a reader's GET of p, and whether the private
// body was served.
func fetch(t *testing.T, srv *httptest.Server, p string) (int, bool) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+p, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) Safari/605.1.15")
	req.Header.Set("Accept", "text/html")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.Contains(string(b), "the private body")
}

// No reading of a refused page's path is served: not with a trailing slash,
// a doubled slash or a dot segment, and not behind a prefix the shield
// exempts (/api/../private.html skipped every gate and an uploaded site's
// file server, which cleans the path, served the page), for an exact rule
// and a prefix rule, on an uploaded file and on a post.
func TestNoReadingOfAPathWalksAroundARefusal(t *testing.T) {
	_, srv := shieldedSite(t, "block path=/private.html\nblock path^=/private-post")
	for _, page := range []string{"/private.html", "/private-post"} {
		for _, p := range pathReadings(page) {
			if code, served := fetch(t, srv, p); served {
				t.Errorf("%s served the refused page %s (%d)", p, page, code)
			}
		}
	}
	if code, served := fetch(t, srv, "/"); code != http.StatusOK || served {
		t.Errorf("the site's home answered %d", code)
	}
}

// While the site is down for maintenance, no reading of a page's path is
// served: the console and the machine endpoints stay reachable by their own
// paths, never as a way round to a page.
func TestMaintenanceIsNotWalkedAroundThroughAnExemptPath(t *testing.T) {
	a, srv := shieldedSite(t, "")
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyMaintenanceMode: "on"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range pathReadings("/private.html") {
		if code, served := fetch(t, srv, p); served {
			t.Errorf("%s served a page while the site was down (%d)", p, code)
		}
	}
}
