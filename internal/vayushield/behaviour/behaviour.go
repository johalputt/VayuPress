// SPDX-License-Identifier: Apache-2.0

// Package behaviour scores a client by what it DOES rather than by what it
// claims to be.
//
// Every other signal the shield has is either transport-derived — and therefore
// unavailable behind a TLS-terminating proxy, which is how essentially every
// install runs — or taken from headers the client fully controls. The practical
// consequence is that a scraper which simply sets a Chrome User-Agent scores
// 0.15 and is classified as a human.
//
// Behaviour is different in kind: a client can lie about its User-Agent for
// free, and it cannot lie about the sequence of requests it makes without
// actually behaving like a browser. Fetching the stylesheets and images a page
// references, spreading requests over time, and not walking a hundred distinct
// paths a minute are things that cost a scraper the very efficiency it exists
// for.
//
// # What this deliberately is not
//
// It is not a blocker. Every signal here is a heuristic over a small, lossy
// sketch, and any of them can be wrong about an individual: a feed reader
// legitimately fetches no assets, a broken link legitimately produces 404s, and
// a single-page app legitimately fetches one document. So this contributes to
// the SCORE and never reaches a verdict on its own — the score still has to
// cross the operator's own threshold, and a challenge is still solvable.
//
// # Cost
//
// Modelled on the L2 pre-filter: a fixed table sized at construction, atomics
// only, no locks, and no allocation on the request path. It runs on every
// unverified request, so anything else would be a denial of service wearing the
// costume of a defence.
package behaviour

import (
	"math/bits"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// slots is a power of two so the index is a mask rather than a modulo.
	// 16384 slots x ~48 bytes is ~800 KiB fixed — the same order of budget as
	// the L2 sketch, and a fixed cost rather than one an attacker can grow.
	// Raised from 4096 (2025 audit): at >4k active clients per minute the old
	// table's birthday collisions started indicting colliding readers, and
	// CGNAT subscribers legitimately share one slot.
	slots     = 16384
	slotMask  = slots - 1
	windowSec = 60
)

// slot is one client's counters for one window. Collisions are accepted:
// two clients sharing a slot blend their behaviour, which costs accuracy and
// never costs memory. That trade is the whole point of a sketch — the
// alternative is a map an attacker chooses the size of.
type slot struct {
	key      atomic.Uint64 // client key hash; 0 means never used
	window   atomic.Int64  // unix window start
	requests atomic.Uint32
	notFound atomic.Uint32
	assets   atomic.Uint32 // sub-resources: css, js, images, fonts
	docs     atomic.Uint32 // navigable documents
	// paths is a 64-bucket presence bitmap over path hashes. Its popcount is a
	// cheap distinct-path estimate: exact counting would need a set per client,
	// which is memory an attacker sizes. Saturating at 64 is fine — everything
	// above "many" reads the same.
	paths atomic.Uint64
}

// Tracker is the fixed-memory behavioural sketch.
type Tracker struct {
	tab  [slots]slot
	site siteState
	now  func() time.Time
}

// New builds an empty tracker.
func New() *Tracker { return &Tracker{now: time.Now} }

// Signals is what one observation reports back.
type Signals struct {
	Requests  int // in the current window
	NotFound  int
	Assets    int
	Docs      int
	Paths     int  // distinct-path estimate, saturating at 64
	Sampled   bool // false when there is not yet enough to say anything
	SlotReset bool // the window rolled on this observation
	// DocFloor is the sample size the document-without-assets signal needs.
	// It is normally minSample and drops while the population looks like a
	// corpus sweep — see sweep.go. Carried on the observation rather than read
	// from the tracker inside Score so that a Signals value stays a complete,
	// self-contained record of what was true when it was taken.
	DocFloor int
}

// Observe records one request and returns the client's behaviour so far in this
// window. key is the caller's enforcement key; status is the response status
// when known, or 0 before the handler has run.
func (t *Tracker) Observe(key string, path string, status int) Signals {
	if t == nil || key == "" {
		return Signals{}
	}
	h := hash64(key)
	if h == 0 {
		h = 1 // 0 marks an unused slot
	}
	s := &t.tab[h&slotMask]

	now := t.now().Unix()
	win := now - now%windowSec

	var reset bool
	// Claim or roll the slot. A CAS loser simply proceeds — worst case one
	// observation lands in the window that is being replaced, which is a rounding
	// error in a sketch and not worth a lock on the request path.
	if s.key.Load() != h {
		s.key.Store(h)
		s.window.Store(win)
		s.requests.Store(0)
		s.notFound.Store(0)
		s.assets.Store(0)
		s.docs.Store(0)
		s.paths.Store(0)
		reset = true
	} else if old := s.window.Load(); old != win {
		if s.window.CompareAndSwap(old, win) {
			s.requests.Store(0)
			s.notFound.Store(0)
			s.assets.Store(0)
			s.docs.Store(0)
			s.paths.Store(0)
			reset = true
		}
	}

	n := s.requests.Add(1)
	if status == http.StatusNotFound {
		s.notFound.Add(1)
	}
	asset := isAsset(path)
	if asset {
		s.assets.Add(1)
	} else {
		s.docs.Add(1)
	}
	t.observeSite(now, asset)
	s.paths.Or(1 << (hash64(path) & 63))

	return Signals{
		Requests:  int(n),
		NotFound:  int(s.notFound.Load()),
		Assets:    int(s.assets.Load()),
		Docs:      int(s.docs.Load()),
		Paths:     bits.OnesCount64(s.paths.Load()),
		Sampled:   n >= minSample,
		SlotReset: reset,
		DocFloor:  t.sampleFloor(),
	}
}

