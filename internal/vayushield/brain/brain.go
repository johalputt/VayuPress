// Package brain is VayuShield's Aegis L5 v1: continuous, online, per-source
// reputation. Where the fingerprint learning database (botdb) learns WHAT a
// bot looks like over days, the brain learns WHO is currently misbehaving
// within minutes — and forgives them just as automatically.
//
// Design principles:
//
//   - Online, not batch. Every enforcement event (hard block, tarpit,
//     rate-limit breach) lowers a source's reputation immediately; every
//     positive proof (a solved challenge, sustained human-scored browsing)
//     raises it. No cron, no operator commands.
//
//   - Auto-jail with escalating sentences. When reputation collapses the
//     source is jailed. First offense is short; repeat offenders double each
//     time (capped), so a persistent attacker converges to cheap O(1)
//     rejections while a one-off mistake costs almost nothing.
//
//   - Rehabilitation and false-positive self-heal. Reputation decays toward
//     neutral over time (half-life ~1 h), release from jail restores a
//     workable score, and a solved challenge instantly redeems most of the
//     penalty — so a shared/NAT IP that hosted one bad actor is never
//     punished forever, and a mis-scored real user has multiple automatic
//     ways back in.
//
//   - Suspicion-only memory. Entries are created ONLY by negative events —
//     well-behaved visitors are never tracked at all (privacy + memory).
//     Sharded, hard-capped, and evicted by irrelevance, so memory stays
//     bounded under any flood.
package brain

import (
	"math"
	"sync"
	"time"
)

const (
	shards       = 256
	perShardCap  = 1024 // ≤ ~256k tracked sources; only suspects are tracked
	neutral      = 0.5  // reputation starts here and decays back toward here
	jailBelow    = 0.15 // reputation floor that triggers an auto-jail
	releaseScore = 0.35 // standing granted on release — behaves = quickly clean
	halfLife     = time.Hour
	baseJail     = 5 * time.Minute
	maxJail      = 6 * time.Hour
	maxOffenses  = 12 // 5m * 2^11 already exceeds maxJail; bound the shift
)

// Weights for the standard signals, expressed as reputation deltas.
const (
	SignalBlock     = -0.25 // hard block / tarpit decision
	SignalRateBurst = -0.10 // rate-limit breach
	SignalProof     = +0.40 // solved PoW/JS challenge — strong human proof
	SignalHuman     = +0.02 // sustained human-scored browsing (existing suspects only)
)

type rep struct {
	score     float64
	updated   time.Time
	jailUntil time.Time
	offenses  int
}

type shard struct {
	mu sync.Mutex
	m  map[string]*rep
}

// Brain is the L5 reputation engine. Safe for concurrent use.
type Brain struct {
	shards [shards]shard
	now    func() time.Time

	mu      sync.Mutex // guards the counters below (telemetry only)
	jails   int64
	redeems int64
}

// New builds an empty brain.
func New() *Brain {
	b := &Brain{now: time.Now}
	for i := range b.shards {
		b.shards[i].m = make(map[string]*rep)
	}
	return b
}

func (b *Brain) shard(key string) *shard {
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return &b.shards[h%shards]
}

// decayLocked drifts a score toward neutral with the configured half-life.
// Lazy — computed on access, so no sweeper goroutine is ever needed.
func decayLocked(r *rep, now time.Time) {
	dt := now.Sub(r.updated)
	if dt <= 0 {
		return
	}
	f := math.Exp2(-float64(dt) / float64(halfLife))
	r.score = neutral + (r.score-neutral)*f
	r.updated = now
}

