// Package resilience is VayuShield's Tier-1 (in-binary, application-layer)
// availability defense: the cheap, O(1)-per-request primitives that let a single
// VayuPress process shed abusive load before it reaches expensive work (TLS
// fingerprinting, template rendering, SQLite).
//
// Everything here is designed to be *fast on the happy path* so it never slows
// legitimate traffic:
//
//   - Limiter        — sharded token-bucket rate limiter (per-IP / per-subnet).
//     256 shards keep lock contention negligible; buckets are
//     lazily refilled and idle ones are evicted so memory stays
//     bounded.
//   - InFlight       — a lock-free concurrency gate for load shedding: when the
//     server is saturated, extra requests get a cheap 503
//     instead of piling onto the work queue.
//   - Blocklist      — a TTL in-memory jail (auto-expiring) for IPs that grossly
//     abuse the limits; checked first as an O(1) gate.
//   - Controller     — a lock-free rolling requests-per-second meter that flags
//     an "under attack" condition so the middleware can tighten
//     thresholds automatically and relax them when it passes.
//
// None of these block a normal visitor at normal load: the limiter's burst is
// generous, load shedding only trips at genuine saturation, the blocklist is
// opt-in with short TTLs, and good bots / verified sessions are exempted by the
// caller before any of this runs.
package resilience

