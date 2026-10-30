// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
)

// Each row's visits are measured by the overview's definitions: Google's
// one visit read two pages and did not bounce, Bing's bounced, and the
// direct visit converted; a visit with no referrer is no referrer's row,
// and a narrowed page measures its readers alone.
func TestEachRowsVisitsAreMeasured(t *testing.T) {
	s := focusStore(t)
	ctx := context.Background()
	refs, err := s.VisitMeasuresScoped(ctx, "s1", "visit_referrer", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]VisitMeasure{}
	for _, m := range refs {
		by[m.Key] = m
	}
	if len(by) != 2 {
		t.Fatalf("referrers = %+v, want Google and Bing alone", refs)
	}
	if g := by["www.google.com"]; g.Visits != 1 || g.PagesPerVisit() != 2 || g.BounceRate() != 0 || g.Long != 1 {
		t.Errorf("Google's visit = %+v, want one of two pages, no bounce", g)
	}
	if b := by["www.bing.com"]; b.BounceRate() != 100 || b.Long != 0 || b.AvgVisit() != 0 {
		t.Errorf("Bing's visit = %+v, want a bounce", b)
	}
	ch, err := s.VisitMeasuresScoped(ctx, "s1", "visit_channel", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ch {
		switch m.Key {
		case "organic":
			if m.Visits != 2 || m.Visitors != 2 || m.ConversionRate() != 0 {
				t.Errorf("organic = %+v, want 2 visits, no conversion", m)
			}
		case "direct":
			if m.Visits != 1 || m.ConversionRate() != 100 {
				t.Errorf("direct = %+v, want one visit that converted", m)
			}
		default:
			t.Errorf("a channel no visit arrived by: %+v", m)
		}
	}
	if us, _ := s.VisitMeasuresScoped(narrowTo("country", FilterIs, "US"), "s1", "visit_referrer", 7, 10); len(us) != 1 || us[0].Key != "www.bing.com" {
		t.Errorf("referrers narrowed to US = %+v, want Bing alone", us)
	}
	if _, err := s.VisitMeasuresScoped(ctx, "s1", "path", 7, 10); err == nil {
		t.Error("a page is measured as if it were a visit's own")
	}
}

// The map's readers: a placed visit is on its city, an unplaced one on
// none; Live places its cities and says, for each country, what its readers
// read and how they arrived, by the referrer each visit arrived by, so a
// move between the site's own pages is no Direct arrival.
func TestTheMapsReadersArePlaced(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	collect := func(ip, path, ref string, geo GeoInfo) {
		t.Helper()
		if err := s.Collect(ctx, CollectRequest{URL: path, Referrer: ref, Hostname: "site.test", EventType: 1, Lang: "en", Geo: geo},
			ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", "s1"); err != nil {
			t.Fatal(err)
		}
	}
	pune := GeoInfo{Country: "IN", City: "Pune", Lat: 18.5, Lon: 73.9, Located: true}
	collect("198.51.100.1", "/a", "https://www.google.com/", pune)
	collect("198.51.100.1", "/b", "https://site.test/a", pune)
	collect("198.51.100.2", "/c", "", GeoInfo{Country: "US", City: "Austin"})
	pts, err := s.CityPointsScoped(ctx, "s1", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 || pts[0].City != "Pune" || pts[0].Lat != 18.5 || pts[0].Lon != 73.9 || pts[0].Visitors != 1 {
		t.Errorf("placed cities = %+v, want Pune alone", pts)
	}
	now, err := s.RealtimeScoped(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range now.ActiveCities {
		if (c.City == "Pune") != c.Located {
			t.Errorf("live city %+v: placed is %v", c, c.Located)
		}
	}
	in := now.Countries["IN"]
	if len(in.Pages) != 2 || len(in.Channels) != 1 || in.Channels[0].Key != "organic" {
		t.Errorf("India now = %+v, want its two pages and one search arrival", in)
	}
	got := map[string]int{}
	for _, ch := range now.ActiveChannels {
		got[ch.Key] = ch.Count
	}
	if len(got) != 2 || got["organic"] != 1 || got["direct"] != 1 {
		t.Errorf("live channels = %v, want one search arrival and one direct; a move between the site's pages is none", got)
	}
}
