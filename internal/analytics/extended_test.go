// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/config"
)

func newExtStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := []string{
		`CREATE TABLE analytics_sessions(id TEXT PRIMARY KEY, visitor_id TEXT NOT NULL, browser TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', device TEXT NOT NULL DEFAULT '', screen TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', city TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, url_path TEXT NOT NULL, url_query TEXT NOT NULL DEFAULT '', page_title TEXT NOT NULL DEFAULT '', referrer TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', utm_source TEXT NOT NULL DEFAULT '', utm_medium TEXT NOT NULL DEFAULT '', utm_campaign TEXT NOT NULL DEFAULT '', utm_content TEXT NOT NULL DEFAULT '', utm_term TEXT NOT NULL DEFAULT '', event_type INTEGER NOT NULL DEFAULT 1, event_name TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_event_data(id INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL, property_key TEXT NOT NULL, property_value TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	}
	for _, q := range ddl {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return New(db)
}

func TestCollectStoresNoPII(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	ip := "203.0.113.7"
	ua := "Mozilla/5.0 (Windows NT 10.0) Chrome/120.0 Safari/537.36"
	if err := s.Collect(ctx, CollectRequest{URL: "/about?utm_source=x", Referrer: "https://news.example.com/path", Hostname: "blog.test", EventType: 1}, ip, ua); err != nil {
		t.Fatalf("collect: %v", err)
	}

	// The raw IP and User-Agent must never be persisted in any column.
	var sid, vid, browser, os, device string
	if err := s.db.QueryRow(`SELECT id,visitor_id,browser,os,device FROM analytics_sessions LIMIT 1`).Scan(&sid, &vid, &browser, &os, &device); err != nil {
		t.Fatalf("session row: %v", err)
	}
	if strings.Contains(vid, ip) || strings.Contains(sid, ip) {
		t.Fatalf("visitor/session id leaks IP")
	}
	if browser != "Chrome" || os != "Windows" || device != "Desktop" {
		t.Fatalf("coarse UA parse wrong: %s/%s/%s", browser, os, device)
	}
	// Ensure the full UA string is nowhere in the sessions table.
	var blob string
	_ = s.db.QueryRow(`SELECT COALESCE(group_concat(visitor_id||browser||os||device||screen||language),'') FROM analytics_sessions`).Scan(&blob)
	if strings.Contains(blob, "Mozilla") {
		t.Fatalf("UA string leaked into storage")
	}

	// Referrer must be reduced to a bare host (no path/scheme).
	var ref, path, query string
	if err := s.db.QueryRow(`SELECT referrer,url_path,url_query FROM analytics_pageviews LIMIT 1`).Scan(&ref, &path, &query); err != nil {
		t.Fatalf("pv row: %v", err)
	}
	if ref != "news.example.com" {
		t.Fatalf("referrer not reduced to host: %q", ref)
	}
	if path != "/about" || query != "utm_source=x" {
		t.Fatalf("path/query split wrong: %q %q", path, query)
	}
}

func TestVisitorIDStableAndDistinct(t *testing.T) {
	t.Parallel()
	ip, ua, host := "198.51.100.4", "curl/8", "blog.test"
	a := visitorID(ip, ua, host)
	b := visitorID(ip, ua, host)
	if a != b {
		t.Fatalf("same inputs must yield same id within a day")
	}
	if visitorID("10.0.0.1", ua, host) == a {
		t.Fatalf("different IP must yield different id")
	}
	if !strings.HasPrefix(a, "v") {
		t.Fatalf("unexpected id format %q", a)
	}
}

func TestOverviewAndTopPages(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	// Two pageviews from one visitor, one from another.
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/post", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "2.2.2.2", "Firefox")

	ov, err := s.OverviewSince(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.TotalPageviews != 3 {
		t.Fatalf("pageviews=%d want 3", ov.TotalPageviews)
	}
	if ov.UniqueVisitors != 2 {
		t.Fatalf("unique visitors=%d want 2", ov.UniqueVisitors)
	}
	pages, err := s.TopPages(ctx, 14, 10)
	if err != nil {
		t.Fatalf("toppages: %v", err)
	}
	if len(pages) == 0 || pages[0].Path != "/" || pages[0].Pageviews != 2 {
		t.Fatalf("top page wrong: %+v", pages)
	}
}

func TestRetentionClampsWindow(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "9.9.9.9", "Chrome")

	// A hostile/oversized weeks value must be clamped (no excessive allocation,
	// no panic) and cohort rows must never exceed maxRetentionWeeks columns.
	for _, w := range []int{-5, 0, 12, 100, 1 << 30} {
		rows, err := s.Retention(ctx, w)
		if err != nil {
			t.Fatalf("Retention(%d): %v", w, err)
		}
		for _, r := range rows {
			if len(r.Weeks) > maxRetentionWeeks {
				t.Fatalf("Retention(%d): cohort has %d week columns, want <= %d", w, len(r.Weeks), maxRetentionWeeks)
			}
		}
	}
}

// TestAverageVisitDurationIsActuallyMeasured is the regression test for a metric
// that was declared, returned in JSON, written to the CSV export — and never
// assigned. Every install has always reported an average visit duration of
// exactly 0.
//
// A zero here is worse than an absent field: it reads as a measurement
// ("everyone leaves instantly") rather than as "not measured", and from outside
// the two are indistinguishable. The data needed to compute it was being
// recorded the entire time; nothing ever queried it.
func TestAverageVisitDurationIsActuallyMeasured(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()

	// One visitor, two pageviews five minutes apart — the same session, because
	// sessions bucket on a 30-minute window.
	if err := s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome"); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := s.Collect(ctx, CollectRequest{URL: "/post", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome"); err != nil {
		t.Fatalf("collect: %v", err)
	}
	// Backdate the first pageview so the session spans a measurable stretch.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE analytics_pageviews SET created_at=datetime(created_at,'-300 seconds') WHERE url_path='/'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	ov, err := s.OverviewSince(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.AvgDuration <= 0 {
		t.Fatalf("avg_duration=%v — the metric is still never computed", ov.AvgDuration)
	}
	// ~300s for the one session. Loose bounds: this asserts a real measurement,
	// not an exact clock.
	if ov.AvgDuration < 250 || ov.AvgDuration > 350 {
		t.Errorf("avg_duration=%v seconds, want roughly 300 for a 5-minute visit", ov.AvgDuration)
	}
}

// TestSinglePageviewVisitScoresZeroDuration — with no exit beacon the dwell on a
// visit's last page cannot be measured, so a one-page visit is 0. That is the
// honest answer and it keeps the metric consistent with BounceRate, which counts
// exactly those visits as bounces.
func TestSinglePageviewVisitScoresZeroDuration(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	if err := s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "9.9.9.9", "Chrome"); err != nil {
		t.Fatalf("collect: %v", err)
	}
	ov, err := s.OverviewSince(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.AvgDuration != 0 {
		t.Errorf("avg_duration=%v for a single-pageview visit, want 0 (its dwell is unmeasurable)", ov.AvgDuration)
	}
	if ov.BounceRate != 100 {
		t.Errorf("bounce_rate=%v, want 100 — the two metrics must agree on what a one-page visit is", ov.BounceRate)
	}
}

// TestTheOverviewCountsPageviewsNotEveryEvent.
//
// The headline number on the analytics page was `COUNT(1)` over
// analytics_pageviews with no event_type filter — so every custom event counted
// as a pageview. TopPages DID filter, which is how this surfaced: a site
// reporting 173,097 pageviews had a busiest page of 836, and the two panels
// could not be reconciled because they were counting different things.
//
// An inflated headline is not a cosmetic bug. It is the number an operator
// reports, plans against, and sells on.
func TestTheOverviewCountsPageviewsNotEveryEvent(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()

	// Three real pageviews...
	for i := 0; i < 3; i++ {
		if err := s.Collect(ctx, CollectRequest{URL: "/article", Hostname: "blog.test", EventType: 1}, "203.0.113.1", "Mozilla/5.0 Chrome/120"); err != nil {
			t.Fatalf("collect: %v", err)
		}
	}
	// ...and twenty custom events, which is what an engagement beacon produces.
	for i := 0; i < 20; i++ {
		if err := s.Collect(ctx, CollectRequest{URL: "/article", Hostname: "blog.test", EventType: 2, EventName: "scroll"}, "203.0.113.1", "Mozilla/5.0 Chrome/120"); err != nil {
			t.Fatalf("collect: %v", err)
		}
	}

	ov, err := s.OverviewSince(ctx, 30)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.TotalPageviews != 3 {
		t.Errorf("overview reports %d pageviews, want 3. Custom events are being counted as "+
			"pageviews, so the headline metric is inflated by however chatty the beacon is",
			ov.TotalPageviews)
	}

	// And it must agree with the panel that breaks it down.
	pages, err := s.TopPages(ctx, 30, 10)
	if err != nil {
		t.Fatalf("top pages: %v", err)
	}
	var sum int64
	for _, p := range pages {
		sum += int64(p.Pageviews)
	}
	if sum != int64(ov.TotalPageviews) {
		t.Errorf("the top-pages breakdown sums to %d against an overview total of %d. A total "+
			"and its own breakdown disagreeing is what makes a dashboard untrustworthy",
			sum, ov.TotalPageviews)
	}
}

// TestInternalNavigationIsNotAReferrer.
//
// The classifier already decides a same-site referrer is Direct/"internal" — but
// it records ReferrerDomain BEFORE reaching that decision and never clears it, so
// the host survived into the referrers table while the Audience card correctly
// counted it as direct. One dataset, two panels, opposite answers: a real site's
// referrer list was topped by its own webmail and MCP subdomains, each with a
// count larger than the site's entire pageview total.
func TestInternalNavigationIsNotAReferrer(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	prev := config.Cfg.Domain
	config.Cfg.Domain = "johal.in"
	t.Cleanup(func() { config.Cfg.Domain = prev })

	for _, ref := range []string{
		"johal.in",      // the site itself
		"mail.johal.in", // its own webmail
		"mcp.johal.in",  // its own MCP host
		"JOHAL.IN",      // case must not smuggle it through
	} {
		for i := 0; i < 5; i++ {
			if err := s.Collect(ctx, CollectRequest{URL: "/a", Referrer: "https://" + ref + "/x", Hostname: "johal.in", EventType: 1}, "203.0.113.1", "Mozilla/5.0 Chrome/120"); err != nil {
				t.Fatalf("collect: %v", err)
			}
		}
	}
	// One genuine external referrer.
	if err := s.Collect(ctx, CollectRequest{URL: "/a", Referrer: "https://news.example.com/p", Hostname: "johal.in", EventType: 1}, "203.0.113.2", "Mozilla/5.0 Chrome/120"); err != nil {
		t.Fatalf("collect: %v", err)
	}

	refs, err := s.TopReferrers(ctx, 30, 20)
	if err != nil {
		t.Fatalf("top referrers: %v", err)
	}
	for _, r := range refs {
		if strings.Contains(strings.ToLower(r.Referrer), "johal.in") {
			t.Errorf("%q is listed as a referrer with %d hits — that is the site's own traffic, "+
				"and counting it means the referrer table is topped by the operator's own "+
				"subdomains instead of by where readers actually come from", r.Referrer, r.Count)
		}
	}
	if len(refs) != 1 || !strings.Contains(refs[0].Referrer, "news.example.com") {
		t.Errorf("the genuine external referrer was lost: %+v", refs)
	}
}

// TestAReferrerBreakdownNeverExceedsItsTotal — the property that made the bug
// visible from the outside. A breakdown that sums past the number it breaks down
// is self-evidently wrong, and it is worth asserting directly because it catches
// any future filter mismatch between the two queries, not just this one.
func TestAReferrerBreakdownNeverExceedsItsTotal(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	prev := config.Cfg.Domain
	config.Cfg.Domain = "johal.in"
	t.Cleanup(func() { config.Cfg.Domain = prev })

	for i := 0; i < 4; i++ {
		_ = s.Collect(ctx, CollectRequest{URL: "/a", Referrer: "https://news.example.com/p", Hostname: "johal.in", EventType: 1}, "203.0.113.1", "Mozilla/5.0 Chrome/120")
	}
	for i := 0; i < 30; i++ {
		_ = s.Collect(ctx, CollectRequest{URL: "/a", Referrer: "https://mail.johal.in/x", Hostname: "johal.in", EventType: 2, EventName: "scroll"}, "203.0.113.1", "Mozilla/5.0 Chrome/120")
	}

	ov, _ := s.OverviewSince(ctx, 30)
	refs, _ := s.TopReferrers(ctx, 30, 50)
	var sum int64
	for _, r := range refs {
		sum += int64(r.Count)
	}
	if sum > int64(ov.TotalPageviews) {
		t.Errorf("the referrer breakdown sums to %d against %d total pageviews. A breakdown "+
			"cannot exceed what it breaks down; when it does, the two queries are filtering "+
			"differently and neither number can be trusted", sum, ov.TotalPageviews)
	}
}
