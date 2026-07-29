// SPDX-License-Identifier: Apache-2.0

// Package calibrate is VayuShield's Aegis L4 feedback controller: it
// continuously tunes the challenge thresholds from observed outcomes, with no
// operator input, so the challenge ladder stays silent-first and self-heals
// when it starts bothering real people.
//
// The signal is the pass rate of issued challenges. A solved proof-of-work is
// overwhelmingly evidence of a real browser: bots either cannot run the
// solver or deliberately abandon the page. So:
//
//   - Pass rate HIGH (≥ 90%): almost everyone we challenge turns out to be a
//     real browser — the thresholds are biting humans. LOOSEN: raise the bias
//     so borderline scores are allowed instead of challenged.
//   - Pass rate LOW (≤ 50%): challenges are absorbing bots — the extra slack
//     is not needed. Walk the bias back toward the operator's configured
//     thresholds.
//   - No traffic to learn from: drift slowly back to zero, so stale looseness
//     never lingers.
//
// THE ATTACKER'S LEVER, AND WHAT BOUNDS IT. A solved proof-of-work is evidence
// of a real browser, not of a real person: a headless browser runs the same
// solver. So an attacker who is willing to solve challenges can push the pass
// rate up and drive the site more permissive — the feedback loop pointed at the
// one input they control.
//
// Three things bound it, and it is worth being precise about which does what:
//
//   - The clamp. The bias is [0, +0.2] with a 0.05 step and per-window decay, and
//     it never touches the BLOCK threshold. So the ceiling is a bounded,
//     self-draining shift of the challenge bands, not open-ended drift.
//   - Qualified passes. Only a solver the reputation brain has NOT already
//     downgraded counts toward loosening. Standing is fed by sustained
//     low-score browsing in the Allow path, never by solving a challenge, so it
//     is genuinely independent of the signal it is qualifying. Honest limit: the
//     brain only tracks suspects, so a source it has never seen returns neutral
//     and qualifies. This excludes an attacker who has already been observed
//     misbehaving — the common case — not a clean-slate one.
//   - A diversity floor, which is what actually bounds a farm. Loosening needs
//     passes from at least minDistinct different sources in the window. A real
//     audience clears that trivially; a solver farm has to pay for address
//     diversity to reach it, and under prefix-keyed enforcement that means
//     distinct /64s, not distinct addresses.
//
// Only LOOSENING is gated. Tightening back toward the operator's settings needs
// no independent signal, because there is nothing for an attacker to gain by
// driving the site stricter than the operator asked for.
//
// SAFETY INVARIANT — loosen-only: the bias is clamped to [0, +0.2]. The
// calibrator can only ever make the shield MORE permissive than the operator's
// settings, never stricter, so it can never be the reason a real user gets
// challenged or blocked. Automatic tightening remains exclusively the job of
// the under-attack controller (which relaxes the moment a flood subsides) and
// never applies to verified visitors or known good bots at all.
package calibrate

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	windowSec = 600 // 10-minute observation windows
	minSample = 20  // don't calibrate on noise
	step      = 0.05
	drift     = 0.01 // per-window decay toward 0 with no signal
	maxBias   = 0.20

	loosenAt  = 0.90 // pass rate at/above which we are bothering humans
	restoreAt = 0.50 // pass rate at/below which challenges are earning their keep

	// minDistinct is the number of DIFFERENT qualified solvers a window needs
	// before its pass rate is allowed to loosen anything. A site with real
	// readers clears this without noticing; a small solver farm cannot, however
	// many challenges it solves.
	minDistinct = 8
	// distinctCap bounds the per-window solver set. Once the floor is cleared the
	// exact count no longer changes any decision, so there is nothing to gain by
	// letting the set grow with traffic.
	distinctCap = 512
)

