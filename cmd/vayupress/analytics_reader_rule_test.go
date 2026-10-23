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
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// One rule for who is counted (row 5, audit A5), stated whole: each client
// type, with and without the page's script having run.
func TestWhoAnalyticsCounts(t *testing.T) {
	for _, tc := range []struct {
		ct             botdb.ClientType
		served, beacon bool
		automatedFlag  bool
	}{
		{botdb.TypeHuman, true, true, false},
		{botdb.TypeUnknown, false, true, false},
		{botdb.TypeGoodBot, false, false, true},
		{botdb.TypeAIAgent, false, false, true},
		{botdb.TypeHeadless, false, false, true},
		{botdb.TypeBadBot, false, false, true},
	} {
		if got := countsAsReader(tc.ct, false); got != tc.served {
			t.Errorf("%s fetching a page: counted %v, want %v", tc.ct, got, tc.served)
		}
		if got := countsAsReader(tc.ct, true); got != tc.beacon {
			t.Errorf("%s running the page's script: counted %v, want %v", tc.ct, got, tc.beacon)
		}
		if got := automatedClient(tc.ct); got != tc.automatedFlag {
			t.Errorf("%s: engagement bot flag %v, want %v", tc.ct, got, tc.automatedFlag)
		}
	}
}

// The beacon counted a verified crawler that ran the page's script, which
// the server's own count never did: the same view was a visitor on one path
// and not on the other.
func TestTheBeaconDoesNotCountACrawlerThatRanTheScript(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	beacon := func(ua, ip string) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect",
			strings.NewReader(`{"u":"/menu","h":"shop.example.com","event_type":1}`))
		r.Host = "shop.example.com"
		r.RemoteAddr = ip + ":40000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", ua)
		r.Header.Set("Accept-Language", "en-GB,en;q=0.9")
		a.handleAnalyticsCollect(httptest.NewRecorder(), r)
	}
	beacon("Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "203.0.113.60")
	beacon("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36", "203.0.113.61")
	if err := a.analytics.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews`).Scan(&n)
	if n != 1 {
		t.Errorf("%d views recorded from a crawler's and a reader's beacon, want 1 (the reader's)", n)
	}
}
