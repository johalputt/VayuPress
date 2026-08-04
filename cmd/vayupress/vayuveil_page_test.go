// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_page_test.go — the page is held to what it may CLAIM, first.
//
// A privacy console has one catastrophic failure mode and it is not a layout
// bug: it is a person reading the page, believing their screen is protected, and
// then typing a seed phrase in front of a compromised machine. ADR-0150 §8 exists
// to stop that, and §8 is only worth the tests under it.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuveil"
	"github.com/johalputt/vayupress/internal/veilaudit"
)

func veilPageFor(t *testing.T, enabled bool, presence vayuveil.Presence) string {
	t.Helper()
	obs := map[vayuveil.ChannelID]vayuveil.Observation{}
	for _, c := range vayuveil.Channels() {
		obs[c.ID] = vayuveil.Observation{Presence: presence, Detail: "probe detail"}
	}
	checks := veilaudit.Run(veilaudit.Inputs{
		Enabled: enabled, Channels: vayuveil.Channels(), Observations: obs,
		EnforcingPhases: map[vayuveil.Phase]bool{},
	})
	return vayuVeilPage(enabled, vayuveil.Channels(), obs, checks)
}

// THE test for this page. No wording anywhere may tell a reader that anything is
// protected, because at P0 nothing is.
func TestThePageNeverClaimsProtectionItDoesNotHave(t *testing.T) {
	// Scoped to the page ABOVE the disclaimer band, because that band's whole job
	// is to print these phrases with "Not" in front of them. A whole-page search
	// finds "screenshot-proof" inside the sentence disclaiming it and fails on
	// correct copy — the same defect this console has produced repeatedly, an
	// assertion that cannot tell which element it matched. It caught this one too.
	const disclaimer = "What VayuVeil will never claim"
	for _, page := range []string{
		veilPageFor(t, true, vayuveil.PresenceAbsent),
		veilPageFor(t, true, vayuveil.PresentReachable),
		veilPageFor(t, false, vayuveil.PresenceAbsent),
	} {
		body := page
		if i := strings.Index(page, disclaimer); i > 0 {
			body = page[:i]
		}
		low := strings.ToLower(body)
		for _, forbidden := range []string{
			"screenshot-proof", "screenshot proof", "cannot be captured",
			"your screen is protected", "fully protected", "impossible to capture",
		} {
			if strings.Contains(low, forbidden) {
				t.Errorf("the page asserts %q outside the band that disclaims it — ADR-0150 §8 "+
					"forbids exactly this claim", forbidden)
			}
		}
	}

	// And the converse, so the scoping above cannot be satisfied by simply moving
	// a claim into the disclaimer band: that band must NEGATE each phrase.
	full := veilPageFor(t, true, vayuveil.PresenceAbsent)
	i := strings.Index(full, disclaimer)
	if i < 0 {
		t.Fatal("the disclaimer band is gone from the page entirely")
	}
	band := full[i:]
	if !strings.Contains(band, "Not &ldquo;screenshot-proof&rdquo;") {
		t.Error("the disclaimer band no longer refuses the screenshot-proof claim by name")
	}
}

// The phase boundary must be the first thing a reader meets, not a footnote.
// Someone who reads only the lede has to come away knowing nothing is enforced.
func TestThePhaseBoundaryIsStatedBeforeAnythingElse(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresenceAbsent)
	lede := page
	if i := strings.Index(page, `class="stat-grid"`); i > 0 {
		lede = page[:i]
	}
	if !strings.Contains(lede, "enforces none of them") {
		t.Error("the lede does not say that Phase 0 enforces nothing, so a reader who stops after " +
			"the first paragraph believes this page describes a defence")
	}
}

// The "Enforcing" tile is the one a person's eye goes to. It must read zero and
// it must not read as good news.
func TestTheEnforcingTileReadsZeroAndIsNotToneGreen(t *testing.T) {
	tile := statCardIn(t, veilPageFor(t, true, vayuveil.PresenceAbsent), "Enforcing")
	if !strings.Contains(tile, ">0<") {
		t.Errorf("the enforcing tile does not read zero while nothing is enforcing: %s", tile)
	}
	if !strings.Contains(tile, "stat-card--warn") {
		t.Errorf("zero controls enforcing is rendered as unremarkable state: %s", tile)
	}
}

