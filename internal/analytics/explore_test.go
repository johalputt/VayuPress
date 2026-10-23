// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func explore(t *testing.T, s *Store, q ExploreQuery) *ExploreResult {
	t.Helper()
	if q.Scope == "" {
		q.Scope = "s1"
	}
	if q.Days == 0 {
		q.Days = 2
	}
	res, err := s.Explore(context.Background(), q)
	if err != nil {
		t.Fatalf("%+v: %v", q, err)
	}
	return res
}

func split(res *ExploreResult) map[string]float64 {
	out := map[string]float64{}
	for _, r := range res.Rows {
		out[r.Key] = r.Value
	}
	return out
}

// Over the contract's day, each figure split by day is the overview's figure
// for that day: the Explorer reads the same definitions, not a second copy
// of them that could drift.
func TestTheExplorerCountsTheOverviewsFigures(t *testing.T) {
	s, day1 := contractStore(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		from := day1.AddDate(0, 0, i).Format("2006-01-02")
		to := day1.AddDate(0, 0, i+1).Format("2006-01-02")
		ov, err := s.overviewWindow(ctx, "s1", from, to)
		if err != nil {
			t.Fatal(err)
		}
		want := map[ExploreMetric]float64{
			ExploreViews: float64(ov.TotalPageviews), ExploreVisitors: float64(ov.UniqueVisitors),
			ExploreVisits: float64(ov.TotalVisits), ExploreBounceRate: ov.BounceRate, ExploreAvgVisit: ov.AvgDuration,
		}
		for m, w := range want {
			got := split(explore(t, s, ExploreQuery{Metric: m, By: "day"}))[from]
			if math.Abs(got-w) > 0.001 {
				t.Errorf("%s on %s = %v, the overview says %v", m, from, got, w)
			}
		}
	}
	ov, err := s.overviewWindow(ctx, "s1", day1.Format("2006-01-02"), "")
	if err != nil {
		t.Fatal(err)
	}
	for m, w := range map[ExploreMetric]float64{
		ExploreViews: float64(ov.TotalPageviews), ExploreVisitors: float64(ov.UniqueVisitors),
		ExploreVisits: float64(ov.TotalVisits), ExploreBounceRate: ov.BounceRate, ExploreAvgVisit: ov.AvgDuration,
	} {
		if got := explore(t, s, ExploreQuery{Metric: m}).Total; math.Abs(got-w) > 0.001 {
			t.Errorf("%s over the range = %v, the overview says %v", m, got, w)
		}
	}
}

