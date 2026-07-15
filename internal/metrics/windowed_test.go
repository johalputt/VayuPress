package metrics

import (
	"testing"
	"time"
)

// TestWindowedPercentile pins the recent-window P95: with a fast majority the
// P95 stays in the fast bucket, and it only climbs once the slow tail exceeds 5%.
func TestWindowedPercentile(t *testing.T) {
	var h WindowedHistogram
	if got := h.Percentile(95); got != 0 {
		t.Fatalf("empty window P95 = %d, want 0", got)
	}
	// 100 fast (10ms) + 4 slow (5000ms): slow share ~3.8% < 5%, so P95 is fast.
	for i := 0; i < 100; i++ {
		h.Record(10 * time.Millisecond)
	}
	for i := 0; i < 4; i++ {
		h.Record(5000 * time.Millisecond)
	}
	if got := h.Percentile(95); got > 16 {
		t.Errorf("P95 with <5%% slow = %dms, want fast bucket (<=16ms)", got)
	}
	// Add enough slow samples that the tail crosses 5% and P95 must reflect it.
	for i := 0; i < 20; i++ {
		h.Record(5000 * time.Millisecond)
	}
	if got := h.Percentile(95); got < 4096 {
		t.Errorf("P95 with a heavy slow tail = %dms, want it to reflect the slow bucket", got)
	}
}

// TestWindowedAgesOut pins the whole point of the window: samples older than the
// window are cleared, so a past burst does not pin the number forever.
func TestWindowedAgesOut(t *testing.T) {
	var h WindowedHistogram
	// Seed the head slot as if 500 slow requests landed at minute 1000.
	h.slotMin = 1000
	h.slots[h.head][13] = 500

	// Jump past a full window: every slot must be cleared.
	h.rotate(1000 + windowMinutes + 5)

	var count int64
	for s := 0; s < windowMinutes; s++ {
		for _, v := range h.slots[s] {
			count += v
		}
	}
	if count != 0 {
		t.Fatalf("stale samples survived a full window: %d remain", count)
	}
	if got := h.Percentile(95); got != 0 {
		t.Errorf("aged-out window P95 = %d, want 0", got)
	}
}

// TestWindowedPartialRotationKeepsRecent pins that a rotation shorter than the
// window keeps in-window samples while clearing only the elapsed slots.
func TestWindowedPartialRotationKeepsRecent(t *testing.T) {
	var h WindowedHistogram
	h.slotMin = 2000
	h.slots[h.head][2] = 30 // 30 recent samples

	h.rotate(2003) // 3 minutes later — still inside the 15-minute window
	var count int64
	for s := 0; s < windowMinutes; s++ {
		for _, v := range h.slots[s] {
			count += v
		}
	}
	if count != 30 {
		t.Fatalf("recent samples lost on a partial rotation: have %d, want 30", count)
	}
}
