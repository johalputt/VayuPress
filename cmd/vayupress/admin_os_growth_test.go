package main

import (
	"strings"
	"testing"
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
