// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strconv"
	"testing"
)

// A visit's trail is its steps in order (its first twelve, the rest
// counted), what it did among them, and its place only as finely as enough
// visits shared it: a city five visits came from is named, a city one came
// from is its country alone.
func TestAVisitsTrailIsItsStepsInOrder(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	collect := func(ip string, req CollectRequest) {
		t.Helper()
		req.Hostname, req.Lang = "site.test", "en"
		if req.EventType == 0 {
			req.EventType = 1
		}
		if err := s.Collect(ctx, req, ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", "s1"); err != nil {
			t.Fatal(err)
		}
	}
	pune := GeoInfo{Country: "IN", City: "Pune"}
	collect("198.51.100.1", CollectRequest{URL: "/a", Referrer: "https://www.google.com/", Geo: pune})
	collect("198.51.100.1", CollectRequest{URL: "/b", Referrer: "https://site.test/a", Geo: pune})
	collect("198.51.100.1", CollectRequest{URL: "/b", EventType: eventOutbound, EventName: "https://github.com/x", Geo: pune})
	collect("198.51.100.1", CollectRequest{URL: "/search", EventName: "ankush johal", search: &searchMark{}, Geo: pune})
	collect("198.51.100.1", CollectRequest{URL: "/b", EventType: 2, EventName: "signup", Geo: pune})
	// A long visit keeps its first steps and counts the rest.
	for i := 0; i < trailSteps+2; i++ {
		collect("192.0.2.7", CollectRequest{URL: "/p" + strconv.Itoa(i), Geo: GeoInfo{Country: "DE"}})
	}
	for i := 0; i < 5; i++ {
		collect("203.0.113."+strconv.Itoa(i+1), CollectRequest{URL: "/c", Geo: GeoInfo{Country: "US", City: "Austin"}})
	}
	trails, err := s.VisitTrailsScoped(ctx, "s1", 7, 50)
	if err != nil {
		t.Fatal(err)
	}
	var a, long *VisitTrail
	austin := 0
	for i := range trails {
		switch trails[i].Country {
		case "IN":
			a = &trails[i]
		case "DE":
			long = &trails[i]
		case "US":
			if trails[i].City == "Austin" {
				austin++
			}
		}
	}
	if a == nil {
		t.Fatalf("no trail for the reader in India: %+v", trails)
	}
	// What was searched stays in the search report: the step says a search
	// was made, never what.
	want := []TrailStep{{"page", "/a"}, {"page", "/b"}, {"out", "github.com/x"}, {"search", ""}, {"event", "signup"}}
	if len(a.Steps) != len(want) || a.Referrer != "www.google.com" || !a.Converted || a.City != "" {
		t.Fatalf("the reader in India's trail = %+v, want %v from Google, converted, Pune unnamed", a, want)
	}
	for i := range want {
		if a.Steps[i] != want[i] {
			t.Errorf("step %d = %+v, want %+v", i, a.Steps[i], want[i])
		}
	}
	if long == nil || len(long.Steps) != trailSteps || long.Steps[trailSteps-1].Value != "/p11" || long.MoreSteps != 2 {
		t.Errorf("a visit of %d pages = %+v, want its first %d and 2 more", trailSteps+2, long, trailSteps)
	}
	if austin != 5 {
		t.Errorf("%d trails name Austin, which five visits shared; want 5", austin)
	}
	if us, _ := s.VisitTrailsScoped(narrowTo("country", FilterIs, "IN"), "s1", 7, 50); len(us) != 1 {
		t.Errorf("trails narrowed to India = %d, want 1", len(us))
	}
}
