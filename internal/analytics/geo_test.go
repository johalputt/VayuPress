package analytics

import (
	"context"
	"testing"
)

func TestCountriesFromProxyGeo(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()

	// Two visitors from US, one from IN — geo supplied server-side (as the
	// collect handler does from proxy headers).
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1, Geo: GeoInfo{Country: "US", City: "Austin"}}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1, Geo: GeoInfo{Country: "US", City: "Reno"}}, "2.2.2.2", "Firefox")
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1, Geo: GeoInfo{Country: "IN", City: "Pune"}}, "3.3.3.3", "Safari")
	// A visitor with no geo (no proxy header) must not appear in country stats.
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "4.4.4.4", "Edge")

	countries, err := s.Countries(ctx, 30)
	if err != nil {
		t.Fatalf("countries: %v", err)
	}
	got := map[string]int{}
	for _, c := range countries {
		got[c.Label] = c.Count
	}
	if got["US"] != 2 {
		t.Fatalf("want US=2, got %d (%+v)", got["US"], countries)
	}
	if got["IN"] != 1 {
		t.Fatalf("want IN=1, got %d (%+v)", got["IN"], countries)
	}
	if len(countries) != 2 {
		t.Fatalf("empty-country visit should be excluded; got %+v", countries)
	}

	cities, err := s.Cities(ctx, 30)
	if err != nil {
		t.Fatalf("cities: %v", err)
	}
	if len(cities) != 3 {
		t.Fatalf("want 3 cities, got %+v", cities)
	}
}
