// Package verifiedbot authenticates legitimate search-engine and AI crawlers by
// IDENTITY, not by a spoofable User-Agent string, so VayuShield can fast-path
// them past every availability gate without opening a UA-spoof bypass.
//
// It answers one hot-path question — Classify(ip, ua) — cheaply and without ever
// blocking: a 256-shard verdict cache fronts everything, published-IP-range
// feeds are matched by an in-memory longest-prefix set, and forward-confirmed
// reverse DNS (for vendors with no feed, and as a backstop) runs on a bounded
// background worker pool. Feeds refresh daily and persist to a disk cache so
// verification works immediately after a restart and degrades safely (to
// UA-recognition) when the network — or a locked-down Tor Space — makes the
// feeds unreachable. It never claims certainty it does not have: an unconfirmed
// recognised crawler is reported Unverifiable (SEO-safe allow), and only a UA
// that claims a vendor whose authoritative feed excludes its IP is a SpoofSuspect.
package verifiedbot

import (
	"context"
	"hash/fnv"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// Verdict is the identity decision for a (client IP, User-Agent) pair.
type Verdict int

const (
	// Unknown: the UA is not a recognised crawler at all — normal handling.
	Unknown Verdict = iota
	// Verified: the IP is confirmed to belong to the claimed crawler vendor
	// (published-IP-range hit or a passing FCrDNS). Safe to fast-path.
	Verified
	// Unverifiable: a recognised crawler we can neither confirm nor deny right
	// now — a UA-only vendor, or a feed vendor whose feed is not yet loaded, or a
	// vendor pending an async FCrDNS check. Treated as SEO-safe "allow on UA":
	// failing to verify is our problem, and de-indexing a real crawler is worse
	// than briefly allowing a rare spoofer that async verification then downgrades.
	Unverifiable
	// SpoofSuspect: the UA claims a vendor whose authoritative feed is loaded and
	// does NOT contain this IP (and no DNS backstop confirmed it). Do not
	// fast-path; hand it to normal scoring/gates.
	SpoofSuspect
)

const (
	positiveTTL = 6 * time.Hour
	negativeTTL = 10 * time.Minute
	numShards   = 256
)

// Config configures a Verifier. Everything is optional; a zero Config yields a
// verifier that recognises crawlers by UA and reports them Unverifiable (the
// SEO-safe fallback) with no network access.
type Config struct {
	Client   *http.Client     // fetches IP-range feeds; nil disables feed loading
	CacheDir string           // disk cache for last-good feeds; "" disables persistence
	Resolver Resolver         // FCrDNS resolver; nil disables reverse-DNS verification
	Now      func() time.Time // clock (tests); nil = time.Now
	Logf     func(format string, args ...any)
	Workers  int // FCrDNS worker goroutines (default 8)
	QueueLen int // FCrDNS job queue depth (default 512)
}

// Verifier is the crawler-identity engine. Safe for concurrent use.
type Verifier struct {
	cfg      Config
	resolver Resolver
	sets     map[string]*atomicCIDRSet // feed vendor name -> loaded CIDRs
	cache    *verdictCache
	jobs     chan fcrdnsJob
	pending  sync.Map // netip.Addr -> struct{} (FCrDNS single-flight)
}

// New builds a Verifier and seeds feed vendors from the on-disk last-good cache
// so verification works immediately (and offline). Call Start to begin the
// daily refresh and the FCrDNS workers.
func New(cfg Config) *Verifier {
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.QueueLen <= 0 {
		cfg.QueueLen = 512
	}
	v := &Verifier{
		cfg:      cfg,
		resolver: cfg.Resolver,
		sets:     make(map[string]*atomicCIDRSet, len(registry)),
		cache:    newVerdictCache(),
		jobs:     make(chan fcrdnsJob, cfg.QueueLen),
	}
	for i := range registry {
		if registry[i].tier == tierFeed {
			v.sets[registry[i].name] = &atomicCIDRSet{}
		}
	}
	v.loadFromDisk()
	return v
}

// Start launches the FCrDNS workers and the daily feed refresh (with an
// immediate first fetch). It returns at once; work runs in the background until
// done is closed. Safe to call with a nil client/resolver — the corresponding
// worker simply has nothing to do.
func (v *Verifier) Start(done <-chan struct{}) {
	for i := 0; i < v.cfg.Workers; i++ {
		go v.runFCrDNSWorker(done)
	}
	go v.refreshLoop(done)
}

func (v *Verifier) refreshLoop(done <-chan struct{}) {
	// One background context for the loop's lifetime, cancelled when the process
	// shuts down so an in-flight fetch is not left hanging.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-done
		cancel()
	}()
	v.refreshAll(ctx)
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			v.refreshAll(ctx)
		}
	}
}

