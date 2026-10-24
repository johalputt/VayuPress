// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// corrupted adds, on a migrated database, a view, its property, its visit and
// a revenue row whose time is not a date, beside a valid view of today.
func corrupted(t *testing.T) *Store {
	t.Helper()
	openTrendingDB(t)
	for _, q := range []string{
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('ok','s','/ok',1,'',datetime('now'),'v1')`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('bad','sb','/bad',1,'','not a date','v2')`,
		`INSERT INTO analytics_event_data(event_id,property_key,property_value,created_at) VALUES('bad','k','v',datetime('now'))`,
		`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,last_at,events,views,landing) VALUES('sb','v2','','garbage',NULL,1,1,'/bad')`,
		`INSERT INTO analytics_revenue(id,session_id,amount,currency,order_id,event_name,created_at) VALUES('r','sb',1,'EUR','o','buy','?? 2026')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return New(dbpkg.DB)
}

// A row whose time is not a date cannot take a report down with it: the
// series, which grouped it under a NULL day and failed to read it, and the
// Explorer's split by day leave it out.
func TestACorruptedRowBreaksNoReport(t *testing.T) {
	s := corrupted(t)
	ctx := context.Background()
	series, err := s.PageviewSeriesScoped(ctx, "", 7)
	if err != nil {
		t.Fatalf("the series failed on a corrupted row: %v", err)
	}
	for _, d := range series {
		if d.Date == "" {
			t.Errorf("the series has a day with no date: %+v", d)
		}
	}
	if len(series) != 1 || series[0].Count != 1 {
		t.Errorf("series %+v, want today's one view", series)
	}
	if _, err := s.Explore(ctx, ExploreQuery{Scope: "", Metric: ExploreViews, By: "day", Days: 7}); err != nil {
		t.Errorf("the Explorer's split by day failed on a corrupted row: %v", err)
	}
	if _, err := s.ViewsOverTimeScoped(ctx, "", 7); err != nil {
		t.Errorf("views over time failed on a corrupted row: %v", err)
	}
}

// The purge removes rows whose time is not a date, which no cutoff reached,
// with the property of such a view; valid rows of today stay.
func TestThePurgeRemovesRowsWhoseTimeIsNotADate(t *testing.T) {
	s := corrupted(t)
	if _, err := s.PurgeOlderThan(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM analytics_pageviews WHERE id='bad'`:        0,
		`SELECT COUNT(*) FROM analytics_event_data WHERE event_id='bad'`: 0,
		`SELECT COUNT(*) FROM analytics_sessions WHERE id='sb'`:          0,
		`SELECT COUNT(*) FROM analytics_revenue WHERE id='r'`:            0,
		`SELECT COUNT(*) FROM analytics_pageviews WHERE id='ok'`:         1,
	} {
		var n int
		if err := dbpkg.DB.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s = %d after the purge, want %d", q, n, want)
		}
	}
}
