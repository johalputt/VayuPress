// SPDX-License-Identifier: Apache-2.0

package sovereign

import (
	"sync"
	"testing"
)

func TestPriorityAlwaysAdmitted(t *testing.T) {
	g := New()
	g.SetLimit(1) // tiny public cap
	// Exhaust the public budget.
	if _, ok := g.Admit(false); !ok {
		t.Fatal("first public admit should succeed")
	}
	if _, ok := g.Admit(false); ok {
		t.Fatal("second public admit should be shed at cap 1")
	}
	// Priority must still get in even though public is saturated.
	for i := 0; i < 1000; i++ {
		if _, ok := g.Admit(true); !ok {
			t.Fatalf("priority admit %d was shed; admin plane must never be shed", i)
		}
	}
	if g.Inflight() != 1 {
		t.Fatalf("priority admits must not consume public budget; inflight=%d want 1", g.Inflight())
	}
}

func TestPublicCappedAndShed(t *testing.T) {
	g := New()
	g.SetLimit(3)
	var releases []func()
	for i := 0; i < 3; i++ {
		rel, ok := g.Admit(false)
		if !ok {
			t.Fatalf("admit %d under cap should succeed", i)
		}
		releases = append(releases, rel)
	}
	if _, ok := g.Admit(false); ok {
		t.Fatal("admit over cap must be shed")
	}
	if g.Shed() != 1 {
		t.Fatalf("shed counter = %d, want 1", g.Shed())
	}
	// Release one slot; the next admit should now fit.
	releases[0]()
	if g.Inflight() != 2 {
		t.Fatalf("inflight after one release = %d, want 2", g.Inflight())
	}
	rel, ok := g.Admit(false)
	if !ok {
		t.Fatal("admit after release should succeed")
	}
	rel()
}

func TestReleaseDecrementsToZero(t *testing.T) {
	g := New()
	rel, ok := g.Admit(false)
	if !ok {
		t.Fatal("admit should succeed")
	}
	if g.Inflight() != 1 {
		t.Fatalf("inflight = %d, want 1", g.Inflight())
	}
	rel()
	if g.Inflight() != 0 {
		t.Fatalf("inflight after release = %d, want 0", g.Inflight())
	}
}

func TestShedReleaseIsNoop(t *testing.T) {
	g := New()
	g.SetLimit(1)
	rel1, ok := g.Admit(false)
	if !ok {
		t.Fatal("first admit should succeed")
	}
	rel2, ok := g.Admit(false) // shed
	if ok {
		t.Fatal("second admit should be shed")
	}
	// Calling the shed request's release must not corrupt the counter.
	rel2()
	rel2()
	if g.Inflight() != 1 {
		t.Fatalf("inflight = %d, want 1 (shed release is a no-op)", g.Inflight())
	}
	rel1()
	if g.Inflight() != 0 {
		t.Fatalf("inflight = %d, want 0", g.Inflight())
	}
}

func TestSetLimitDefaults(t *testing.T) {
	g := New()
	def := g.Cap()
	if def < 32 {
		t.Fatalf("default cap = %d, want >= 32", def)
	}
	g.SetLimit(500)
	if g.Cap() != 500 {
		t.Fatalf("cap after SetLimit(500) = %d, want 500", g.Cap())
	}
	g.SetLimit(0) // restore default
	if g.Cap() != def {
		t.Fatalf("cap after SetLimit(0) = %d, want default %d", g.Cap(), def)
	}
	g.SetLimit(-5) // negative also restores default
	if g.Cap() != def {
		t.Fatalf("cap after SetLimit(-5) = %d, want default %d", g.Cap(), def)
	}
}

func TestConcurrentAdmitReleaseIsRaceFree(t *testing.T) {
	g := New()
	g.SetLimit(64)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				rel, ok := g.Admit(false)
				if ok {
					rel()
				}
			}
		}()
	}
	wg.Wait()
	if g.Inflight() != 0 {
		t.Fatalf("inflight after all releases = %d, want 0", g.Inflight())
	}
	if g.Admitted()+g.Shed() != 200*500 {
		t.Fatalf("admitted+shed = %d, want %d", g.Admitted()+g.Shed(), 200*500)
	}
}

