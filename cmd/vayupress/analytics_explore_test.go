// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// exploreApp is an install with two sites' traffic: site one has three views
// of /a from a search and one of /b, site two one view of /a.
func exploreApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,last_at,events,views,landing,device) VALUES
			('v1','r1','s1',datetime('now'),datetime('now'),3,3,'/a','desktop'),('v2','r2','s1',datetime('now'),datetime('now'),2,1,'/b','mobile'),
			('v3','r3','s2',datetime('now'),datetime('now'),1,1,'/a','desktop')`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,referrer,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			('p1','v1','/a','www.google.com',1,'','s1',datetime('now'),'r1'),('p2','v1','/a','',1,'','s1',datetime('now'),'r1'),
			('p3','v1','/a','',1,'','s1',datetime('now'),'r1'),('p4','v2','/b','',1,'','s1',datetime('now'),'r2'),
			('p5','v2','/b','',2,'=HYPERLINK("http://x")','s1',datetime('now'),'r2'),
			('p6','v3','/a','',1,'','s2',datetime('now'),'r3')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// Registered after the database, so it runs before the database closes.
	t.Cleanup(func() { forgetCache(t, exploreDash) })
	return &App{analytics: analytics.New(dbpkg.DB)}
}

// explorePage asks the Explorer at path (a site's when site is set) and
// waits for the background count.
func explorePage(t *testing.T, a *App, path string, site *domain.Domain) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if site != nil {
			r = r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, *site))
		}
		w := httptest.NewRecorder()
		a.handleOSExplore(w, r)
		if !strings.Contains(w.Body.String(), "counted in the background") || time.Now().After(deadline) {
			return w
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A site's Explorer counts that site alone, splits as asked, and puts each
// value beside the range before.
func TestASitesExplorerCountsItsOwnTraffic(t *testing.T) {
	a := exploreApp(t)
	one := domain.Domain{ID: "s1", Host: "one.test"}
	body := explorePage(t, a, "/os/d/s1/analytics/explore?metric=views&by=path&days=7&compare=1", &one).Body.String()
	for _, want := range []string{
		`data-page-kind="overview"`, `<form method="get" action="/os/d/s1/analytics/explore">`,
		">4<", // site one's views, not the install's 5
		"<td>/a</td><td>3</td><td>75%</td><td>None</td><td>None before</td>",
		"<td>/b</td><td>1</td><td>25%</td>",
		"Last 7 days, by page",
		`href="/os/d/s1/analytics/explore?by=path&amp;compare=1&amp;days=7&amp;metric=views&amp;format=csv"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("site one's Explorer has no %q", want)
		}
	}
	// One view of the primary's own: its Explorer counts that alone, and
	// Platform's every site's six.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,last_at,events,views,landing) VALUES('v0','r0','',datetime('now'),datetime('now'),1,1,'/home');
		INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('p0','v0','/home',1,'',datetime('now'),'r0')`); err != nil {
		t.Fatal(err)
	}
	if own := explorePage(t, a, "/os/analytics/explore?metric=views&by=path&days=7", nil).Body.String(); !strings.Contains(own, "<td>/home</td><td>1</td>") || strings.Contains(own, "<td>/a</td>") {
		t.Errorf("the primary's Explorer does not count its own one view alone")
	}
	all := explorePage(t, a, "/os/platform/analytics/explore?metric=views&days=7", nil).Body.String()
	if !strings.Contains(all, ">6<") || !strings.Contains(all, "Explorer, every site") {
		t.Errorf("Platform's Explorer does not count every site's 6 views, named as every site's")
	}
	ch := explorePage(t, a, "/os/d/s1/analytics/explore?metric=views&by=channel&days=7&fd=path&fo=is&fv=/a&fd=&fo=is&fv=", &one).Body.String()
	for _, want := range []string{"<td>Organic search</td><td>1</td>", "<td>Direct</td><td>2</td>", `where page is &#34;/a&#34;`} {
		if !strings.Contains(ch, want) {
			t.Errorf("views of /a by channel have no %q", want)
		}
	}
}

// A question the figures cannot answer is shown its reason beside the form
// that asked it, and counts nothing.
func TestTheExplorerSaysWhyItCannotAnswer(t *testing.T) {
	a := exploreApp(t)
	body := explorePage(t, a, "/os/analytics/explore?metric=visits&by=path&days=7", nil).Body.String()
	if !strings.Contains(body, "Visits is counted per visit, and a visit has no one page") || !strings.Contains(body, `<form method="get"`) {
		t.Errorf("a split visits cannot have is not refused with its reason beside the form")
	}
	if strings.Contains(body, "Download CSV") {
		t.Errorf("a refused question offers a download")
	}
}

// The file is the answer, every value and the total, with a cell that a
// spreadsheet would run as a formula written as text.
func TestTheExplorersFileIsTheAnswer(t *testing.T) {
	a := exploreApp(t)
	w := explorePage(t, a, "/os/d/s1/analytics/explore?metric=events&by=event&days=7&format=csv", &domain.Domain{ID: "s1"})
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("the file is %q: %s", ct, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), `filename="explore-events-by-event-7d-`) {
		t.Errorf("the file is named %q", w.Header().Get("Content-Disposition"))
	}
	recs, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || strings.Join(recs[0], ",") != "event,events" || recs[2][0] != "Total" || recs[2][1] != "1" {
		t.Fatalf("the file is %q", recs)
	}
	if strings.HasPrefix(recs[1][0], "=") {
		t.Errorf("an event named as a formula is written as one: %q", recs[1][0])
	}
	if w := explorePage(t, a, "/os/analytics/explore?metric=bounce_rate&by=path&days=7&format=csv", nil); w.Code != http.StatusBadRequest {
		t.Errorf("a file of a question that cannot be answered answered %d", w.Code)
	}
}

// The Explorer's cache keeps a bounded number of answers, dropping the one
// counted longest ago, never one being counted.
func TestTheExplorersCacheIsBounded(t *testing.T) {
	c := &adminFragmentCache{m: map[string]*adminFragment{}, sem: make(chan struct{}, 1), max: 2}
	c.m["old"] = &adminFragment{html: "x", at: time.Now().Add(-time.Hour)}
	c.m["busy"] = &adminFragment{at: time.Now().Add(-2 * time.Hour), computing: true}
	c.get("new", time.Minute, func(context.Context) string { return "" })
	if _, ok := c.m["old"]; ok || len(c.m) != 2 {
		t.Errorf("the cache holds %d answers, the oldest among them: %v", len(c.m), c.m)
	}
	if _, ok := c.m["busy"]; !ok {
		t.Errorf("an answer being counted was dropped")
	}
}

// Opened with no question, the Explorer asks page views by page; the form's
// "Nothing" is the total alone. Platform's, whose rows are both sites'.
func TestTheExplorerOpensOnViewsByPage(t *testing.T) {
	a := exploreApp(t)
	if body := explorePage(t, a, "/os/platform/analytics/explore", nil).Body.String(); !strings.Contains(body, "<td>/a</td><td>4</td>") {
		t.Errorf("the Explorer does not open on views by page")
	}
	if body := explorePage(t, a, "/os/platform/analytics/explore?metric=views&by=&days=30", nil).Body.String(); strings.Contains(body, "By page") || !strings.Contains(body, ">5<") {
		t.Errorf("an empty split is not the total alone")
	}
}
