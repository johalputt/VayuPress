// SPDX-License-Identifier: Apache-2.0

package calibrate

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func atSec(sec *int64) func() time.Time {
	return func() time.Time { return time.Unix(*sec, 0) }
}

// feed pushes served challenges with the given number passed, then advances
// one window so the fold happens. Passes come from distinct qualified solvers,
// which is what a real audience looks like — see feedFrom for the cases where
// that is exactly the thing under test.
func feed(c *Calibrator, sec *int64, served, passed int) {
	feedFrom(c, sec, served, passed, passed, passed)
}

// feedFrom is feed with the qualification and diversity of the passes spelled
// out: of `passed` solves, `qualified` came from solvers the brain has not
// downgraded, spread across `distinct` different sources.
func feedFrom(c *Calibrator, sec *int64, served, passed, qualified, distinct int) {
	for i := 0; i < served; i++ {
		c.Served()
	}
	for i := 0; i < passed; i++ {
		key := ""
		if distinct > 0 {
			key = "src-" + strconv.Itoa(i%distinct)
		}
		c.Passed(i < qualified, key)
	}
	*sec += windowSec
	c.rotate()
}

func TestHighPassRateLoosens(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)
	// 100 challenged, 96 solved: we are challenging humans.
	feed(c, &sec, 100, 96)
	if b := c.Bias(); b != step {
		t.Fatalf("bias after one FP-heavy window = %v, want %v", b, step)
	}
	// Keeps loosening but never past the cap.
	for i := 0; i < 10; i++ {
		feed(c, &sec, 100, 95)
	}
	if b := c.Bias(); b != maxBias {
		t.Fatalf("bias = %v, want cap %v", b, maxBias)
	}
}

func TestLowPassRateRestores(t *testing.T) {
	sec := int64(2_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // loosen once
	feed(c, &sec, 100, 95) // loosen twice
	if b := c.Bias(); b < 2*step-1e-9 {
		t.Fatalf("setup bias = %v", b)
	}
	// Bots arrive: 100 challenged, 10 solved — pull the slack back.
	feed(c, &sec, 100, 10)
	feed(c, &sec, 100, 10)
	if b := c.Bias(); b > 1e-9 {
		t.Fatalf("bias after bot-heavy windows = %v, want 0", b)
	}
	// Never negative — loosen-only invariant.
	feed(c, &sec, 100, 0)
	if b := c.Bias(); b != 0 {
		t.Fatalf("bias must clamp at 0, got %v", b)
	}
}

func TestSmallSamplesDriftBackNotCalibrate(t *testing.T) {
	sec := int64(3_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // bias = step
	// A quiet window with 5 challenges all passed must NOT loosen further
	// (below minSample) — it drifts back instead.
	feed(c, &sec, 5, 5)
	want := step - drift
	if b := c.Bias(); b < want-1e-9 || b > want+1e-9 {
		t.Fatalf("bias after quiet window = %v, want %v", b, want)
	}
	// Long silence decays all the way to zero.
	for i := 0; i < 20; i++ {
		feed(c, &sec, 0, 0)
	}
	if b := c.Bias(); b != 0 {
		t.Fatalf("bias after long silence = %v, want 0", b)
	}
}

func TestMidRatePassHoldsSteady(t *testing.T) {
	sec := int64(4_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // bias = step
	// 70% pass rate: ambiguous — hold, don't oscillate.
	feed(c, &sec, 100, 70)
	if b := c.Bias(); b != step {
		t.Fatalf("bias after ambiguous window = %v, want unchanged %v", b, step)
	}
}

func TestConcurrentUseIsRaceFree(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Served()
				if i%3 == 0 {
					c.Passed(true, "src-"+strconv.Itoa(i%16))
				}
				_ = c.Bias()
			}
		}()
	}
	wg.Wait()
	s, p, _ := c.Snapshot()
	if s <= 0 || p <= 0 {
		t.Fatalf("snapshot counters should be positive: served=%d passed=%d", s, p)
	}
}

// The calibrator's input is a solved proof-of-work, and a solve proves a real
// BROWSER — which a headless one also is. So the one signal driving the site
// more permissive is the one an attacker can manufacture by running a real JS
// engine. These tests pin what bounds that.

// TestAFarmOfSolversCannotLoosen — the realistic shape of the attack: a small
// number of machines solving every challenge they are served, at a pass rate far
// above the loosen threshold.
func TestAFarmOfSolversCannotLoosen(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)

	// 100 served, 98 solved — a 98% pass rate, comfortably over loosenAt — but
	// from only three sources.
	for i := 0; i < 12; i++ {
		feedFrom(c, &sec, 100, 98, 98, 3)
	}
	if b := c.Bias(); b != 0 {
		t.Errorf("bias = %v after twelve windows of a 3-source solver farm at 98%%, want 0 — "+
			"an attacker willing to solve challenges could drive the site more permissive", b)
	}
}

