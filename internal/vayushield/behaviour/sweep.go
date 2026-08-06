// SPDX-License-Identifier: Apache-2.0

package behaviour

import (
	"sync/atomic"
)

// Sweep detection: the population-level companion to the per-client signals.
//
// # The gap this closes
//
// Every threshold in behaviour.go is per client per minute — eight requests
// before anything is sampled, twenty-four before path diversity counts. Those
// numbers are right for a scraper running from one address, and they are
// exactly what a distributed sweep is designed to stay under. Spread the same
// crawl across a few thousand residential addresses at one or two requests
// each and no client ever reaches a sample size, so no client is ever scored,
// and the whole campaign is invisible to a package whose entire purpose is
// seeing it.
//
// This was found on a live install: 38,403 pageviews over seven days, ~1.5% of
// visits carrying any referrer, the top ten pages accounting for under 300 of
// those views, and the shield's own history showing it had challenged a
// completely different population — self-identifying bots with scanner User-
// Agents — while the sweep went straight through presenting as Chrome.
//
// # Why this is a change detector and not a threshold
//
// The tempting version compares assets to documents against a fixed floor and
// calls anything below it a sweep. That version is a site-wide outage waiting
// for the first operator whose reverse proxy serves static files without ever
// consulting the app: their origin sees documents and no assets by design, so
// every reader looks like a scraper and everybody gets a puzzle forever.
//
// So the question asked here is not "are there few assets?" but "did the ratio
// this install normally runs at just collapse?". An install that never sees
// assets has a baseline of roughly zero, nothing can fall below it, and the
// signal correctly disables itself. The failure mode of the fixed threshold is
// challenging every reader on a correctly configured site; the failure mode of
// this one is staying quiet on a site whose baseline was already degenerate.
// Only one of those is acceptable in something that ships on by default.
//
// # What it is allowed to do
//
// Nothing, on its own. It never reaches a verdict and never touches a score
// directly. Its whole effect is to lower the SAMPLE SIZE the per-client
// document-without-assets signal needs, from eight requests to two, while a
// sweep is in progress. Who gets challenged is still decided per client, by
// that client's own behaviour, and the result is still a solvable puzzle that
// costs a reader one round and costs a crawler its economics.
//
// The individual is judged individually. The population only decides how much
// patience the individual is owed.

const (
	// sweepWindowSec is longer than the per-client window on purpose: a
	// population signal wants a stable denominator, and a minute of a big site
	// is noisy enough that the ratio swings on nothing.
	sweepWindowSec = 300

	// minSiteSample is how many documents a window needs before the current
	// ratio means anything at all. Below this the denominator is small enough
	// that a handful of cached page loads reads as a collapse.
	minSiteSample = 300

	// minBaselineAssetRatio is the healthy asset-to-document ratio an install
	// must have DEMONSTRATED before a collapse can be detected. A site whose
	// origin never sees assets — statics served at the edge — never reaches it,
	// and the whole mechanism stays dormant there, which is the intent.
	minBaselineAssetRatio = 1.0

	// collapseFraction is how far below its own baseline the current window has
	// to fall. A quarter is deliberately far: normal caching alone moves this
	// ratio a lot across a day, and a signal that fires on ordinary cache
	// warming is a signal operators turn off.
	collapseFraction = 0.25

	// sweepSampleFloor is the sample size the document-without-assets signal
	// drops to while a sweep is in progress. Three, because the sweep that
	// prompted this averaged three pageviews per visitor — below that there is
	// no pattern to see, and above it the campaign slips under again.
	sweepSampleFloor = 3
)

// siteState is the population sketch. Atomics only, fixed size, no allocation —
// same constraints as the per-client table, for the same reason.
type siteState struct {
	window atomic.Int64
	docs   atomic.Uint32
	assets atomic.Uint32

	// baseline is the best asset-to-document ratio this install has been
	// observed running at, in parts per thousand so it fits an atomic integer.
	// It only ever rises: it is a record of what this site is capable of, not a
	// moving average that a long enough attack would drag down to meet itself.
	// An attacker who can lower the baseline can disable the detector, and a
	// decaying average hands them exactly that lever.
	baselinePerMille atomic.Uint32

	// sweeping is the published verdict for the window that just closed, so
	// readers never see a ratio computed from a partly-filled window.
	sweeping atomic.Bool
}

// observeSite records one request into the population counters and rolls the
// window when it expires.
func (t *Tracker) observeSite(now int64, asset bool) {
	win := now - now%sweepWindowSec
	if old := t.site.window.Load(); old != win {
		if t.site.window.CompareAndSwap(old, win) {
			t.closeSiteWindow()
		}
	}
	if asset {
		t.site.assets.Add(1)
	} else {
		t.site.docs.Add(1)
	}
}

// closeSiteWindow evaluates the window that just ended, updates the baseline,
// publishes the verdict and resets the counters.
func (t *Tracker) closeSiteWindow() {
	docs := t.site.docs.Swap(0)
	assets := t.site.assets.Swap(0)

	if docs < minSiteSample {
		// Too little to say anything. Deliberately NOT clearing the verdict:
		// a quiet five minutes in the middle of a sweep is not the end of it,
		// and flapping the sample floor is worse than holding it.
		return
	}

	ratio := float64(assets) / float64(docs)
	perMille := uint32(ratio * 1000)

	// The baseline only climbs.
	for {
		old := t.site.baselinePerMille.Load()
		if perMille <= old {
			break
		}
		if t.site.baselinePerMille.CompareAndSwap(old, perMille) {
			break
		}
	}

	base := float64(t.site.baselinePerMille.Load()) / 1000
	if base < minBaselineAssetRatio {
		// This install has never demonstrated a healthy ratio, so there is no
		// collapse to detect and no honest verdict to publish.
		t.site.sweeping.Store(false)
		return
	}
	t.site.sweeping.Store(ratio < base*collapseFraction)
}

// Sweeping reports whether the population currently looks like a corpus sweep
// rather than an audience.
//
// It is a report, not a verdict. Nothing is refused because this is true.
func (t *Tracker) Sweeping() bool {
	if t == nil {
		return false
	}
	return t.site.sweeping.Load()
}

// SiteBaselineAssetRatio exposes the healthy ratio this install has been seen
// running at, so the panel can show whether the detector is armed at all rather
// than claiming a protection that is dormant.
func (t *Tracker) SiteBaselineAssetRatio() float64 {
	if t == nil {
		return 0
	}
	return float64(t.site.baselinePerMille.Load()) / 1000
}

// sampleFloor is the number of requests a window needs before the
// document-without-assets signal is allowed to speak.
func (t *Tracker) sampleFloor() int {
	if t.Sweeping() {
		return sweepSampleFloor
	}
	return minSample
}