// TestCostAccountsForWorkNotArrivals is the defect weighting exists for. The
// lane counted a 2 ms cached page and a 400 ms search as one slot each, so a
// flood aimed at the expensive route saturated it at a fraction of the request
// rate the cheap route would have needed.
func TestCostAccountsForWorkNotArrivals(t *testing.T) {
	g := New()
	g.SetLimit(32)

	// Eight concurrent cost-4 requests fill a cap of 32.
	var rel []func()
	for i := 0; i < 8; i++ {
		r, ok := g.AdmitCost(false, 4)
		if !ok {
			t.Fatalf("expensive admit %d should fit under the cap", i)
		}
		rel = append(rel, r)
	}
	if _, ok := g.AdmitCost(false, 4); ok {
		t.Error("a ninth cost-4 request was admitted past a cap of 32 — the weight is not " +
			"being reserved")
	}
	// The same cap holds 32 cheap ones, which is the whole point: the budget is
	// work, not arrivals.
	for _, r := range rel {
		r()
	}
	if g.Inflight() != 0 {
		t.Fatalf("inflight after releasing every weighted request = %d, want 0", g.Inflight())
	}
	for i := 0; i < 32; i++ {
		if _, ok := g.AdmitCost(false, 1); !ok {
			t.Fatalf("cheap admit %d should fit under the same cap", i)
		}
	}
}

// TestAWeightCannotTakeARouteOffline — a weight above the cap would mean the
// first request on that route already exceeds the budget and every request on it
// sheds forever. That is a total outage on one route produced by a number an
// operator typed, on a machine whose cap they cannot see.
func TestAWeightCannotTakeARouteOffline(t *testing.T) {
	g := New()
	g.SetLimit(8)
	for i := 0; i < minConcurrentPerRoute; i++ {
		if _, ok := g.AdmitCost(false, 1_000_000); !ok {
			t.Fatalf("an absurd weight shed request %d — the route is offline by configuration", i)
		}
	}
	// A cap too small to divide must still admit, rather than rounding the
	// reservation to zero and switching the lane off.
	g2 := New()
	g2.SetLimit(1)
	if _, ok := g2.AdmitCost(false, 50); !ok {
		t.Error("a cap of 1 shed a weighted request outright")
	}
	if _, ok := g2.AdmitCost(false, 50); ok {
		t.Error("a cap of 1 admitted twice — the weight rounded down to a free reservation")
	}
}

// TestReleaseReturnsWhatWasReserved — the weight is captured at admission. If
// release re-read the live policy, an operator saving the page mid-request would
// leak or double-free slots, and a leaked slot never comes back: the lane would
// shrink a little every time they pressed Save.
func TestReleaseReturnsWhatWasReserved(t *testing.T) {
	g := New()
	g.SetLimit(64)
	rel, ok := g.AdmitCost(false, 6)
	if !ok {
		t.Fatal("admit should succeed")
	}
	if g.Inflight() != 6 {
		t.Fatalf("inflight = %d, want the 6 reserved", g.Inflight())
	}
	g.SetLimit(1000) // the operator edits policy mid-request
	rel()
	if g.Inflight() != 0 {
		t.Fatalf("inflight after release = %d, want 0 — the lane lost or over-returned slots "+
			"because the reservation size was re-derived instead of remembered", g.Inflight())
	}
}

// TestShedWeightedReleaseIsNoop — a shed request's release must not return
// budget it never took, or a flood of shed heavy requests would drive inflight
// negative and uncap the lane entirely.
func TestShedWeightedReleaseIsNoop(t *testing.T) {
	g := New()
	g.SetLimit(8) // so a weight of 2 survives the cap/4 clamp
	var held []func()
	for i := 0; i < 4; i++ {
		rel, ok := g.AdmitCost(false, 2)
		if !ok {
			t.Fatalf("setup: admit %d should fit", i)
		}
		held = append(held, rel)
	}
	for i := 0; i < 50; i++ {
		rel, ok := g.AdmitCost(false, 1)
		if ok {
			t.Fatal("admit past a full cap should shed")
		}
		rel()
	}
	if g.Inflight() != 8 {
		t.Fatalf("inflight = %d, want 8 — shed releases returned budget they never took, so "+
			"the cap can be driven negative and the lane uncapped", g.Inflight())
	}
	for _, rel := range held {
		rel()
	}
}

// TestConcurrentWeightedAdmitReleaseIsRaceFree — weighted reservations run on
// the same hot path as every public request.
func TestConcurrentWeightedAdmitReleaseIsRaceFree(t *testing.T) {
	g := New()
	g.SetLimit(64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				rel, ok := g.AdmitCost(false, 1+(i+j)%9)
				if ok {
					rel()
				}
			}
		}(i)
	}
	wg.Wait()
	if g.Inflight() != 0 {
		t.Fatalf("inflight after every release = %d, want 0", g.Inflight())
	}
	if g.Admitted()+g.Shed() != 64*500 {
		t.Fatalf("admitted+shed = %d, want %d", g.Admitted()+g.Shed(), 64*500)
	}
}
