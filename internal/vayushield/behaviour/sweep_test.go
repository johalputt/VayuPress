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

// ── adversarial pass ─────────────────────────────────────────────────────────

// The attack the "baseline only climbs" decision created.
//
// I made the baseline monotonic so an attacker could not drag it down and
// switch the detector off. That closed one door and opened a worse one: if the
// baseline only ever rises, an attacker who INFLATES it poisons the install
// permanently.
//
// The move is cheap. Send a burst of asset requests — stylesheets, images,
// anything the origin serves — alongside enough documents to clear the sample
// gate. The baseline records that window as this site's "healthy" ratio. Now
// stop. Ordinary traffic at six assets per document sits below a quarter of the
// poisoned baseline, so the install reports itself as permanently sweeping, the
// sample floor stays at three forever, and every reader with a warm cache who
// opens three articles gets a puzzle. It never recovers, because the baseline
// never decays. I have turned the operator's own defence into the outage.
func TestAnInflatedBaselineCannotPoisonTheDetector(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))

	// The poisoning window: 400 documents and 40 assets each.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("attacker-%d", i)
		tr.Observe(key, fmt.Sprintf("/doc-%d", i), 200)
		for a := 0; a < 40; a++ {
			tr.Observe(key, fmt.Sprintf("/assets/%d-%d.css", i, a), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	// Now entirely ordinary traffic: six sub-resources per page, the profile of
	// a real site with real readers.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("reader-%d", i)
		tr.Observe(key, fmt.Sprintf("/article-%d", i), 200)
		for a := 0; a < 6; a++ {
			tr.Observe(key, fmt.Sprintf("/assets/a-%d-%d.css", i, a), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	if tr.Sweeping() {
		t.Errorf("a site serving six sub-resources per document is reported as sweeping, "+
			"because a burst of asset requests inflated the baseline to %.1f and a quarter of "+
			"that is more than any honest ratio.\n"+
			"Every warm-cache reader opening three pages now gets a puzzle, permanently, "+
			"because the baseline never decays. The monotonic baseline closed one attack and "+
			"opened this one: a site actually serving its assets is not sweeping, whatever "+
			"number the baseline happens to hold.", tr.SiteBaselineAssetRatio())
	}
}

// Isolates the absolute ceiling. The baseline here is inside the cap, so the
// cap cannot be what saves this site — only the ceiling can.
//
// This test exists because removing the ceiling killed nothing: the poisoning
// test was being rescued by the baseline cap instead, so both mutations passed
// and neither control was actually under test. A suite that cannot say WHICH
// guard is holding is not testing either of them.
func TestTheAbsoluteCeilingAloneStopsAPoisonedBaseline(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))

	// Baseline of 10 assets per document — high, plausible, and under the cap.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("rich-%d", i)
		tr.Observe(key, fmt.Sprintf("/doc-%d", i), 200)
		for a := 0; a < 10; a++ {
			tr.Observe(key, fmt.Sprintf("/assets/%d-%d.css", i, a), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)
	if base := tr.SiteBaselineAssetRatio(); base >= maxBaselineAssetRatio {
		t.Fatalf("precondition: baseline %.1f reached the cap, so this test would be "+
			"measuring the cap rather than the ceiling", base)
	}

	// Ordinary traffic at 2 sub-resources per document. A quarter of 10 is 2.5,
	// so the RELATIVE test says this collapsed. It did not — a site serving two
	// assets per page is serving its assets.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("reader-%d", i)
		tr.Observe(key, fmt.Sprintf("/article-%d", i), 200)
		tr.Observe(key, fmt.Sprintf("/assets/a-%d.css", i), 200)
		tr.Observe(key, fmt.Sprintf("/assets/b-%d.js", i), 200)
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	if tr.Sweeping() {
		t.Error("a site serving 2 sub-resources per document was reported as sweeping because " +
			"its baseline happened to be 10. The relative test alone cannot tell a quieter " +
			"day from a corpus sweep; the absolute ceiling is what says 'this is still a site " +
			"serving its assets'.")
	}
}

// Isolates the relative test. Here the absolute ceiling would fire on its own
// and must not, because this install's own baseline says the ratio is normal
// for it.
func TestTheRelativeTestStillMattersOnALowAssetSite(t *testing.T) {
	tr, clock := trackerAt(time.Unix(1_700_000_000, 0))

	// A site that genuinely runs at ~1 asset per document: mostly text, one
	// stylesheet, everything else at the edge.
	for round := 0; round < 2; round++ {
		for i := 0; i < 400; i++ {
			key := fmt.Sprintf("reader-%d-%d", round, i)
			tr.Observe(key, fmt.Sprintf("/article-%d-%d", round, i), 200)
			tr.Observe(key, fmt.Sprintf("/assets/site-%d-%d.css", round, i), 200)
		}
		*clock = clock.Add(sweepWindowSec * time.Second)
		tr.Observe("tick", "/", 200)
	}
	base := tr.SiteBaselineAssetRatio()
	if base < minBaselineAssetRatio {
		t.Fatalf("precondition: baseline %.2f is below the arming floor", base)
	}

	// A window at 0.45 assets per document — under the absolute ceiling of 0.5,
	// but well ABOVE a quarter of this site's own baseline, so nothing has
	// collapsed. Only the relative test knows that.
	for i := 0; i < 400; i++ {
		key := fmt.Sprintf("later-%d", i)
		tr.Observe(key, fmt.Sprintf("/late-%d", i), 200)
		if i%100 < 45 {
			tr.Observe(key, fmt.Sprintf("/assets/late-%d.css", i), 200)
		}
	}
	*clock = clock.Add(sweepWindowSec * time.Second)
	tr.Observe("tick", "/", 200)

	if tr.Sweeping() {
		t.Errorf("a site whose baseline is %.2f was reported as sweeping at 0.45 assets per "+
			"document. That is above a quarter of its own baseline, so nothing collapsed — "+
			"the absolute ceiling on its own would challenge readers on every text-heavy "+
			"site in the world.", base)
	}
}
