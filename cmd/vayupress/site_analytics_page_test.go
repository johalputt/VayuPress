// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// A site's own Analytics showed four figures and its busiest pages, and
// called its visits its visitors. It is now the install's page for that site
// alone: its visitors, visits, landing pages, referrers,
// audience and speed, and none of another site's.
func TestASitesAnalyticsIsThatSitesAlone(t *testing.T) {
	openMigratedDB(t)
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("page")))
	a := &App{analytics: st}
	ctx := context.Background()
	collect := func(site, ip, path, ref string) {
		t.Helper()
		if err := st.Collect(ctx, analytics.CollectRequest{URL: path, Referrer: ref, Hostname: site + ".test", EventType: 1, Lang: "en"},
			ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", site); err != nil {
			t.Fatal(err)
		}
	}
	collect("s1", "198.51.100.1", "/s1-landing", "https://news.ycombinator.com/item?id=1")
	collect("s1", "198.51.100.2", "/s1-landing", "")
	collect("s2", "198.51.100.3", "/s2-only-page", "https://s2-referrer.example/")

	one := a.renderSiteAnalyticsBody(ctx, hostedView(domain.Domain{ID: "s1"}), "one.test", 30, "last 30 days")
	for _, want := range []string{"2 visitors to one.test", "/s1-landing", "news.ycombinator.com", "Landing pages", "What readers&#39; browsers measured"} {
		if !strings.Contains(one, want) {
			t.Errorf("site one's page has no %q", want)
		}
	}
	for _, leak := range []string{"s2-only-page", "s2-referrer.example"} {
		if strings.Contains(one, leak) {
			t.Errorf("site one's page shows site two's %q", leak)
		}
	}
	// Each list on its own: the landing pages are site one's landings alone.
	landing := betweenMarkers(t, one, ">Landing pages<", ">Referrers<")
	if !strings.Contains(landing, "/s1-landing") || strings.Contains(landing, "s2-only-page") {
		t.Errorf("site one's landing pages are not its own:\n%s", landing)
	}
	two := a.renderSiteAnalyticsBody(ctx, hostedView(domain.Domain{ID: "s2"}), "two.test", 30, "last 30 days")
	if !strings.Contains(two, "1 visitor to two.test") || strings.Contains(two, "s1-landing") {
		t.Errorf("site two's page is not site two's alone")
	}
}

// siteAnalyticsPage renders site's Analytics page over a store holding one
// view of each path, read by its own reader: the page the console shows, for
// the tests that hold its markup to the console's rules.
func siteAnalyticsPage(t *testing.T, site string, paths ...string) string {
	t.Helper()
	openMigratedDB(t)
	st := analytics.New(dbpkg.DB)
	a := &App{analytics: st}
	for i, p := range paths {
		if err := st.Collect(context.Background(), analytics.CollectRequest{URL: p, Hostname: "customer.example", EventType: 1, Lang: "en"},
			"198.51.100."+strconv.Itoa(10+i), "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", site); err != nil {
			t.Fatal(err)
		}
	}
	return a.renderSiteAnalyticsBody(context.Background(), hostedView(domain.Domain{ID: site}), "customer.example", 30, "last 30 days")
}

// The page is an overview in the console's page grammar, as the install's
// Analytics is: its range a segmented control on its own address, its
// sections laid open, nothing of the older card-and-accordion pages.
func TestASitesAnalyticsIsAnOverview(t *testing.T) {
	page := siteAnalyticsPage(t, "d1", "/", "/about")
	for _, want := range []string{`data-page-kind="overview"`, `href="/os/d/d1/analytics?days=30">30 days`,
		">Top pages<", ">Landing pages<", ">Referrers<", ">Where they are<", ">Performance<", "/about"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{"mon-acc", `class="card`, `class="badge`, "style="} {
		if strings.Contains(page, not) {
			t.Errorf("the page carries %q", not)
		}
	}
	// One form, the New goal sheet's: nothing else on the page is a form.
	if forms, goal := strings.Count(page, "<form"), strings.Count(page, "<form data-goal-form>"); forms != 1 || goal != 1 {
		t.Errorf("the page carries %d forms, %d of them the goal sheet; want only the goal sheet", forms, goal)
	}
	empty := siteAnalyticsPage(t, "d1")
	if !strings.Contains(empty, "No visits to customer.example") || !strings.Contains(empty, "Visits to this site appear here") {
		t.Error("a site with no traffic does not say so")
	}
}

// A site's page has its own Right now: the live panel polls that site's
// realtime, never the install's, and the route sits with the rest of the
// per-site console, admin-only.
func TestASitesLivePanelPollsThatSite(t *testing.T) {
	openMigratedDB(t)
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("live")))
	a := &App{analytics: st}
	page := a.renderSiteAnalyticsBody(context.Background(), hostedView(domain.Domain{ID: "s1"}), "one.test", 30, "last 30 days")
	if !strings.Contains(page, `data-live-src="/os/d/s1/analytics/realtime"`) {
		t.Errorf("site one's live panel does not poll site one:\n%s", page)
	}
	if strings.Contains(page, `data-live-src="/os/api/analytics/realtime"`) {
		t.Error("site one's live panel polls the install's figures")
	}
	if got := osPathMinLevel("/os/d/s1/analytics/realtime"); got != accessAdmin {
		t.Errorf("osPathMinLevel(site realtime) = %d, want accessAdmin", got)
	}
}
