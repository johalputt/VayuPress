package main

import (
	"sync/atomic"
	"time"
)

// surgeHysteresis debounces the raw L0-lane occupancy into the auto Sovereign
// Surge signal (SurgePressureFn). Without it, a single-request blip to >=90%
// occupancy instantly meets every unproven visitor with a browser check; a brief
// but legitimate traffic spike (a post going viral, a newsletter blast) would
// then challenge real readers for no good reason.
//
// The debounce is a classic hysteresis band:
//   - ENGAGE only after occupancy stays at/above the high-water mark (>=90% of
//     the cap) continuously for at least `dwell` — a momentary spike never trips.
//   - once engaged, STAY engaged until occupancy falls back below the low-water
//     mark (<75% of the cap), so it does not flap on and off around 90%.
//
// A sustained flood still engages surge (occupancy stays pinned high well past
// the dwell); recognised crawlers already bypass surge entirely at gate 0, so
// this only ever affects how quickly unproven human/unknown traffic is asked to
// prove itself. Best-effort/lock-free: the surge decision is inherently
// approximate, so a raced read costs at most one request's worth of imprecision.
type surgeHysteresis struct {
	occupancy func() (inflight, capacity int)
	dwell     time.Duration
	now       func() time.Time

	hotSinceNs atomic.Int64 // unix-nano when occupancy first crossed the high line; 0 = below
	engaged    atomic.Bool
}

func newSurgeHysteresis(occupancy func() (int, int), dwell time.Duration) *surgeHysteresis {
	return &surgeHysteresis{occupancy: occupancy, dwell: dwell, now: time.Now}
}

// pressured is the debounced critical-saturation signal wired into
// VayuShield.SurgePressureFn.
func (h *surgeHysteresis) pressured() bool {
	if h == nil || h.occupancy == nil {
		return false
	}
	inflight, capacity := h.occupancy()
	if capacity <= 0 {
		return false
	}
	if h.engaged.Load() {
		if inflight*4 < capacity*3 { // < 75% → relax
			h.engaged.Store(false)
			h.hotSinceNs.Store(0)
			return false
		}
		return true
	}
	if inflight*10 >= capacity*9 { // ≥ 90% → saturation candidate
		now := h.now().UnixNano()
		if h.hotSinceNs.CompareAndSwap(0, now) {
			return false // just crossed the line — require the dwell to elapse
		}
		if since := h.hotSinceNs.Load(); since != 0 && time.Duration(now-since) >= h.dwell {
			h.engaged.Store(true)
			return true
		}
		return false
	}
	// Below the high-water mark — reset the dwell timer.
	h.hotSinceNs.Store(0)
	return false
}