// Observe applies a signed reputation delta to key. Negative deltas create the
// entry if needed (suspicion starts tracking); positive deltas only affect
// sources already under suspicion — the innocent are never tracked. When a
// negative observation drops the score below the jail floor, the source is
// jailed with an escalating sentence and its score is reset to the release
// standing so it starts clean when the sentence ends.
func (b *Brain) Observe(key string, delta float64) {
	if key == "" || delta == 0 {
		return
	}
	now := b.now()
	s := b.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.m[key]
	if r == nil {
		if delta > 0 {
			return // never track the well-behaved
		}
		if len(s.m) >= perShardCap {
			evictLocked(s, now)
		}
		r = &rep{score: neutral, updated: now}
		s.m[key] = r
	}
	decayLocked(r, now)
	r.score += delta
	if r.score > 1 {
		r.score = 1
	}
	if r.score < 0 {
		r.score = 0
	}
	if delta > 0 && now.Before(r.jailUntil) && r.score >= releaseScore {
		// A positive proof (solved challenge) during a sentence is an instant
		// pardon — the strongest false-positive self-heal we have.
		r.jailUntil = time.Time{}
		b.count(&b.redeems)
	}
	if delta < 0 && r.score < jailBelow && !now.Before(r.jailUntil) {
		if r.offenses < maxOffenses {
			r.offenses++
		}
		ttl := baseJail << (r.offenses - 1)
		if ttl > maxJail {
			ttl = maxJail
		}
		r.jailUntil = now.Add(ttl)
		r.score = releaseScore
		b.count(&b.jails)
	}
}

// Jailed reports whether key is currently serving a reputation sentence.
// O(1), read-mostly; the common case (unknown key) is a single map miss.
func (b *Brain) Jailed(key string) bool {
	if key == "" {
		return false
	}
	s := b.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.m[key]
	if r == nil {
		return false
	}
	return b.now().Before(r.jailUntil)
}

// Standing returns the current (decayed) reputation for key; unknown sources
// are neutral. Exposed for the dashboard and downstream layers (L4 uses it to
// pick challenge difficulty).
func (b *Brain) Standing(key string) float64 {
	if key == "" {
		return neutral
	}
	now := b.now()
	s := b.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.m[key]
	if r == nil {
		return neutral
	}
	decayLocked(r, now)
	return r.score
}

// evictLocked frees room by dropping the most irrelevant entries: expired
// sentences with near-neutral scores first, then the stalest entries.
// Caller holds the shard lock.
func evictLocked(s *shard, now time.Time) {
	for k, r := range s.m {
		if !now.Before(r.jailUntil) && math.Abs(r.score-neutral) < 0.12 {
			delete(s.m, k)
		}
	}
	if len(s.m) < perShardCap {
		return
	}
	// Still full of active entries: drop the stalest eighth to guarantee room.
	n := 0
	for k, r := range s.m {
		if now.Sub(r.updated) > halfLife {
			delete(s.m, k)
			if n++; n >= perShardCap/8 {
				return
			}
		}
	}
	for k := range s.m {
		delete(s.m, k)
		if n++; n >= perShardCap/8 {
			return
		}
	}
}

func (b *Brain) count(c *int64) {
	b.mu.Lock()
	*c++
	b.mu.Unlock()
}

// Stats is a telemetry snapshot for the dashboard.
type Stats struct {
	Tracked int   // sources currently under suspicion
	Jailed  int   // sources currently serving a sentence
	Jails   int64 // cumulative sentences handed down
	Redeems int64 // cumulative instant pardons via positive proof
}

// ReleaseAll forgets every tracked source: all sentences are lifted and all
// standing returns to neutral. It is the operator's amnesty switch — the escape
// hatch for a run of false positives (a load test from the office IP, a
// misconfigured proxy that made every reader look like one address, or a
// threshold that was set too tight for a while). Sentences are otherwise
// self-expiring but escalate up to hours, so without this an operator who has
// already fixed the cause still has to wait out punishments aimed at their own
// readers. Returns how many tracked sources were cleared.
func (b *Brain) ReleaseAll() int {
	n := 0
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		n += len(s.m)
		s.m = make(map[string]*rep)
		s.mu.Unlock()
	}
	return n
}

// Stats returns the live counters. Walks the shards; dashboard-only.
func (b *Brain) Stats() Stats {
	now := b.now()
	st := Stats{}
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		st.Tracked += len(s.m)
		for _, r := range s.m {
			if now.Before(r.jailUntil) {
				st.Jailed++
			}
		}
		s.mu.Unlock()
	}
	b.mu.Lock()
	st.Jails, st.Redeems = b.jails, b.redeems
	b.mu.Unlock()
	return st
}
