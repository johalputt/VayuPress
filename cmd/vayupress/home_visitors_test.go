// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Home is the console's first page, and it counted the last fortnight's
// visitors on the request: on johal.in it took more than 15 s to open (3cm).
// It now reads the figures from the dashboard cache. The first request after a
// start finds nothing counted, says so, and does not wait; the count lands in
// the background and the next request shows it.
func TestHomeNeverCountsVisitorsOnTheRequest(t *testing.T) {
	openMigratedDB(t)
	now := time.Now().UTC()
	for i, sid := range []string{"s1", "s1", "s2"} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_sessions(id,visitor_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, sid, "v-"+sid, now); err != nil {
			t.Fatal(err)
		}
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,created_at) VALUES(?,?,?,?)`, "p"+string(rune('0'+i)), sid, "/a", now); err != nil {
			t.Fatal(err)
		}
	}
	adminDash.mu.Lock()
	delete(adminDash.m, homeVisitorsKey)
	adminDash.mu.Unlock()

	a := &App{analytics: analytics.New(dbpkg.DB)}
	cfg := &osSettings{AccessLevel: accessAdmin}
	if got := a.saHomeVisitors(cfg); !strings.Contains(got, "Counting the last 14 days") {
		t.Fatalf("the first Home after a start = %q, want the counting notice and no figures", got)
	}
	var got string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got = a.saHomeVisitors(cfg); strings.Contains(got, "sa-kpis") {
			break
		}
	}
	for _, want := range []string{`<div class="sa-kpi__v">2</div><div class="sa-kpi__l">Visitors</div>`, `<div class="sa-kpi__v">3</div><div class="sa-kpi__l">Page views</div>`} {
		if !strings.Contains(got, want) {
			t.Errorf("Home, once counted, does not show %s:\n%s", want, got)
		}
	}
}
