// SPDX-License-Identifier: Apache-2.0

package session

import (
	"strings"
	"testing"
	"time"
)

// A reading session must not split at a clock boundary: hits under 30 minutes
// apart keep one token even across an hour edge (audit: the hour bucket
// double-counted every session straddling :00).
func TestSessionSlidingWindowContinuity(t *testing.T) {
	h := NewHasher()
	base := time.Date(2026, 3, 1, 10, 50, 0, 0, time.UTC)
	first := h.Session("1.2.3.4", "Mozilla/5.0", "en-US", base)
	steps := []time.Duration{10 * time.Minute, 15 * time.Minute, 29 * time.Minute} // crosses 11:00
	for _, d := range steps {
		got := h.Session("1.2.3.4", "Mozilla/5.0", "en-US", base.Add(d))
		if got != first {
			t.Fatalf("session split after %v inside the window", d)
		}
	}
	// 31 minutes after the last hit -> new session token.
	later := h.Session("1.2.3.4", "Mozilla/5.0", "en-US", base.Add(60*time.Minute))
	if later == first {
		t.Fatal("session did not open a new token after the inactivity gap")
	}
}

// The visitor hash is stable for the day regardless of hour boundaries, and a
// master-key hasher reproduces identical salts across restarts within the day
// while still rotating across days.
func TestVisitorStableAndRestartProof(t *testing.T) {
	master := []byte("unit-test-master")
	now := time.Date(2026, 3, 1, 23, 55, 0, 0, time.UTC)
	a := NewMasterHasher(master)
	b := NewMasterHasher(master) // "restart"
	v1 := a.Visitor("1.2.3.4", "UA", "en", now)
	if v1 != b.Visitor("1.2.3.4", "UA", "en", now) {
		t.Fatal("visitor identity changed across restart with the same master key")
	}
	nextDay := now.Add(2 * time.Hour) // past midnight UTC
	if v1 == b.Visitor("1.2.3.4", "UA", "en", nextDay) {
		t.Fatal("visitor identity survived day rotation — unlinkability broken")
	}
	// Different visitors never collide; hour edges do not split them.
	if v1 == a.Visitor("5.6.7.8", "UA", "en", now) {
		t.Fatal("distinct clients share a visitor hash")
	}
	if a.Session("1.2.3.4", "UA", "en", now) != a.Session("1.2.3.4", "UA", "en", now.Add(time.Minute)) {
		t.Fatal("one sitting produced two tokens inside the window")
	}
}

// The random-salt fallback keeps working when no keyring exists.
func TestRandomHasherStillDistinctPerDay(t *testing.T) {
	h := NewHasher()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	v1 := h.Visitor("1.2.3.4", "UA", "", now)
	v2 := h.Visitor("1.2.3.4", "UA", "", now.Add(24*time.Hour))
	if v1 == v2 || strings.TrimSpace(v1) == "" {
		t.Fatal("random mode must rotate daily and never be empty")
	}
}
