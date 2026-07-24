package main

import (
	"testing"
	"time"
)

func TestSurgeHysteresis(t *testing.T) {
	var inflight, capacity int
	now := time.Unix(1_700_000_000, 0)
	h := newSurgeHysteresis(func() (int, int) { return inflight, capacity }, 2*time.Second)
	h.now = func() time.Time { return now }

	capacity = 100

	// Calm: well below the high-water mark → never engaged.
	inflight = 40
	if h.pressured() {
		t.Fatal("calm traffic must not engage surge")
	}

	// A momentary spike to >=90% does NOT engage until the dwell elapses.
	inflight = 95
	if h.pressured() {
		t.Fatal("first crossing of the high-water mark must not engage (dwell not elapsed)")
	}
	now = now.Add(1 * time.Second)
	if h.pressured() {
		t.Fatal("still within the dwell window — must not engage yet")
	}

	// Sustained past the dwell → engage.
	now = now.Add(2 * time.Second)
	if !h.pressured() {
		t.Fatal("sustained saturation past the dwell must engage surge")
	}

	// Hysteresis: dropping to 80% (between 75% and 90%) keeps it engaged.
	inflight = 80
	if !h.pressured() {
		t.Fatal("occupancy in the hysteresis band must keep surge engaged (no flapping)")
	}

	// Falling below the low-water mark (<75%) relaxes.
	inflight = 70
	if h.pressured() {
		t.Fatal("occupancy below the low-water mark must relax surge")
	}

	// And a brief spike that immediately subsides never engages (dwell resets).
	inflight = 96
	_ = h.pressured() // arms the dwell timer
	inflight = 50     // subsides before the dwell elapses
	now = now.Add(5 * time.Second)
	if h.pressured() {
		t.Fatal("a spike that subsided before the dwell must not engage")
	}
}

func TestSurgeHysteresisZeroCap(t *testing.T) {
	h := newSurgeHysteresis(func() (int, int) { return 10, 0 }, time.Second)
	if h.pressured() {
		t.Fatal("a zero/unknown cap must never report pressure")
	}
	var nilGate *surgeHysteresis
	if nilGate.pressured() {
		t.Fatal("nil hysteresis must be safe and report no pressure")
	}
}
