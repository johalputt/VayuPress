// SPDX-License-Identifier: Apache-2.0

package vayutalk

import (
	"sync"
	"time"
)

// Token caps.
const (
	// TokenTTL is the lifetime of a connect token (12h).
	TokenTTL = 12 * time.Hour
	// maxTokens bounds the in-memory token table.
	maxTokens = 20000
)

type tokenRec struct {
	email     string
	expiresAt time.Time
}

// tokenStore is an in-memory bearer-token table with O(1) lookup. Mint on
// connect, prune on the shared purge tick. A restart purges every token.
type tokenStore struct {
	mu sync.Mutex
	m  map[string]tokenRec
}

func newTokenStore() *tokenStore {
	return &tokenStore{m: make(map[string]tokenRec)}
}

// maxTokensPerEmail caps how many live tokens one mailbox may hold at once.
//
// Without it, the global table is a shared resource one account can exhaust.
// Each connect mints a NEW token and never invalidates the old, so a single
// authenticated user (the cheapest thing an attacker can obtain — one mailbox on
// the install) could call /api/v1/talk/connect maxTokens times and, because
// eviction picked the soonest-to-expire entry across ALL users, evict every other
// user's token in the process. One ordinary account, no privileges, and VayuTalk
// signs the entire install out.
//
// Capping per email confines the damage to the account doing it: a user who mints
// past their own cap loses their own oldest session and nobody else's. Ten covers
// phone, desktop, tablet and a few stale sessions with room to spare.
const maxTokensPerEmail = 10

// mint issues a fresh random token for email, valid for TokenTTL.
//
// Eviction is deliberately ordered so that pressure created by one account is
// paid for by that account: expired entries first, then that email's own oldest,
// and only then — when the table is globally full of other people's live tokens —
// the soonest-to-expire entry overall.
func (t *tokenStore) mint(email string, now time.Time) string {
	token := randID()
	if token == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.m) >= maxTokens {
		t.pruneLocked(now)
	}
	// Enforce this email's own quota before touching anyone else's tokens.
	for t.countForLocked(email) >= maxTokensPerEmail {
		if !t.evictOldestForLocked(email) {
			break
		}
	}
	if len(t.m) >= maxTokens {
		t.evictSoonestLocked()
	}
	t.m[token] = tokenRec{email: email, expiresAt: now.Add(TokenTTL)}
	return token
}

// countForLocked returns how many live tokens email currently holds.
func (t *tokenStore) countForLocked(email string) int {
	n := 0
	for _, rec := range t.m {
		if rec.email == email {
			n++
		}
	}
	return n
}

// evictOldestForLocked drops the soonest-to-expire token belonging to email,
// reporting whether one was removed.
func (t *tokenStore) evictOldestForLocked(email string) bool {
	var oldestTok string
	var oldest time.Time
	first := true
	for tok, rec := range t.m {
		if rec.email != email {
			continue
		}
		if first || rec.expiresAt.Before(oldest) {
			oldestTok, oldest, first = tok, rec.expiresAt, false
		}
	}
	if oldestTok == "" {
		return false
	}
	delete(t.m, oldestTok)
	return true
}

// lookup resolves a token to its email if present and unexpired.
func (t *tokenStore) lookup(token string, now time.Time) (string, bool) {
	if token == "" {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.m[token]
	if !ok || !rec.expiresAt.After(now) {
		return "", false
	}
	return rec.email, true
}

// sweep removes expired tokens.
func (t *tokenStore) sweep(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
}

func (t *tokenStore) pruneLocked(now time.Time) {
	for tok, rec := range t.m {
		if !rec.expiresAt.After(now) {
			delete(t.m, tok)
		}
	}
}

func (t *tokenStore) evictSoonestLocked() {
	var soonestTok string
	var soonest time.Time
	first := true
	for tok, rec := range t.m {
		if first || rec.expiresAt.Before(soonest) {
			soonestTok, soonest, first = tok, rec.expiresAt, false
		}
	}
	if soonestTok != "" {
		delete(t.m, soonestTok)
	}
}
