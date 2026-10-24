// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/sitehttp"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// A site's Performance is its own: its readers' vitals, a vital no one
// reported said so, and what the server measured of its requests, each
// status in its place.
func TestASitesPerformanceIsItsOwn(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO vayuanalytics_sessions(session_hash,page_path,entry_time,client_type,domain_id,lcp_ms,cls_x100,cls_seen) VALUES
			('a','/a',datetime('now'),'human','s1',1800,0,1),('b','/a',datetime('now'),'human','s1',2200,4,1),
			('c','/a',datetime('now'),'human','s2',9000,90,1)`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	r := &sitehttp.Recorder{}
	now := time.Now()
	r.Record("s1", 200, true, sitehttp.Cached, 39*time.Millisecond, now) // a bucket bound: read exactly
	r.Record("s1", 404, false, sitehttp.NoCache, 0, now)
	r.Record("s1", 500, false, sitehttp.NoCache, 0, now)
	r.Record("s2", 200, true, sitehttp.NoCache, 900*time.Millisecond, now)
	if err := r.Flush(ctx, dbpkg.DB); err != nil {
		t.Fatal(err)
	}
	a := &App{vaEngagement: vastore.New(dbpkg.DB)}
	got := a.osPerformanceSection(ctx, "s1", 7)
	for _, want := range []string{
		"2200 ms", // site one's LCP p75: the second of two, not site two's 9000
		"0.04",    // its CLS p75, the first page's 0 a reading
		`Response to input (INP)<br><span class="muted">Good is 200 ms or less</span></td><td>Not reported</td>`,
		"<td>Requests</td><td>3</td>", "<td>Not found</td><td>1 (33%)</td>", "1 (33%)</span>",
		"<td>Pages answered in full</td><td>1</td>", "at most 39 ms",
		"<td>Answered from the cache</td><td>1, 39.0 ms</td>", "<td>Rendered for the request</td><td>0, 0.0 ms</td>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("site one's Performance has no %q", want)
		}
	}
	for _, leak := range []string{"9000", "0.90", "900 ms"} {
		if strings.Contains(got, leak) {
			t.Errorf("site one's Performance shows site two's %q", leak)
		}
	}
}

// Through the request logger and the domain resolver as they run: a site's
// page is its page, a stylesheet a request and not a page, a missing path
// its 404, a page the page cache answered or rendered is counted as such,
// and the console and the API are not the site's at all.
func TestTheServerCountsEachSitesOwnRequests(t *testing.T) {
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	_ = sitehttp.Requests.Flush(ctx, dbpkg.DB) // what earlier tests left
	before, _, _ := sitehttp.Read(ctx, dbpkg.DB, "c1", 1)
	h := structuredLoggerMiddleware(a.domainMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cached", "/rendered":
			countPageCache(r, r.URL.Path == "/cached")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<p>page</p>"))
		case "/post", "/os/analytics", "/api/v1/posts":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<p>page</p>"))
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte("p{}"))
		default:
			http.NotFound(w, r)
		}
	})))
	for _, p := range []string{"/post", "/cached", "/cached", "/rendered", "/style.css", "/missing", "/os/analytics", "/api/v1/posts"} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.Host = "one.example"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	if err := sitehttp.Requests.Flush(ctx, dbpkg.DB); err != nil {
		t.Fatal(err)
	}
	after, _, _ := sitehttp.Read(ctx, dbpkg.DB, "c1", 1)
	if d := (after.Requests - before.Requests); d != 6 || after.Pages-before.Pages != 4 || after.S404-before.S404 != 1 {
		t.Errorf("site one gained %d requests, %d pages, %d not found; want 6, 4 and 1", d, after.Pages-before.Pages, after.S404-before.S404)
	}
	if c, rd := after.Cached-before.Cached, after.Rendered-before.Rendered; c != 2 || rd != 1 {
		t.Errorf("site one gained %d pages from the cache and %d rendered; want 2 and 1 (a page the cache took no part in is neither)", c, rd)
	}
}
