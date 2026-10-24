// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/alerts"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/users"
)

// Every site's figures added up are an administrator's: Platform's pages and
// the install-wide console API they read. The primary's own figures are its
// page's, at an editor's level as before, and so are the install's goals and
// segments, which the primary's page writes.
func TestEverySitesFiguresAreAnAdministrators(t *testing.T) {
	for _, p := range []string{
		"/os/platform/analytics", "/os/platform/analytics/explore",
		"/os/api/analytics/realtime", "/os/api/analytics/export", "/os/api/analytics/export-parquet",
		"/os/api/analytics/alerts", "/os/api/analytics/alerts/seen",
	} {
		if got := osPathMinLevel(p); got != accessAdmin {
			t.Errorf("%s opens at level %d, want an administrator's (%d)", p, got, accessAdmin)
		}
	}
	for _, p := range []string{
		"/os/analytics", "/os/analytics/explore", "/os/analytics/realtime", "/os/analytics/export",
		"/os/api/analytics/goals", "/os/api/analytics/segments",
	} {
		if got := osPathMinLevel(p); got != accessEditor {
			t.Errorf("%s opens at level %d, want an editor's (%d)", p, got, accessEditor)
		}
	}
}

// seedTwoSites writes two views of the primary's /home and three of site
// c1's /a, each its own visit.
func seedTwoSites(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,last_at,events,views,landing) VALUES
			('h1','rh1','',datetime('now'),datetime('now'),1,1,'/home'),('h2','rh2','',datetime('now'),datetime('now'),1,1,'/home'),
			('a1','ra1','c1',datetime('now'),datetime('now'),1,1,'/a'),('a2','ra2','c1',datetime('now'),datetime('now'),1,1,'/a'),
			('a3','ra3','c1',datetime('now'),datetime('now'),1,1,'/a')`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES
			('ph1','h1','/home',1,'',datetime('now'),'rh1'),('ph2','h2','/home',1,'',datetime('now'),'rh2'),
			('pa1','a1','/a',1,'c1',datetime('now'),'ra1'),('pa2','a2','/a',1,'c1',datetime('now'),'ra2'),
			('pa3','a3','/a',1,'c1',datetime('now'),'ra3')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

// The primary's page is its own visits: a hosted site's pages are not among
// them. The install's goals are listed there, counted over the primary's
// visits, and on Platform over every site's; a hosted site's page lists its
// own goals alone.
func TestThePrimarysPageIsItsOwnVisits(t *testing.T) {
	a, one := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	ctx := context.Background()
	if _, err := a.analytics.CreateGoal(ctx, "Reached /a", "path", "/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.analytics.CreateSegment(ctx, analytics.ScopeAllDomains, "The operator's readers", []analytics.ExploreFilter{{Dim: "device", Op: analytics.FilterIsNot, Value: "bot"}}); err != nil {
		t.Fatal(err)
	}
	primary := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7)
	for _, want := range []string{"/home", "2 visitors to example.test", "Reached /a", "The operator&#39;s readers", `href="/os/analytics?days=7"`, `data-live-src="/os/analytics/realtime"`} {
		if !strings.Contains(primary, want) {
			t.Errorf("the primary's page has no %q", want)
		}
	}
	// "/a" is on it once: the install's goal's target. Not as a page read.
	if n := strings.Count(primary, ">/a<"); n != 1 {
		t.Errorf("the primary's page names the hosted site's /a %d times, want once (the goal's target)", n)
	}
	if !strings.Contains(primary, "<td>0 <span") {
		t.Error("the install's goal on the primary's page counts other sites' visits")
	}
	platform := a.renderSiteAnalyticsBody(ctx, platformView, "every site", 7)
	if !strings.Contains(platform, "<td>3 <span") || !strings.Contains(platform, "Every site&#39;s analytics") || !strings.Contains(platform, ">Each site<") {
		t.Error("Platform does not count the install's goal over every site, under its name, with each site's share")
	}
	if hosted := a.renderSiteAnalyticsBody(ctx, hostedView(one), one.Host, 7); strings.Contains(hosted, "Reached /a") {
		t.Error("a hosted site's page lists the install's goal")
	}
}

// Platform says what each site adds, with the way to its own page; rows of a
// site no longer registered are named as removed, still counted.
func TestPlatformSaysWhatEachSiteAdds(t *testing.T) {
	a, _ := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('pg','g','/x',1,'gone',datetime('now'),'rg')`); err != nil {
		t.Fatal(err)
	}
	got := a.osEachSiteSection(context.Background(), 7)
	if i, j, k := strings.Index(got, "one.example"), strings.Index(got, "example.test"), strings.Index(got, "A removed site"); i < 0 || !(i < j && j < k) {
		t.Errorf("each site is not listed most viewed first: %d, %d, %d", i, j, k)
	}
	for _, want := range []string{
		`<a href="/os/d/c1/analytics?days=7">one.example</a></td><td>3</td><td>3</td><td>50%`,
		`<a href="/os/analytics?days=7">example.test</a></td><td>2</td><td>2</td><td>33%`,
		`<td>A removed site</td><td>1</td><td>1</td><td>16%`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("each site's share has no %q:\n%s", want, got)
		}
	}
}

