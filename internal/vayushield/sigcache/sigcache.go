// SPDX-License-Identifier: Apache-2.0

// Package sigcache is VayuShield's Aegis L6: a fixed-memory, sharded, negative-
// caching layer in front of the adaptive signature database's per-request
// Lookup SELECT. Under a distributed swarm each unproven request that reaches
// classification would otherwise hit SQLite once; this keeps the overwhelming-
// ly common case (a fingerprint the DB has never seen — legitimate readers and
// flash crowds are ~all misses) off the writer-adjacent read path.
//
// Design (mirrors internal/vayushield/brain): 256 shards, per-shard mutex, FNV
// keying, lazy TTL/age eviction on access (no sweeper goroutine), a hard
// per-shard cap so a 1M-distinct-fingerprint flood can never grow it unbounded.
// It caches ONLY the Lookup result (a value snapshot of the stored signature
// plus a found flag) keyed on the fingerprint hash — never an IP, never the
// full Verdict (which also depends on per-request inputs the hash does not
// capture). A short TTL bounds staleness so a freshly-learned/pardoned
// signature takes effect quickly; operator actions call Forget for instant
// invalidation.
package sigcache

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

const (
	shards      = 256
	perShardCap = 1024 // <= ~256k cached fingerprints, fixed memory
)

type entry struct {
	sig     botdb.StoredSignature // valid only when found
	found   bool
	expires time.Time
}

type shard struct {
	mu sync.Mutex
	m  map[string]entry
}

// Cache is the L6 signature-lookup cache. Safe for concurrent use.
type Cache struct {
	shards [shards]shard
	ttl    time.Duration
	now    func() time.Time

	hits   atomic.Int64
	misses atomic.Int64 // cache misses = fell through to SQLite (also the fresh-lookup rate)
}

// New builds a cache with the given entry TTL (a short window, e.g. 45s, keeps
// staleness bounded). A non-positive ttl disables caching (every Get misses).
func New(ttl time.Duration) *Cache {
	c := &Cache{ttl: ttl, now: time.Now}
	for i := range c.shards {
		c.shards[i].m = make(map[string]entry)
	}
	return c
}

func (c *Cache) shard(key string) *shard {
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return &c.shards[h%shards]
}

// Get returns the cached Lookup result for a fingerprint hash. hit reports
// whether the cache answered (fresh entry present); when hit is true, found
// reports whether the DB had a row and sig is a copy of it. On a miss the
// caller performs the real SELECT and calls Put.
func (c *Cache) Get(fph string) (sig *botdb.StoredSignature, found, hit bool) {
	if c == nil || c.ttl <= 0 || fph == "" {
		return nil, false, false
	}
	s := c.shard(fph)
	now := c.now()
	s.mu.Lock()
	e, ok := s.m[fph]
	if ok && now.After(e.expires) {
		delete(s.m, fph)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		c.misses.Add(1)
		return nil, false, false
	}
	c.hits.Add(1)
	if !e.found {
		return nil, false, true
	}
	cp := e.sig // copy out under no shared aliasing
	return &cp, true, true
}

// Put records a Lookup outcome. found=false caches the negative result (the
// fingerprint is unknown to the DB) — this is the load-bearing case, since a
// swarm/flash-crowd is almost all DB misses. sig may be nil when found=false.
func (c *Cache) Put(fph string, sig *botdb.StoredSignature, found bool) {
	if c == nil || c.ttl <= 0 || fph == "" {
		return
	}
	s := c.shard(fph)
	now := c.now()
	e := entry{found: found, expires: now.Add(c.ttl)}
	if found && sig != nil {
		e.sig = *sig
	} else {
		e.found = false
	}
	s.mu.Lock()
	if _, exists := s.m[fph]; !exists && len(s.m) >= perShardCap {
		c.evictLocked(s, now)
	}
	s.m[fph] = e
	s.mu.Unlock()
}

// Forget drops a fingerprint immediately, so an operator verify/dismiss/false-
// positive (which flips the verdict) takes effect at once rather than after the
// TTL — a stale block of a just-pardoned client is the worst outcome.
func (c *Cache) Forget(fph string) {
	if c == nil || fph == "" {
		return
	}
	s := c.shard(fph)
	s.mu.Lock()
	delete(s.m, fph)
	s.mu.Unlock()
}

// evictLocked frees room by first dropping expired entries, then, if still full,
// an arbitrary fraction to guarantee progress. Caller holds the shard lock.
func (c *Cache) evictLocked(s *shard, now time.Time) {
	for k, e := range s.m {
		if now.After(e.expires) {
			delete(s.m, k)
		}
	}
	if len(s.m) < perShardCap {
		return
	}
	n := 0
	for k := range s.m {
		delete(s.m, k)
		if n++; n >= perShardCap/8+1 {
			return
		}
	}
}

// Hits / Misses expose telemetry. Misses double as the "fresh fingerprint /
// SQLite lookup" rate, which the surge controller reads to detect a high-
// cardinality (low-and-slow, distinct-IP) swarm that never spikes global RPS.
func (c *Cache) Hits() int64   { return c.hits.Load() }
func (c *Cache) Misses() int64 { return c.misses.Load() }

// Clear drops every cached entry. Called when an operator action (verify /
// dismiss / report-false-positive) may have flipped any verdict, so the cache
// cannot serve a stale classification even for a moment.
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	for i := range c.shards {
		c.shards[i].mu.Lock()
		c.shards[i].m = make(map[string]entry)
		c.shards[i].mu.Unlock()
	}
}

// Len returns the number of cached entries (telemetry/tests).
func (c *Cache) Len() int {
	n := 0
	for i := range c.shards {
		c.shards[i].mu.Lock()
		n += len(c.shards[i].m)
		c.shards[i].mu.Unlock()
	}
	return n
}
