// Package prefilter is VayuShield's Aegis L2: a probabilistic, fixed-memory
// pre-filter that identifies and fair-sheds heavy hitters during an attack —
// BEFORE the per-IP rate-limiter map, classification, rendering or SQLite are
// touched.
//
// Why it exists: the sharded token-bucket limiter (Tier 1) keys on exact IPs,
// so a spoofed or botnet flood with millions of distinct source addresses
// thrashes its bounded map, and a single hot IP still costs a map lookup +
// shard lock per request. The pre-filter replaces per-key state with a
// windowed Count-Min Sketch: 256 KiB of atomic counters — FIXED memory no
// matter how many attackers there are — giving an (over-)estimate of each
// key's request count in the last ~2 windows with zero allocation and no
// locks on the hot path.
//
// Fairness model (never harms real users): every client gets a fair per-window
// budget. At or under budget the shed probability is exactly 0 — a normal
// reader, a search-engine crawler or an AI assistant fetching a handful of
// pages can NEVER be shed here, even during an attack. Above budget, the shed
// probability ramps as 1 − budget/estimate, so the heaviest sources shed the
// most of their own traffic while everyone else passes untouched. Shedding
// only happens at all while the caller reports genuine pressure (the global
// attack meter tripped, or the L0 sovereignty lane near saturation); in calm
// conditions the filter only observes.
//
// Both the exact key (IP) and its network group (/24 or /48) are tracked, so a
// botnet spreading load across one subnet is caught at the group level even
// when each individual address stays under the per-IP budget.
package prefilter

import (
	"math/rand/v2"
	"net/netip"
	"sync/atomic"
	"time"
)

const (
	depth = 4    // sketch rows (independent hashes)
	width = 8192 // counters per row; power of two (mask below)
	mask  = width - 1

	// windowSec is the sliding-window granularity. Estimates cover the current
	// plus the previous window (~10–20 s of history) so a burst decays quickly
	// once an attack stops.
	windowSec = 10

	// defaultFairShare is the per-IP request budget per window below which the
	// shed probability is exactly zero (~6 req/s sustained — far above a human
	// reading pages, low enough to bite on a flood source).
	defaultFairShare = 60

	// subnetFactor multiplies the fair share for the subnet-level check, since
	// many legitimate users can share one NAT/carrier subnet.
	subnetFactor = 8

	// maxShed caps the shed probability so even the worst offender's requests
	// trickle through — the classifier downstream still sees (and learns from)
	// the traffic, and a false positive self-heals instead of hard-failing.
	maxShed = 0.98
)

// Prefilter is the Aegis L2 engine. Zero value is not usable; call New.
// Memory: 2 epochs × 4 rows × 8192 × 4 B = 256 KiB, fixed at construction.
type Prefilter struct {
	epochs [2][depth * width]atomic.Uint32
	epoch  atomic.Int64 // current window number (unix / windowSec)

	fairShare atomic.Int64 // per-key budget per window; 0 = default

	globalCur  atomic.Int64 // requests observed this window
	globalPrev atomic.Int64 // requests in the previous full window
	shed       atomic.Int64 // cumulative fair-shed count (telemetry)

	now func() time.Time // injectable clock (tests)
}

// New builds a pre-filter with the default fair share.
func New() *Prefilter {
	return &Prefilter{now: time.Now}
}

// SetFairShare live-tunes the per-key budget per window. n <= 0 restores the
// default. The subnet budget is always subnetFactor× this value.
func (p *Prefilter) SetFairShare(n int) {
	if n <= 0 {
		p.fairShare.Store(0)
		return
	}
	p.fairShare.Store(int64(n))
}

func (p *Prefilter) share() int64 {
	if v := p.fairShare.Load(); v > 0 {
		return v
	}
	return defaultFairShare
}

// Shed returns the cumulative number of fair-shed requests (telemetry).
func (p *Prefilter) Shed() int64 { return p.shed.Load() }

// WindowRate returns the request count of the busiest recent window (telemetry).
func (p *Prefilter) WindowRate() int64 {
	if c := p.globalCur.Load(); c > p.globalPrev.Load() {
		return c
	}
	return p.globalPrev.Load()
}