// The switch is the control the operator was promised. It has to exist, be
// bound, and say what it does NOT do.
func TestTheActivateControlExistsIsBoundAndStatesItsLimits(t *testing.T) {
	off := veilPageFor(t, false, vayuveil.PresenceAbsent)
	if !strings.Contains(off, `data-veil-toggle="1"`) {
		t.Fatal("an inactive install offers no way to activate VayuVeil")
	}
	if !strings.Contains(off, ">Activate<") {
		t.Error("the control does not say what pressing it will do")
	}
	on := veilPageFor(t, true, vayuveil.PresenceAbsent)
	if !strings.Contains(on, `data-veil-toggle="0"`) || !strings.Contains(on, ">Deactivate<") {
		t.Error("an active install offers no way to deactivate it")
	}
	// Bound, not merely rendered — the defect this console produced once already.
	if !strings.Contains(vayuVeilScript, "data-veil-toggle") ||
		!strings.Contains(vayuVeilScript, "if(btn)btn.addEventListener('click',") {
		t.Error("the toggle is rendered but no click listener is attached, so it does nothing")
	}
	if !strings.Contains(vayuVeilScript, "/os/api/vayuveil/toggle") {
		t.Error("the toggle posts to no endpoint")
	}
	// And the copy beside it must refuse the obvious misreading.
	if !strings.Contains(off, "does not protect anything") {
		t.Error("the switch does not tell the operator that activating it protects nothing, which " +
			"is the single most likely thing for them to assume")
	}
}

// Every permanent limit from §8 has to be visible on the page, not just in the
// package. A boundary recorded where nobody reads it is not a boundary.
func TestThePageNamesEveryThingItWillNeverClaim(t *testing.T) {
	low := strings.ToLower(veilPageFor(t, true, vayuveil.PresenceAbsent))
	for _, must := range []string{"root", "kernel", "firmware", "camera", "hdmi", "compositor", "recall"} {
		if !strings.Contains(low, must) {
			t.Errorf("the page never mentions %q, so the boundary is not stated where an operator reads it", must)
		}
	}
}

// A channel open on this host is the actionable finding. It must reach the page.
func TestAnOpenChannelIsVisibleOnThePage(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresentReachable)
	if !strings.Contains(page, "open</span>") {
		t.Error("no row is chipped as open on a host where every channel is reachable")
	}
	tile := statCardIn(t, page, "Open on this host")
	if strings.Contains(tile, ">0<") {
		t.Error("the tile reports nothing open while every channel is reachable")
	}
	if !strings.Contains(tile, "stat-card--warn") {
		t.Error("open channels are toned as ordinary state")
	}
}

// House style and the two rendering gates every VayuOS page is held to.
func TestTheVayuVeilPageMeetsTheHouseStyle(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresenceAbsent)
	assertHouseStyle(t, page, houseStyle{
		Name: "VayuVeil", MinTiles: 4, MinBands: 4,
		IDs:   []string{"veil-status"},
		Hooks: []string{"data-veil-toggle"},
	})
	assertCSPSafe(t, "VayuVeil", page)
	assertClassesAreStyled(t, "the VayuVeil page", loadAdminOSCSS(t), page)
}

// The registry table has to show the actual obligations, or the page is a list
// of names and the contract is invisible.
func TestTheRegistryTableShowsEveryChannelsObligations(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresenceAbsent)
	for _, c := range vayuveil.Channels() {
		if !strings.Contains(page, string(c.ID)) {
			t.Errorf("channel %q is registered and not shown on the page", c.ID)
		}
	}
	for _, want := range []string{"absent (not built in)", "deny", "grant only",
		"compositor-drawn", "panel only", "every attempt", "pinned, listed"} {
		if !strings.Contains(page, want) {
			t.Errorf("the registry table never renders %q, so that obligation is invisible", want)
		}
	}
}