// Classify is the hot-path entry point. It never performs a synchronous DNS
// lookup or feed fetch — the heaviest thing on a cache miss is one in-memory
// longest-prefix scan — so it is safe to call inline in the request middleware.
func (v *Verifier) Classify(ip netip.Addr, ua string) (Verdict, string, Class) {
	if !ip.IsValid() {
		if vd, ok := matchVendor(ua); ok {
			return Unverifiable, vd.name, vd.class // no IP to judge; SEO-safe
		}
		return Unknown, "", ""
	}
	ip = ip.Unmap()
	if e, ok := v.cache.get(ip, v.now()); ok {
		return e.verdict, e.vendor, e.class
	}
	vd, ok := matchVendor(ua)
	if !ok {
		return Unknown, "", ""
	}
	switch vd.tier {
	case tierUAOnly:
		return Unverifiable, vd.name, vd.class
	case tierFCrDNS:
		v.kickFCrDNS(ip, vd) // async; SEO-safe until it answers
		return Unverifiable, vd.name, vd.class
	case tierFeed:
		set := v.sets[vd.name]
		if !set.loaded() {
			return Unverifiable, vd.name, vd.class // no authoritative data yet
		}
		if set.load().contains(ip) {
			v.cache.put(ip, Verified, vd.name, vd.class, v.now().Add(positiveTTL))
			return Verified, vd.name, vd.class
		}
		if len(vd.ptrSuffix) > 0 {
			v.kickFCrDNS(ip, vd) // maybe a real crawler on a new IP — confirm async
			return Unverifiable, vd.name, vd.class
		}
		// Feed loaded, IP absent, no DNS backstop: a real crawler for this vendor
		// would be in the feed, so this is a spoof.
		v.cache.put(ip, SpoofSuspect, vd.name, vd.class, v.now().Add(negativeTTL))
		return SpoofSuspect, vd.name, vd.class
	}
	return Unknown, "", ""
}

func (v *Verifier) now() time.Time {
	if v.cfg.Now != nil {
		return v.cfg.Now()
	}
	return time.Now()
}

func (v *Verifier) logf(format string, args ...any) {
	if v.cfg.Logf != nil {
		v.cfg.Logf(format, args...)
	}
}

// ── sharded verdict cache ──────────────────────────────────────────────────────

type cacheEntry struct {
	verdict Verdict
	vendor  string
	class   Class
	expires time.Time
}

type cacheShard struct {
	mu sync.RWMutex
	m  map[netip.Addr]cacheEntry
}

type verdictCache struct {
	shards [numShards]*cacheShard
}

func newVerdictCache() *verdictCache {
	c := &verdictCache{}
	for i := range c.shards {
		c.shards[i] = &cacheShard{m: make(map[netip.Addr]cacheEntry)}
	}
	return c
}

func (c *verdictCache) shard(ip netip.Addr) *cacheShard {
	b := ip.As16()
	h := fnv.New32a()
	_, _ = h.Write(b[:])
	return c.shards[h.Sum32()%numShards]
}

func (c *verdictCache) get(ip netip.Addr, now time.Time) (cacheEntry, bool) {
	s := c.shard(ip)
	s.mu.RLock()
	e, ok := s.m[ip]
	s.mu.RUnlock()
	if !ok || now.After(e.expires) {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *verdictCache) put(ip netip.Addr, verdict Verdict, vendor string, class Class, expires time.Time) {
	s := c.shard(ip)
	s.mu.Lock()
	// Bound memory: a spoof flood of distinct IPs can't grow a shard without
	// limit. At the cap, drop the whole shard (correctness is unaffected — entries
	// just re-verify on next request).
	if len(s.m) >= 8192 {
		s.m = make(map[netip.Addr]cacheEntry, 1024)
	}
	s.m[ip] = cacheEntry{verdict: verdict, vendor: vendor, class: class, expires: expires}
	s.mu.Unlock()
}
