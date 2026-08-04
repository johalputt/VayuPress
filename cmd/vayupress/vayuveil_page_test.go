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
	return vayuVeilPage(enabled, vayuveil.Channels(), obs, checks,
		vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true}, nil)
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
		EnforcingPhases: map[vayuveil.Phase]bool{}, SelfHardening: self, RedTeam: red,
	})
	return vayuVeilPage(true, vayuveil.Channels(), obs, checks, self, red)
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

// The tile counts VERIFIED controls, and the count has to move with reality in
// both directions — a tile hardcoded either way is the thing to catch.
func TestTheVerifiedEnforcingTileCountsWhatIsActuallyVerified(t *testing.T) {
	// Kernel says undumpable but the core limit could not be read: ONE control is
	// verified, not two. Unverified must not be rounded up.
	on := statCardIn(t, veilPageWith(t,
		vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true}, nil), "Verified enforcing")
	if !strings.Contains(on, ">1<") {
		t.Errorf("one control is verified and the tile does not say so: %s", on)
	}
	// Both mechanisms verified: two.
	both := statCardIn(t, veilPageWith(t, vayuveil.SelfHardening{
		Supported: true, Known: true, Undumpable: true,
		CoreLimitKnown: true, CoreLimitZero: true,
	}, nil), "Verified enforcing")
	if !strings.Contains(both, ">2<") {
		t.Errorf("both controls are verified and the tile does not count both: %s", both)
	}
	if strings.Contains(on, "stat-card--warn") {
		t.Errorf("a verified control is toned as a problem: %s", on)
	}
	// Kernel says dumpable: nothing is enforcing, and that IS a problem.
	off := statCardIn(t, veilPageWith(t,
		vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: false}, nil), "Verified enforcing")
	if !strings.Contains(off, ">0<") {
		t.Errorf("nothing is enforcing and the tile does not read zero: %s", off)
	}
	if !strings.Contains(off, "stat-card--warn") {
		t.Errorf("zero verified controls is rendered as unremarkable: %s", off)
	}
	// Kernel could not be asked: unverified is NOT a pass.
	unk := statCardIn(t, veilPageWith(t,
		vayuveil.SelfHardening{Supported: false}, nil), "Verified enforcing")
	if !strings.Contains(unk, ">0<") {
		t.Errorf("an unverifiable platform is counted as enforcing: %s", unk)
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
		Name: "VayuVeil", MinTiles: 4, MinBands: 6,
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

// VayuVeil is reached from the Operations hub, not from the sidebar.
//
// It is install-scoped — this host's device nodes, display sockets and kernel
// tunables, plus what this process enforces about its own memory — so it belongs
// with the other install-level diagnostics under Health & governance. Optimize
// is where a SITE's reach and protection live, and filing it there would have
// implied it protects a site, which is the one thing this subsystem must never
// imply.
func TestVayuVeilIsReachedFromOperationsAndNotTheSidebar(t *testing.T) {
	nav := osSidebarNav("operations", &osSettings{AccessLevel: accessAdmin})
	if strings.Contains(nav, `/os/vayuveil`) {
		t.Error("VayuVeil is still pinned in the sidebar; it was asked to live under a hub instead")
	}
	ops := osOperationsGrid("", 0, false, "")
	if !strings.Contains(ops, `href="/os/vayuveil"`) {
		t.Fatal("the Operations hub does not link to VayuVeil, so it is now unreachable from any " +
			"navigation at all — a page with a route and no way in")
	}
	// Under Health & governance rather than Controls & diagnostics: it reports a
	// posture, it does not run or recover anything.
	i := strings.Index(ops, "Health &amp; governance")
	if i < 0 {
		i = strings.Index(ops, "Health & governance")
	}
	if i < 0 {
		t.Fatal("the Health & governance band is gone from the Operations hub")
	}
	if strings.Index(ops, `href="/os/vayuveil"`) < i {
		t.Error("VayuVeil sits above the Health & governance band, among the controls that run and " +
			"recover things; it reports a posture and does neither")
	}
}

// The hub that carries the VayuVeil card must itself be admin-only.
//
// Found during the pre-release pass, from a mistake rather than a defect: the
// card was added with osWorkCard's last argument set to true in the belief that
// it gated access. It does not — it accents the icon. The placement is safe only
// because /os/operations is admin-gated in its own right, and nothing was
// holding that. Now something is.
//
// If Operations were ever opened to editors, this fails rather than quietly
// showing every editor a link to a page enumerating the host's device nodes.
func TestTheHubCarryingTheVayuVeilCardIsAdminOnly(t *testing.T) {
	if got := osPathMinLevel("/os/operations"); got != accessAdmin {
		t.Errorf("/os/operations requires level %d, not admin (%d) — it renders a link to VayuVeil, "+
			"which maps this host's device nodes and kernel tunables", got, accessAdmin)
	}
	// Belt and braces: the destination is gated on its own, so a card rendered
	// somewhere unexpected still cannot open the page.
	if got := osPathMinLevel("/os/vayuveil"); got != accessAdmin {
		t.Errorf("/os/vayuveil requires level %d, not admin (%d)", got, accessAdmin)
	}
}
