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
	// Neither the original ops tools NOR the moved Health & governance items
	// (Monitoring, Governance, Storage & System, Security) appear in the sidebar.
	for _, gone := range []string{
		">System Modes<", ">Policy Inspector<", ">Topology<", ">Replay Explorer<", ">Fault Engine<", ">ADR Registry<",
		">Monitoring<", ">Governance<", ">Storage & System<", ">Security<",
	} {
		if strings.Contains(nav, gone) {
			t.Errorf("sidebar must not show %q (moved into the Operations hub)", gone)
		}
	}

	// Normal mode, healthy disk → no attention badge anywhere.
	normal := osOperationsGrid(mode.ModeNormal, 40)
	assertCSPSafe(t, "osOperationsGrid/normal", normal)
	for _, want := range []string{
		`href="/os/modes"`, `href="/os/policy"`, `href="/os/topology"`,
		`href="/os/replay"`, `href="/os/faults"`, `href="/os/adr"`,
		`href="/os/monitoring"`, `href="/os/governance"`, `href="/os/storage"`, `href="/os/security"`,
		"Operations",
	} {
		if !strings.Contains(normal, want) {
			t.Errorf("Operations hub missing %q", want)
		}
	}
	if strings.Contains(normal, "work-card__badge") {
		t.Error("normal mode + healthy disk must not show an attention badge")
	}
	// A non-normal mode badges the Modes card; high disk badges the Storage card.
	attn := osOperationsGrid(mode.ModeQuarantined, 88)
	if !strings.Contains(attn, `work-card__badge">quarantined<`) {
		t.Error("a non-normal mode must be badged on the System Modes card")
	}
	if !strings.Contains(attn, `work-card__badge">88%<`) {
		t.Error("high disk usage must be badged on the Storage & System card")
	}
}

// TestOptimizeHubConsolidatesSidebar verifies the circled Optimize items collapse
// into one pinned hub while the products stay pinned, and that the hub gates the
// admin-only Bot Shield card away from editors.
func TestOptimizeHubConsolidatesSidebar(t *testing.T) {
	nav := osSidebarNav("dashboard", &osSettings{AccessLevel: accessAdmin})

	if !strings.Contains(nav, ">Optimize<") {
		t.Error("sidebar must show the Optimize hub tab")
	}
	for _, gone := range []string{">SEO<", ">Analytics<", ">Bot Shield<", ">Theme Studio<", ">Theme Store<"} {
		if strings.Contains(nav, gone) {
			t.Errorf("sidebar must not show %q (moved into the Optimize hub)", gone)
		}
	}
	for _, keep := range []string{">VayuMail<", ">VayuTalk<", ">VayuTor<", "Products"} {
		if !strings.Contains(nav, keep) {
			t.Errorf("sidebar must still pin %q", keep)
		}
	}

	// Admins see every card, including the admin-only Bot Shield.
	admin := osOptimizeGrid(accessAdmin)
	assertCSPSafe(t, "osOptimizeGrid/admin", admin)
	for _, want := range []string{`href="/os/seo"`, `href="/os/analytics"`, `href="/os/shield"`, `href="/os/theme"`, `href="/os/theme/store"`} {
		if !strings.Contains(admin, want) {
			t.Errorf("admin Optimize hub missing %q", want)
		}
	}
	// Editors see the editor cards but NOT the admin-only Bot Shield.
	editor := osOptimizeGrid(accessEditor)
	if strings.Contains(editor, `href="/os/shield"`) {
		t.Error("editor must not see the admin-only Bot Shield card")
	}
	if !strings.Contains(editor, `href="/os/seo"`) {
		t.Error("editor must still see the SEO card")
	}
}
