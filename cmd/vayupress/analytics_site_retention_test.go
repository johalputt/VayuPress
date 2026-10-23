// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func saveRetention(a *App, d domain.Domain, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/os/d/"+d.ID+"/api/analytics/retention", strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", d.ID)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	ctx = context.WithValue(ctx, ctxScopedDomainKey, d)
	ctx = context.WithValue(ctx, ctxUserKey, &users.User{ID: "op", Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleOSScopedRetention(w, r.WithContext(ctx))
	return w
}

func withRetention(t *testing.T, days int) {
	t.Helper()
	prev := config.Cfg.AnalyticsRetainDays
	config.Cfg.AnalyticsRetainDays = days
	t.Cleanup(func() { config.Cfg.AnalyticsRetainDays = prev })
}

// A site keeps its data for less time than the install, never more; the
// install's own number is stored as inheriting it.
func TestASitesRetentionIsShorterOrInherited(t *testing.T) {
	withRetention(t, 365)
	a, d := shieldSitesApp(t)
	ctx := context.Background()
	body := func(v string) string {
		return `{"domain_id":"c1","settings":{"` + settings.KeyAnalyticsSiteRetainDays + `":"` + v + `"}}`
	}
	for v, code := range map[string]int{"400": 400, "0": 400, "ninety": 400} {
		if w := saveRetention(a, d, body(v)); w.Code != code {
			t.Errorf("%q answered %d, want %d", v, w.Code, code)
		}
	}
	if w := saveRetention(a, d, body("400")); !strings.Contains(w.Body.String(), "never for more") {
		t.Errorf("a longer window is refused without saying why: %s", w.Body.String())
	}
	if w := saveRetention(a, d, body("90")); w.Code != 200 {
		t.Fatalf("90 days answered %d", w.Code)
	}
	if days, own := a.siteRetainDays(ctx, "c1"); days != 90 || !own {
		t.Errorf("site keeps %d days (own %v), want its own 90", days, own)
	}
	saveRetention(a, d, body("365"))
	if days, own := a.siteRetainDays(ctx, "c1"); days != 365 || own {
		t.Errorf("the install's own number saved as %d (own %v), want it inherited", days, own)
	}
	if w := saveRetention(a, d, `{"domain_id":"other","settings":{}}`); w.Code != 400 {
		t.Errorf("a body naming another site answered %d", w.Code)
	}
}

// The site's purge runs only for a site with its own, shorter window,
// removes that site's old rows, and records the run for the page.
func TestASitesPurgeRunsAndIsRecorded(t *testing.T) {
	withRetention(t, 365)
	a, d := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -40).Format("2006-01-02 15:04:05")
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at) VALUES('a','s','/x',1,'c1',?),('b','s','/x',1,'',?)`, old, old); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain("c1"), map[string]string{settings.KeyAnalyticsSiteRetainDays: "30"}); err != nil {
		t.Fatal(err)
	}
	a.purgeSitesAnalytics(ctx)
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews`).Scan(&n)
	if n != 1 {
		t.Errorf("%d rows left, want the primary's alone", n)
	}
	section := a.siteRetentionSection(ctx, d, "n")
	for _, want := range []string{"Set for this site: kept 30 days, where the install keeps 365 days.", "removing 1 row."} {
		if !strings.Contains(section, want) {
			t.Errorf("the page does not say %q", want)
		}
	}
}
