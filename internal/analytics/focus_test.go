// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/focus"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// focusStore holds three visits on site s1, each the one reader a filter
// below is meant to keep, and a fourth on another site:
//
//	A  from www.google.com, in IN on a phone, reads /a then /b
//	B  from www.bing.com, in US, reads /c
//	C  with no referrer, in DE, reads /a, then signs up
//	D  on site s2, from www.google.com, reads /a
func focusStore(t *testing.T) *Store {
	t.Helper()
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("focus")))
	ctx := context.Background()
	collect := func(ip, ua, site, path, ref, country, event string) {
		t.Helper()
		req := CollectRequest{URL: path, Referrer: ref, Hostname: "site.test", EventType: 1, Lang: "en", Geo: GeoInfo{Country: country}}
		if event != "" {
			req.EventType, req.EventName = 2, event
		}
		if err := s.Collect(ctx, req, ip, ua, site); err != nil {
			t.Fatal(err)
		}
	}
	phone := "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile Safari/604.1"
	desk := "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0"
	collect("198.51.100.1", phone, "s1", "/a", "https://www.google.com/search?q=x", "IN", "")
	collect("198.51.100.1", phone, "s1", "/b", "https://site.test/a", "IN", "")
	collect("198.51.100.2", desk, "s1", "/c", "https://www.bing.com/", "US", "")
	collect("198.51.100.3", desk, "s1", "/a", "", "DE", "")
	collect("198.51.100.3", desk, "s1", "/a", "", "DE", "signup")
	collect("198.51.100.4", desk, "s2", "/a", "https://www.google.com/", "IN", "")
	return s
}

func narrowTo(dim, op, value string) context.Context {
	return focus.With(context.Background(), []ExploreFilter{{Dim: dim, Op: op, Value: value}})
}

