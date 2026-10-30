// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
	"time"

	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// Right now is a count of readers, by the visitor definition every other
// figure uses, and a site's live panel counts that site alone. Counting
// visits instead, one person reading two of the install's sites was two
// readers on the install's panel; and the panel had no site at all.
func TestRightNowCountsReadersAndOneSiteAlone(t *testing.T) {
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("live")))
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	collect := func(who contractReader, site, path string) {
		t.Helper()
		req := CollectRequest{URL: path, Hostname: site + ".test", EventType: 1, Lang: "en"}
		if err := s.Collect(context.Background(), req, who.ip, who.ua, site); err != nil {
			t.Fatal(err)
		}
	}
	collect(r1, "s1", "/a")
	collect(r1, "s1", "/b") // the same reader, another page: still one reader
	collect(r1, "s2", "/c") // the same reader on another site: a second visit, one reader
	collect(r2, "s2", "/c")

	ctx := context.Background()
	all, err := s.RealtimeScoped(ctx, ScopeAllDomains)
	if err != nil {
		t.Fatal(err)
	}
	if all.ActiveVisitors != 2 {
		t.Errorf("the install reads %d readers right now, want 2 (three visits by two people)", all.ActiveVisitors)
	}
	one, err := s.RealtimeScoped(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if one.ActiveVisitors != 1 {
		t.Errorf("site one reads %d readers right now, want 1", one.ActiveVisitors)
	}
	for _, p := range one.ActivePages {
		if p.Path == "/c" {
			t.Errorf("site one's live pages list site two's page %q", p.Path)
		}
	}
	two, _ := s.RealtimeScoped(ctx, "s2")
	if two.ActiveVisitors != 2 || len(two.ActivePages) != 1 || two.ActivePages[0].Count != 2 {
		t.Errorf("site two: %d readers, pages %+v; want 2 readers on /c", two.ActiveVisitors, two.ActivePages)
	}
}

// Right now says where its readers are, what they read on and how they
// arrived, each reader counted once in each list however many pages they
// read, and a city by its country: Paris, France is not Paris, Texas.
func TestRightNowSaysWhereReadersAreAndWhatTheyReadOn(t *testing.T) {
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("live")))
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	collect := func(who contractReader, path, ref string, geo GeoInfo) {
		t.Helper()
		req := CollectRequest{URL: path, Hostname: "s1.test", EventType: 1, Lang: "en", Referrer: ref, Geo: geo}
		if err := s.Collect(context.Background(), req, who.ip, who.ua, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	r4 := contractReader{"198.51.100.4", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) Chrome/130.0"}
	google := "https://www.google.com/"
	pune := GeoInfo{Country: "IN", Region: "Maharashtra", City: "Pune"}
	collect(r1, "/a", google, pune)
	collect(r1, "/b", google, pune) // one reader, two pages
	collect(r2, "/a", google, GeoInfo{Country: "US", City: "Austin"})
	collect(r3, "/a", "", GeoInfo{Country: "US", City: "Paris"})
	collect(r4, "/a", google, GeoInfo{Country: "FR", City: "Paris"})

	rs, err := s.RealtimeScoped(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	cities := map[string]int{}
	for _, c := range rs.ActiveCities {
		cities[c.City+"/"+c.Country] = c.Count
	}
	if len(cities) != 4 || cities["Pune/IN"] != 1 || cities["Austin/US"] != 1 || cities["Paris/US"] != 1 || cities["Paris/FR"] != 1 {
		t.Errorf("cities %+v, want Pune/IN, Austin/US, Paris/US and Paris/FR, one reader each", rs.ActiveCities)
	}
	counts := func(list []AudienceStat) map[string]int {
		m := map[string]int{}
		for _, a := range list {
			m[a.Label] = a.Count
		}
		return m
	}
	if d := counts(rs.ActiveDevices); len(d) != 2 || d["Desktop"] != 3 || d["Mobile"] != 1 {
		t.Errorf("devices %+v, want Desktop 3 and Mobile 1", rs.ActiveDevices)
	}
	if b := counts(rs.ActiveBrowsers); len(b) != 3 || b["Chrome"] != 2 {
		t.Errorf("browsers %+v, want three, Chrome with two readers", rs.ActiveBrowsers)
	}
	if c := counts(rs.ActiveChannels); len(c) != 2 || c["Organic search"] != 3 || c["Direct"] != 1 {
		t.Errorf("channels %+v, want Organic search 3 and Direct 1", rs.ActiveChannels)
	}
	if len(rs.ActiveChannels) == 2 && rs.ActiveChannels[0].Label != "Organic search" {
		t.Errorf("channels %+v are not largest first", rs.ActiveChannels)
	}
}

// The line under the live count is the readers in each of the last 30
// minutes, oldest first: a reader counted once in each minute they read in,
// and nothing from before the half hour.
func TestRightNowCountsReadersEachMinute(t *testing.T) {
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("live")))
	now := time.Now().UTC().Truncate(time.Minute).Add(20 * time.Second)
	at := func(ago time.Duration, who contractReader, path string) {
		t.Helper()
		s.now = func() time.Time { return now.Add(-ago) }
		if err := s.Collect(context.Background(), CollectRequest{URL: path, Hostname: "s1.test", EventType: 1, Lang: "en"}, who.ip, who.ua, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	at(40*time.Minute, r1, "/old") // before the half hour
	at(2*time.Minute, r1, "/a")
	at(2*time.Minute, r1, "/b") // the same reader, the same minute: once
	at(2*time.Minute, r2, "/a")
	at(0, r3, "/c")
	s.now = func() time.Time { return now }
	rs, err := s.RealtimeScoped(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.ActiveByMinute) != 30 {
		t.Fatalf("%d minutes, want 30", len(rs.ActiveByMinute))
	}
	want := make([]int, 30)
	want[27], want[29] = 2, 1
	for i := range want {
		if rs.ActiveByMinute[i] != want[i] {
			t.Fatalf("readers a minute %v, want %v", rs.ActiveByMinute, want)
		}
	}
}