// exportCSV asks the export at path, as a hosted site's page when site is
// set, with key's site binding when key is not "".
func exportCSV(a *App, path string, site *domain.Domain, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if site != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, *site))
	}
	if key != "" {
		r = auth.RequestWithKeyInfo(r, apikeys.KeyInfo{ID: "k", DomainID: key})
	}
	w := httptest.NewRecorder()
	a.handleAnalyticsExport(w, r)
	return w
}

// An export is its view's: the primary's own pages from its page, a hosted
// site's from its own, every site's from Platform. A key bound to a hosted
// site reads that site's alone.
func TestTheExportIsItsViewsOwn(t *testing.T) {
	a, one := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	for _, c := range []struct {
		path      string
		site      *domain.Domain
		has, hasn string
	}{
		{"/os/analytics/export?report=pages&days=7", nil, "/home", "/a,"},
		{"/os/d/c1/analytics/export?report=pages&days=7", &one, "/a,", "/home"},
		{"/os/api/analytics/export?report=pages&days=7", nil, "/home", ""},
	} {
		w := exportCSV(a, c.path, c.site, "")
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, c.has) || (c.hasn != "" && strings.Contains(body, c.hasn)) {
			t.Errorf("%s = %d %q, want %q and not %q", c.path, w.Code, body, c.has, c.hasn)
		}
	}
	if w := exportCSV(a, "/os/api/analytics/export?report=pages&days=7", nil, ""); !strings.Contains(w.Body.String(), "/a,") {
		t.Errorf("Platform's export lacks the hosted site's page: %q", w.Body.String())
	}
	// Each report reads the view's own: its visits and its journeys too.
	for report, c := range map[string][2]string{"sessions": {"h1,", "a1,"}, "journey": {"/home", "/a"}} {
		if body := exportCSV(a, "/os/analytics/export?report="+report+"&days=7", nil, "").Body.String(); !strings.Contains(body, c[0]) || strings.Contains(body, c[1]) {
			t.Errorf("the primary's %s export = %q, want %q and not %q", report, body, c[0], c[1])
		}
	}
	// Beyond the raw cutoff, the install's daily sketches give its visitors:
	// every site's, so a site's long export keeps its own count.
	if err := a.analytics.BuildDailyRollup(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	long := strconv.Itoa(analytics.RollupRawCutoffDays + 1)
	if body := exportCSV(a, "/os/analytics/export?report=overview&days="+long, nil, "").Body.String(); !strings.Contains(body, "unique_visitors,2\n") {
		t.Errorf("the primary's long overview = %q, want its own 2 visitors", body)
	}
	if body := exportCSV(a, "/os/api/analytics/export?report=overview&days="+long, nil, "").Body.String(); !strings.Contains(body, "unique_visitors,5\n") {
		t.Errorf("Platform's long overview = %q, want every site's 5 visitors", body)
	}
	if w := exportCSV(a, "/os/analytics/export?report=pages&days=7", nil, "c1"); w.Code != http.StatusForbidden {
		t.Errorf("a key bound to c1 read the primary's export: %d", w.Code)
	}
	if w := exportCSV(a, "/os/d/c1/analytics/export?report=pages&days=7", &one, "c1"); w.Code != http.StatusOK {
		t.Errorf("a key bound to c1 was refused its own export: %d", w.Code)
	}
}

