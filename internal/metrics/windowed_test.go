// SPDX-License-Identifier: Apache-2.0

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

// TestPercentileResolvesWithinTheBucket is the regression test for a dashboard
// figure that could not show an improvement.
//
// Buckets double: 1, 2, 4, 8, 16, 32, 64, 128… Returning the bucket's CEILING
// reported the worst case in the bucket as though it were the measurement, so a
// P95 of 65 ms and one of 128 ms both read "128 ms". An operator halving real
// latency from 128 ms to 70 ms saw the number not move at all — the only way to
// observe any gain was to cross a power of two.
func TestPercentileResolvesWithinTheBucket(t *testing.T) {
	// Everything lands in the (64,128] bucket, but concentrated at its bottom.
	var lowInBucket WindowedHistogram
	for i := 0; i < 100; i++ {
		lowInBucket.Record(66 * time.Millisecond)
	}
	got := lowInBucket.Percentile(95)
	if got > 128 {
		t.Fatalf("P95 = %d ms, above the bucket ceiling", got)
	}
	if got == 128 {
		t.Errorf("P95 = %d ms — still reporting the bucket ceiling, so a real improvement inside the bucket is invisible", got)
	}

	// What interpolation CAN deliver: a figure that moves continuously as the
	// distribution shifts across buckets, instead of jumping only at powers of two.
	//
	// It cannot separate two distributions that fall entirely inside ONE bucket —
	// the stored state for those is identical by construction, and no amount of
	// interpolation recovers detail that was never recorded. Asking for that would
	// be asking the histogram to be something it is not.
	prev := int64(-1)
	for slow := 0; slow <= 20; slow++ {
		var h WindowedHistogram
		for i := 0; i < 100-slow; i++ {
			h.Record(10 * time.Millisecond) // (8,16]
		}
		for i := 0; i < slow; i++ {
			h.Record(200 * time.Millisecond) // (128,256]
		}
		got := h.Percentile(95)
		if prev >= 0 && got < prev {
			t.Errorf("P95 fell from %d to %d ms as MORE slow requests were added", prev, got)
		}
		prev = got
	}
	// With 20%% slow requests the P95 must have climbed out of the fast bucket.
	if prev <= 16 {
		t.Errorf("P95 = %d ms with a fifth of requests at 200 ms; it is not tracking the tail", prev)
	}
}

// TestPercentileStaysWithinItsBucketBounds — interpolation must estimate, never
// invent. A reported value outside the bucket the sample fell in would be worse
// than the ceiling it replaced.
func TestPercentileStaysWithinItsBucketBounds(t *testing.T) {
	var h WindowedHistogram
	for i := 0; i < 200; i++ {
		h.Record(70 * time.Millisecond) // (64,128]
	}
	got := h.Percentile(95)
	if got <= 64 || got > 128 {
		t.Errorf("P95 = %d ms, outside the (64,128] bucket the samples fell in", got)
	}
}
