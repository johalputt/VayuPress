// SPDX-License-Identifier: Apache-2.0

package settings

import "testing"

// These tests are written from the position of the operator who reported the
// defect this type exists to fix: they set up a hosted domain, opened Theme
// Studio, and found they were editing their own blog.

// The zero value must not address anything. A caller that reaches the store
// without saying whose settings it wants is a caller that forgot, and the cost
// of guessing on their behalf is one tenant's configuration served to another.
func TestAnUnsetScopeAddressesNothing(t *testing.T) {
	var s Scope
	if s.Valid() {
		t.Error("the zero Scope reports itself valid, so a caller who supplied no scope is " +
			"served as though they had")
	}
	if s.IsPrimary() {
		t.Error("the zero Scope reports as the primary — which is precisely the silent " +
			"inheritance this type exists to end")
	}
	if s.DomainID() != "" || s.key() != "" {
		t.Errorf("the zero Scope addresses %q/%q", s.DomainID(), s.key())
	}
	if s.String() != "unset" {
		t.Errorf("String() = %q, want \"unset\" — an audit line must say when nobody said", s.String())
	}
}

// An empty domain id must NOT resolve to the primary. "" is the primary's
// sentinel everywhere in this codebase, so a blank id silently resolving would
// hand a hosted domain the operator's own configuration — the exact shape of
// two separate defects already found in ADR-0152.
func TestABlankDomainIDIsRefusedRatherThanResolvedToThePrimary(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t"} {
		s := ForDomain(blank)
		if s.Valid() {
			t.Errorf("ForDomain(%q) produced a valid scope", blank)
		}
		if s.IsPrimary() {
			t.Errorf("ForDomain(%q) resolved to the PRIMARY. A hosted domain would be served "+
				"the operator's theme, SEO and newsletter settings, and the panel would look "+
				"like it was working", blank)
		}
	}
}

// The primary and a domain must never collide, and two domains must never
// collide with each other — the storage and cache key is derived from this.
func TestScopesAddressDistinctThings(t *testing.T) {
	primary := ForPrimary()
	if !primary.Valid() || !primary.IsPrimary() {
		t.Fatal("ForPrimary did not produce a valid primary scope")
	}
	// The primary's key stays "" so rows written before ADR-0153 keep their
	// meaning without being rewritten.
	if primary.key() != "" {
		t.Errorf("primary key = %q, want \"\" — existing rows must not need rewriting", primary.key())
	}

	a, b := ForDomain("d1"), ForDomain("d2")
	if !a.Valid() || !b.Valid() {
		t.Fatal("ForDomain refused a real id")
	}
	if a.key() == b.key() {
		t.Error("two domains share a storage key, so one would read and overwrite the other's settings")
	}
	if a.key() == primary.key() {
		t.Error("a hosted domain shares the primary's storage key")
	}
	if a.IsPrimary() {
		t.Error("a hosted domain reports as the primary")
	}
}

// The same domain named differently is the same domain. A scope that changed
// with the casing would address a row nothing else writes to, so a setting
// would appear to save and then vanish.
func TestTheSameDomainNamedTwoWaysIsOneScope(t *testing.T) {
	for _, pair := range [][2]string{
		{"client.example", "CLIENT.EXAMPLE"},
		{"client.example", "  client.example  "},
	} {
		if x, y := ForDomain(pair[0]), ForDomain(pair[1]); x.key() != y.key() {
			t.Errorf("%q and %q are one domain but address %q and %q", pair[0], pair[1], x.key(), y.key())
		}
	}
}

// A caller holding a Scope must not be able to reach a different one. The
// storage key is unexported for that reason; this pins the surface so a future
// convenience accessor does not quietly reopen it.
func TestAScopeCannotBeRetargetedByItsHolder(t *testing.T) {
	s := ForDomain("client.example")
	if got := s.DomainID(); got != "client.example" {
		t.Fatalf("DomainID() = %q", got)
	}
	// DomainID is a read of what was granted, not a way to build another scope:
	// re-deriving one from it must go back through ForDomain, which normalises
	// and refuses blanks.
	if again := ForDomain(s.DomainID()); again.key() != s.key() {
		t.Error("a scope rebuilt from its own DomainID does not address the same settings")
	}
}