// Calibrator accumulates challenge outcomes and derives the threshold bias.
// All methods are lock-free and safe for concurrent use.
type Calibrator struct {
	served    atomic.Int64
	passed    atomic.Int64
	qualified atomic.Int64 // passes an independent signal agrees look human
	epoch     atomic.Int64
	biasBits  atomic.Uint64 // float64 bits

	// solvers holds the distinct qualified solver keys seen this window. Guarded
	// by a mutex rather than built from atomics: it is touched once per SOLVED
	// challenge, which is orders of magnitude rarer than a request, so the lock
	// is nowhere near a hot path.
	mu      sync.Mutex
	solvers map[string]struct{}

	now func() time.Time
}

// New builds a calibrator with zero bias.
func New() *Calibrator {
	return &Calibrator{now: time.Now, solvers: make(map[string]struct{})}
}

// Served records that a challenge was issued.
func (c *Calibrator) Served() { c.rotate(); c.served.Add(1) }

// Passed records that a challenge was solved.
//
// A solve proves a real BROWSER, which a headless one also is — so the caller
// also reports whether an independent signal agrees this looks like a real
// person, and the key that solved it. Only qualified solves, from enough
// distinct sources, are allowed to loosen the thresholds; every solve still
// counts toward the raw rate that tightens them back.
func (c *Calibrator) Passed(qualified bool, key string) {
	c.rotate()
	c.passed.Add(1)
	if !qualified {
		return
	}
	c.qualified.Add(1)
	if key == "" {
		return
	}
	c.mu.Lock()
	if len(c.solvers) < distinctCap {
		c.solvers[key] = struct{}{}
	}
	c.mu.Unlock()
}

// Bias returns the current additive threshold bias in [0, +0.2]. Add it to
// the configured PoW/JS thresholds: a positive bias means "challenge less".
func (c *Calibrator) Bias() float64 {
	c.rotate()
	return math.Float64frombits(c.biasBits.Load())
}

// rotate closes the current window when its time is up and folds the window's
// pass rate into the bias. The CAS winner does the fold; losers proceed —
// worst case an observation lands in the next window.
func (c *Calibrator) rotate() {
	e := c.now().Unix() / windowSec
	old := c.epoch.Load()
	if old == e {
		return
	}
	if !c.epoch.CompareAndSwap(old, e) {
		return
	}
	served := c.served.Swap(0)
	passed := c.passed.Swap(0)
	qualified := c.qualified.Swap(0)
	c.mu.Lock()
	distinct := len(c.solvers)
	c.solvers = make(map[string]struct{})
	c.mu.Unlock()

	bias := math.Float64frombits(c.biasBits.Load())
	switch {
	case served < minSample:
		bias -= drift
	default:
		// Loosening is judged on the QUALIFIED rate and needs source diversity.
		// Tightening is judged on the raw rate and needs neither: an attacker has
		// nothing to gain from making the site stricter than the operator asked.
		if float64(qualified)/float64(served) >= loosenAt && distinct >= minDistinct {
			bias += step
		} else if float64(passed)/float64(served) <= restoreAt {
			bias -= step
		}
	}
	if bias < 0 {
		bias = 0
	}
	if bias > maxBias {
		bias = maxBias
	}
	c.biasBits.Store(math.Float64bits(bias))
}

// Snapshot reports the live window's counters and the current bias (dashboard).
func (c *Calibrator) Snapshot() (served, passed int64, bias float64) {
	return c.served.Load(), c.passed.Load(), math.Float64frombits(c.biasBits.Load())
}

// BiasedForTest returns a calibrator pre-loaded with the given bias, for tests
// that need to observe downstream behaviour without simulating whole windows.
func BiasedForTest(bias float64) *Calibrator {
	c := New()
	if bias < 0 {
		bias = 0
	}
	if bias > maxBias {
		bias = maxBias
	}
	c.biasBits.Store(math.Float64bits(bias))
	// Pin the epoch to the current window so Bias() won't rotate-drift it away.
	c.epoch.Store(c.now().Unix() / windowSec)
	return c
}
