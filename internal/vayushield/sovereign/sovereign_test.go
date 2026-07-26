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
