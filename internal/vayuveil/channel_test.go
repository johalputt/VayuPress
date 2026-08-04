// SPDX-License-Identifier: Apache-2.0

package vayuveil

// channel_test.go — the three properties ADR-0150 §3.1 asks the registry to have.
//
// These are the whole value of the construction. Without them the registry is a
// list of good intentions that compiles.

import (
	"strings"
	"testing"
)

// Property 2 — completeness. A channel added without deciding its policy must
// not pass. The zero value of every obligation is invalid, so an author who
// omits a field gets a failure rather than a default nobody chose.
func TestEveryChannelAnswersEveryObligation(t *testing.T) {
	for _, c := range Channels() {
		if !c.Complete() {
			t.Errorf("channel %q is incomplete: an obligation is unanswered or the answers "+
				"contradict each other. A channel whose policy nobody decided is a loophole with "+
				"a name.", c.ID)
		}
	}
}

// Uniqueness. Two rows for one interface means one of them is being read and the
// other is being ignored, and nothing says which.
func TestEveryChannelIDAppearsExactlyOnce(t *testing.T) {
	seen := map[ChannelID]int{}
	for _, c := range Channels() {
		seen[c.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("channel %q appears %d times; one of those rows is dead policy", id, n)
		}
	}
}

// Property 3 — default-deny. There is no disposition that permits observation
// without a person saying yes, and no grantable channel without an indicator.
func TestNoChannelAllowsObservationWithoutAsking(t *testing.T) {
	for _, c := range Channels() {
		if c.Default == DispositionGrant && c.Grant == GrantNone {
			t.Errorf("channel %q is reachable but has no grant model — that is silent observation "+
				"written down as policy", c.ID)
		}
		if c.Default == DispositionGrant && c.Indicator == IndicatorNone {
			t.Errorf("channel %q can be granted and shows nothing while in use", c.ID)
		}
		if c.Default == DispositionGrant && c.Audit == AuditNone {
			t.Errorf("channel %q can be granted and records nothing", c.ID)
		}
	}
}

// Property 1 — exhaustiveness, in the form this binary can actually assert.
//
// The ADR's full version diffs a generated inventory of the running system
// against the registry. That needs a live desktop, so what is pinned here is the
// other direction: every asset ADR-0150 §2 enumerates has at least one channel,
// and every interface §3.2 names is registered. Deleting a channel fails.
//
// This is weaker than the ADR's version and is labelled as such rather than
// dressed up: it proves nothing was dropped, not that nothing was missed.
func TestEveryEnumeratedAssetHasAChannel(t *testing.T) {
	covered := map[Asset]bool{}
	for _, c := range Channels() {
		covered[c.Asset] = true
	}
	for _, a := range []Asset{
		AssetFramebuffer, AssetWindowPixels, AssetWindowText, AssetKeyboard,
		AssetPointer, AssetClipboard, AssetNotifications, AssetWindowMetadata,
		AssetThumbnails, AssetMemoryImage,
	} {
		if !covered[a] {
			t.Errorf("asset %d is enumerated in ADR-0150 §2 and has no channel. An unenumerated "+
				"asset is an unprotected one; an enumerated one with no channel is worse, because "+
				"the list implies it was handled.", a)
		}
	}
}

func TestEveryInterfaceNamedByTheADRIsRegistered(t *testing.T) {
	have := map[ChannelID]bool{}
	for _, c := range Channels() {
		have[c.ID] = true
	}
	for _, id := range []ChannelID{
		"wlr-screencopy", "wlr-export-dmabuf", "xdg-portal-screencast",
		"dev-fb", "dev-dri", "x11-root-read", "xwayland-shared",
		"at-spi", "dev-input", "virtual-keyboard", "input-method", "pointer-position",
		"data-control", "notification-bodies", "window-metadata", "thumbnailer",
		"proc-pid-mem", "core-dumps", "hibernate-image", "vayuos-own-capture",
	} {
		if !have[id] {
			t.Errorf("channel %q is named in ADR-0150 and is not in the registry", id)
		}
	}
}

// VayuOS gets no exemption from its own policy. §3.3 states this as a
// constitutional constraint precisely because the product is the thing best
// placed to violate it, and a constraint nobody tests is a sentence.
func TestVayuOSsOwnCapturePathIsSubjectToTheSameRules(t *testing.T) {
	var own *Channel
	for _, c := range Channels() {
		if c.ID == "vayuos-own-capture" {
			cc := c
			own = &cc
		}
	}
	if own == nil {
		t.Fatal("VayuOS's own capture path is not registered, which is the one exemption the ADR " +
			"forbids by name")
	}
	if !own.Complete() {
		t.Error("VayuOS's own channel does not answer the obligations it imposes on everything else")
	}
	if own.Audit != AuditAll {
		t.Error("VayuOS's own capture path records less than it requires of others")
	}
	if own.Grant == GrantNone && own.Default == DispositionGrant {
		t.Error("VayuOS's own path is reachable without asking")
	}
}

// Complete() must reject each obligation individually. Without this the function
// could be satisfied by any one field and would pass the registry unchanged.
func TestCompleteRejectsEachMissingObligationOnItsOwn(t *testing.T) {
	valid := Channel{
		ID: "x", Name: "x", Asset: AssetFramebuffer, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP1Screen, Rationale: "because",
	}
	if !valid.Complete() {
		t.Fatal("the fixture is not valid, so every case below proves nothing")
	}
	for name, break_ := range map[string]func(Channel) Channel{
		"no asset":       func(c Channel) Channel { c.Asset = assetUnset; return c },
		"no disposition": func(c Channel) Channel { c.Default = dispositionUnset; return c },
		"no grant model": func(c Channel) Channel { c.Grant = grantUnset; return c },
		"no indicator":   func(c Channel) Channel { c.Indicator = indicatorUnset; return c },
		"no audit level": func(c Channel) Channel { c.Audit = auditUnset; return c },
		"no phase":       func(c Channel) Channel { c.Phase = phaseUnset; return c },
		"no name":        func(c Channel) Channel { c.Name = "  "; return c },
		"no id":          func(c Channel) Channel { c.ID = " "; return c },
		"no rationale":   func(c Channel) Channel { c.Rationale = ""; return c },
		"grantable with no way to say yes": func(c Channel) Channel {
			c.Default = DispositionGrant
			return c
		},
		"a grant model on an unreachable channel": func(c Channel) Channel {
			c.Grant = GrantPerUse
			return c
		},
		"silent on a channel this install can see": func(c Channel) Channel {
			c.Audit = AuditNone
			return c
		},
	} {
		if break_(valid).Complete() {
			t.Errorf("Complete() accepts a channel with %s", name)
		}
	}
}

// Every rationale has to say something. A one-word rationale passes a non-empty
// check and tells a later reader nothing about why the disposition is right,
// which is the only thing the field is for.
func TestEveryRationaleIsAnActualExplanation(t *testing.T) {
	for _, c := range Channels() {
		if n := len(strings.Fields(c.Rationale)); n < 12 {
			t.Errorf("channel %q has a %d-word rationale. The field exists so a reader can check "+
				"the policy against the reasoning later; a placeholder defeats it.", c.ID, n)
		}
	}
}
