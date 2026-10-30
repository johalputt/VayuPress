// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/span"
)

// A chosen range counts its own days and nothing after them, on every view:
// a reader within it is counted, one the day after it and one today are not,
// and the page says the range in words where it said the last N days.
func TestAChosenRangeCountsItsDaysOnEveryView(t *testing.T) {
	openMigratedDB(t)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from, to := today.AddDate(0, 0, -20), today.AddDate(0, 0, -10)
	for _, q := range []string{
		`INSERT INTO analytics_sessions(id,visitor_id,country,city,device,browser,landing,events,created_at) VALUES('in','v-in','IN','Pune','Mobile','Firefox','/inside',1,?)`,
		`INSERT INTO analytics_sessions(id,visitor_id,country,city,device,browser,landing,events,created_at) VALUES('after','v-after','US','Austin','Desktop','Chrome','/after',1,?)`,
		`INSERT INTO analytics_sessions(id,visitor_id,country,city,device,browser,landing,events,created_at) VALUES('now','v-now','DE','Berlin','Desktop','Safari','/today',1,?)`,
	} {
		at := map[string]time.Time{"in": from.Add(36 * time.Hour), "after": to.Add(30 * time.Hour), "now": today.Add(time.Hour)}[strings.Split(strings.Split(q, "VALUES('")[1], "'")[0]]
		if _, err := dbpkg.DB.Exec(q, at.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
		id := strings.Split(strings.Split(q, "VALUES('")[1], "'")[0]
		path := map[string]string{"in": "/inside", "after": "/after", "now": "/today"}[id]
		ref := map[string]string{"in": "duckduckgo.com", "after": "www.google.com", "now": "chatgpt.com"}[id]
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,referrer,event_type,domain_id,created_at,visitor_id) VALUES(?,?,?,?,1,'',?,?)`,
			"p-"+id, id, path, ref, at.Format("2006-01-02 15:04:05"), "v-"+id); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: analytics.New(dbpkg.DB)}
	ctx := span.With(context.Background(), span.Range{From: from, To: to})
	words := "1 visitor to example.test from " + from.Format("2 Jan 2006") + " to " + to.Format("2 Jan 2006")
	for _, view := range []string{"overview", "audience", "sources", "content"} {
		page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 30, view)
		if !strings.Contains(page, words) {
			t.Errorf("%s does not say %q", view, words)
		}
		for _, outside := range []string{"/after", "/today", "Austin", "Berlin", "www.google.com", "chatgpt.com", "Chrome", "Safari"} {
			if strings.Contains(page, ">"+outside) || strings.Contains(page, `title="`+outside) {
				t.Errorf("%s counts %q, from outside the range", view, outside)
			}
		}
	}
	for view, inside := range map[string]string{"content": "/inside", "audience": "Pune", "sources": "duckduckgo.com"} {
		if page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 30, view); !strings.Contains(page, inside) {
			t.Errorf("%s does not count %q, inside the range", view, inside)
		}
	}
	// The bar and the period menu keep the range; the menu marks it chosen.
	page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 30, "audience")
	q := "from=" + from.Format("2006-01-02") + "&amp;to=" + to.Format("2006-01-02")
	if !strings.Contains(page, `href="/os/analytics?`+q+`&amp;view=sources"`) {
		t.Error("the bar drops the chosen range")
	}
	if !strings.Contains(page, `data-sheet="analytics-range" aria-current="true">Choose dates…`) ||
		!strings.Contains(page, `<summary class="btn btn--sm">`+from.Format("2 Jan 2006")+" – "+to.Format("2 Jan 2006")+`</summary>`) {
		t.Error("the period menu does not name the chosen range")
	}
}

// A range is taken only as two days in order, the last not after today and
// the whole no longer than three years; anything else is the ?days= window.
func TestOnlyASensibleRangeIsTaken(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	ago := func(d int) string { return time.Now().UTC().AddDate(0, 0, -d).Format("2006-01-02") }
	for q, ranged := range map[string]bool{
		"from=" + ago(9) + "&to=" + ago(2):             true,
		"from=" + today + "&to=" + today:               true,
		"from=" + ago(2) + "&to=" + ago(9):             false, // backwards
		"from=" + ago(2) + "&to=" + ago(-1):            false, // ends tomorrow
		"from=" + ago(1095) + "&to=" + today:           false, // over three years
		"from=" + ago(1094) + "&to=" + today:           true,
		"from=2026-02-30&to=" + today:                  false, // no such day
		"from=" + ago(9):                               false, // half a range
		"from=" + ago(9) + "&to=" + ago(2) + "&days=7": true,
	} {
		p := analyticsSpanOf(httptest.NewRequest(http.MethodGet, "/os/analytics?"+q+"&days=7", nil))
		if p.ranged() != ranged {
			t.Errorf("?%s taken as a range: %v, want %v", q, p.ranged(), ranged)
		}
		if !ranged && p.days != 7 {
			t.Errorf("?%s falls back to %d days, want the asked 7", q, p.days)
		}
	}
}

// The bar ends with how long the data is kept: a hosted site's own setting,
// linked to it on Goals, and the install's, which its environment sets.
func TestTheBarSaysHowLongTheDataIsKept(t *testing.T) {
	openMigratedDB(t)
	prev := config.Cfg.AnalyticsRetainDays
	config.Cfg.AnalyticsRetainDays = 365
	t.Cleanup(func() { config.Cfg.AnalyticsRetainDays = prev })
	a := &App{analytics: analytics.New(dbpkg.DB), siteSettings: settings.New(dbpkg.DB)}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForDomain("s1"), map[string]string{settings.KeyAnalyticsSiteRetainDays: "90"}); err != nil {
		t.Fatal(err)
	}
	hosted := a.renderSiteAnalyticsBody(context.Background(), hostedView(domain.Domain{ID: "s1"}), "one.test", 30, "overview")
	if !strings.Contains(hosted, `<a class="sa-kept" href="/os/d/s1/analytics?view=goals#retention">Data kept 90 days</a>`) {
		t.Error("a hosted site's bar does not link its own 90 days")
	}
	primary := a.renderSiteAnalyticsBody(context.Background(), primaryView, "example.test", 30, "overview")
	if !strings.Contains(primary, `>Data kept 365 days</span>`) {
		t.Error("the install's bar does not say its 365 days")
	}
}

// The console's page, asked for a range with ?from=&to=, counts that range:
// today's two readers are not in a range that ended five days ago.
func TestTheConsoleCountsTheRangeItIsAskedFor(t *testing.T) {
	t.Cleanup(func() { settleDash(t) })
	a, _ := shieldSitesApp(t)
	a.analytics = analytics.New(dbpkg.DB)
	seedTwoSites(t)
	from, to := time.Now().UTC().AddDate(0, 0, -10), time.Now().UTC().AddDate(0, 0, -5)
	url := "/os/analytics?from=" + from.Format("2006-01-02") + "&to=" + to.Format("2006-01-02")
	want := "No visits to example.test from " + from.Format("2 Jan 2006") + " to " + to.Format("2 Jan 2006")
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := httptest.NewRecorder()
		a.handleOSAnalytics(w, httptest.NewRequest(http.MethodGet, url, nil))
		if body := w.Body.String(); !strings.Contains(body, "counted in the background") {
			if !strings.Contains(body, want) {
				t.Errorf("the page for a past range does not say %q", want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the page was never counted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