// TestARealAudienceStillLoosens — the diversity floor must not break the feature
// it protects. A site that is genuinely challenging its own readers has to be
// able to self-heal, which is the entire reason this controller exists.
func TestARealAudienceStillLoosens(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)

	feedFrom(c, &sec, 100, 96, 96, 96) // 96 different people
	if b := c.Bias(); b != step {
		t.Errorf("bias = %v after one window of 96 distinct human solvers, want %v — "+
			"the shield can no longer stop bothering real readers", b, step)
	}
}

// TestDowngradedSolversDoNotCountTowardLoosening — a source the brain has already
// observed misbehaving does not get a vote on making the site more permissive,
// however many challenges it solves. Standing is fed by sustained low-score
// browsing in the Allow path, never by solving a challenge, so it is genuinely
// independent of the signal it qualifies.
func TestDowngradedSolversDoNotCountTowardLoosening(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)

	// The discriminating shape, and it took a surviving mutant to find it: the
	// diversity floor and the qualified-rate check can mask each other. Here
	// diversity is comfortably cleared — ten different qualified solvers — while
	// the qualified RATE is far below the threshold, because the other 85 solves
	// came from sources the brain had already downgraded. Only the rate check can
	// refuse this window. A version that loosened on the raw pass rate would see
	// 95/100 and loosen.
	for i := 0; i < 12; i++ {
		feedFrom(c, &sec, 100, 95, 10, 95)
	}
	if b := c.Bias(); b != 0 {
		t.Errorf("bias = %v after twelve windows where 85 of 95 solves came from downgraded "+
			"sources, want 0 — the raw pass rate is being used to loosen", b)
	}

	// And the degenerate case: nothing qualifies at all.
	for i := 0; i < 12; i++ {
		feedFrom(c, &sec, 100, 95, 0, 95)
	}
	if b := c.Bias(); b != 0 {
		t.Errorf("bias = %v after twelve windows of solves from downgraded sources, want 0", b)
	}
}

// TestUnqualifiedSolvesStillTighten — the gate is on LOOSENING only. Tightening
// back toward the operator's configured thresholds needs no independent signal,
// because there is nothing for an attacker to gain by driving the site stricter
// than the operator asked for. Gating both directions would let an attacker
// FREEZE the calibrator instead, which is a subtler version of the same win.
func TestUnqualifiedSolvesStillTighten(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)

	// Loosen legitimately first, so there is something to walk back.
	feedFrom(c, &sec, 100, 96, 96, 96)
	feedFrom(c, &sec, 100, 96, 96, 96)
	loosened := c.Bias()
	if loosened <= 0 {
		t.Fatalf("setup failed: bias = %v, expected a positive bias to walk back", loosened)
	}

	// Now a window where challenges are earning their keep: a low pass rate, and
	// none of the few solves qualify.
	feedFrom(c, &sec, 100, 20, 0, 20)
	if b := c.Bias(); b >= loosened {
		t.Errorf("bias = %v after a 20%% pass-rate window, want less than %v — "+
			"the calibrator can be frozen at its loosest by unqualified traffic", b, loosened)
	}
}

// TestTheCeilingIsStillBounded — the clamp is the backstop behind everything
// above, and it is what makes the honest claim about this controller true: even
// if every gate were defeated, the reachable state is a bounded, self-draining
// shift of the CHALLENGE bands. The block threshold is never touched.
func TestTheCeilingIsStillBounded(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)

	for i := 0; i < 50; i++ {
		feedFrom(c, &sec, 200, 200, 200, 200)
	}
	if b := c.Bias(); b != maxBias {
		t.Errorf("bias = %v after 50 maximally-loosening windows, want the %v cap", b, maxBias)
	}
	// And it drains on its own once the traffic stops.
	for i := 0; i < 5; i++ {
		feedFrom(c, &sec, 0, 0, 0, 0)
	}
	if b := c.Bias(); b >= maxBias {
		t.Errorf("bias = %v after five silent windows, want decay below the %v cap", b, maxBias)
	}
}

// TestSolverSetIsBounded — the per-window solver set is memory an attacker can
// make the shield allocate, one entry per solved challenge. Once the diversity
// floor is cleared the exact count changes no decision, so the set is capped.
func TestSolverSetIsBounded(t *testing.T) {
	c := New()
	for i := 0; i < distinctCap*4; i++ {
		c.Passed(true, "src-"+strconv.Itoa(i))
	}
	c.mu.Lock()
	n := len(c.solvers)
	c.mu.Unlock()
	if n > distinctCap {
		t.Errorf("solver set grew to %d entries, cap is %d — a solver farm can make the "+
			"shield allocate unboundedly", n, distinctCap)
	}
	if n < minDistinct {
		t.Errorf("solver set holds %d entries, below the %d the floor needs to ever clear", n, minDistinct)
	}
}
