package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mode"
)

// TestGrowthHubConsolidatesSidebar verifies the Audience + Monetization sidebar
// groups are collapsed into a single pinned Growth hub, and that the hub page
// surfaces each former item as a card (with live counts) linking to its page.
func TestGrowthHubConsolidatesSidebar(t *testing.T) {
	nav := osSidebarNav("dashboard", &osSettings{AccessLevel: accessAdmin})

	if !strings.Contains(nav, ">Growth<") {
		t.Error("clearnet sidebar must show the consolidated Growth hub tab")
	}
	// The consolidated items must no longer appear as their own sidebar rows.
	for _, gone := range []string{">Members<", ">Newsletter<", ">Monetization<", ">Advertising<", ">My Profile<"} {
		if strings.Contains(nav, gone) {
			t.Errorf("sidebar must not show %q (moved into the Growth hub)", gone)
		}
	}

	// The hub body carries each card, its link and the live member count.
	body := osGrowthGrid(1234, 56)
	assertCSPSafe(t, "osGrowthGrid", body)
	for _, want := range []string{
		`href="/os/members"`, `href="/os/newsletter"`, `href="/os/profile"`,
		`href="/os/monetization"`, `href="/os/ads"`,
		"1,234", // grouped member count
		"Growth",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Growth hub missing %q", want)
		}
	}
}

// TestOperationsHubConsolidatesSidebar verifies the Operations sidebar group is
// collapsed into one pinned hub tab, and that the hub page renders each tool as a
// card — including a status badge when the install is not in normal mode.
func TestOperationsHubConsolidatesSidebar(t *testing.T) {
	nav := osSidebarNav("dashboard", &osSettings{AccessLevel: accessAdmin})

	if !strings.Contains(nav, ">Operations<") {
		t.Error("clearnet sidebar must show the consolidated Operations hub tab")
	}
	for _, gone := range []string{">System Modes<", ">Policy Inspector<", ">Topology<", ">Replay Explorer<", ">Fault Engine<", ">ADR Registry<"} {
		if strings.Contains(nav, gone) {
			t.Errorf("sidebar must not show %q (moved into the Operations hub)", gone)
		}
	}

	// Normal mode → no attention badge on the Modes card.
	normal := osOperationsGrid(mode.ModeNormal)
	assertCSPSafe(t, "osOperationsGrid/normal", normal)
	for _, want := range []string{
		`href="/os/modes"`, `href="/os/policy"`, `href="/os/topology"`,
		`href="/os/replay"`, `href="/os/faults"`, `href="/os/adr"`, "Operations",
	} {
		if !strings.Contains(normal, want) {
			t.Errorf("Operations hub missing %q", want)
		}
	}
	if strings.Contains(normal, "work-card__badge") {
		t.Error("normal mode must not show an attention badge")
	}
	// A non-normal mode surfaces as a badge on the Modes card.
	quarantined := osOperationsGrid(mode.ModeQuarantined)
	if !strings.Contains(quarantined, `work-card__badge">quarantined<`) {
		t.Error("a non-normal mode must be badged on the System Modes card")
	}
}
