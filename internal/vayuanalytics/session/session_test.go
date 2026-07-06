package session

import (
	"testing"
	"time"
)

func TestSameVisitorSameHourStableSession(t *testing.T) {
	h := NewHasher()
	now := time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC)
	a := h.Session("1.2.3.4", "Chrome", "en", now)
	b := h.Session("1.2.3.4", "Chrome", "en", now.Add(20*time.Minute)) // same hour
	if a != b {
		t.Fatal("same visitor within the hour must share a session hash")
	}
	if len(a) != 64 {
		t.Fatalf("session hash must be sha256 hex, got %d", len(a))
	}
}

func TestDifferentHourDifferentSession(t *testing.T) {
	h := NewHasher()
	base := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	a := h.Session("1.2.3.4", "Chrome", "en", base)
	b := h.Session("1.2.3.4", "Chrome", "en", base.Add(2*time.Hour))
	if a == b {
		t.Fatal("different hour should be a different session")
	}
}

func TestDifferentVisitorsDiffer(t *testing.T) {
	h := NewHasher()
	now := time.Now().UTC()
	a := h.Session("1.1.1.1", "Chrome", "en", now)
	b := h.Session("2.2.2.2", "Chrome", "en", now)
	if a == b {
		t.Fatal("different IPs must produce different sessions")
	}
}

func TestSaltRotationBreaksCrossDayLink(t *testing.T) {
	h := NewHasher()
	// Force day 1.
	h.mu.Lock()
	h.day = "2026-07-06"
	h.mu.Unlock()
	d1 := h.Session("1.2.3.4", "Chrome", "en", time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC))
	// Advance to next day — should rotate salt and change the hash for the same visitor.
	d2 := h.Session("1.2.3.4", "Chrome", "en", time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC))
	if d1 == d2 {
		t.Fatal("cross-day sessions must not correlate (salt rotates)")
	}
	h.mu.Lock()
	gotDay := h.day
	h.mu.Unlock()
	if gotDay != "2026-07-07" {
		t.Fatalf("expected day rotation, got %s", gotDay)
	}
}

func TestConcurrentSafe(t *testing.T) {
	h := NewHasher()
	now := time.Now().UTC()
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				_ = h.Session("1.2.3.4", "UA", "en", now)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
