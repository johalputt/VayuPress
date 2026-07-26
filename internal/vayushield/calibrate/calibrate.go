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
// SAFETY INVARIANT — loosen-only: the bias is clamped to [0, +0.2]. The
// calibrator can only ever make the shield MORE permissive than the operator's
// settings, never stricter, so it can never be the reason a real user gets
// challenged or blocked. Automatic tightening remains exclusively the job of
// the under-attack controller (which relaxes the moment a flood subsides) and
// never applies to verified visitors or known good bots at all.
package calibrate

import (
	"math"
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
)

// Calibrator accumulates challenge outcomes and derives the threshold bias.
// All methods are lock-free and safe for concurrent use.
type Calibrator struct {
	served   atomic.Int64
	passed   atomic.Int64
	epoch    atomic.Int64
	biasBits atomic.Uint64 // float64 bits

	now func() time.Time
}

// New builds a calibrator with zero bias.
func New() *Calibrator {
	return &Calibrator{now: time.Now}
}

// Served records that a challenge was issued.
func (c *Calibrator) Served() { c.rotate(); c.served.Add(1) }

// Passed records that a challenge was solved (real browser proven).
func (c *Calibrator) Passed() { c.rotate(); c.passed.Add(1) }

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
	bias := math.Float64frombits(c.biasBits.Load())
	switch {
	case served < minSample:
		bias -= drift
	default:
		rate := float64(passed) / float64(served)
		if rate >= loosenAt {
			bias += step
		} else if rate <= restoreAt {
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
