// SPDX-License-Identifier: Apache-2.0

package mail

import "testing"

func TestIsPremiumLocalpart(t *testing.T) {
	// Ultra-short + curated handles are premium.
	for _, s := range []string{"a", "x", "me", "hi", "jo", "vip", "PRO", " boss ", "ceo", "money", "shop", "inbox", "chat"} {
		if !IsPremiumLocalpart(s) {
			t.Errorf("expected premium: %q", s)
		}
	}
	// Ordinary generic addresses are NOT premium and stay freely claimable.
	for _, s := range []string{"john", "jane.doe", "ankush", "team7", "john.smith", "hello123", "a1b2c3"} {
		if IsPremiumLocalpart(s) {
			t.Errorf("expected NOT premium: %q", s)
		}
	}
	// Empty is neither.
	if IsPremiumLocalpart("") || IsPremiumLocalpart("  ") {
		t.Error("blank must not be premium")
	}
}

// TestPremiumDoesNotOverlapReserved documents the intended layering: a name that
// is reserved is refused as reserved regardless of premium status, so the two
// sets are checked in order (reserved first) on the claim path. This test just
// guards that the curated premium seed stays distinct from reserved so the
// "premium" signal in the portal is never shown for an operator-only name.
func TestPremiumDoesNotOverlapReserved(t *testing.T) {
	for name := range premiumLocalparts {
		if IsReservedLocalpart(name) {
			t.Errorf("curated premium %q also appears in reserved — remove it from one set", name)
		}
	}
}
