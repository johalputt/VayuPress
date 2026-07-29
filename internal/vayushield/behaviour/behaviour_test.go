// SPDX-License-Identifier: Apache-2.0

package behaviour

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The gap this package exists for: a scraper that sets a Chrome User-Agent
// scores 0.15 and is classified as a human, because every other signal is either
// unavailable behind a TLS-terminating proxy or taken from headers the client
// controls. Behaviour is the one thing a client cannot fake without actually
// behaving like a browser.

// TestAScraperWearingABrowserUAIsScored is the headline case: identical headers
// to a real browser, completely different behaviour.
func TestAScraperWearingABrowserUAIsScored(t *testing.T) {
	tr := New()

	// A scraper: many documents, no sub-resources, many distinct paths.
	var last Signals
	for i := 0; i < 40; i++ {
		last = tr.Observe("scraper", "/article/"+strconv.Itoa(i), http.StatusOK)
	}
	scraperDelta, reasons := last.Score()
	if scraperDelta <= 0 {
		t.Errorf("a client fetching 40 distinct documents and zero sub-resources scored %v — "+
			"this is the exact profile the package exists to catch", scraperDelta)
	}
	t.Logf("scraper: delta=%v reasons=%v", scraperDelta, reasons)

	// A reader: a few pages, each with its assets.
	tr2 := New()
	for i := 0; i < 5; i++ {
		last = tr2.Observe("reader", "/article/"+strconv.Itoa(i), http.StatusOK)
		for _, a := range []string{"/static/app.css", "/static/app.js", "/media/hero.webp"} {
			last = tr2.Observe("reader", a, http.StatusOK)
		}
	}
	readerDelta, _ := last.Score()
	if readerDelta != 0 {
		t.Errorf("a reader browsing five articles and loading their assets scored %v, want 0 — "+
			"a behavioural signal that fires on real readers is worse than none", readerDelta)
	}
}

// TestSmallSamplesScoreNothing — one 404 out of one request is a 100% error rate
// that says nothing. Acting on it would mean every visitor's first request could
// move their score.
func TestSmallSamplesScoreNothing(t *testing.T) {
	tr := New()
	var s Signals
	for i := 0; i < minSample-1; i++ {
		s = tr.Observe("newcomer", "/missing", http.StatusNotFound)
	}
	if s.Sampled {
		t.Errorf("%d requests were treated as a sample; the floor is %d", s.Requests, minSample)
	}
	if d, _ := s.Score(); d != 0 {
		t.Errorf("an unsampled window scored %v, want 0", d)
	}
}

// TestBehaviourCannotBlockOnItsOwn is the bound that makes this safe to ship.
// Every signal here is a heuristic over a lossy sketch and each has a legitimate
// client that trips it, so behaviour must be able to push a client into a
// solvable challenge and never into a block by itself.
func TestBehaviourCannotBlockOnItsOwn(t *testing.T) {
	tr := New()
	// The worst possible behaviour: every request a 404, no assets, every path
	// distinct.
	var s Signals
	for i := 0; i < 200; i++ {
		s = tr.Observe("worst", "/scan/"+strconv.Itoa(i), http.StatusNotFound)
	}
	delta, _ := s.Score()
	if delta > MaxDelta {
		t.Errorf("delta %v exceeds the %v bound", delta, MaxDelta)
	}

	// The shipped defaults: unknown clients start at 0.25, block at 0.8.
	const unknownStart, blockThreshold = 0.25, 0.8
	if unknownStart+delta >= blockThreshold {
		t.Errorf("the worst possible behaviour takes an unknown client from %v to %v, past the "+
			"%v block threshold — heuristics must be able to reach a challenge and never a block",
			unknownStart, unknownStart+delta, blockThreshold)
	}
	// And it must still be able to reach a challenge, or the bound has made the
	// whole package decorative.
	const powThreshold = 0.4
	if unknownStart+delta < powThreshold {
		t.Errorf("the worst possible client reaches only %v, below the %v challenge threshold — "+
			"this package now changes nothing", unknownStart+delta, powThreshold)
	}
}

// TestWindowsRoll — the counters are per-minute, so yesterday's scan does not
// follow a client that has since behaved.
func TestWindowsRoll(t *testing.T) {
	tr := New()
	now := time.Now()
	tr.now = func() time.Time { return now }

	for i := 0; i < 40; i++ {
		tr.Observe("client", "/scan/"+strconv.Itoa(i), http.StatusNotFound)
	}
	now = now.Add(2 * time.Minute)
	s := tr.Observe("client", "/article", http.StatusOK)
	if !s.SlotReset {
		t.Error("the window did not roll after two minutes")
	}
	if s.Requests != 1 || s.NotFound != 0 {
		t.Errorf("counters carried across the window boundary: %+v", s)
	}
}

// TestMemoryIsFixed — the table is sized at construction. An attacker rotating
// through unlimited keys must cost accuracy, never memory: the alternative is a
// map whose size they choose.
func TestMemoryIsFixed(t *testing.T) {
	tr := New()
	for i := 0; i < slots*8; i++ {
		tr.Observe("client-"+strconv.Itoa(i), "/p", http.StatusOK)
	}
	// The table is an array; the assertion is that nothing here can grow it. A
	// compile-time fact, checked at runtime for the reader's benefit.
	if len(tr.tab) != slots {
		t.Errorf("table grew to %d slots from %d", len(tr.tab), slots)
	}
}

// TestConcurrentObservationsAreRaceFree — this runs on every unverified request,
// so it is as concurrent as the server is.
func TestConcurrentObservationsAreRaceFree(t *testing.T) {
	tr := New()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tr.Observe("k"+strconv.Itoa(g%7), "/p"+strconv.Itoa(i%13), http.StatusOK)
			}
		}(g)
	}
	wg.Wait()
}

// TestAssetClassification — the documents-without-sub-resources signal is only
// as good as this predicate, and getting it wrong in the generous direction
// means a scraper looks like a browser.
func TestAssetClassification(t *testing.T) {
	for _, p := range []string{"/static/app.css", "/a/b/c.js", "/media/x.WEBP", "/f/g.woff2", "/x.map"} {
		if !isAsset(p) {
			t.Errorf("%q was not recognised as a sub-resource", p)
		}
	}
	for _, p := range []string{"/", "/article/slug", "/feed.xml", "/about.html", "/x.", "/no-extension"} {
		if isAsset(p) {
			t.Errorf("%q was treated as a sub-resource, so a client fetching only these would "+
				"look like a browser loading assets", p)
		}
	}
}

// TestReloadingOnePageIsNotCrawling — found by an existing shield test rather
// than written up front. Twelve requests to the same document tripped the
// no-sub-resources signal, but a client reloading one page is not crawling; that
// is the rate limiter's business, and misclassifying it would put a challenge in
// front of someone hitting refresh.
func TestReloadingOnePageIsNotCrawling(t *testing.T) {
	tr := New()
	var s Signals
	for i := 0; i < 30; i++ {
		s = tr.Observe("reloader", "/article", http.StatusOK)
	}
	if d, r := s.Score(); d != 0 {
		t.Errorf("30 requests to ONE document scored %v (%v), want 0", d, r)
	}
}
