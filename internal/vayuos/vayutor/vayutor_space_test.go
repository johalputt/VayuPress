package vayutor

import "testing"

// TestSpaceOnionPlan covers the dedicated-onion decision core: add (mint vs
// restore), noop (already live / already down), and teardown.
func TestSpaceOnionPlan(t *testing.T) {
	cases := []struct {
		name         string
		target       string
		liveHost     string
		persistedKey string
		wantOp       spaceOnionOp
		wantBlob     string
	}{
		{"mint fresh", "127.0.0.1:8181", "", "", spaceOnionAdd, "NEW:ED25519-V3"},
		{"restore persisted", "127.0.0.1:8181", "", "ED25519-V3:abc", spaceOnionAdd, "ED25519-V3:abc"},
		{"already live", "127.0.0.1:8181", "abc.onion", "ED25519-V3:abc", spaceOnionNoop, ""},
		{"teardown when off", "", "abc.onion", "ED25519-V3:abc", spaceOnionRemove, ""},
		{"off and already down", "", "", "ED25519-V3:abc", spaceOnionNoop, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			op, blob := spaceOnionPlan(c.target, c.liveHost, c.persistedKey)
			if op != c.wantOp {
				t.Errorf("op = %d, want %d", op, c.wantOp)
			}
			if blob != c.wantBlob {
				t.Errorf("blob = %q, want %q", blob, c.wantBlob)
			}
		})
	}
}

// TestReservedSpaceHostNotOnion: the reserved store host must never look like a
// served clearnet host or an onion, so the domain want/reap logic can never
// mint, reap, or collide with the dedicated onion's identity.
func TestReservedSpaceHostNotOnion(t *testing.T) {
	if reservedSpaceHost == "" {
		t.Fatal("reserved host must be set")
	}
	if got := normHost(reservedSpaceHost); got == "" {
		t.Error("reserved host should normalise to a stable non-empty key")
	}
	// It must not be mistaken for a real onion (would be filtered from want, but
	// also must never be treated as a domain to serve).
	if len(reservedSpaceHost) > 3 && reservedSpaceHost[len(reservedSpaceHost)-6:] == ".onion" {
		t.Error("reserved host must not end in .onion")
	}
}

// TestSpaceOnionAccessorNil: SpaceOnion is safe on a zero engine and empty by
// default.
func TestSpaceOnionAccessorDefault(t *testing.T) {
	var e *Engine
	if e.SpaceOnion() != "" {
		t.Error("nil engine SpaceOnion must be empty")
	}
	e2 := NewEngine(Config{})
	if e2.SpaceOnion() != "" {
		t.Error("fresh engine SpaceOnion must be empty")
	}
}
