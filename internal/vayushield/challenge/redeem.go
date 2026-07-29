// SPDX-License-Identifier: Apache-2.0

package challenge

// redeem.go — one-shot redemption for a solved proof of work.
//
// VerifyPoW is stateless: it checks the HMAC, the expiry and the solution, and
// nothing records that a proof has already been spent. So a solved (salt, nonce)
// pair could be redeemed repeatedly until it expired.
//
// The interesting part is not that one client could mint several sessions for
// itself — it needs one, and each is bound to its own address, so that gains an
// attacker nothing. The hole is that the proof is bound to NOBODY before it is
// redeemed. An attacker solves once, distributes the pair to N hosts, and each
// host redeems it for a session bound to its OWN address. The cost of entry for
// an entire swarm collapses from N proofs to one.
//
// That inverts what the challenge is for. The whole design rests on making an
// unproven client pay per session; a proof that can be spent an unbounded number
// of times prices the swarm at one.
//
// The fix does not touch the PoW core, which is the strongest cryptography in
// the package. It records the proofs that have been spent, and claims them
// atomically — a check-then-set would let a burst of concurrent redemptions all
// observe "unspent" and all succeed, which is precisely the shape of the attack.

import (
	"sync"
	"time"
)

const (
	redeemShards = 16
	// redeemShardCap bounds each shard. A proof lives at most as long as the
	// challenge TTL (minutes), so this only binds under sustained redemption —
	// and the eviction that follows is the honest trade: dropping the OLDEST
	// records first means the only replays that could slip through are of proofs
	// already close to expiring anyway.
	redeemShardCap = 8192
)

type redeemShard struct {
	mu    sync.Mutex
	spent map[string]int64 // proof id -> unix expiry
}

// Redeemer records spent proofs so each can be redeemed exactly once.
//
// Sharded rather than a single mutex because, once this exists, a replay attempt
// still takes the lock — and a replay flood is free for the attacker to generate,
// unlike an honest redemption which costs a proof of work first.
type Redeemer struct {
	shards [redeemShards]*redeemShard
	now    func() time.Time
}

// NewRedeemer builds an empty redeemer.
func NewRedeemer() *Redeemer {
	r := &Redeemer{now: time.Now}
	for i := range r.shards {
		r.shards[i] = &redeemShard{spent: make(map[string]int64)}
	}
	return r
}

// shardFor picks a shard from the proof id. FNV-1a over the id: the ids are
// base64 signatures, so any cheap spread is fine and the cost has to stay well
// under the cost of the HMAC verification it accompanies.
func (r *Redeemer) shardFor(id string) *redeemShard {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return r.shards[h%redeemShards]
}

// Claim marks a proof spent and reports whether this caller is the FIRST to do
// so. A false return is a replay.
//
// expiresUnix is the proof's own expiry: the record is kept exactly that long,
// because after it a replay is refused by the expiry check anyway and holding
// the record longer would only cost memory.
func (r *Redeemer) Claim(id string, expiresUnix int64) bool {
	if r == nil || id == "" {
		// A nil redeemer means the caller did not wire one. Allowing the claim
		// keeps that a degradation rather than a lockout — but it is a real
		// degradation, which is why the Manager always constructs one.
		return true
	}
	now := r.now().Unix()
	if expiresUnix <= now {
		// Already expired; VerifyPoW rejects it regardless. Do not store it —
		// storing expired ids is how a replay flood turns into a memory leak.
		return false
	}

	s := r.shardFor(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	if exp, ok := s.spent[id]; ok && exp > now {
		return false
	}

	if len(s.spent) >= redeemShardCap {
		r.evictLocked(s, now)
	}
	s.spent[id] = expiresUnix
	return true
}

// evictLocked frees room. Expired records go first — they are pure waste. Only
// if that is not enough does it drop live ones, oldest-expiry first, so what is
// given up is the replay protection on proofs that were about to expire anyway.
// Caller holds the shard lock.
func (r *Redeemer) evictLocked(s *redeemShard, now int64) {
	for k, exp := range s.spent {
		if exp <= now {
			delete(s.spent, k)
		}
	}
	if len(s.spent) < redeemShardCap {
		return
	}
	// Still full: drop the soonest-to-expire until there is headroom.
	//
	// The threshold is the MIDPOINT of the observed expiry range, which is
	// scale-free and always sheds the earlier half. The first version added a
	// constant derived from the shard capacity — a count used as a number of
	// seconds — so with fillers two seconds out and a real proof nine minutes
	// out, the cut landed past both and evicted the one worth keeping. A test
	// caught it; the lesson is that a threshold must carry the units of the thing
	// it is thresholding.
	var lo, hi int64
	first := true
	for _, exp := range s.spent {
		if first {
			lo, hi, first = exp, exp, false
			continue
		}
		if exp < lo {
			lo = exp
		}
		if exp > hi {
			hi = exp
		}
	}
	cut := lo + (hi-lo)/2
	for k, exp := range s.spent {
		if len(s.spent) < redeemShardCap/2 {
			break
		}
		if exp <= cut {
			delete(s.spent, k)
		}
	}
}

// Len reports how many proofs are currently recorded, for tests and telemetry.
func (r *Redeemer) Len() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, s := range r.shards {
		s.mu.Lock()
		n += len(s.spent)
		s.mu.Unlock()
	}
	return n
}

// ProofID is the identity a proof is claimed under.
//
// The signature, not the salt: the salt is the server's challenge and could in
// principle be reissued, while the signature covers every field of the proof and
// is what an attacker would have to forge rather than replay. Using the salt
// would also mean two legitimately distinct proofs over the same salt collided.
func ProofID(p PoW) string { return p.Sig }
