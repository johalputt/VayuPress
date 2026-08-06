// SPDX-License-Identifier: Apache-2.0

package behaviour

import (
	"fmt"
	"testing"
	"time"
)

// The attacker's plan, stated plainly: I am not going to beat any threshold in
// this package. I am going to stay under all of them. I have a few thousand
// residential addresses and I want your whole corpus, so I take three pages
// from each address and move on. Eight requests before you sample me, twenty-
// four before path diversity counts — I will never make eight. Your behavioural
// scorer is excellent and it will never once be asked about me.

// trackerAt builds a tracker with a controllable clock.
func trackerAt(start time.Time) (*Tracker, *time.Time) {
	now := start
	t := New()
	t.now = func() time.Time { return now }
	return t, &now
}

// warmBaseline drives the traffic of a healthy site through the tracker so the
// install demonstrates the asset-to-document ratio a real audience produces,
// then closes the window so the baseline is recorded.
func warmBaseline(t *Tracker, clock *time.Time) {
	// 400 documents, 6 assets each — an ordinary page with a stylesheet, a
	// script and a few images.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("reader-%d", i)
		t.Observe(key, fmt.Sprintf("/article-%d", i), 200)
		for a := 0; a < 6; a++ {
			t.Observe(key, fmt.Sprintf("/assets/%d-%d.css", i, a), 200)
		}
	}
	// Roll past the site window so the baseline is computed and published.
	*clock = clock.Add(sweepWindowSec * time.Second)
	t.Observe("tick", "/", 200)
}

// THE finding. A distributed sweep at three requests per address is invisible.
func TestADistributedSweepIsScoredAtAll(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))
	warmBaseline(tr, clock)

	if base := tr.SiteBaselineAssetRatio(); base < minBaselineAssetRatio {
		t.Fatalf("baseline %.2f did not reach %.2f, so the detector is dormant and this test "+
			"proves nothing about it", base, minBaselineAssetRatio)
	}

	// The sweep: many addresses, three distinct documents each, no assets ever.
	// Enough of them to fill a site window so the collapse is visible.
	var lastScored int
	for i := 0; i < 200; i++ {
		key := fmt.Sprintf("sweeper-%d", i)
		var sig Signals
		for p := 0; p < 3; p++ {
			sig = tr.Observe(key, fmt.Sprintf("/corpus-%d-%d", i, p), 200)
		}
		if d, _ := sig.Score(); d > 0 {
			lastScored++
		}
	}

	// Close the window the sweep filled, so its ratio is evaluated.
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	if !tr.Sweeping() {
		t.Fatalf("after 600 document fetches with zero assets against a baseline of %.2f "+
			"assets per document, the population is not reported as sweeping. Nothing below "+
			"can work if this does not.", tr.SiteBaselineAssetRatio())
	}

	// Now the same client profile, with the sweep verdict live.
	key := "sweeper-during"
	var sig Signals
	for p := 0; p < 3; p++ {
		sig = tr.Observe(key, fmt.Sprintf("/corpus-late-%d", p), 200)
	}
	delta, reasons := sig.Score()
	if delta <= 0 {
		t.Errorf("a client that fetched 3 distinct documents and no assets, while the whole "+
			"site's asset ratio has collapsed, scored %.2f with reasons %v.\n"+
			"This is the entire shape of the campaign — thousands of addresses, three pages "+
			"each, never eight — and it is being handed a score of zero because a per-client "+
			"sample size was chosen for a scraper that runs from one address.",
			delta, reasons)
	}
}

// The same client profile must score NOTHING when the site is healthy. The
// population is what buys the lowered sample size; without it this would be a
// signal that fires on any reader with a warm cache.
func TestTheLoweredSampleSizeAppliesOnlyDuringASweep(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))
	warmBaseline(tr, clock)

	if tr.Sweeping() {
		t.Fatal("a healthy site was reported as sweeping")
	}

	var sig Signals
	for p := 0; p < 3; p++ {
		sig = tr.Observe("warm-cache-reader", fmt.Sprintf("/page-%d", p), 200)
	}
	if delta, reasons := sig.Score(); delta != 0 {
		t.Errorf("a reader with a warm cache reading 3 pages on a HEALTHY site scored %.2f (%v). "+
			"Outside a sweep the sample floor must stay at %d, or every returning reader whose "+
			"browser already has the stylesheet gets a puzzle.", delta, reasons, minSample)
	}
}

// An install whose origin never sees assets — statics served at the edge — must
// never arm this. Its baseline is degenerate, so there is no collapse to detect,
// and firing there would challenge every reader on the site forever.
func TestAnInstallThatNeverSeesAssetsNeverArmsTheDetector(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))

	// Documents only, for a long time. This is a correctly configured site whose
	// reverse proxy answers for /assets without consulting the app.
	for round := 0; round < 3; round++ {
		for i := 0; i < 400; i++ {
			tr.Observe(fmt.Sprintf("reader-%d", i), fmt.Sprintf("/article-%d", i), 200)
		}
		*clock = clock.Add(sweepWindowSec * time.Second)
		tr.Observe("tick", "/", 200)
	}

	if tr.Sweeping() {
		t.Fatal("an install whose origin never sees assets was reported as sweeping. Every " +
			"reader on that site would be challenged, permanently, because of how their " +
			"reverse proxy is configured. This is the failure mode a fixed asset-ratio " +
			"threshold has and this design exists to avoid.")
	}
	if base := tr.SiteBaselineAssetRatio(); base >= minBaselineAssetRatio {
		t.Errorf("baseline reached %.2f on a site that served no assets at all", base)
	}
}