// The primary's alerts are its own, kept with the site its visits are
// recorded with (""), counted over its visits alone, and checked: the
// primary is a live site to the checker. Every site's stay Platform's.
func TestThePrimaryHasItsOwnAlerts(t *testing.T) {
	a, _ := alertsApp(t)
	ctx := context.Background()
	w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, "/os/analytics/alerts", nil, "", alertJSON(""), users.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("the primary's alert answered %d: %s", w.Code, w.Body.String())
	}
	if list, _ := a.alerts.List(ctx, ""); len(list) != 1 {
		t.Fatalf("the primary holds %d alerts, want 1", len(list))
	}
	if list, _ := a.alerts.List(ctx, alerts.Install); len(list) != 0 {
		t.Errorf("the primary's alert was saved as every site's")
	}
	// A hosted site's view does not fire it; the primary's own does.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('pc','s','/',1,'c1',datetime('now'),'v')`); err != nil {
		t.Fatal(err)
	}
	a.checkAlerts(ctx)
	if u, _ := a.alerts.UnseenBySite(ctx); len(u) != 0 {
		t.Fatalf("a hosted site's view fired the primary's alert: %+v", u)
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('pp','s','/',1,'',datetime('now'),'v')`); err != nil {
		t.Fatal(err)
	}
	a.checkAlerts(ctx)
	if u, _ := a.alerts.UnseenBySite(ctx); len(u) != 1 || u[0].Site != "" {
		t.Errorf("the primary's view did not fire its alert: %+v", u)
	}
	if got := alertPage(""); got != "/os/analytics#alerts" {
		t.Errorf("the primary's alerts are at %q", got)
	}
	if got := alertPage(alerts.Install); got != "/os/platform/analytics#alerts" {
		t.Errorf("every site's alerts are at %q", got)
	}
}

// Home's visitors are the primary's own, as its Analytics counts them: an
// editor reads Home.
func TestHomesVisitorsAreThePrimarysOwn(t *testing.T) {
	a, _ := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	got := a.renderHomeVisitors(context.Background())
	if !strings.Contains(got, saKPI("2", "Visitors")) {
		t.Errorf("Home's visitors are not the primary's 2:\n%s", got)
	}
}

// The rail offers every site's figures to an administrator, under System,
// and never to an editor, whose Analytics is the primary's own.
func TestTheRailOffersEverySitesFiguresToAnAdministratorAlone(t *testing.T) {
	offered := func(level int) map[string]string {
		have := map[string]string{}
		for _, app := range saVisibleApps(saSession(level)) {
			for _, sec := range app.Sections {
				have[sec.Href] = app.Key
			}
		}
		return have
	}
	if app := offered(accessAdmin)[platformAnalyticsBase]; app != "system" {
		t.Errorf("an administrator's rail has every site's analytics under %q, want system", app)
	}
	editor := offered(accessEditor)
	if _, ok := editor[platformAnalyticsBase]; ok {
		t.Error("an editor's rail offers every site's analytics")
	}
	if app := editor[primaryAnalyticsBase]; app != "audience" {
		t.Errorf("an editor's rail has the primary's Analytics under %q, want audience", app)
	}
}

// The primary's page, as the console serves it at /os/analytics, is its own
// visits: the handler hands the renderer the primary's view.
func TestTheConsoleServesThePrimarysOwnPage(t *testing.T) {
	t.Cleanup(func() { settleDash(t) })
	a, _ := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := httptest.NewRecorder()
		a.handleOSAnalytics(w, httptest.NewRequest(http.MethodGet, "/os/analytics?days=7", nil))
		body := w.Body.String()
		if !strings.Contains(body, "counted in the background") {
			if !strings.Contains(body, "2 visitors to example.test") {
				t.Errorf("the console's /os/analytics is not the primary's own 2 visitors")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the page was never counted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The install's alerts count every site, so they are on every site's page;
// the primary's page tells an administrator so, and an editor not at all.
// Its own alerts are a fold, with How this works after it.
func TestThePrimarysPageSaysWhereEverySitesAlertsAre(t *testing.T) {
	t.Cleanup(func() { settleDash(t) })
	a, _ := alertsApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	if w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, "/os/api/analytics/alerts", nil, "", alertJSON(""), users.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("every site's alert answered %d: %s", w.Code, w.Body.String())
	}
	const pointer = `<a href="/os/platform/analytics#alerts">Every site&#39;s analytics</a>`
	for role, want := range map[string]bool{users.RoleAdmin: true, users.RoleEditor: false} {
		r := httptest.NewRequest(http.MethodGet, "/os/analytics", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{ID: "u-" + role, Role: role}))
		w := httptest.NewRecorder()
		a.handleOSAnalytics(w, r)
		if got := strings.Contains(w.Body.String(), pointer); got != want {
			t.Errorf("%s: the pointer to every site's alerts shown %v, want %v", role, got, want)
		}
		// How this works closes the page, after every fold, the alerts read
		// off the request included.
		body := w.Body.String()
		if i, j := strings.LastIndex(body, `<details class="sa-fold"><summary class="section-head"><h2 class="section-head__title">Alerts`), strings.LastIndex(body, "How this works"); i < 0 || j < i {
			t.Errorf("%s: How this works (at %d) is not after the Alerts fold (at %d)", role, j, i)
		}
	}
}

