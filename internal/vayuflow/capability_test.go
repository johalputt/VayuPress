// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"strings"
	"testing"
)

// The registry's whole purpose: a registration missing any answer fails a test,
// not a review.
func TestEveryRegisteredCapabilityIsComplete(t *testing.T) {
	all := Capabilities()
	if len(all) == 0 {
		t.Fatal("the registry is empty; this test is proving nothing")
	}
	for _, c := range all {
		if err := c.Complete(); err != nil {
			t.Errorf("%s: %v", c.Action, err)
		}
	}
}

// Each case removes exactly one answer, so a failure names the field that was
// supposed to be required rather than "invalid capability".
func TestACapabilityMissingAnyAnswerIsRefused(t *testing.T) {
	good := Capability{
		Action: "test.action", Kind: KindContent, Writes: WriteDraft,
		Onion: OnionActive, Undo: ReversibleByOperator, MinRole: RoleEditor,
		Rationale: "a test fixture",
	}
	if err := good.Complete(); err != nil {
		t.Fatalf("the fixture must itself be complete, or every case below passes for the wrong reason: %v", err)
	}
	for _, tc := range []struct {
		name string
		bend func(*Capability)
		want string
	}{
		{"no action name", func(c *Capability) { c.Action = "" }, "no Action name"},
		{"no kind", func(c *Capability) { c.Kind = kindUnset }, "KIND"},
		{"no write ceiling", func(c *Capability) { c.Writes = writeUnset }, "WRITE ceiling"},
		{"no onion policy", func(c *Capability) { c.Onion = onionUnset }, "VAYUOS_MODE=tor"},
		{"no reversibility", func(c *Capability) { c.Undo = reversibleUnset }, "undo"},
		{"no min role", func(c *Capability) { c.MinRole = "" }, "MinRole"},
		{"client as min role", func(c *Capability) { c.MinRole = "client" }, "MinRole"},
		{"no rationale", func(c *Capability) { c.Rationale = "" }, "rationale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.bend(&c)
			err := c.Complete()
			if err == nil {
				t.Fatalf("a capability with %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal should name %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestAdminKindHasNoMembers pins the ADR's decision that settings, users, keys,
// domains, VayuShield tiers and payment config are NOT automatable in v1.
//
// The registry exists so that this is a visible, testable emptiness rather than
// an absence nobody audited. If a later change registers an admin action, this
// test fails and the decision gets re-made deliberately instead of drifting.
func TestAdminKindHasNoMembers(t *testing.T) {
	if got := CapabilitiesOfKind(KindAdmin); len(got) != 0 {
		var names []string
		for _, c := range got {
			names = append(names, c.Action)
		}
		t.Errorf("KindAdmin has %d member(s): %s\n"+
			"ADR-0151 says settings, users, keys, domains, shield tiers and payment config are not "+
			"automatable in v1. Adding one is a decision to re-make in the ADR, not a test to update.",
			len(got), strings.Join(names, ", "))
	}
}

// An action the registry does not know about cannot be invoked. That is the
// difference between a registry and a function map.
func TestAnUnregisteredActionCannotBeResolved(t *testing.T) {
	if _, err := CapabilityFor("content.publish.now"); err == nil {
		t.Fatal("an unregistered action resolved to a capability")
	}
	if _, err := CapabilityFor(""); err == nil {
		t.Fatal("the empty action resolved to a capability")
	}
}

// Every content action in v1 is capped at draft. This is the P3 gate stated at
// the type level, and it is checked here so that adding a live-writing content
// action is a deliberate act with a failing test in front of it.
func TestNoContentActionCanWriteLiveInV1(t *testing.T) {
	for _, c := range CapabilitiesOfKind(KindContent) {
		if !c.Writes.atMost(WriteDraft) {
			t.Errorf("%s writes at %s; v1 content actions are capped at draft", c.Action, c.Writes)
		}
	}
}

// Every egress action must be inert in a Tor Space. An outbound callback is
// precisely what ADR-0141 exists to prevent, and this is the registry making
// that testable rather than relying on each call site staying correct.
func TestEveryEgressActionIsInertUnderTor(t *testing.T) {
	for _, c := range CapabilitiesOfKind(KindEgress) {
		if c.Onion != OnionInert {
			t.Errorf("%s is an egress action but declares Onion=%s; it must be inert in a Tor Space",
				c.Action, c.Onion)
		}
	}
}

// Mail cannot be recalled. A registration that claimed otherwise would drive a
// panel that offers an undo button for a message already delivered.
func TestMailIsNeverAdvertisedAsReversible(t *testing.T) {
	for _, c := range CapabilitiesOfKind(KindMail) {
		if c.Undo != Irreversible {
			t.Errorf("%s sends mail but declares Undo=%s; delivered mail cannot be recalled",
				c.Action, c.Undo)
		}
	}
}

// atMost must not treat an unset policy as satisfying anything. A ceiling
// comparison that silently admitted the zero value would reinstate the hole the
// unset-is-invalid pattern exists to close.
func TestAnUnsetWritePolicySatisfiesNoCeiling(t *testing.T) {
	if writeUnset.atMost(WriteLive) {
		t.Error("an unset write policy satisfied the most permissive ceiling")
	}
	if WriteNone.atMost(writeUnset) {
		t.Error("an unset ceiling admitted a real policy")
	}
	if !WriteDraft.atMost(WriteLive) {
		t.Error("draft should be within a live ceiling")
	}
	if WriteLive.atMost(WriteDraft) {
		t.Error("live must NOT be within a draft ceiling — this is the clamp §6 relies on")
	}
}