// The dangerous case is not a baseline of zero — the arithmetic alone stops
// that, since nothing is below zero. It is a baseline that is non-zero and
// still degenerate: an install whose edge serves almost every asset but lets
// the odd one through to the origin. A quarter of "almost nothing" is nothing,
// so without an explicit floor on what counts as healthy, that site arms the
// detector on a ratio that never meant anything.
//
// This test exists because removing the floor did NOT fail anything — the
// zero-baseline test passed on the arithmetic and the guard was never
// exercised. A mutation that survives is the test suite reporting a hole in
// itself.
func TestADegenerateButNonZeroBaselineNeverArmsTheDetector(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))

	// ~0.1 assets per document: the edge serves the rest.
	for round := 0; round < 3; round++ {
		for i := 0; i < 400; i++ {
			tr.Observe(fmt.Sprintf("reader-%d", i), fmt.Sprintf("/article-%d-%d", round, i), 200)
			if i%10 == 0 {
				tr.Observe(fmt.Sprintf("reader-%d", i), fmt.Sprintf("/assets/%d-%d.css", round, i), 200)
			}
		}
		*clock = clock.Add(sweepWindowSec * time.Second)
		tr.Observe("tick", "/", 200)
	}

	base := tr.SiteBaselineAssetRatio()
	if base <= 0 || base >= minBaselineAssetRatio {
		t.Fatalf("precondition: wanted a small non-zero baseline, got %.3f. This test only "+
			"means something when the baseline is degenerate but not zero.", base)
	}

	// Now a window with no assets at all — a quarter of 0.1 is 0.025, and this
	// window's ratio of 0 is below it.
	for i := 0; i < 400; i++ {
		tr.Observe(fmt.Sprintf("reader-%d", i), fmt.Sprintf("/late-%d", i), 200)
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	if tr.Sweeping() {
		t.Errorf("an install with a baseline of %.3f assets per document armed the sweep "+
			"detector. That baseline never described a healthy site, so a collapse relative "+
			"to it describes nothing — and every reader there now gets a puzzle.", base)
	}
}

// The baseline must not be draggable downward. An attacker who can lower it can
// switch the detector off by sweeping slowly for long enough first.
func TestTheBaselineCannotBeDraggedDown(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))
	warmBaseline(tr, clock)
	high := tr.SiteBaselineAssetRatio()
	if high < minBaselineAssetRatio {
		t.Fatalf("precondition: baseline %.2f", high)
	}

	// Many windows of pure document traffic — the patient attacker.
	for round := 0; round < 10; round++ {
		for i := 0; i < 400; i++ {
			tr.Observe(fmt.Sprintf("sweeper-%d", i), fmt.Sprintf("/corpus-%d-%d", round, i), 200)
		}
		*clock = clock.Add(sweepWindowSec * time.Second)
		tr.Observe("tick", "/", 200)
	}

	if got := tr.SiteBaselineAssetRatio(); got < high {
		t.Errorf("baseline fell from %.2f to %.2f under sustained document-only traffic. "+
			"A baseline that decays is a switch the attacker holds: sweep gently until the "+
			"install forgets what healthy looked like, then sweep freely.", high, got)
	}
	if !tr.Sweeping() {
		t.Error("ten windows of document-only traffic against a healthy baseline did not " +
			"register as a sweep")
	}
}

// Reloading one page is not crawling, during a sweep or outside one. This is the
// case the original signal got wrong; the lowered floor must not reintroduce it.
func TestReloadingOnePageIsNotASweepEvenDuringOne(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))
	warmBaseline(tr, clock)
	for i := 0; i < 200; i++ {
		for p := 0; p < 3; p++ {
			tr.Observe(fmt.Sprintf("sweeper-%d", i), fmt.Sprintf("/corpus-%d-%d", i, p), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)
	if !tr.Sweeping() {
		t.Fatal("precondition: expected a sweep")
	}

	var sig Signals
	for p := 0; p < 6; p++ {
		sig = tr.Observe("reloader", "/the-same-page", 200)
	}
	if delta, reasons := sig.Score(); delta != 0 {
		t.Errorf("a client reloading ONE page scored %.2f (%v). One path is not a corpus "+
			"sweep — that is the rate limiter's business, not the classifier's.", delta, reasons)
	}
}

// The bound survives. Behaviour must never be able to block on its own, and the
// new path must not have opened a way around MaxDelta.
func TestSweepScoringStaysWithinTheBudget(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))
	warmBaseline(tr, clock)
	for i := 0; i < 200; i++ {
		for p := 0; p < 3; p++ {
			tr.Observe(fmt.Sprintf("sweeper-%d", i), fmt.Sprintf("/corpus-%d-%d", i, p), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	// The worst client available: many distinct paths, all 404, no assets.
	var sig Signals
	for p := 0; p < 40; p++ {
		sig = tr.Observe("worst", fmt.Sprintf("/nope-%d", p), 404)
	}
	delta, _ := sig.Score()
	if delta > MaxDelta {
		t.Errorf("behaviour scored %.2f, above its own budget of %.2f — the clamp is what "+
			"keeps this from reaching a block on heuristics alone", delta, MaxDelta)
	}
}
