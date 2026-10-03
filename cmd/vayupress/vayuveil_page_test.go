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
	"time"

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
		Enforced: map[vayuveil.Needs]bool{},
	})
	return vayuVeilPage(enabled, vayuveil.Channels(), obs, checks,
		vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true}, nil,
		vayuveil.HardenState{}, vayuveil.SandboxState{}, time.Time{}, time.Time{}, "")
}

// veilPageWith renders the page with a given hardening state and suite run, for
// the assertions that are about exactly those.
func veilPageWith(t *testing.T, self vayuveil.SelfHardening, red []vayuveil.AttackResult) string {
	t.Helper()
	obs := map[vayuveil.ChannelID]vayuveil.Observation{}
	for _, c := range vayuveil.Channels() {
		obs[c.ID] = vayuveil.Observation{Presence: vayuveil.PresenceAbsent, Detail: "probe detail"}
	}
	checks := veilaudit.Run(veilaudit.Inputs{
		Enabled: true, Channels: vayuveil.Channels(), Observations: obs,
		Enforced: map[vayuveil.Needs]bool{}, SelfHardening: self, RedTeam: red,
	})
	return vayuVeilPage(true, vayuveil.Channels(), obs, checks, self, red,
		vayuveil.HardenState{}, vayuveil.SandboxState{}, time.Time{}, time.Time{}, "")
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
// Someone who reads only the state line has to come away knowing nothing is
// enforced.
func TestThePhaseBoundaryIsStatedBeforeAnythingElse(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		if head := veilHead(t, veilPageFor(t, enabled, vayuveil.PresenceAbsent)); !strings.Contains(head, "enforces none of them") {
			t.Errorf("enabled=%v: the state line does not say that Phase 0 enforces nothing, so a reader who "+
				"stops there believes this page describes a defence", enabled)
		}
	}
}

// veilHead is the page's state line, everything before its first section, so
// an assertion about it cannot be met by a row further down.
func veilHead(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `class="sa-status__head`)
	j := strings.Index(page, "Observation control")
	if i < 0 || j < i {
		t.Fatal("the page has no state line before its first section")
	}
	return page[i:j]
}

// The state line counts VERIFIED controls, and the count has to move with
// reality in both directions; a count hardcoded either way is the thing to catch.
func TestTheStateCountsWhatIsActuallyVerified(t *testing.T) {
	for _, c := range []struct {
		name  string
		self  vayuveil.SelfHardening
		count string
		warn  bool
	}{
		// Kernel says undumpable but the core limit could not be read: ONE control
		// is verified, not two. Unverified must not be rounded up.
		{"one", vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true}, "1 verified enforcing", false},
		{"both", vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true, CoreLimitKnown: true, CoreLimitZero: true}, "2 verified enforcing", false},
		// Kernel says dumpable: nothing is enforcing, and that IS a problem.
		{"dumpable", vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: false}, "Nothing verified enforcing", true},
		// Kernel could not be asked: unverified is NOT a pass.
		{"unknown", vayuveil.SelfHardening{Supported: false}, "Nothing verified enforcing", true},
	} {
		head := veilHead(t, veilPageWith(t, c.self, nil))
		if !strings.Contains(head, c.count) {
			t.Errorf("%s: the state line does not say %q: %s", c.name, c.count, head)
		}
		if warned := strings.Contains(head, "sa-status__head--warn"); warned != c.warn {
			t.Errorf("%s: the state line is toned warn=%v, want %v", c.name, warned, c.warn)
		}
		if strings.Contains(head, "sa-status__head--ok") {
			t.Errorf("%s: an observation console is marked ok, which reads as protection it does not give", c.name)
		}
		// The permanent limits are always open by construction: they are the
		// boundary, listed apart, not findings that make a host read open.
		if c.name == "one" && !strings.Contains(head, "Nothing open on this host") {
			t.Errorf("the permanent limits are counted as open findings on this host: %s", head)
		}
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
	// And the copy beside it must refuse the obvious misreading. The wording is
	// scoped to the SWITCH — "activating it protects nothing" — rather than to
	// VayuVeil as a whole, because the process hardening below genuinely does
	// protect something and a blanket denial would be false in the other
	// direction. Under-claiming is a claim defect too.
	if !strings.Contains(off, "Activating it protects nothing") {
		t.Error("the switch does not tell the operator that activating it protects nothing, which " +
			"is the single most likely thing for them to assume")
	}
	if !strings.Contains(off, "turning it off exposes") {
		t.Error("the switch does not say that deactivating it exposes nothing either")
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

// A channel open on this host is the actionable finding. It must reach the
// page, in its row and in the state line, as a warning.
func TestAnOpenChannelIsVisibleOnThePage(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresentReachable)
	if !strings.Contains(page, ">Open</span>") {
		t.Error("no row reads open on a host where every channel is reachable")
	}
	head := veilHead(t, page)
	if !strings.Contains(head, "open on this host") || strings.Contains(head, "Nothing open") {
		t.Errorf("the state line does not say channels are open: %s", head)
	}
	if !strings.Contains(head, "sa-status__head--warn") {
		t.Error("open channels are toned as ordinary state")
	}
}

// House style and the two rendering gates every VayuOS page is held to.
func TestTheVayuVeilPageMeetsTheHouseStyle(t *testing.T) {
	page := veilPageFor(t, true, vayuveil.PresenceAbsent)
	assertHouseStyle(t, page, houseStyle{
		Name:  "VayuVeil",
		IDs:   []string{"veil-status"},
		Hooks: []string{"data-veil-toggle"},
	})
	assertCSPSafe(t, "VayuVeil", page)
	assertClassesAreStyled(t, "the VayuVeil page", loadConsoleCSS(t), page)
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

// VayuVeil is reached from the Shield app, and only by an administrator.
//
// It is install-scoped — this host's device nodes, display sockets and kernel
// tunables, plus what this process enforces about its own memory — and it
// enumerates things no editor should see. The rail is judged against the route
// guard elsewhere; this pins where it lives and that the guard itself is admin,
// so opening Shield to editors fails here rather than quietly showing them a
// map of the host.
func TestVayuVeilIsReachedFromShieldByAnAdministratorOnly(t *testing.T) {
	offers := func(s *osSettings) string {
		for _, app := range saVisibleApps(s) {
			for _, sec := range app.Sections {
				if sec.Href == "/os/vayuveil" {
					return app.Key
				}
			}
		}
		return ""
	}
	if got := offers(saSession(accessAdmin)); got != "shield" {
		t.Errorf("an administrator reaches VayuVeil from %q, want the Shield app", got)
	}
	if got := offers(saSession(accessEditor)); got != "" {
		t.Errorf("an editor is offered VayuVeil from %q", got)
	}
	if got := osPathMinLevel("/os/vayuveil"); got != accessAdmin {
		t.Errorf("/os/vayuveil requires level %d, not admin (%d)", got, accessAdmin)
	}
}
