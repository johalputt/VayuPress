package vayutalk

import (
	"testing"
	"time"
)

func mkEnv(t *testing.T, from, to string, ttl int, mode string, now time.Time) *Envelope {
	t.Helper()
	env, err := NewEnvelope(from, to, []byte("ct"), ttl, mode, now)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	return env
}

func TestClampBurn(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultBurnSeconds},  // unset -> default 5m
		{-1, DefaultBurnSeconds}, // negative -> default
		{1, MinBurnSeconds},      // below min 5s -> 5s
		{5, 5}, {60, 60}, {300, 300}, {1000, 1000},
		{3600, 3600}, {99999, MaxBurnSeconds},
	}
	for _, c := range cases {
		if got := ClampBurn(c.in); got != c.want {
			t.Errorf("ClampBurn(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNewEnvelopeRejectsOversize(t *testing.T) {
	_, err := NewEnvelope("a@x", "b@x", make([]byte, MaxCiphertextBytes+1), 60, "store", time.Now())
	if err != ErrCiphertextTooLarge {
		t.Fatalf("err = %v, want ErrCiphertextTooLarge", err)
	}
	if _, err := NewEnvelope("a@x", "b@x", make([]byte, MaxCiphertextBytes), 60, "store", time.Now()); err != nil {
		t.Fatalf("exact cap rejected: %v", err)
	}
}

// TestEnvelopeBurnAndUnreadCap: the burn timer is carried on the envelope
// (clamped), while ExpiresAt is the independent unread holding deadline.
func TestEnvelopeBurnAndUnreadCap(t *testing.T) {
	now := time.Unix(1000, 0)
	env := mkEnv(t, "a@x", "b@x", 1, "store", now) // 1s below min -> clamped to 5s burn
	if env.BurnSeconds != MinBurnSeconds {
		t.Fatalf("burn = %d, want %d", env.BurnSeconds, MinBurnSeconds)
	}
	// ExpiresAt is the unread cap, NOT the burn timer.
	if got := env.ExpiresAt.Sub(now); got != time.Duration(UnreadTTLSeconds)*time.Second {
		t.Fatalf("unread cap = %v, want %v", got, time.Duration(UnreadTTLSeconds)*time.Second)
	}
}

func TestSetUnreadTTL(t *testing.T) {
	orig := UnreadTTLSeconds
	defer func() { UnreadTTLSeconds = orig }()
	SetUnreadTTL(0) // ignored
	if UnreadTTLSeconds != orig {
		t.Fatal("0 should be ignored")
	}
	SetUnreadTTL(60) // below floor -> 300
	if UnreadTTLSeconds != 300 {
		t.Fatalf("got %d, want 300 (floor)", UnreadTTLSeconds)
	}
	SetUnreadTTL(9999999) // above ceiling -> 604800
	if UnreadTTLSeconds != 604800 {
		t.Fatalf("got %d, want 604800 (ceiling)", UnreadTTLSeconds)
	}
	SetUnreadTTL(3600)
	if UnreadTTLSeconds != 3600 {
		t.Fatalf("got %d, want 3600", UnreadTTLSeconds)
	}
}

func TestEnqueueQueuedPeek(t *testing.T) {
	s := NewStore()
	now := time.Now()
	e1 := mkEnv(t, "a@x", "b@x", 60, "store", now)
	e2 := mkEnv(t, "c@x", "b@x", 60, "store", now)
	if err := s.Enqueue(e1); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(e2); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}
	got := s.Queued("b@x")
	if len(got) != 2 || got[0].ID != e1.ID || got[1].ID != e2.ID {
		t.Fatalf("Queued order wrong: %+v", got)
	}
	// Queued is a non-destructive peek: the envelopes remain until ack/purge, so
	// a reconnecting client re-receives anything it never acknowledged.
	if s.Len() != 2 {
		t.Fatalf("len after peek = %d, want 2", s.Len())
	}
	if again := s.Queued("b@x"); len(again) != 2 {
		t.Fatalf("second peek = %d, want 2", len(again))
	}
	// The returned slice is a copy: mutating it does not disturb the store.
	got[0] = nil
	if s.Queued("b@x")[0] == nil {
		t.Fatal("Queued returned an aliased slice")
	}
}

func TestPerRecipientCap(t *testing.T) {
	s := NewStore()
	now := time.Now()
	for i := 0; i < MaxPerRecipientQueue; i++ {
		if err := s.Enqueue(mkEnv(t, "a@x", "b@x", 60, "store", now)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := s.Enqueue(mkEnv(t, "a@x", "b@x", 60, "store", now)); err != ErrRecipientQueueFull {
		t.Fatalf("over-cap err = %v, want ErrRecipientQueueFull", err)
	}
	// A different recipient is unaffected.
	if err := s.Enqueue(mkEnv(t, "a@x", "c@x", 60, "store", now)); err != nil {
		t.Fatalf("other recipient rejected: %v", err)
	}
}

func TestGlobalCap(t *testing.T) {
	s := NewStore()
	now := time.Now()
	// Spread across many recipients so the per-recipient cap is never hit first.
	rcpt := 0
	for i := 0; i < MaxGlobalQueue; i++ {
		if i%MaxPerRecipientQueue == 0 {
			rcpt++
		}
		to := "u" + string(rune('a'+rcpt%26)) + string(rune('0'+rcpt/26)) + "@x"
		if err := s.Enqueue(mkEnv(t, "a@x", to, 60, "store", now)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if s.Len() != MaxGlobalQueue {
		t.Fatalf("len = %d, want %d", s.Len(), MaxGlobalQueue)
	}
	if err := s.Enqueue(mkEnv(t, "a@x", "fresh@x", 60, "store", now)); err != ErrGlobalQueueFull {
		t.Fatalf("over-global err = %v, want ErrGlobalQueueFull", err)
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	now := time.Now()
	env := mkEnv(t, "sender@x", "b@x", 60, "store", now)
	if err := s.Enqueue(env); err != nil {
		t.Fatal(err)
	}
	sender, existed := s.Delete(env.ID)
	if !existed || sender != "sender@x" {
		t.Fatalf("Delete = (%q,%v), want (sender@x,true)", sender, existed)
	}
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0", s.Len())
	}
	if _, existed := s.Delete(env.ID); existed {
		t.Fatal("second delete reported existed=true")
	}
	if _, existed := s.Delete("nope"); existed {
		t.Fatal("delete of unknown id reported existed=true")
	}
}

func TestPurgeExpired(t *testing.T) {
	s := NewStore()
	base := time.Unix(10000, 0)
	// ExpiresAt is the unread holding deadline; set it explicitly to exercise the
	// sweeper (NewEnvelope now derives it from the unread cap, not the burn timer).
	live := mkEnv(t, "a@x", "b@x", 3600, "store", base)
	live.ExpiresAt = base.Add(3600 * time.Second)
	soon := mkEnv(t, "sndr@x", "b@x", 60, "store", base)
	soon.ExpiresAt = base.Add(60 * time.Second)
	if err := s.Enqueue(live); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(soon); err != nil {
		t.Fatal(err)
	}
	// Advance past the short TTL but not the long one.
	receipts := s.PurgeExpired(base.Add(120 * time.Second))
	if len(receipts) != 1 || receipts[0].ID != soon.ID || receipts[0].Sender != "sndr@x" {
		t.Fatalf("receipts = %+v, want one for soon/sndr@x", receipts)
	}
	if s.Len() != 1 {
		t.Fatalf("len = %d, want 1 (live survives)", s.Len())
	}
	// Nothing left to expire yet.
	if r := s.PurgeExpired(base.Add(120 * time.Second)); len(r) != 0 {
		t.Fatalf("second purge = %+v, want none", r)
	}
	// Advance past the long TTL.
	if r := s.PurgeExpired(base.Add(4000 * time.Second)); len(r) != 1 || r[0].ID != live.ID {
		t.Fatalf("late purge = %+v, want live", r)
	}
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0", s.Len())
	}
}

func TestIDsUnique(t *testing.T) {
	seen := make(map[string]struct{})
	now := time.Now()
	for i := 0; i < 5000; i++ {
		env := mkEnv(t, "a@x", "b@x", 60, "store", now)
		if env.ID == "" {
			t.Fatal("empty id")
		}
		if _, dup := seen[env.ID]; dup {
			t.Fatalf("duplicate id at %d: %s", i, env.ID)
		}
		seen[env.ID] = struct{}{}
	}
}
