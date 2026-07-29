// SPDX-License-Identifier: Apache-2.0

package challenge

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestAProofIsSpendableOnce is the property. VerifyPoW is stateless — HMAC,
// expiry, solution — so before this existed a solved pair could be redeemed
// until it expired.
func TestAProofIsSpendableOnce(t *testing.T) {
	r := NewRedeemer()
	exp := time.Now().Add(5 * time.Minute).Unix()

	if !r.Claim("sig-a", exp) {
		t.Fatal("the first claim on a fresh proof was refused")
	}
	if r.Claim("sig-a", exp) {
		t.Error("the same proof was redeemed twice — one solve still buys unlimited sessions")
	}
	// A different proof is unaffected.
	if !r.Claim("sig-b", exp) {
		t.Error("an unrelated proof was refused")
	}
}

// TestConcurrentRedemptionsOfOneProofYieldExactlyOne is the assertion that a
// check-then-set would fail. The attack is not a polite sequence of retries: it
// is N hosts redeeming the same distributed proof simultaneously, and a
// non-atomic claim would let all of them observe "unspent" and all succeed —
// which is the entire hole, reproduced.
func TestConcurrentRedemptionsOfOneProofYieldExactlyOne(t *testing.T) {
	r := NewRedeemer()
	exp := time.Now().Add(5 * time.Minute).Unix()

	// Many rounds in one run, deliberately. The window a check-then-set opens is
	// nanoseconds wide, so a single round catches it perhaps one run in twenty —
	// and a test that catches a race one run in twenty is a test that will be
	// believed when it passes and reverted when it flakes. Rounds turn the same
	// assertion into one that fires on essentially every execution.
	const (
		racers = 64
		rounds = 200
	)
	for round := 0; round < rounds; round++ {
		id := "proof-" + strconv.Itoa(round)
		var wg sync.WaitGroup
		var mu sync.Mutex
		won := 0
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start // release them together, so they genuinely race
				if r.Claim(id, exp) {
					mu.Lock()
					won++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()

		if won != 1 {
			t.Fatalf("round %d: %d of %d concurrent redemptions of the SAME proof succeeded, "+
				"want exactly 1 — a swarm handed one solved proof would all get in",
				round, won, racers)
		}
	}
}

// TestExpiredProofsAreNeitherClaimedNorStored — an expired proof is refused by
// VerifyPoW anyway, so recording it buys nothing and a flood of expired replays
// would turn the redeemer into a memory leak fed by an attacker for free.
func TestExpiredProofsAreNeitherClaimedNorStored(t *testing.T) {
	r := NewRedeemer()
	past := time.Now().Add(-time.Minute).Unix()

	if r.Claim("stale", past) {
		t.Error("an already-expired proof was accepted")
	}
	if n := r.Len(); n != 0 {
		t.Errorf("%d expired records were stored — an attacker can grow this for free", n)
	}
}

// TestMemoryIsBounded — the records are attacker-influenced: every honest
// redemption adds one, and under a surge there are many. Unbounded growth here
// would make the anti-replay fix its own denial of service.
func TestMemoryIsBounded(t *testing.T) {
	r := NewRedeemer()
	exp := time.Now().Add(5 * time.Minute).Unix()
	const flood = redeemShards * redeemShardCap * 3

	for i := 0; i < flood; i++ {
		r.Claim("proof-"+strconv.Itoa(i), exp+int64(i%600))
	}
	if n := r.Len(); n > redeemShards*redeemShardCap {
		t.Errorf("%d records held after %d distinct proofs; the per-shard cap is %d",
			n, flood, redeemShardCap)
	}
}

// TestEvictionPrefersProofsAboutToExpire — when a shard is full something has to
// go, and what is given up is replay protection. Dropping the soonest-to-expire
// first means the protection surrendered is on proofs that were about to become
// unusable anyway.
func TestEvictionPrefersProofsAboutToExpire(t *testing.T) {
	r := NewRedeemer()
	now := time.Now()
	r.now = func() time.Time { return now }
	soon := now.Add(2 * time.Second).Unix()
	late := now.Add(9 * time.Minute).Unix()

	// One long-lived proof we care about, then enough short-lived ones to force
	// eviction in whichever shard it landed in.
	//
	// Waiting for len(shard) to EXCEED the cap would wait forever: eviction fires
	// at the cap and drops the shard below half, so that condition is unreachable
	// by construction. An earlier version of this test did exactly that and
	// skipped itself, which is a test that never ran rather than a test that
	// passed. Watch for the drop instead.
	const keep = "long-lived-proof"
	r.Claim(keep, late)
	s := r.shardFor(keep)

	evicted := false
	prev := 0
	for i := 0; i < redeemShardCap*redeemShards*4 && !evicted; i++ {
		id := "filler-" + strconv.Itoa(i)
		if r.shardFor(id) != s {
			continue
		}
		r.Claim(id, soon)
		s.mu.Lock()
		n := len(s.spent)
		s.mu.Unlock()
		if n < prev {
			evicted = true
		}
		prev = n
	}
	if !evicted {
		t.Fatal("the shard never evicted, so this test proved nothing about what eviction drops")
	}

	// The long-lived proof must still be protected: replaying it must fail.
	if r.Claim(keep, late) {
		t.Error("eviction dropped a long-lived proof, so it can be replayed — the records " +
			"surrendered should be the ones closest to expiring")
	}
}

// TestNilRedeemerDegradesRatherThanLocksOut — a nil redeemer means the caller
// never wired one. That must not turn every visitor's solved challenge into a
// rejection; it is a real loss of protection, and the Manager always constructs
// one, but failing closed here would be a total lockout caused by a wiring
// mistake rather than by an attacker.
func TestNilRedeemerDegradesRatherThanLocksOut(t *testing.T) {
	var r *Redeemer
	if !r.Claim("x", time.Now().Add(time.Minute).Unix()) {
		t.Error("a nil redeemer refused a claim — every solved challenge would be rejected")
	}
	if r.Len() != 0 {
		t.Error("a nil redeemer reported records")
	}
}

// TestProofIDIsTheSignature — the salt is the server's challenge and could in
// principle be reissued; the signature covers every field of the proof, so it is
// what an attacker must forge rather than replay. Keying on the salt would also
// collide two legitimately distinct proofs over the same salt.
func TestProofIDIsTheSignature(t *testing.T) {
	p := PoW{Salt: "s", Difficulty: 4, Expires: 123, Sig: "the-signature"}
	if got := ProofID(p); got != "the-signature" {
		t.Errorf("ProofID = %q, want the signature", got)
	}
	if ProofID(PoW{Salt: "s"}) == ProofID(PoW{Salt: "s", Sig: "x"}) {
		t.Error("two proofs sharing a salt shared an id")
	}
}
