// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newAttrStore builds a scratch store carrying the sessions + pageviews +
// goals schema attribution reads (mirrors the shipped migrations).
func newAttrStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := []string{
		`CREATE TABLE analytics_sessions(id TEXT PRIMARY KEY, visitor_id TEXT NOT NULL, browser TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', device TEXT NOT NULL DEFAULT '', screen TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', city TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, url_path TEXT NOT NULL, url_query TEXT NOT NULL DEFAULT '', page_title TEXT NOT NULL DEFAULT '', referrer TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', utm_source TEXT NOT NULL DEFAULT '', utm_medium TEXT NOT NULL DEFAULT '', utm_campaign TEXT NOT NULL DEFAULT '', utm_content TEXT NOT NULL DEFAULT '', utm_term TEXT NOT NULL DEFAULT '', event_type INTEGER NOT NULL DEFAULT 1, event_name TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, domain_id TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE analytics_goals(id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, target TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	}
	for _, q := range ddl {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return New(db)
}

func TestAttributionModels(t *testing.T) {
	s := newAttrStore(t)
	ctx := context.Background()

	if _, err := s.CreateGoal(ctx, "signup", GoalKindPath, "/welcome"); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	mk := func(ip string, path, src, med, camp string) {
		t.Helper()
		// The browser beacon posts UTM as separate fields (the JS extracts
		// them from location.search), which is exactly what Collect stores.
		if err := s.Collect(ctx, CollectRequest{
			URL: path, Hostname: "t", EventType: 1,
			UTMSource: src, UTMMedium: med, UTMCampaign: camp,
		}, ip, "UA-Chrome", ""); err != nil {
			t.Fatalf("collect %s: %v", path, err)
		}
	}
	// Session A (ip .1): ad → newsletter → converts on /welcome.
	mk("203.0.113.1", "/", "google", "cpc", "spring")
	mk("203.0.113.1", "/blog", "newsletter", "email", "april")
	mk("203.0.113.1", "/welcome", "", "", "")
	// Session B (ip .2): newsletter only → converts.
	mk("203.0.113.2", "/", "newsletter", "email", "april")
	mk("203.0.113.2", "/welcome", "newsletter", "email", "april")

	rows, err := s.Attribution(ctx, 30, func() string { gs, _ := s.ListGoals(ctx); return gs[0].ID }())
	if err != nil {
		t.Fatalf("attribution: %v", err)
	}
	get := func(src string) AttributionRow {
		for _, r := range rows {
			if r.Source == src {
				return r
			}
		}
		t.Fatalf("no row for source %q in %+v", src, rows)
		return AttributionRow{}
	}
	gAd := get("google")
	// Session A touches three distinct triples, so linear credit is ⅓ each;
	// B contributes nothing to google.
	if gAd.FirstTouch != 1 || gAd.LastTouch != 0 || gAd.Linear < 0.32 || gAd.Linear > 0.35 {
		t.Fatalf("google credits wrong: %+v", gAd)
	}
	gNl := get("newsletter")
	// newsletter: first touch in A and B (=2); last touch in both (=2);
	// linear = ⅓ (A) + 1 (B).
	if gNl.LastTouch != 2 || gNl.FirstTouch != 2 || gNl.Linear < 1.31 || gNl.Linear > 1.36 {
		t.Fatalf("newsletter credits wrong: %+v", gNl)
	}
}

func TestAttributionUnknownGoal(t *testing.T) {
	s := newAttrStore(t)
	if _, err := s.Attribution(context.Background(), 30, "nope"); err == nil {
		t.Fatal("expected error for unknown goal id")
	}
}
