// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"testing"
	"time"
)

// The slowest routes come first, each with its own count and p95, so a slow
// overall figure names what made it.
func TestSlowestNamesTheRoutesThatMakeTheTail(t *testing.T) {
	var r RouteWindows
	for i := 0; i < 1000; i++ {
		r.Record("GET /fast", time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		r.Record("GET /mirror/{file}", 1500*time.Millisecond)
	}
	for i := 0; i < 50; i++ {
		r.Record("GET /os/monitoring", 40*time.Millisecond)
	}
	got := r.Slowest(2)
	if len(got) != 2 || got[0].Route != "GET /mirror/{file}" || got[1].Route != "GET /os/monitoring" {
		t.Fatalf("Slowest(2) = %+v, want the transfer then the console page", got)
	}
	if got[0].Requests != 20 || got[0].P95 < 1024 || got[0].P95 > 2048 {
		t.Errorf("the slow route's figures are wrong: %+v", got[0])
	}
	// 20 requests of 1.5 s sit in the 1024–2048 ms bucket: counted at its middle.
	if got[0].Total != 20*1536 {
		t.Errorf("time taken = %d ms, want %d", got[0].Total, 20*1536)
	}
	if all := r.Slowest(10); len(all) != 3 {
		t.Errorf("Slowest(10) lists %d routes, want the 3 recorded", len(all))
	}
}

// Routes whose windows are identical are ranked by name, so the list does not
// reshuffle between two reads of the same traffic (map order would).
func TestSlowestIsStable(t *testing.T) {
	var r RouteWindows
	for _, route := range []string{"GET /c", "GET /a", "GET /b"} {
		r.Record(route, 3*time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		got := r.Slowest(3)
		if got[0].Route != "GET /a" || got[1].Route != "GET /b" || got[2].Route != "GET /c" {
			t.Fatalf("order = %v %v %v, want /a, /b, /c", got[0].Route, got[1].Route, got[2].Route)
		}
	}
}
