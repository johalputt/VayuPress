// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Account lockout: one counter per account, shared by every door that takes
// its password or its codes (the console, the site's sign-in, IMAP, POP3 and
// SMTP, the app's sign-in, recovery and two-factor codes).
//
// The address lockout above stops one address guessing many times. It does
// not stop many addresses guessing one account, and each door used to count
// on its own, so every door was a fresh set of guesses. This counter is per
// account, whatever the door and the address: 5 failures within 15 minutes
// lock the account for 15 minutes, each further lockout doubles the wait up
// to 24 hours, and a correct sign-in resets it. A lockout stops new sign-ins
// only: the owner's sessions already signed in are not touched, so locking
// an account cannot sign its owner out.

const (
	accountFailMax    = 5
	accountFailWindow = 15 * time.Minute
	accountBaseLock   = 15 * time.Minute
	accountMaxLock    = 24 * time.Hour
	// accountLevelReset is how long after a lockout ends the doubling starts
	// again from the first wait.
	accountLevelReset = 24 * time.Hour
	// accountSourcesMax bounds the addresses kept for the lockout notice.
	accountSourcesMax = 10
)

type accountState struct {
	failures    []time.Time
	level       int // lockouts in this run, for the doubling
	lockedUntil time.Time
	sources     map[string]bool
}

var (
	accountMu     sync.Mutex
	accountStates = map[string]*accountState{}
	accountNow    = time.Now

	// OnAccountLocked is called, outside the lock, when an account is locked:
	// with the account, until when, and the sources of the failures that
	// locked it. The app sets it to tell the owner.
	OnAccountLocked func(account string, until time.Time, sources []string)
)

// AccountKey is the form an account is counted under: an address, trimmed
// and lower-cased, so "Ann@Example.com" and "ann@example.com " are one.
func AccountKey(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

// AccountLockoutPolicy is the account lockout as enforced, for the pages
// that state it.
func AccountLockoutPolicy() (failures int, window, first, max time.Duration) {
	return accountFailMax, accountFailWindow, accountBaseLock, accountMaxLock
}

// CheckAccountLockout reports whether account is locked, and until when.
func CheckAccountLockout(account string) (bool, time.Time) {
	k := AccountKey(account)
	if k == "" {
		return false, time.Time{}
	}
	accountMu.Lock()
	defer accountMu.Unlock()
	s := accountStates[k]
	if s == nil {
		return false, time.Time{}
	}
	if now := accountNow(); now.Before(s.lockedUntil) {
		return true, s.lockedUntil
	}
	return false, time.Time{}
}

// RecordAccountFailure counts one failed sign-in or code for account, from
// source (an address, or a door's name where none is known). It returns
// whether this failure locked the account, and until when.
func RecordAccountFailure(account, source string) (bool, time.Time) {
	k := AccountKey(account)
	if k == "" {
		return false, time.Time{}
	}
	now := accountNow()
	accountMu.Lock()
	s := accountStates[k]
	if s == nil {
		s = &accountState{sources: map[string]bool{}}
		accountStates[k] = s
	}
	if now.Before(s.lockedUntil) {
		accountMu.Unlock()
		return false, s.lockedUntil
	}
	if !s.lockedUntil.IsZero() && now.Sub(s.lockedUntil) > accountLevelReset {
		s.level = 0
	}
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < accountFailWindow {
			kept = append(kept, t)
		}
	}
	s.failures = append(kept, now)
	if source != "" && len(s.sources) < accountSourcesMax {
		s.sources[source] = true
	}
	if len(s.failures) < accountFailMax {
		accountMu.Unlock()
		return false, time.Time{}
	}
	wait := accountBaseLock << s.level
	if wait > accountMaxLock || wait <= 0 {
		wait = accountMaxLock
	}
	s.level++
	s.lockedUntil = now.Add(wait)
	s.failures = nil
	sources := make([]string, 0, len(s.sources))
	for src := range s.sources {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	s.sources = map[string]bool{}
	until := s.lockedUntil
	notify := OnAccountLocked
	accountMu.Unlock()
	if notify != nil {
		notify(k, until, sources)
	}
	return true, until
}

// RecordAccountSuccess resets account after a correct sign-in.
func RecordAccountSuccess(account string) {
	k := AccountKey(account)
	accountMu.Lock()
	delete(accountStates, k)
	accountMu.Unlock()
}

// LiftAccountLockout ends account's lockout and its doubling now, for the
// operator. The caller decides who may (a private mailbox's cannot be).
func LiftAccountLockout(account string) {
	RecordAccountSuccess(account)
}

// LiftAllAccountLockouts ends every account's lockout and its doubling now,
// for the operator after, say, a burst of failures from their own office.
func LiftAllAccountLockouts() {
	accountMu.Lock()
	accountStates = map[string]*accountState{}
	accountMu.Unlock()
}

// LockedAccounts lists the accounts locked now, with until when.
func LockedAccounts() map[string]time.Time {
	now := accountNow()
	out := map[string]time.Time{}
	accountMu.Lock()
	defer accountMu.Unlock()
	for k, s := range accountStates {
		if now.Before(s.lockedUntil) {
			out[k] = s.lockedUntil
		}
	}
	return out
}

// sweepAccounts drops the state of accounts with nothing left to count.
func sweepAccounts(now time.Time) {
	accountMu.Lock()
	defer accountMu.Unlock()
	for k, s := range accountStates {
		recent := len(s.failures) > 0 && now.Sub(s.failures[len(s.failures)-1]) < accountFailWindow
		if !recent && now.After(s.lockedUntil.Add(accountLevelReset)) {
			delete(accountStates, k)
		}
	}
}