// The splits and filters, against the contract's events counted by hand.
func TestTheExplorerSplitsAndFilters(t *testing.T) {
	s, _ := contractStore(t)
	if got := split(explore(t, s, ExploreQuery{Metric: ExploreViews, By: "path"})); len(got) != 4 || got["/"] != 2 || got["/a"] != 3 || got["/b"] != 2 || got["/c"] != 1 {
		t.Errorf("views by page = %v, want / 2, /a 3, /b 2, /c 1", got)
	}
	if got := split(explore(t, s, ExploreQuery{Metric: ExploreViews, By: "channel"})); len(got) != 3 || got["organic"] != 1 || got["social"] != 1 || got["direct"] != 6 {
		t.Errorf("views by channel = %v, want organic 1, social 1, direct 6", got)
	}
	if got := split(explore(t, s, ExploreQuery{Metric: ExploreVisits, By: "landing"})); len(got) != 3 || got["/"] != 2 || got["/b"] != 2 || got["/a"] != 1 {
		t.Errorf("visits by landing page = %v, want / 2, /b 2, /a 1", got)
	}
	if got := split(explore(t, s, ExploreQuery{Metric: ExploreEvents, By: "event"})); len(got) != 1 || got["signup"] != 1 {
		t.Errorf("events = %v, want signup 1", got)
	}
	for _, c := range []struct {
		f    ExploreFilter
		want float64
	}{
		{ExploreFilter{"path", FilterIs, "/a"}, 3},
		{ExploreFilter{"path", FilterIsNot, "/"}, 6},
		{ExploreFilter{"path", FilterPrefix, "/"}, 8},
		{ExploreFilter{"path", FilterPrefix, "/A"}, 0}, // a start is matched with its case
		{ExploreFilter{"path", FilterPrefix, "%"}, 0},  // and literally
		{ExploreFilter{"path", FilterPrefix, "_"}, 0},
		{ExploreFilter{"channel", FilterIs, "organic"}, 1},
		{ExploreFilter{"landing", FilterIs, "/b"}, 3},
	} {
		if got := explore(t, s, ExploreQuery{Metric: ExploreViews, Filters: []ExploreFilter{c.f}}).Total; got != c.want {
			t.Errorf("views where %+v = %v, want %v", c.f, got, c.want)
		}
	}
	// Two filters must both hold.
	if got := explore(t, s, ExploreQuery{Metric: ExploreViews, Filters: []ExploreFilter{{"path", FilterIs, "/a"}, {"channel", FilterIs, "social"}}}).Total; got != 1 {
		t.Errorf("views of /a from social = %v, want 1", got)
	}
	// A visit filtered by its landing page.
	if got := explore(t, s, ExploreQuery{Metric: ExploreVisits, Filters: []ExploreFilter{{"landing", FilterIsNot, "/"}}}).Total; got != 3 {
		t.Errorf("visits not landing on / = %v, want 3", got)
	}
	// One site's figures are its own; the install's are every site's.
	if got := explore(t, s, ExploreQuery{Metric: ExploreViews}).Total; got != 8 {
		t.Errorf("site one's views = %v, want 8", got)
	}
	if got := explore(t, s, ExploreQuery{Scope: ScopeAllDomains, Metric: ExploreViews}).Total; got != 9 {
		t.Errorf("the install's views = %v, want 9, site two's among them", got)
	}
	// A visit row with no event yet is no visit, as the overview has it.
	if _, err := s.db.Exec(`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,events,views,landing) VALUES('empty','e','s1',?,0,0,'/z')`,
		time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	if got := split(explore(t, s, ExploreQuery{Metric: ExploreVisits, By: "landing"})); got["/z"] != 0 || len(got) != 3 {
		t.Errorf("a visit row with no event is counted as a visit: %v", got)
	}
	// An additive figure's split adds up to its total.
	res := explore(t, s, ExploreQuery{Metric: ExploreVisits, By: "device"})
	sum := 0.0
	for _, r := range res.Rows {
		sum += r.Value
	}
	if sum != res.Total || sum != 5 {
		t.Errorf("visits by device add up to %v, total %v, want 5", sum, res.Total)
	}
}

// Today against yesterday: each value beside its own figure the range
// before, a day beside the day as far into it.
func TestTheExplorerComparesTheRangeBefore(t *testing.T) {
	s, day1 := contractStore(t)
	res := explore(t, s, ExploreQuery{Metric: ExploreViews, By: "path", Days: 1, Compare: true})
	if res.Total != 1 || res.PrevTotal != 7 || !res.HasPrevTotal {
		t.Errorf("today's views %v against yesterday's %v, want 1 against 7", res.Total, res.PrevTotal)
	}
	if len(res.Rows) != 1 || res.Rows[0].Key != "/a" || res.Rows[0].Prev != 2 || !res.Rows[0].HasPrev {
		t.Errorf("today's /a = %+v, want 1 against yesterday's 2", res.Rows)
	}
	days := explore(t, s, ExploreQuery{Metric: ExploreViews, By: "day", Days: 1, Compare: true})
	if len(days.Rows) != 1 || days.Rows[0].Prev != 7 || days.Rows[0].Key != day1.AddDate(0, 0, 1).Format("2006-01-02") {
		t.Errorf("today beside yesterday = %+v, want 1 beside 7", days.Rows)
	}
}

// A value shown is compared with its own count the range before, however
// low it ranked there.
func TestTheRangeBeforeIsCountedForTheValuesShown(t *testing.T) {
	s := newExtStore(t)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02 12:00:00")
	today := time.Now().UTC().Format("2006-01-02 00:00:01")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,events,views) VALUES('v','r','s1',?,1,1)`, yesterday)
	// Yesterday: 101 pages viewed twice each, and /rare once, the least.
	for i := 0; i <= ExploreMaxRows; i++ {
		for j := 0; j < 2; j++ {
			exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES(?,'v',?,1,'s1',?,'r')`,
				"y"+strconv.Itoa(i)+"-"+strconv.Itoa(j), "/p"+strconv.Itoa(i), yesterday)
		}
	}
	exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('rare-y','v','/rare',1,'s1',?,'r')`, yesterday)
	exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('rare-t','v','/rare',1,'s1',?,'r')`, today)
	res := explore(t, s, ExploreQuery{Metric: ExploreViews, By: "path", Days: 1, Compare: true})
	if len(res.Rows) != 1 || res.Rows[0].Key != "/rare" || !res.Rows[0].HasPrev || res.Rows[0].Prev != 1 {
		t.Errorf("/rare today = %+v, want compared with yesterday's 1", res.Rows)
	}
	all := explore(t, s, ExploreQuery{Metric: ExploreViews, By: "path", Days: 2})
	if len(all.Rows) != ExploreMaxRows || !all.Truncated {
		t.Errorf("a split of %d values shows %d, truncated %v; want %d, said to be cut", ExploreMaxRows+2, len(all.Rows), all.Truncated, ExploreMaxRows)
	}
}

// A question the figures cannot answer is refused with the reason, never
// answered with another figure.
func TestTheExplorerRefusesWhatItCannotCount(t *testing.T) {
	for _, c := range []struct {
		q    ExploreQuery
		want string
	}{
		{ExploreQuery{Metric: "sessions", Days: 7}, "choose a figure"},
		{ExploreQuery{Metric: ExploreViews, Days: 0}, "1 to 1095"},
		{ExploreQuery{Metric: ExploreViews, Days: 1096}, "1 to 1095"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, By: "colour"}, "split by"},
		{ExploreQuery{Metric: ExploreVisits, Days: 7, By: "path"}, "a visit has no one page"},
		{ExploreQuery{Metric: ExploreBounceRate, Days: 7, Filters: []ExploreFilter{{"referrer", FilterIs, "x"}}}, "no one referrer to filter by"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: []ExploreFilter{{"day", FilterIs, "2026-01-01"}}}, "the range sets the days"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: []ExploreFilter{{"country", FilterPrefix, "I"}}}, "matched whole"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: []ExploreFilter{{"path", "contains", "a"}}}, "is, is not, or starts with"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: []ExploreFilter{{"path", FilterPrefix, ""}}}, "not empty"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: []ExploreFilter{{"path", FilterIs, strings.Repeat("a", 201)}}}, "200 characters"},
		{ExploreQuery{Metric: ExploreViews, Days: 7, Filters: make([]ExploreFilter, ExploreMaxFilters+1)}, "at most"},
	} {
		if err := c.q.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want the reason to say %q", c.q, err, c.want)
		}
	}
}