func topPaths(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	pages, err := s.TopPagesScoped(ctx, "s1", 7, 20)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range pages {
		out = append(out, p.Path)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// A page narrowed to some readers counts their visits whole: a filter on a
// visit's own fact (its referrer, channel, place, landing page) keeps the
// visits that have it, a filter on something a visit does (a page read, an
// event) keeps the visits that did it, and "is not" the visits that did
// not. Each case is decided by one reader, so each rule is its own seed.
func TestANarrowedPageCountsItsReadersVisitsWhole(t *testing.T) {
	s := focusStore(t)
	for _, tc := range []struct {
		dim, op, value string
		views          int    // page views on s1
		pages          string // the paths their visits read
	}{
		{"visit_referrer", FilterIs, "www.google.com", 2, "/a /b"}, // A, and its second page too
		{"visit_channel", FilterIs, "organic", 3, "/a /b /c"},      // A and B
		{"path", FilterIs, "/b", 2, "/a /b"},                       // A: the visit that read /b, whole
		{"path", FilterIsNot, "/a", 1, "/c"},                       // B
		{"country", FilterIs, "US", 1, "/c"},                       // B
		{"event", FilterIs, "signup", 1, "/a"},                     // C
		{"landing", FilterPrefix, "/c", 1, "/c"},                   // B
		{"device", FilterIs, "Mobile", 2, "/a /b"},                 // A
	} {
		ctx := narrowTo(tc.dim, tc.op, tc.value)
		ov, err := s.OverviewSinceScoped(ctx, "s1", 7)
		if err != nil {
			t.Fatal(err)
		}
		if ov.TotalPageviews != tc.views {
			t.Errorf("%s %s %s: %d page views, want %d", tc.dim, tc.op, tc.value, ov.TotalPageviews, tc.views)
		}
		if got := topPaths(t, s, ctx); got != tc.pages {
			t.Errorf("%s %s %s: top pages %q, want %q", tc.dim, tc.op, tc.value, got, tc.pages)
		}
	}
}

// Every list the page shows keeps to the readers it is narrowed to: the
// visits and their figures, the referrers and channels, the landing pages,
// the places and devices, the events, the days, and who is reading now.
func TestEveryReaderKeepsToTheNarrowedReaders(t *testing.T) {
	s := focusStore(t)
	ctx := narrowTo("visit_referrer", FilterIs, "www.google.com")
	ov, err := s.OverviewSinceScoped(ctx, "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalVisits != 1 || ov.UniqueVisitors != 1 {
		t.Errorf("overview = %d visits, %d visitors; want A's one", ov.TotalVisits, ov.UniqueVisitors)
	}
	if refs, _ := s.TopReferrersScoped(ctx, "s1", 7, 10); len(refs) != 1 || refs[0].Referrer != "www.google.com" || refs[0].Count != 1 {
		t.Errorf("referrers = %+v, want www.google.com's one visit", refs)
	}
	if ch, _ := s.ChannelsScoped(ctx, "s1", 7); len(ch) != 1 || ch[0].Key != "organic" || ch[0].Count != 1 {
		t.Errorf("channels = %+v, want one organic visit", ch)
	}
	if land, _ := s.LandingPagesScoped(ctx, "s1", 7, 10); len(land) != 1 || land[0].Path != "/a" {
		t.Errorf("landing pages = %+v, want /a", land)
	}
	if places, _ := s.AudienceScoped(ctx, "s1", "country", 7); len(places) != 1 || places[0].Label != "IN" {
		t.Errorf("countries = %+v, want IN", places)
	}
	if days, _ := s.PageviewSeriesScoped(ctx, "s1", 7); len(days) != 1 || days[0].Count != 2 {
		t.Errorf("days = %+v, want one day of 2 views", days)
	}
	signup := narrowTo("country", FilterIs, "DE")
	if ev, _ := s.CustomEventsScoped(signup, "s1", 7); len(ev) != 1 || ev[0].Name != "signup" {
		t.Errorf("events in DE = %+v, want signup", ev)
	}
	if ev, _ := s.CustomEventsScoped(ctx, "s1", 7); len(ev) != 0 {
		t.Errorf("events from Google = %+v, want none", ev)
	}
	now, _ := s.RealtimeScoped(ctx, "s1")
	if now.ActiveVisitors != 1 || len(now.ActivePages) != 2 {
		t.Errorf("right now = %d readers on %d pages, want A on /a and /b", now.ActiveVisitors, len(now.ActivePages))
	}
	if len(now.ActiveCountries) != 1 || now.ActiveCountries[0].Label != "IN" || len(now.ActiveDevices) != 1 {
		t.Errorf("right now in %+v on %+v, want A's IN and phone alone", now.ActiveCountries, now.ActiveDevices)
	}
	res, err := s.Explore(ctx, ExploreQuery{Scope: "s1", Metric: ExploreViews, By: "path", Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("the Explorer counts %v views, want A's 2", res.Total)
	}
	// Another site's visit from Google is never one of s1's readers.
	if all, _ := s.OverviewSinceScoped(ctx, ScopeAllDomains, 7); all.TotalVisits != 2 {
		t.Errorf("every site from Google = %d visits, want A and D", all.TotalVisits)
	}
}

// The engagement store is narrowed through the visitors the event log
// names: the clause keeps the visitor of every narrowed visit, and no other.
func TestFocusVisitorsNamesTheNarrowedVisitors(t *testing.T) {
	s := focusStore(t)
	if c, _ := s.FocusVisitors(context.Background(), "v", "2000-01-01", "9999-12-31"); c != "" {
		t.Errorf("a page that is not narrowed narrows: %q", c)
	}
	ctx := narrowTo("country", FilterIs, "US")
	clause, args := s.FocusVisitors(ctx, "visitor_id", "2000-01-01", "9999-12-31")
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE 1`+clause, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the visitors in US = %d, want B alone", n)
	}
}

// A filter that cannot be applied keeps nothing rather than everything.
func TestAFilterThatCannotBeAppliedKeepsNothing(t *testing.T) {
	s := focusStore(t)
	ov, err := s.OverviewSinceScoped(narrowTo("nonsense", FilterIs, "x"), "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalPageviews != 0 {
		t.Errorf("an unknown filter kept %d page views", ov.TotalPageviews)
	}
}
