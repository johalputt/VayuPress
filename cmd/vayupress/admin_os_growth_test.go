// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
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

	// The clean Growth hub launches Audience + Monetization; the revenue controls,
	// KPIs and premium marketplace live inside Monetization, not here.
	body := osGrowthGrid(1234, 56, 3)
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
	// The monetization control surfaces must NOT be duplicated on the Growth hub.
	for _, gone := range []string{"Premium mail-ID marketplace", "Orders — audit ledger", "Revenue collected"} {
		if strings.Contains(body, gone) {
			t.Errorf("Growth hub must not carry monetization control %q (moved to Monetization)", gone)
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
	normal := osOperationsGrid(mode.ModeNormal, 40, false)
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
	attn := osOperationsGrid(mode.ModeQuarantined, 88, false)
	if !strings.Contains(attn, `work-card__badge">quarantined<`) {
		t.Error("a non-normal mode must be badged on the System Modes card")
	}
	if !strings.Contains(attn, `work-card__badge">88%<`) {
		t.Error("high disk usage must be badged on the Storage & System card")
	}
}

// TestOptimizeHubConsolidatesSidebar verifies the Optimize hub now fronts BOTH the
// original optimize items and the config surfaces (Tools/Domains/Settings/VayuAPI/
// VayuMCP), that the sidebar is flat/label-less (no Products/System headings), and
// that the hub gates admin-only cards away from editors.
func TestOptimizeHubConsolidatesSidebar(t *testing.T) {
	nav := osSidebarNav("dashboard", &osSettings{AccessLevel: accessAdmin})

	if !strings.Contains(nav, ">Optimize<") {
		t.Error("sidebar must show the Optimize hub tab")
	}
	// Moved into hubs — must NOT be sidebar rows anymore.
	for _, gone := range []string{
		">SEO<", ">Analytics<", ">Bot Shield<", ">Theme Studio<", ">Theme Store<",
		">Tools & Plugins<", ">Domains<", ">Settings<", ">API Keys<", ">VayuMCP<",
	} {
		if strings.Contains(nav, gone) {
			t.Errorf("sidebar must not show %q (moved into a hub)", gone)
		}
	}
	// The "Products" and "System" section labels are gone (flat sidebar).
	for _, label := range []string{"sidebar-section-label\">Products<", "sidebar-section-label\">System<"} {
		if strings.Contains(nav, label) {
			t.Errorf("sidebar must not render the %q heading anymore", label)
		}
	}
	// Still pinned (label-less): the products + Update & Backup (href-checked to
	// dodge the &amp; escaping in the label).
	for _, keep := range []string{">VayuMail<", ">VayuTalk<", ">VayuTor<", `href="/os/update"`} {
		if !strings.Contains(nav, keep) {
			t.Errorf("sidebar must still pin %q", keep)
		}
	}

	// Admins see every card, including the config surfaces and the renamed VayuAPI.
	admin := osOptimizeGrid(accessAdmin, nil)
	assertCSPSafe(t, "osOptimizeGrid/admin", admin)
	for _, want := range []string{
		`href="/os/seo"`, `href="/os/analytics"`, `href="/os/shield"`, `href="/os/theme"`, `href="/os/theme/store"`,
		`href="/os/tools"`, `href="/os/domains"`, `href="/os/settings"`, `href="/os/apikeys"`, `href="/os/connector"`,
		">VayuAPI<", // API Keys renamed
	} {
		if !strings.Contains(admin, want) {
			t.Errorf("admin Optimize hub missing %q", want)
		}
	}
	// Editors see the editor cards but NOT the admin-only ones (Bot Shield, config).
	editor := osOptimizeGrid(accessEditor, nil)
	for _, deny := range []string{`href="/os/shield"`, `href="/os/settings"`, `href="/os/apikeys"`} {
		if strings.Contains(editor, deny) {
			t.Errorf("editor must not see admin-only card %q", deny)
		}
	}
	if !strings.Contains(editor, `href="/os/seo"`) {
		t.Error("editor must still see the SEO card")
	}
}

// TestTorSystemHub verifies the Tor console's System group is consolidated into a
// pinned System hub tab (design parity) and the hub renders its cards.
func TestTorSystemHub(t *testing.T) {
	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	defer func() { config.Cfg.OnionMode = prev }()

	nav := osSidebarNav("dashboard", &osSettings{AccessLevel: accessAdmin})
	if !strings.Contains(nav, ">System<") {
		t.Error("Tor sidebar must show the consolidated System hub tab")
	}
	for _, gone := range []string{">Storage & System<", ">My Profile<"} {
		if strings.Contains(nav, gone) {
			t.Errorf("Tor sidebar must not show %q (moved into the System hub)", gone)
		}
	}
	body := osSystemGrid(accessAdmin)
	assertCSPSafe(t, "osSystemGrid", body)
	for _, want := range []string{`href="/os/storage"`, `href="/os/settings"`, `href="/os/profile"`, "System"} {
		if !strings.Contains(body, want) {
			t.Errorf("System hub missing %q", want)
		}
	}
}