// Check observes one request from ip and decides whether to shed it.
//
// It ALWAYS records the observation (a handful of atomic increments — the
// sketch must stay warm so the moment pressure appears the heavy hitters are
// already known). It returns true (shed) only when pressure is true AND the
// key or its subnet is over its fair budget, with probability proportional to
// the excess. pressure=false is therefore a pure observation pass.
func (p *Prefilter) Check(ip string, pressure bool) bool {
	p.rotate()
	p.globalCur.Add(1)

	kh := hash64(ip)
	sh := subnetHash(ip)
	e := int(p.epoch.Load()) // single load so cur/prev stay a consistent pair
	cur := &p.epochs[e&1]
	prev := &p.epochs[(e+1)&1]

	kEst := bumpAndEstimate(cur, prev, kh)
	sEst := bumpAndEstimate(cur, prev, sh)

	if !pressure {
		return false
	}
	fs := p.share()
	prob := shedProb(int64(kEst), fs)
	if sp := shedProb(int64(sEst), fs*subnetFactor); sp > prob {
		prob = sp
	}
	if prob <= 0 {
		return false
	}
	if rand.Float64() < prob {
		p.shed.Add(1)
		return true
	}
	return false
}

// shedProb ramps from 0 at/under the budget to maxShed for extreme excess:
// est = 2×budget sheds ~50%, 10×budget sheds ~90%.
func shedProb(est, budget int64) float64 {
	if est <= budget || budget <= 0 {
		return 0
	}
	pr := 1 - float64(budget)/float64(est)
	if pr > maxShed {
		return maxShed
	}
	return pr
}

// rotate advances the sliding window. The CAS winner clears the buffer that
// becomes the new current epoch; the other buffer keeps the previous window's
// counts for smoothed estimates. Concurrent writers racing the clear can lose
// an increment or two — an acceptable undercount for a probabilistic filter.
func (p *Prefilter) rotate() {
	e := p.now().Unix() / windowSec
	old := p.epoch.Load()
	if old == e {
		return
	}
	if !p.epoch.CompareAndSwap(old, e) {
		return
	}
	buf := &p.epochs[int(e)&1]
	for i := range buf {
		buf[i].Store(0)
	}
	p.globalPrev.Store(p.globalCur.Swap(0))
}

// bumpAndEstimate increments the key's counter in every row of the current
// sketch and returns the windowed min-estimate (current + previous window).
// Count-Min guarantees estimate >= true count (never misses a heavy hitter);
// collisions can only overestimate, and only matter under pressure, where an
// unlucky light key still passes with probability budget/estimate.
func bumpAndEstimate(cur, prev *[depth * width]atomic.Uint32, h uint64) uint32 {
	h2 := h>>32 | h<<32
	est := ^uint32(0)
	for i := 0; i < depth; i++ {
		// Kirsch–Mitzenmacher: row i uses h + i*h2 — one hash, d row positions.
		idx := i*width + int((h+uint64(i)*h2)&mask)
		c := cur[idx].Add(1)
		if v := c + prev[idx].Load(); v < est {
			est = v
		}
	}
	return est
}

const (
	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
)

// hash64 is FNV-1a 64 inlined over the key bytes (no allocation).
func hash64(s string) uint64 {
	h := fnvOffset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}
	return h
}

// subnetHash maps an IP to the sketch key for its network group — /24 for IPv4,
// /48 for IPv6, the granularity at which botnets and cloud scrapers cluster —
// and hashes it directly from the address bytes with a distinguishing group tag.
// This is the allocation-free replacement for hash64(subnetOf(ip)): it runs on
// EVERY non-verified request, so building a "g/1.2.3.0/24" string and re-hashing
// it per request was pure GC pressure on the hot path. The 'g' tag guarantees a
// subnet key can never collide with the exact-IP key hash64(ip). Unparseable
// input groups under a tagged hash of itself (still a bounded sketch key).
func subnetHash(ip string) uint64 {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		h := fnvOffset
		h ^= 'g'
		h *= fnvPrime
		for i := 0; i < len(ip); i++ {
			h ^= uint64(ip[i])
			h *= fnvPrime
		}
		return h
	}
	h := fnvOffset
	h ^= 'g'
	h *= fnvPrime
	if addr.Is4() || addr.Is4In6() {
		a := addr.As4() // stack array, no heap alloc
		h ^= 4          // family tag
		h *= fnvPrime
		for i := 0; i < 3; i++ { // /24 = first 3 octets
			h ^= uint64(a[i])
			h *= fnvPrime
		}
		return h
	}
	a := addr.As16()
	h ^= 6 // family tag
	h *= fnvPrime
	for i := 0; i < 6; i++ { // /48 = first 6 bytes
		h ^= uint64(a[i])
		h *= fnvPrime
	}
	return h
}