import (
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// ── Token-bucket rate limiter ─────────────────────────────────────────────────

const limiterShards = 256

type bucket struct {
	tokens float64
	last   time.Time
}

type limiterShard struct {
	mu sync.Mutex
	m  map[string]*bucket
}

// Limiter is a sharded token-bucket rate limiter keyed by an arbitrary string
// (typically a client IP or /24 subnet). Rate and burst can be updated live.
// Safe for concurrent use.
type Limiter struct {
	shards     [limiterShards]limiterShard
	rateBits   atomic.Uint64 // float64 tokens/sec
	burstBits  atomic.Uint64 // float64 max tokens
	maxPerShrd int
}

// NewLimiter builds a limiter allowing ratePerSec sustained requests with a
// burst ceiling. maxKeys bounds total tracked keys (evenly across shards).
func NewLimiter(ratePerSec, burst float64, maxKeys int) *Limiter {
	l := &Limiter{}
	for i := range l.shards {
		l.shards[i].m = make(map[string]*bucket)
	}
	l.SetRate(ratePerSec, burst)
	if maxKeys < limiterShards {
		maxKeys = limiterShards * 16
	}
	l.maxPerShrd = maxKeys / limiterShards
	return l
}

// SetRate updates the sustained rate and burst live (lock-free readers).
func (l *Limiter) SetRate(ratePerSec, burst float64) {
	if ratePerSec < 0 {
		ratePerSec = 0
	}
	if burst < 1 {
		burst = 1
	}
	l.rateBits.Store(math.Float64bits(ratePerSec))
	l.burstBits.Store(math.Float64bits(burst))
}

func (l *Limiter) rate() (float64, float64) {
	return math.Float64frombits(l.rateBits.Load()), math.Float64frombits(l.burstBits.Load())
}

func shardIndex(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % limiterShards
}

// Allow reports whether a request from key may proceed, consuming one token.
// A key seen for the first time starts with a full burst, so a normal visitor
// is never rate-limited until they exceed the burst.
func (l *Limiter) Allow(key string) bool {
	rate, burst := l.rate()
	now := time.Now()
	s := &l.shards[shardIndex(key)]
	s.mu.Lock()
	defer s.mu.Unlock()

	b := s.m[key]
	if b == nil {
		if len(s.m) >= l.maxPerShrd {
			l.evictLocked(s, burst)
		}
		s.m[key] = &bucket{tokens: burst - 1, last: now}
		return true
	}
	// Lazy refill.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(burst, b.tokens+elapsed*rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// evictLocked drops idle (fully-refilled) buckets to keep the shard bounded.
// Caller holds the shard lock.
func (l *Limiter) evictLocked(s *limiterShard, burst float64) {
	for k, b := range s.m {
		if b.tokens >= burst-0.001 {
			delete(s.m, k)
		}
	}
	// If still full of active buckets, drop an arbitrary few to guarantee progress.
	if len(s.m) >= l.maxPerShrd {
		n := 0
		for k := range s.m {
			delete(s.m, k)
			if n++; n >= l.maxPerShrd/8+1 {
				break
			}
		}
	}
}

// ── In-flight concurrency gate (load shedding) ────────────────────────────────

// InFlight caps concurrent in-flight requests. When the cap is exceeded, Acquire
// fails and the caller sheds the request with a cheap 503, protecting the server
// from collapse under an L7 flood. A max <= 0 disables the gate (always admits).
type InFlight struct {
	n   atomic.Int64
	max atomic.Int64
}

// SetMax updates the concurrency ceiling live.
func (f *InFlight) SetMax(m int64) { f.max.Store(m) }

// Acquire admits a request if under the cap. Pair every true with Release.
func (f *InFlight) Acquire() bool {
	max := f.max.Load()
	if max <= 0 {
		return true
	}
	if f.n.Add(1) > max {
		f.n.Add(-1)
		return false
	}
	return true
}

// Release frees an admitted slot.
func (f *InFlight) Release() {
	if f.n.Load() > 0 {
		f.n.Add(-1)
	}
}

// Current returns the number of in-flight requests.
func (f *InFlight) Current() int64 { return f.n.Load() }

// ── TTL blocklist ─────────────────────────────────────────────────────────────

const blocklistShards = 64

type blocklistShard struct {
	mu sync.Mutex
	m  map[string]time.Time // key -> expiry
}

// Blocklist is an auto-expiring in-memory jail. Entries are checked in O(1) and
// removed lazily on expiry, so it never needs a sweeper goroutine.
type Blocklist struct {
	shards [blocklistShards]blocklistShard
}

// NewBlocklist creates an empty blocklist.
func NewBlocklist() *Blocklist {
	b := &Blocklist{}
	for i := range b.shards {
		b.shards[i].m = make(map[string]time.Time)
	}
	return b
}

func (b *Blocklist) shard(key string) *blocklistShard {
	return &b.shards[shardIndex(key)%blocklistShards]
}

// Block jails key until now+ttl (extending any existing entry).
func (b *Blocklist) Block(key string, ttl time.Duration) {
	if key == "" || ttl <= 0 {
		return
	}
	s := b.shard(key)
	until := time.Now().Add(ttl)
	s.mu.Lock()
	if cur, ok := s.m[key]; !ok || until.After(cur) {
		s.m[key] = until
	}
	if len(s.m) > 20000 {
		now := time.Now()
		for k, exp := range s.m {
			if now.After(exp) {
				delete(s.m, k)
			}
		}
	}
	s.mu.Unlock()
}

// Unblock releases key from the jail immediately (a pardon). No-op when the
// key is not jailed.
func (b *Blocklist) Unblock(key string) {
	if key == "" {
		return
	}
	s := b.shard(key)
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

// Blocked reports whether key is currently jailed (expired entries are dropped).
func (b *Blocklist) Blocked(key string) bool {
	if key == "" {
		return false
	}
	s := b.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[key]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.m, key)
		return false
	}
	return true
}

// Len returns the number of jailed keys (for metrics/tests).
func (b *Blocklist) Len() int {
	n := 0
	for i := range b.shards {
		b.shards[i].mu.Lock()
		n += len(b.shards[i].m)
		b.shards[i].mu.Unlock()
	}
	return n
}

// ── Under-attack RPS controller ───────────────────────────────────────────────

// Controller is a lock-free rolling requests-per-second meter. Observe is called
// once per request; UnderAttack reports whether the current or immediately
// previous second exceeded the threshold, so the middleware can auto-tighten
// challenge thresholds during a flood and relax them the moment it subsides.
type Controller struct {
	sec       atomic.Int64 // unix second of the current window
	count     atomic.Int64 // requests in the current second so far
	lastRPS   atomic.Int64 // requests in the previous full second
	threshold atomic.Int64 // requests/sec that constitutes an attack (0 = disabled)
}

// SetThreshold sets the attack RPS threshold live (0 disables detection).
func (c *Controller) SetThreshold(rps int64) { c.threshold.Store(rps) }

// Observe records one request and rolls the per-second window.
func (c *Controller) Observe() {
	now := time.Now().Unix()
	if s := c.sec.Load(); s != now {
		if c.sec.CompareAndSwap(s, now) {
			c.lastRPS.Store(c.count.Swap(0))
		}
	}
	c.count.Add(1)
}

// UnderAttack reports whether traffic currently exceeds the configured RPS.
func (c *Controller) UnderAttack() bool {
	t := c.threshold.Load()
	if t <= 0 {
		return false
	}
	return c.count.Load() > t || c.lastRPS.Load() > t
}

// RPS returns the most recent full-second request count (for the dashboard).
func (c *Controller) RPS() int64 {
	// Prefer the current second if it already exceeds the last full second.
	if cur := c.count.Load(); cur > c.lastRPS.Load() {
		return cur
	}
	return c.lastRPS.Load()
}
