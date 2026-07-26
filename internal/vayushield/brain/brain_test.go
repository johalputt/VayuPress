// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// clockAt pins the brain's clock to a mutable seconds counter.
func clockAt(sec *int64) func() time.Time {
	return func() time.Time { return time.Unix(*sec, 0) }
}

func TestUnknownSourceIsNeutralAndFree(t *testing.T) {
	b := New()
	if b.Jailed("203.0.113.1") {
		t.Fatal("unknown source must not be jailed")
	}
	if got := b.Standing("203.0.113.1"); got != neutral {
		t.Fatalf("unknown standing = %v, want %v", got, neutral)
	}
	// Positive signals for unknown sources must not create tracking entries.
	b.Observe("203.0.113.1", SignalHuman)
	b.Observe("203.0.113.1", SignalProof)
	if st := b.Stats(); st.Tracked != 0 {
		t.Fatalf("tracked = %d after positive-only signals, want 0", st.Tracked)
	}
}

func TestRepeatedBlocksJailWithEscalation(t *testing.T) {
	sec := int64(1_000_000)
	b := New()
	b.now = clockAt(&sec)
	ip := "203.0.113.9"

	// Two hard blocks: 0.5 -> 0.25 -> 0.0 => below floor => jailed.
	b.Observe(ip, SignalBlock)
	if b.Jailed(ip) {
		t.Fatal("one block must not jail")
	}
	b.Observe(ip, SignalBlock)
	if !b.Jailed(ip) {
		t.Fatal("collapsed reputation must jail")
	}
	st := b.Stats()
	if st.Jails != 1 || st.Jailed != 1 {
		t.Fatalf("stats = %+v, want 1 jail", st)
	}

	// First sentence is baseJail: released after it elapses.
	sec += int64(baseJail/time.Second) + 1
	if b.Jailed(ip) {
		t.Fatal("first sentence should have ended")
	}

	// Re-offend: second sentence doubles.
	b.Observe(ip, SignalBlock)
	b.Observe(ip, SignalBlock)
	if !b.Jailed(ip) {
		t.Fatal("re-offense must jail again")
	}
	sec += int64(baseJail/time.Second) + 1
	if !b.Jailed(ip) {
		t.Fatal("second sentence must be longer than the first")
	}
	sec += int64(baseJail/time.Second) + 1
	if b.Jailed(ip) {
		t.Fatal("second sentence (2x base) should have ended")
	}
}

func TestSolvedChallengePardonsInstantly(t *testing.T) {
	b := New()
	ip := "203.0.113.21"
	b.Observe(ip, SignalBlock)
	b.Observe(ip, SignalBlock)
	if !b.Jailed(ip) {
		t.Fatal("setup: source should be jailed")
	}
	// The real user behind a shared IP solves a challenge: instant pardon.
	b.Observe(ip, SignalProof)
	if b.Jailed(ip) {
		t.Fatal("positive proof must pardon the sentence")
	}
	if st := b.Stats(); st.Redeems != 1 {
		t.Fatalf("redeems = %d, want 1", st.Redeems)
	}
}

func TestReputationDecaysTowardNeutral(t *testing.T) {
	sec := int64(2_000_000)
	b := New()
	b.now = clockAt(&sec)
	ip := "203.0.113.30"
	b.Observe(ip, SignalRateBurst) // 0.5 -> 0.4, tracked
	got := b.Standing(ip)
	if got >= 0.45 {
		t.Fatalf("fresh penalty should show: standing = %v", got)
	}
	// Several half-lives later the score is essentially neutral again.
	sec += int64(6 * halfLife / time.Second)
	if got := b.Standing(ip); got < 0.49 {
		t.Fatalf("standing after decay = %v, want ~neutral", got)
	}
}

func TestMaxSentenceIsCapped(t *testing.T) {
	sec := int64(3_000_000)
	b := New()
	b.now = clockAt(&sec)
	ip := "203.0.113.40"
	// Drive many offense cycles; sentences must never exceed maxJail.
	for cycle := 0; cycle < maxOffenses+3; cycle++ {
		b.Observe(ip, SignalBlock)
		b.Observe(ip, SignalBlock)
		b.Observe(ip, SignalBlock)
		if !b.Jailed(ip) {
			t.Fatalf("cycle %d: expected jail", cycle)
		}
		sec += int64(maxJail/time.Second) + 1
		if b.Jailed(ip) {
			t.Fatalf("cycle %d: sentence exceeded maxJail", cycle)
		}
	}
}

func TestMemoryStaysBoundedUnderFlood(t *testing.T) {
	b := New()
	// A flood of unique offenders far past the global cap.
	for i := 0; i < shards*perShardCap*2; i++ {
		b.Observe(fmt.Sprintf("2001:db8::%x", i), SignalBlock)
	}
	if st := b.Stats(); st.Tracked > shards*perShardCap {
		t.Fatalf("tracked = %d, exceeds hard cap %d", st.Tracked, shards*perShardCap)
	}
}

func TestConcurrentObserveIsRaceFree(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := fmt.Sprintf("198.51.100.%d", id%40)
			for i := 0; i < 300; i++ {
				switch i % 3 {
				case 0:
					b.Observe(ip, SignalRateBurst)
				case 1:
					b.Observe(ip, SignalProof)
				default:
					b.Jailed(ip)
				}
			}
		}(g)
	}
	wg.Wait()
	_ = b.Stats()
}
