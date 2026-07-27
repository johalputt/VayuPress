// SPDX-License-Identifier: Apache-2.0

package vayutalk

import (
	"testing"
	"time"
)

// TestOneAccountCannotEvictOthers is the regression for the token-table
// exhaustion DoS.
//
// Every connect mints a NEW token and never invalidates the old one, and
// eviction used to pick the soonest-to-expire entry across ALL users. So the
// cheapest thing an attacker can obtain — one ordinary mailbox on the install,
// no privileges — could call /api/v1/talk/connect in a loop and evict every other
// user's token on the way past the cap. One account, and VayuTalk signs the whole
// install out.
func TestOneAccountCannotEvictOthers(t *testing.T) {
	ts := newTokenStore()
	now := time.Now()

	// A legitimate user with a live session.
	victim := ts.mint("victim@example.com", now)
	if victim == "" {
		t.Fatal("victim mint failed")
	}

	// The attacker mints far past the whole table's capacity.
	for i := 0; i < maxTokens+500; i++ {
		if ts.mint("attacker@example.com", now) == "" {
			t.Fatalf("attacker mint %d failed", i)
		}
	}

	if _, ok := ts.lookup(victim, now); !ok {
		t.Fatal("victim's token was evicted by another account's minting")
	}
}

// TestPerEmailQuotaConfinesTheDamage — an account that mints past its own quota
// loses its OWN oldest session, which is the correct place for the cost to land.
func TestPerEmailQuotaConfinesTheDamage(t *testing.T) {
	ts := newTokenStore()
	now := time.Now()

	// Mint each token a second apart so expiry order is well defined. With an
	// identical timestamp every entry shares an expiry and "oldest" is whichever
	// the map iteration reaches first — arbitrary, and not a property worth
	// asserting.
	first := ts.mint("user@example.com", now)
	var last string
	for i := 1; i <= maxTokensPerEmail; i++ {
		last = ts.mint("user@example.com", now.Add(time.Duration(i)*time.Second))
	}
	at := now.Add(time.Duration(maxTokensPerEmail) * time.Second)

	if _, ok := ts.lookup(first, at); ok {
		t.Error("oldest token survived past the per-email quota")
	}
	if _, ok := ts.lookup(last, at); !ok {
		t.Error("newest token was not usable")
	}
	if n := ts.countForLocked("user@example.com"); n > maxTokensPerEmail {
		t.Errorf("quota exceeded: %d live tokens, cap %d", n, maxTokensPerEmail)
	}
}

// TestNormalMultiDeviceUseIsUnaffected guards against over-correction: the quota
// must comfortably cover phone, desktop, tablet and a few stale sessions, or this
// defence becomes a sign-out bug.
func TestNormalMultiDeviceUseIsUnaffected(t *testing.T) {
	ts := newTokenStore()
	now := time.Now()

	var toks []string
	for i := 0; i < 4; i++ {
		toks = append(toks, ts.mint("user@example.com", now))
	}
	for i, tok := range toks {
		if _, ok := ts.lookup(tok, now); !ok {
			t.Errorf("device %d was signed out by the quota", i)
		}
	}
}

// TestExpiredTokensAreNotUsable pins the basic property the quota logic must not
// disturb while pruning.
func TestExpiredTokensAreNotUsable(t *testing.T) {
	ts := newTokenStore()
	now := time.Now()
	tok := ts.mint("user@example.com", now)
	if _, ok := ts.lookup(tok, now.Add(TokenTTL+time.Second)); ok {
		t.Fatal("expired token still resolved")
	}
}
