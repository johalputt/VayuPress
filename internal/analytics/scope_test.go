// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"fmt"
	"testing"
)

// readerView records one reader's page view of path on scope, as the
// counting script's ingest writes it.
func readerView(t *testing.T, s *Store, scope, path string) {
	t.Helper()
	readerViews++
	if _, err := s.db.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES(?,?,?,1,?,datetime('now'),?)`,
		fmt.Sprintf("v%d", readerViews), "s", path, scope, "r"); err != nil {
		t.Fatal(err)
	}
}

var readerViews int

// Two hosted domains that both publish /about must not share a count.
//
// Before migration 080 the per-render tally's key was (day, path), so their
// view counts were ADDED TOGETHER. That is not a missing feature but a wrong
// number: showing a client that figure is showing them another client's
// visits and calling it theirs — a cross-tenant leak with a chart around it.
func TestTwoDomainsWithTheSamePathDoNotShareACounter(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()

	// Client A gets three views of /about, client B gets one, the primary two.
	for i := 0; i < 3; i++ {
		readerView(t, s, "domA", "/about")
	}
	readerView(t, s, "domB", "/about")
	for i := 0; i < 2; i++ {
		readerView(t, s, "", "/about")
	}

	for scope, want := range map[string]int64{"domA": 3, "domB": 1, "": 2} {
		total, top, err := s.ViewsForScope(ctx, scope, 30, 10)
		if err != nil {
			t.Fatalf("scope %q: %v", scope, err)
		}
		if total != want {
			t.Errorf("scope %q total = %d, want %d — counts are being shared between domains",
				scope, total, want)
		}
		if len(top) != 1 || top[0].Path != "/about" || top[0].Views != want {
			t.Errorf("scope %q top pages = %+v, want /about x%d", scope, top, want)
		}
	}
}

// A domain with no traffic must report nothing, not everything. A scope filter
// that silently matches all rows is the exact failure a client would never
// notice and a competitor would.
func TestAnUntrafficedDomainSeesNothing(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	readerView(t, s, "domA", "/busy")
	total, top, err := s.ViewsForScope(ctx, "domQUIET", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(top) != 0 {
		t.Fatalf("a domain with no views reported total=%d top=%+v — the scope filter is "+
			"not filtering, so every client sees every client's traffic", total, top)
	}
}

// A site's Visitors page counts what its Analytics page counts: readers'
// page views. A request the per-render tally counted (a crawler's, an AI
// agent's) is not a page view there, so it is not one here.
func TestASitesVisitorsAreItsReadersPageViews(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TABLE analytics_daily(day TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,views INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,domain_id,path))`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Record(ctx, "domA", "/about", ""); err != nil {
			t.Fatal(err)
		}
	}
	readerView(t, s, "domA", "/about")
	total, _, err := s.ViewsForScope(ctx, "domA", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := s.OverviewSinceScoped(ctx, "domA", 30)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || int64(ov.TotalPageviews) != total {
		t.Errorf("the Visitors page counts %d page views and the Analytics page %d; want 1 each, the one reader's", total, ov.TotalPageviews)
	}
}

// Referrers are scoped by the same key, so a client's traffic sources are theirs.
func TestReferrersAreScopedPerDomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, "domA", "/x", "https://news.example.com/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, "domB", "/x", "https://news.example.com/a"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM analytics_referrers WHERE host='news.example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("the same referrer on two domains produced %d rows, want 2 — referrer "+
			"counts are being merged across clients", n)
	}
}
