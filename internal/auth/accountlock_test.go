// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"
	"time"
)

// clock sets the account counter's time for one test.
func clock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	accountNow = func() time.Time { return now }
	t.Cleanup(func() {
		accountNow = time.Now
		accountMu.Lock()
		accountStates = map[string]*accountState{}
		accountMu.Unlock()
		OnAccountLocked = nil
	})
	return &now
}

func failN(account, source string, n int) (bool, time.Time) {
	var locked bool
	var until time.Time
	for i := 0; i < n; i++ {
		locked, until = RecordAccountFailure(account, source)
	}
	return locked, until
}

// The fifth failure within 15 minutes locks the account for 15 minutes, from
// any door and any address, and the account's own spelling does not matter.
func TestAnAccountLocksOnItsFifthFailure(t *testing.T) {
	now := clock(t)
	var told []string
	OnAccountLocked = func(account string, until time.Time, sources []string) { told = append(told, account) }
	for i, src := range []string{"203.0.113.1", "203.0.113.2", "a mail app", "198.51.100.9"} {
		if locked, _ := RecordAccountFailure("Ann@Example.com", src); locked {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	if locked, _ := CheckAccountLockout("ann@example.com"); locked {
		t.Fatal("locked before the fifth failure")
	}
	locked, until := RecordAccountFailure(" ann@example.com", "203.0.113.3")
	if !locked || !until.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("the fifth failure gave locked=%v until %v, want 15 minutes", locked, until)
	}
	if l, _ := CheckAccountLockout("ANN@example.com"); !l {
		t.Error("the account does not read as locked")
	}
	if len(told) != 1 || told[0] != "ann@example.com" {
		t.Errorf("the owner was told %v, want once for ann@example.com", told)
	}
	if l, _ := CheckAccountLockout("bob@example.com"); l {
		t.Error("another account was locked")
	}
}

// Failures further apart than the window do not add up.
func TestOldFailuresDoNotCount(t *testing.T) {
	now := clock(t)
	failN("ann@example.com", "x", 4)
	*now = now.Add(16 * time.Minute)
	if locked, _ := RecordAccountFailure("ann@example.com", "x"); locked {
		t.Error("four failures 16 minutes ago and one now locked the account")
	}
}

// Each further lockout doubles the wait, up to 24 hours; a correct sign-in
// resets it, and so does a day without a lockout.
func TestTheWaitDoublesAndCaps(t *testing.T) {
	now := clock(t)
	want := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		locked, until := failN("ann@example.com", "x", 5)
		if !locked || until.Sub(*now) != w {
			t.Fatalf("lockout %d: locked=%v for %v, want %v", i+1, locked, until.Sub(*now), w)
		}
		*now = until.Add(time.Second)
	}
	RecordAccountSuccess("ann@example.com")
	if _, until := failN("ann@example.com", "x", 5); until.Sub(*now) != 15*time.Minute {
		t.Errorf("after a correct sign-in the wait is %v, want 15 minutes", until.Sub(*now))
	}
	*now = now.Add(15*time.Minute + 25*time.Hour)
	if _, until := failN("ann@example.com", "x", 5); until.Sub(*now) != 15*time.Minute {
		t.Errorf("a day after the last lockout the wait is %v, want 15 minutes", until.Sub(*now))
	}
}

// A failure while locked neither extends the lockout nor counts toward the
// next one, and the operator can lift a lockout.
func TestALockedAccountIsNotLockedFurther(t *testing.T) {
	now := clock(t)
	_, until := failN("ann@example.com", "x", 5)
	*now = now.Add(time.Minute)
	if _, u := failN("ann@example.com", "x", 20); !u.Equal(until) {
		t.Errorf("failures while locked moved the end from %v to %v", until, u)
	}
	LiftAccountLockout("ann@example.com")
	if l, _ := CheckAccountLockout("ann@example.com"); l {
		t.Error("a lifted lockout still holds")
	}
}
