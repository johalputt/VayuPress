// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// focusApp holds two readers of the primary site, each reading a page only
// they read: one from Google in India, one from Bing in the US.
func focusApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("focus-page")))
	ctx := context.Background()
	for _, r := range []struct{ ip, path, ref, country string }{
		{"198.51.100.1", "/google-only", "https://www.google.com/", "IN"},
		{"198.51.100.1", "/google-next", "https://example.test/google-only", "IN"},
		{"198.51.100.2", "/bing-only", "https://www.bing.com/", "US"},
	} {
		if err := st.Collect(ctx, analytics.CollectRequest{URL: r.path, Referrer: r.ref, Hostname: "example.test", EventType: 1, Lang: "en",
			Geo: analytics.GeoInfo{Country: r.country}}, r.ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", ""); err != nil {
			t.Fatal(err)
		}
	}
	return &App{analytics: st}
}

// The address's filters are the Explorer's: one the Explorer would refuse
// is left out, a later one on the same dimension replaces an earlier, a
// value may hold a colon, and a page takes five.
func TestTheAddressNamesTheFilters(t *testing.T) {
	q := url.Values{focusParam: {"country:is:IN", "nonsense:is:x", "path:contains:/a", "country:is:US", "visit_referrer:is:a:b"}}
	got := focusOf(q)
	if len(got) != 2 || got[0] != (analytics.ExploreFilter{Dim: "country", Op: "is", Value: "US"}) || got[1].Value != "a:b" {
		t.Errorf("focusOf = %+v", got)
	}
	var many []string
	for _, d := range []string{"country", "city", "device", "browser", "os", "language"} {
		many = append(many, d+":is:x")
	}
	if n := len(focusOf(url.Values{focusParam: many})); n != analytics.ExploreMaxFilters {
		t.Errorf("%d filters taken, want %d", n, analytics.ExploreMaxFilters)
	}
}

// Every view of a page narrowed to some readers counts them alone: the
// other reader's page is on none of them, the readers' own pages are where
// their lists are, and what cannot be narrowed (the server's own measures)
// says so.
func TestEveryViewKeepsToTheNarrowedReaders(t *testing.T) {
	a := focusApp(t)
	p := analyticsSpan{days: 7, focus: []analytics.ExploreFilter{{Dim: "visit_referrer", Op: analytics.FilterIs, Value: "www.google.com"}}}
	ctx := a.scoped(context.Background(), p)
	for _, view := range analyticsViews {
		page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, view.id)
		if strings.Contains(page, "bing-only") || strings.Contains(page, "www.bing.com") {
			t.Errorf("%s narrowed to Google counts the reader from Bing", view.id)
		}
		if !strings.Contains(page, `<span class="sa-focus__chip">Came from www.google.com<a class="sa-focus__off" href="/os/analytics?days=7`) {
			t.Errorf("%s does not name what it is narrowed to, with the way to take it off", view.id)
		}
	}
	over := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, "overview")
	for _, want := range []string{"1 visitor to example.test", "/google-only", "/google-next", `data-part="landing"`,
		`data-live-src="/os/analytics/realtime?f=visit_referrer%3Ais%3Awww.google.com"`} {
		if !strings.Contains(over, want) {
			t.Errorf("the narrowed overview has no %q", want)
		}
	}
	if perf := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, "performance"); dbpkg.DB != nil && strings.Contains(perf, "Answered with") && !strings.Contains(perf, "data-unfiltered") {
		t.Error("the server's measures are shown under the filter without saying they are not narrowed")
	}
}

// Every row is the way into its readers: the overview narrowed by the row,
// the page's period and filters kept, a filter on the same dimension
// replaced.
func TestEveryRowIsTheWayIntoItsReaders(t *testing.T) {
	a := focusApp(t)
	plain := analyticsSpan{days: 7}
	ctx := a.scoped(context.Background(), plain)
	for view, want := range map[string]string{
		"sources":  `<a class="vp-bar__door" href="/os/analytics?days=7&amp;f=visit_referrer%3Ais%3Awww.bing.com">`,
		"overview": `<a class="vp-bar__door" href="/os/analytics?days=7&amp;f=path%3Ais%3A%2Fbing-only">`,
		"audience": `href="/os/analytics?days=7&amp;f=country%3Ais%3AUS">`,
		"content":  `<a class="vp-bar__door" href="/os/analytics?days=7&amp;f=landing%3Ais%3A%2Fbing-only">`,
	} {
		if page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, view); !strings.Contains(page, want) {
			t.Errorf("%s has no row leading to %s", view, want)
		}
	}
	channels := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, "sources")
	if !strings.Contains(channels, `f=visit_channel%3Ais%3Aorganic">Organic search</a>`) {
		t.Error("a channel's row does not narrow by its category")
	}
	in := analyticsSpan{days: 7, focus: []analytics.ExploreFilter{{Dim: "country", Op: analytics.FilterIs, Value: "IN"}}}
	door := analyticsDoor(primaryView, in)
	if got := door("country", "US"); got != "/os/analytics?days=7&f=country%3Ais%3AUS" {
		t.Errorf("a second country replaces the first: %s", got)
	}
	if got := door("country", "IN"); got != "" {
		t.Errorf("the row of the readers the page is already narrowed to is a link: %s", got)
	}
	if got := door("path", "/a"); got != "/os/analytics?days=7&f=country%3Ais%3AIN&f=path%3Ais%3A%2Fa" {
		t.Errorf("a row adds its filter to the page's: %s", got)
	}
}

// The live panel, the export and the Explorer of a narrowed page count its
// readers alone.
func TestLiveExportAndExplorerKeepTheFilters(t *testing.T) {
	a := focusApp(t)
	narrow := "f=" + url.QueryEscape("visit_referrer:is:www.bing.com")
	w := httptest.NewRecorder()
	a.handleOSScopedRealtime(w, httptest.NewRequest(http.MethodGet, "/os/analytics/realtime?"+narrow, nil))
	if body := w.Body.String(); !strings.Contains(body, `"active_visitors": 1,`) || strings.Contains(body, "google-only") {
		t.Errorf("the narrowed live panel: %s", body)
	}
	w = httptest.NewRecorder()
	a.handleAnalyticsExport(w, httptest.NewRequest(http.MethodGet, "/os/analytics/export?report=pages&format=csv&days=7&"+narrow, nil))
	if body := w.Body.String(); !strings.Contains(body, "/bing-only") || strings.Contains(body, "google") {
		t.Errorf("the narrowed export:\n%s", body)
	}
	page := explorePageWait(t, a, "/os/analytics/explore?metric=views&by=path&days=7&"+narrow)
	if !strings.Contains(page, "/bing-only") || strings.Contains(page, "/google-only") || !strings.Contains(page, "Narrowed to: Came from www.bing.com") {
		t.Errorf("the narrowed Explorer does not keep to its readers")
	}
	if !strings.Contains(page, `<input type="hidden" name="f" value="visit_referrer:is:www.bing.com">`) {
		t.Error("asking the Explorer again loses the filter")
	}
}

func explorePageWait(t *testing.T, a *App, path string) string {
	t.Helper()
	t.Cleanup(func() { forgetCache(t, exploreDash) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := httptest.NewRecorder()
		a.handleOSExplore(w, httptest.NewRequest(http.MethodGet, path, nil))
		if body := w.Body.String(); !strings.Contains(body, "counted in the background") || time.Now().After(deadline) {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
}