// minSample is how many requests a window needs before its ratios mean
// anything. Below it every ratio is dominated by its first observation — one
// 404 out of one request is a 100% error rate that says nothing at all.
const minSample = 8

// Score converts observed behaviour into a bounded score delta, and the reasons
// behind it.
//
// The bound is the honest part. These are heuristics over a lossy sketch, and
// each of them has a legitimate client that trips it: a feed reader fetches no
// assets, a site with a broken link produces 404s, a single-page app fetches one
// document. So the total is clamped well below the distance between "unknown"
// and the block threshold — behaviour can push a client into a challenge, and
// cannot by itself push one into a block.
func (s Signals) Score() (delta float64, reasons []string) {
	// The document-without-assets signal is evaluated FIRST and outside the
	// Sampled gate, because during a sweep its whole job is to speak before the
	// standard sample size is reached — a client that will only ever make three
	// requests never reaches eight, which is the gap sweep.go exists to close.
	//
	// Every other signal here still waits for the full sample. Lowering the bar
	// for all of them would let a 404 ratio fire on two requests, and one 404
	// out of two is not an error rate, it is a coincidence.
	docFloor := s.DocFloor
	if docFloor <= 0 {
		docFloor = minSample
	}
	// Path diversity scales with the floor rather than sitting at a constant.
	// At the normal floor this is the original >= 4. At the sweep floor it
	// becomes "every document was a different one", which is what separates a
	// crawler from a reader hammering reload — the case an earlier version of
	// this signal got wrong and a test still pins.
	wantPaths := 4
	if docFloor < wantPaths {
		wantPaths = docFloor
	}
	if s.Docs >= docFloor && s.Assets == 0 && s.Paths >= wantPaths {
		delta += 0.2
		reasons = append(reasons, "fetches documents but never their sub-resources")
	}

	if !s.Sampled {
		if delta > MaxDelta {
			delta = MaxDelta
		}
		return delta, reasons
	}
	req := float64(s.Requests)

	// A high 404 ratio over a real sample is path scanning: a browser follows
	// links that exist, a scanner guesses paths that mostly do not.
	if ratio := float64(s.NotFound) / req; ratio > 0.3 {
		delta += 0.2
		reasons = append(reasons, "high 404 ratio")
	}

	// (Documents without sub-resources is scored above, before the Sampled gate,
	// because during a sweep it has to speak below the standard sample size.)

	// Many distinct paths in one minute is crawling. A reader moves between a
	// handful of pages; the estimate saturates at 64, so this fires only well
	// beyond any plausible human session.
	if s.Paths >= 24 && req >= 24 {
		delta += 0.15
		reasons = append(reasons, "many distinct paths in one minute")
	}

	if delta > MaxDelta {
		delta = MaxDelta
	}
	return delta, reasons
}

// MaxDelta bounds this package's whole contribution to a score.
//
// With the shipped defaults an unknown client starts at 0.25 and blocks at 0.8,
// so behaviour can move a client into a solvable challenge and cannot, on its
// own, move one into a block. That is by construction rather than by the current
// numbers happening to work out, and a test pins it.
//
// It is one budget rather than one per signal family. An earlier version also
// scored header coherence and clamped each half separately, which let a client
// accumulate 0.65 across both and reach a hard block on heuristics alone — two
// bounded things are not a bounded thing.
const MaxDelta = 0.35

// ── Why header coherence is not here ─────────────────────────────────────────
//
// The obvious companion signal is header coherence: a browser User-Agent that
// arrives with no Accept-Language, or with `Accept: */*` instead of a real media
// list, is a strong tell for a tool that set only the UA. It was built, tested,
// and then deliberately not shipped, for two reasons worth recording so it is
// not re-added without answering them.
//
// The false positives land on exactly the wrong people. Privacy tooling strips
// Accept-Language ON PURPOSE, because it is a fingerprinting surface — so
// penalising its absence means penalising the readers who care most about not
// being fingerprinted, on a product that ships a Tor Space.
//
// And it reclassified requests this repository's own test corpus treats as real
// browsers. That is evidence about the signal, not about the fixtures: a
// heuristic that moves a request the project already considers legitimate is a
// heuristic whose threshold is wrong. Behavioural signals need eight requests
// before they say anything; a header signal fires on the first one, which is the
// worst moment to be wrong about a reader.

// isAsset reports whether a path looks like a sub-resource rather than a
// navigable document. Extension-based on purpose: it runs on every request, and
// the alternative — asking the router — would couple this package to the app.
func isAsset(path string) bool {
	i := strings.LastIndexByte(path, '.')
	if i < 0 || i == len(path)-1 {
		return false
	}
	switch strings.ToLower(path[i+1:]) {
	case "css", "js", "mjs", "png", "jpg", "jpeg", "gif", "webp", "avif", "svg",
		"ico", "woff", "woff2", "ttf", "otf", "map":
		return true
	}
	return false
}

// hash64 is FNV-1a. Not cryptographic and not required to be: it indexes a
// sketch, and the worst an adversary achieves by forcing a collision is sharing
// a slot with someone else's behaviour.
func hash64(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
