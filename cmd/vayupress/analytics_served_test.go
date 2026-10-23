// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayushield"
)

func browserGet(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Host = "shop.example.com"
	r.RemoteAddr = "203.0.113.50:40000"
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	r.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	return r
}

// An uploaded site or a template website carries no beacon, so none of its
// visits was ever counted and its Analytics read nothing. The
// server now counts the view as it serves the page, for a person only, by the
// beacon's rules. One request per rule.
func TestAServedPageIsCountedForAPersonOnly(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	ctx := context.Background()
	views := func() int {
		if err := a.analytics.FlushEvents(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews WHERE event_type=1`).Scan(&n)
		return n
	}

	a.recordServedPage(browserGet("/menu/?utm_source=flyer"))
	if got := views(); got != 1 {
		t.Fatalf("a person's view of a served page counted %d times, want 1", got)
	}
	var path, src string
	_ = dbpkg.DB.QueryRow(`SELECT url_path, utm_source FROM analytics_pageviews`).Scan(&path, &src)
	if path != "/menu" && path != "/menu/" || src != "flyer" {
		t.Errorf("the view was recorded as path %q, utm_source %q", path, src)
	}

	crawler := browserGet("/menu/")
	crawler.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	a.recordServedPage(crawler)
	optOut := browserGet("/menu/")
	optOut.Header.Set("Sec-GPC", "1")
	a.recordServedPage(optOut)
	prefetch := browserGet("/menu/")
	prefetch.Header.Set("Sec-Purpose", "prefetch")
	a.recordServedPage(prefetch)
	head := browserGet("/menu/")
	head.Method = http.MethodHead
	a.recordServedPage(head)
	if got := views(); got != 1 {
		t.Errorf("a crawler, an opt-out, a prefetch or a HEAD was counted: %d views, want 1", got)
	}
}

// A view is a page, not each of its images and stylesheets, and only a page
// that was served.
func TestOnlyAServedPageCounts(t *testing.T) {
	for p, want := range map[string]bool{"/": true, "/about/": true, "/menu.html": true, "/style.css": false, "/logo.png": false, "/app.js": false} {
		if isPagePath(p) != want {
			t.Errorf("isPagePath(%q) = %v, want %v", p, !want, want)
		}
	}
	for code, want := range map[int]bool{http.StatusOK: true, http.StatusNotModified: true, http.StatusMovedPermanently: false, http.StatusNotFound: false} {
		s := &servedStatus{ResponseWriter: httptest.NewRecorder()}
		s.WriteHeader(code)
		if s.served() != want {
			t.Errorf("a %d counted as served = %v, want %v", code, !want, want)
		}
	}
	// Each public path that serves such a page records it.
	for file, fn := range map[string]string{"handlers_customsite.go": "serveCustomIfActive", "handlers_admin.go": "handleHome", "site_documents.go": "serveSitePageIfActive"} {
		if body := goFuncBody(readSourceFile(t, file), fn); body == "" || !strings.Contains(body, "recordServedPage(") {
			t.Errorf("%s no longer records the page it serves", fn)
		}
	}
}

// Audit, before the release: the beacon allows one address 120 events a
// minute (analyticsLimiter), and counting a served page bypassed it. A script
// sending browser headers could add views to any site as fast as the site
// answered. The two now share the one budget.
func TestAServedPageSpendsTheBeaconsBudget(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	for i := 0; i < 200; i++ {
		r := browserGet("/menu/")
		r.RemoteAddr = "198.51.100.77:40000"
		a.recordServedPage(r)
	}
	if err := a.analytics.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews`).Scan(&n)
	if n > 120 {
		t.Errorf("one address added %d views in a burst; the beacon's budget is 120 a minute", n)
	}
}