// Every site's page shows the figures the install-wide API returns for the
// same range: its visitors, page views and visits, and each top page with
// its views. Read from the API's answer, not from the store the page reads,
// so a page that counted anything differently would be caught.
func TestEverySitesPageShowsWhatTheAPIReturns(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	seedTwoSites(t)
	page := a.renderSiteAnalyticsBody(context.Background(), platformView, "every site", 7)

	var ov analytics.Overview
	readAPI(t, a.handleAnalyticsOverview, "/api/v1/analytics/overview?days=7", &ov)
	if ov.UniqueVisitors != 5 || ov.TotalPageviews != 5 {
		t.Fatalf("seed: the API reads %+v", ov)
	}
	for label, n := range map[string]int{"Visitors": ov.UniqueVisitors, "Page views": ov.TotalPageviews, "Visits": ov.TotalVisits} {
		if want := label + `</a></div><div class="stat-card__value">` + strconv.Itoa(n) + `</div>`; !strings.Contains(page, want) {
			t.Errorf("the page's %s is not the API's %d", label, n)
		}
	}
	var pages []analytics.PageStat
	readAPI(t, a.handleAnalyticsPages, "/api/v1/analytics/pages?days=7&limit=10", &pages)
	if len(pages) != 2 {
		t.Fatalf("seed: the API lists %+v", pages)
	}
	// Read in Top pages alone: Landing pages lists the same paths.
	_, top, _ := strings.Cut(page, ">Top pages<")
	top, _, _ = strings.Cut(top, ">Landing pages<")
	for _, p := range pages {
		if want := `title="` + p.Path + `">` + p.Path + `</span><span class="vp-bar__val">` + strconv.Itoa(p.Pageviews) + `<`; !strings.Contains(top, want) {
			t.Errorf("the page's %s is not the API's %d views", p.Path, p.Pageviews)
		}
	}
}

func readAPI(t *testing.T, h http.HandlerFunc, target string, into any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d (%s)", target, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("%s: %v (%s)", target, err, rec.Body.String())
	}
}

// settleDash waits until no background count is running. A page served from
// the cache starts its count on a goroutine of its own; one that outlived
// its test read the configuration the next test was rewriting.
func settleDash(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		busy := false
		adminDash.mu.Lock()
		for _, e := range adminDash.m {
			busy = busy || e.computing
		}
		adminDash.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a background count was still running 10 s after its test")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
