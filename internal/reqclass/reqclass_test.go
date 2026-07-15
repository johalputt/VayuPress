package reqclass

import (
	"context"
	"testing"
)

func TestShieldedMarkFlowsThroughPointer(t *testing.T) {
	// A bare context (no classification) is never shielded and never panics.
	if Shielded(context.Background()) {
		t.Fatal("bare context should not be shielded")
	}
	MarkShielded(context.Background()) // no-op, must not panic

	// The outer middleware seeds the mark; an inner one mutates it; the outer sees it.
	outer := NewContext(context.Background())
	if Shielded(outer) {
		t.Fatal("freshly seeded context should not be shielded yet")
	}
	// Simulate the inner middleware receiving a derived context and marking it.
	inner := context.WithValue(outer, struct{ x int }{}, "csp-nonce")
	MarkShielded(inner)
	if !Shielded(outer) {
		t.Fatal("mark set on a derived context must be visible on the seeding context (shared pointer)")
	}
}
