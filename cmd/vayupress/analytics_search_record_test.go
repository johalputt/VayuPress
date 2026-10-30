// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// A person's search on the site's search page is counted, a crawler's is
// not, and the site's Analytics shows what was looked for and what was not
// found.
func TestASearchIsCountedForAPersonAndShownOnTheSite(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	ctx := context.Background()
	a.recordSearch(browserGet("/search?q=Pricing"), "Pricing", 0)
	crawler := browserGet("/search?q=crawled")
	crawler.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	a.recordSearch(crawler, "crawled", 3)
	if err := a.analytics.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := a.analytics.SearchesScoped(ctx, analytics.ScopeAllDomains, 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Query != "pricing" || stats[0].Empty != 1 {
		t.Fatalf("searches recorded: %+v; want the person's search for pricing, which found nothing", stats)
	}
	page := renderAllAnalyticsViews(a, ctx, platformView, "every site", 30)
	sec := betweenMarkers(t, page, ">Searches<", ">Goals<")
	if !strings.Contains(sec, "pricing") || !strings.Contains(sec, ">Found nothing<") {
		t.Errorf("the Searches section does not show the search:\n%s", sec)
	}
}
