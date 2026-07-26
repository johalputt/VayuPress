// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"testing"
	"time"
)

func TestAuthThrottle(t *testing.T) {
	now := time.Now()
	th := NewAuthThrottle()
	th.now = func() time.Time { return now }

	const key = "user@example.com"

	// A clean key has no delay.
	if d := th.Delay(key); d != 0 {
		t.Fatalf("clean key delay = %v, want 0", d)
	}

	// Delay grows with failures but is capped.
	th.Fail(key) // 1
	if d := th.Delay(key); d != authDelayStep {
		t.Fatalf("after 1 fail delay = %v, want %v", d, authDelayStep)
	}
	for i := 0; i < 50; i++ {
		th.Fail(key)
	}
	if d := th.Delay(key); d != authDelayMax {
		t.Fatalf("after many fails delay = %v, want cap %v", d, authDelayMax)
	}

	// Success wipes the history immediately — a correct password is never
	// left throttled.
	th.Success(key)
	if d := th.Delay(key); d != 0 {
		t.Fatalf("after success delay = %v, want 0", d)
	}

	// Failures decay over time so a key is never permanently penalised.
	th.Fail(key)
	th.Fail(key)
	th.Fail(key) // 3 fails -> would be 3*step
	now = now.Add(2 * authDecayPerFail)
	if d := th.Delay(key); d != authDelayStep {
		t.Fatalf("after decay delay = %v, want %v (3 fails - 2 decayed)", d, authDelayStep)
	}
	now = now.Add(10 * authDecayPerFail)
	if d := th.Delay(key); d != 0 {
		t.Fatalf("after full decay delay = %v, want 0", d)
	}
}

func TestAuthThrottleEviction(t *testing.T) {
	th := NewAuthThrottle()
	// Overfill the tracked set; it must stay bounded.
	for i := 0; i < authMaxTracked+500; i++ {
		th.Fail(string(rune(i%1114111)) + "@x")
	}
	th.mu.Lock()
	n := len(th.m)
	th.mu.Unlock()
	if n > authMaxTracked {
		t.Fatalf("tracked entries = %d, want <= %d", n, authMaxTracked)
	}
}
