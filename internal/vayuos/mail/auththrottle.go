// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"sync"
	"time"
)

// AuthThrottle slows repeated failed mailbox authentications to defeat online
// password guessing — WITHOUT ever locking anyone out. It only imposes a short,
// time-decaying delay before each attempt for a key (the mailbox address), so a
// correct password always still authenticates; it is just a little slower on an
// account that is actively under attack. This is deliberately a delay rather
// than a hard lockout, because a hard lockout keyed by username is itself a
// denial-of-service vector (an attacker could lock a victim out by failing on
// purpose). A capped ~2s delay crushes a brute-force rate (from thousands/sec to
// well under one per second) while a legitimate user with a typo waits only a
// few hundred milliseconds.
type AuthThrottle struct {
	mu  sync.Mutex
	m   map[string]*authAttempts
	now func() time.Time // injectable for tests
}

type authAttempts struct {
	fails    int
	lastFail time.Time
}

const (
	// authDecayPerFail is how much wall-clock time "forgets" one prior failure.
	authDecayPerFail = 20 * time.Second
	// authDelayStep is the added delay per (decayed) failure.
	authDelayStep = 250 * time.Millisecond
	// authDelayMax caps the per-attempt delay so a targeted account can never be
	// held longer than this — bounding both user impact and goroutine holding.
	authDelayMax = 2 * time.Second
	// authMaxTracked bounds the map so a flood of distinct usernames cannot grow
	// memory without limit; the oldest entry is evicted past this.
	authMaxTracked = 4096
)

// NewAuthThrottle returns a ready throttle.
func NewAuthThrottle() *AuthThrottle {
	return &AuthThrottle{m: make(map[string]*authAttempts), now: time.Now}
}

// Delay returns how long to wait before authenticating key, from its recent
// (decayed) failure history. Zero when the key is clean.
func (t *AuthThrottle) Delay(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.m[key]
	if a == nil {
		return 0
	}
	f := t.decayed(a)
	if f <= 0 {
		delete(t.m, key)
		return 0
	}
	d := time.Duration(f) * authDelayStep
	if d > authDelayMax {
		d = authDelayMax
	}
	return d
}

// Fail records a failed authentication for key.
func (t *AuthThrottle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.m[key]
	if a == nil {
		if len(t.m) >= authMaxTracked {
			t.evictOldest()
		}
		a = &authAttempts{}
		t.m[key] = a
	}
	a.fails = t.decayed(a) + 1
	a.lastFail = t.now()
}

// Success clears the failure history for key.
func (t *AuthThrottle) Success(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, key)
}

// decayed returns the effective failure count for a, after time-based decay.
func (t *AuthThrottle) decayed(a *authAttempts) int {
	if a.fails <= 0 {
		return 0
	}
	decay := int(t.now().Sub(a.lastFail) / authDecayPerFail)
	if f := a.fails - decay; f > 0 {
		return f
	}
	return 0
}

// evictOldest drops the least-recently-failed entry. Caller holds the lock.
func (t *AuthThrottle) evictOldest() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, a := range t.m {
		if first || a.lastFail.Before(oldest) {
			oldestKey, oldest, first = k, a.lastFail, false
		}
	}
	if oldestKey != "" {
		delete(t.m, oldestKey)
	}
}
