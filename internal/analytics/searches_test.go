// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strings"
	"testing"

	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// A query is kept as what was looked for, never as something that names a
// reader: an email address or a long number is not kept at all.
func TestASearchQueryIsKeptOnlyWhenItNamesNoOne(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  Go   Generics ", "go generics"},
		{"rfc 2119", "rfc 2119"},
		{"1234 main", "1234 main"},
		{"me@example.com", ""},
		{"order 12345", ""},
		{"call 0800 123 456", "call 0800 123 456"},
		{"+919876543210", ""},
		{"", ""},
		{strings.Repeat("ä", 100), strings.Repeat("ä", maxSearchQuery)},
	} {
		got, ok := NormalizeSearchQuery(tc.in)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("NormalizeSearchQuery(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}

// Searches are counted per site, with the ones that found nothing; and a
// beacon cannot claim to be a search, whatever event type it names.
func TestSearchesAreASitesOwnAndOnlyTheServerMakesThem(t *testing.T) {
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("search")))
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS analytics_event_data(id INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL, property_key TEXT NOT NULL, property_value TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	search := func(site, ip, q string, results int) {
		s.RecordSearch(CollectRequest{URL: "/search", Hostname: site + ".test", Lang: "en"}, q, results, ip, "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", site)
	}
	search("s1", "198.51.100.1", "Generics", 4)
	search("s1", "198.51.100.2", "generics", 0)
	search("s1", "198.51.100.1", "kubernetes", 0)
	search("s1", "198.51.100.1", "me@example.com", 0)
	search("s2", "198.51.100.3", "s2 only", 2)
	// A beacon naming the search type is a page view, never a search.
	if err := s.Collect(ctx, CollectRequest{URL: "/x", Hostname: "s1.test", EventType: eventSearch, EventName: "forged", Lang: "en"},
		"198.51.100.9", "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchesScoped(ctx, "s1", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SearchStat{
		"generics":   {Query: "generics", Searches: 2, Visitors: 2, Empty: 1},
		"kubernetes": {Query: "kubernetes", Searches: 1, Visitors: 1, Empty: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("site one's searches: %+v; want generics and kubernetes alone", got)
	}
	for _, st := range got {
		if st != want[st.Query] {
			t.Errorf("site one searched %q: %+v; want %+v", st.Query, st, want[st.Query])
		}
	}
	var searches, forgedType int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM analytics_pageviews WHERE event_type=?`, eventSearch).Scan(&searches)
	_ = s.db.QueryRow(`SELECT event_type FROM analytics_pageviews WHERE event_name='forged'`).Scan(&forgedType)
	if searches != 4 || forgedType != 1 {
		t.Errorf("%d search rows and the forged beacon stored as type %d; want the 4 the server recorded, and a page view", searches, forgedType)
	}
}
