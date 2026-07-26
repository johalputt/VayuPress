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

// mint issues a fresh random token for email, valid for TokenTTL. When the
// table is at capacity it first prunes expired entries, then evicts the
// soonest-to-expire entry to make room, so a token is always issued.
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
	if len(t.m) >= maxTokens {
		t.evictSoonestLocked()
	}
	t.m[token] = tokenRec{email: email, expiresAt: now.Add(TokenTTL)}
	return token
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
