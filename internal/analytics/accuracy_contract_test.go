// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// The accuracy contract: a day of known events through the ingest path, and
// the report each definition in docs/ANALYTICS.md must give for it. The
// fixture is contractStore below, one event per line with the rule it pins
// beside it. One assertion per figure, so a failure names the rule it broke.
//
// Each of 3.18.8's definitions, put back, fails it: visits cut at fixed
// half-hours (6, not 5), a site's visitors counted as its visits (5, not 3),
// a bounce that ignored the conversion (40 %, not 20 %), one-event visits
// averaged in as 0 s (62 s, not 77.5 s), and visits counted on every day they
// touched (day 2 had one). So does dropping the repeat rule (8 views).
//
// Crawlers and opt-outs never reach the store; that is the caller's rule and
// cmd/vayupress/analytics_served_test.go pins it.

type contractReader struct{ ip, ua string }

var (
	r1 = contractReader{"198.51.100.1", "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0"}
	r2 = contractReader{"198.51.100.2", "Mozilla/5.0 (iPhone) Safari/604.1"}
	r3 = contractReader{"198.51.100.3", "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0"}
)

func contractStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("contract")))
	s.UseSelfHosts(func() []string { return []string{"site.test"} })
	// Yesterday, so the trailing reports (which count back from the real
	// clock) see the fixture too.
	day1 := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	collect := func(h, m, sec int, who contractReader, site, path, ref string, event string) {
		t.Helper()
		at := day1.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second)
		s.now = func() time.Time { return at }
		req := CollectRequest{URL: path, Referrer: ref, Hostname: "site.test", EventType: 1, Lang: "en"}
		if event != "" {
			req.EventType, req.EventName = 2, event
		}
		if err := s.Collect(context.Background(), req, who.ip, who.ua, site); err != nil {
			t.Fatalf("collect %02d:%02d:%02d %s: %v", h, m, sec, path, err)
		}
	}
	self := "https://site.test/"
	collect(9, 0, 0, r1, "s1", "/", "https://www.google.com/search?q=x", "")        // R1 lands from a search
	collect(9, 4, 0, r1, "s1", "/a", self, "")                                      // the site's own page is no referrer
	collect(9, 4, 0, r1, "s1", "/a", self, "")                                      // the same view delivered twice: one view
	collect(9, 29, 50, r2, "s1", "/b", "", "")                                      // R2 lands just before the half-hour
	collect(9, 30, 10, r2, "s1", "/c", self, "")                                    // just after it: the same visit
	collect(10, 10, 0, r2, "s1", "/b", "", "")                                      // 40 idle minutes: a new visit, a bounce
	collect(11, 0, 0, r3, "s1", "/a", "https://news.ycombinator.com/item?id=1", "") // R3 lands from a link
	collect(11, 0, 30, r3, "s1", "/a", "", "signup")                                // a conversion: not a view, not a bounce
	collect(23, 59, 50, r1, "s1", "/", "", "")                                      // R1 again, late: a visit of day 1
	collect(24, 0, 10, r1, "s1", "/a", self, "")                                    // past midnight: the same visit, a view of day 2
	// Another site on the install: R1 reads it too. It must not reach s1's
	// figures.
	collect(9, 10, 0, r1, "s2", "/", "", "")
	s.now = time.Now
	return s, day1
}

func TestTheAccuracyContract(t *testing.T) {
	s, day1 := contractStore(t)
	ctx := context.Background()
	d := func(n int) string { return day1.AddDate(0, 0, n).Format("2006-01-02") }

	ov, err := s.overviewWindow(ctx, "s1", d(0), d(1))
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalPageviews != 7 {
		t.Errorf("page views, day 1 = %d, want 7: a retry counts once, a custom event is not a view, 00:00:10 is day 2's", ov.TotalPageviews)
	}
	if ov.TotalVisits != 5 {
		t.Errorf("visits, day 1 = %d, want 5: a visit ends after 30 idle minutes, not at a clock's half-hour", ov.TotalVisits)
	}
	if ov.UniqueVisitors != 3 {
		t.Errorf("the site's visitors, day 1 = %d, want 3: counted from its own rows, never its visits", ov.UniqueVisitors)
	}
	if ov.BounceRate != 20 {
		t.Errorf("bounce rate, day 1 = %v, want 20 (1 of 5): one page view and nothing else; a conversion is engagement", ov.BounceRate)
	}
	if want := (240.0 + 20 + 30 + 20) / 4; math.Abs(ov.AvgDuration-want) > 0.01 {
		t.Errorf("average visit, day 1 = %.2f s, want %.2f: the visits of two or more events, first to last, the midnight visit whole", ov.AvgDuration, want)
	}

	day2, err := s.overviewWindow(ctx, "s1", d(1), d(2))
	if err != nil {
		t.Fatal(err)
	}
	if day2.TotalPageviews != 1 || day2.TotalVisits != 0 || day2.UniqueVisitors != 1 {
		t.Errorf("day 2 = %d views, %d visits, %d visitors; want 1, 0, 1: a visit belongs to the day it began, its views to the day they happened",
			day2.TotalPageviews, day2.TotalVisits, day2.UniqueVisitors)
	}

	var conversions int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews WHERE domain_id='s1' AND event_type=2 AND event_name='signup'`).Scan(&conversions); err != nil {
		t.Fatal(err)
	}
	if conversions != 1 {
		t.Errorf("conversions = %d, want 1", conversions)
	}

	landing := map[string]int{}
	rows, err := s.db.Query(`SELECT landing, COUNT(1) FROM analytics_sessions WHERE domain_id='s1' GROUP BY landing`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			t.Fatal(err)
		}
		landing[p] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(landing) != 3 || landing["/"] != 2 || landing["/b"] != 2 || landing["/a"] != 1 {
		t.Errorf("landing pages = %v, want / 2, /b 2, /a 1: the first view of each visit", landing)
	}

	refs, err := s.TopReferrers(ctx, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range refs {
		got[r.Referrer] = r.Count
	}
	if len(got) != 2 || got["www.google.com"] != 1 || got["news.ycombinator.com"] != 1 {
		t.Errorf("referrers = %v, want www.google.com 1 and news.ycombinator.com 1, the site's own pages none", got)
	}

	// The install's view: both sites, the second site's visit counted, R1
	// one visitor across both.
	all, err := s.OverviewBetween(ctx, d(0), d(1))
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalVisits != 6 || all.UniqueVisitors != 3 || all.TotalPageviews != 8 {
		t.Errorf("every site, day 1 = %d visits, %d visitors, %d views; want 6, 3, 8", all.TotalVisits, all.UniqueVisitors, all.TotalPageviews)
	}
	// The trailing report and the windowed one read the same definitions:
	// over the same rows they agree, figure for figure.
	since, err := s.OverviewSince(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	between, err := s.OverviewBetween(ctx, d(0), d(2))
	if err != nil {
		t.Fatal(err)
	}
	if *since != *between {
		t.Errorf("OverviewSince = %+v, OverviewBetween over the same days = %+v", *since, *between)
	}
}
