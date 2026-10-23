// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// siteAPI is the per-site analytics API over an install with a primary
// (example.test) and two client sites, each with its own traffic, answering
// as the key ki.
func siteAPI(t *testing.T) func(ki *apikeys.KeyInfo, path string) (int, string) {
	t.Helper()
	openMigratedDB(t)
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(ctx, "example.test", domain.SiteBlog); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c1','one.example','blog',0,'active',datetime('now'),datetime('now'))`,
		`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c2','two.example','blog',0,'active',datetime('now'),datetime('now'))`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("api")))
	collect := func(scope, ip, path string) {
		if err := st.Collect(ctx, analytics.CollectRequest{URL: path, Hostname: "x", EventType: 1, Lang: "en"},
			ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", scope); err != nil {
			t.Fatal(err)
		}
	}
	collect("c1", "198.51.100.1", "/one-only")
	collect("c2", "198.51.100.2", "/two-only")
	collect("c2", "198.51.100.3", "/two-only")
	collect("", "198.51.100.4", "/primary-only")
	a := &App{analytics: st, domains: reg}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if k, ok := req.Context().Value(testKeyCtx{}).(*apikeys.KeyInfo); ok && k != nil {
				req = auth.RequestWithKeyInfo(req, *k)
			}
			next.ServeHTTP(w, req)
		})
	})
	a.siteAnalyticsRoutes(r)
	return func(ki *apikeys.KeyInfo, path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), testKeyCtx{}, ki))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
}

type testKeyCtx struct{}

// A key bound to one site reads that site through this family, and nothing
// else: another site is not found, so a key cannot learn which sites an
// install hosts, and the figures are that site's alone (row 5, §17).
func TestASiteKeyReadsItsOwnSiteAlone(t *testing.T) {
	get := siteAPI(t)
	bound := &apikeys.KeyInfo{ID: "k1", DomainID: "c1"}
	code, body := get(bound, "/api/v1/sites/c1/analytics/pages")
	if code != http.StatusOK || !strings.Contains(body, "/one-only") {
		t.Fatalf("site one's key reading site one: %d %s", code, body)
	}
	for _, leak := range []string{"/two-only", "/primary-only"} {
		if strings.Contains(body, leak) {
			t.Errorf("site one's pages carry %s", leak)
		}
	}
	// By host too.
	if code, _ := get(bound, "/api/v1/sites/one.example/analytics/overview"); code != http.StatusOK {
		t.Errorf("site one's key reading its site by host: %d", code)
	}
	for _, other := range []string{"/api/v1/sites/c2/analytics/pages", "/api/v1/sites/two.example/analytics/overview",
		"/api/v1/sites/example.test/analytics/realtime", "/api/v1/sites/nowhere/analytics/overview"} {
		code, body := get(bound, other)
		if code != http.StatusNotFound || strings.Contains(body, "two-only") {
			t.Errorf("site one's key reading %s: %d %s; want 404 and nothing of it", other, code, body)
		}
	}
	// The other site's existence is not told apart from a site that is not
	// there: the two answers are the same.
	_, a := get(bound, "/api/v1/sites/c2/analytics/overview")
	_, b := get(bound, "/api/v1/sites/nowhere/analytics/overview")
	if a != b {
		t.Errorf("another site and no site answer differently:\n%s\n%s", a, b)
	}
}

// An install key reads any site, each its own figures; the primary's are
// the ones stored with no site.
func TestAnInstallKeyReadsEachSitesOwnFigures(t *testing.T) {
	get := siteAPI(t)
	install := &apikeys.KeyInfo{ID: "k2"}
	var ov struct {
		Site     string             `json:"site"`
		Overview analytics.Overview `json:"overview"`
	}
	_, body := get(install, "/api/v1/sites/c2/analytics/overview")
	if err := json.Unmarshal([]byte(body), &ov); err != nil || ov.Site != "c2" || ov.Overview.UniqueVisitors != 2 {
		t.Errorf("site two's overview: %s; want site c2 with 2 visitors", body)
	}
	_, body = get(install, "/api/v1/sites/example.test/analytics/pages")
	if !strings.Contains(body, "/primary-only") || strings.Contains(body, "/one-only") {
		t.Errorf("the primary's pages are not its own: %s", body)
	}
}

// The family is analytics: a key without the analytics grant is refused it
// at the door every key passes, and a site-bound key with the grant gets
// through to the site check above.
func TestTheSitesFamilyNeedsTheAnalyticsGrant(t *testing.T) {
	perms := apikeys.NewPermissions()
	perms.Grant(apikeys.SectionAnalytics, apikeys.ActionRead)
	p := "/api/v1/sites/c1/analytics/overview"
	if keyMayCall(apikeys.KeyInfo{ID: "none", Perms: apikeys.NewPermissions()}, "GET", p) {
		t.Error("a key without the analytics grant reads a site's analytics")
	}
	if !keyMayCall(apikeys.KeyInfo{ID: "site", DomainID: "c1", Perms: perms}, "GET", p) {
		t.Error("a site-bound key with the analytics grant is refused its own site's family")
	}
}
