// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// TestDomainManagerBrandingEscapes pins the CWE-116 successor guarantee for the
// per-site manager: a hostile brand value is prefilled into an html-escaped
// value="" attribute (never interpolated raw into markup or the inline script),
// so it can never break out. The old multi-domain brand form carried the map in a
// data attribute; the per-site page is even simpler — direct, escaped values.
func TestDomainManagerBrandingEscapes(t *testing.T) {
	brand := domain.Brand{SiteName: `"><script>x`, Tagline: "Shop"}
	cfg, err := domain.EncodeBrandConfig(brand)
	if err != nil {
		t.Fatalf("EncodeBrandConfig: %v", err)
	}
	d := domain.Domain{
		ID: "s1", Host: "shop.example", SiteType: domain.SiteBlog,
		Status: domain.StatusActive, ConfigJSON: cfg,
	}

	page := domainManagePage(d, 3, 2, 0, true)
	assertCSPSafe(t, "domainManagePage", page)

	// The hostile value must be escaped — the `">` breakout sequence must not
	// survive into the markup, and the raw <script> must never appear.
	if strings.Contains(page, `"><script>`) {
		t.Fatalf("hostile brand value broke out of the attribute:\n%s", page)
	}
	// The branding fields are prefilled and the save/reset controls are present.
	for _, want := range []string{`value="Shop"`, "data-site-brand-save", "data-site-brand-clear", "data-site-assign"} {
		if !strings.Contains(page, want) {
			t.Errorf("manager missing %q", want)
		}
	}
	// The live-view link uses the site's own origin (https for a clearnet host).
	if !strings.Contains(page, `href="https://shop.example"`) {
		t.Errorf("manager should link to the site's public origin:\n%s", page)
	}
}

// TestDomainManageScriptReadsIDFromNode verifies the manager script reads the
// domain id from a hidden data node (CSP-safe) and wires every control, rather
// than interpolating anything raw into the script body.
func TestDomainManageScriptReadsIDFromNode(t *testing.T) {
	script := domainManageScript("nonce123")
	if !strings.Contains(script, `nonce="nonce123"`) {
		t.Error("script must carry the CSP nonce")
	}
	if !strings.Contains(script, "getElementById('dom-manage')") {
		t.Error("script should read the domain id from the data node")
	}
	for _, want := range []string{
		"[data-site-brand-save]", "[data-site-brand-clear]", "[data-site-assign]",
		"[data-site-sync]", "[data-site-toggle]", "[data-site-delete]",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing handler for %q", want)
		}
	}
}

// TestDomainManagePrimaryHidesEditor is a light guard that the manager never
// renders the branding editor's breakout risk for a pending Tor site (its host
// is a placeholder, not a real address, so no live-view link is offered).
func TestDomainManagePendingTorSite(t *testing.T) {
	d := domain.Domain{ID: "t1", Host: torSitePending + "abc.local", SiteType: domain.SiteBlog, Status: domain.StatusActive}
	page := domainManagePage(d, 0, 0, 0, false)
	assertCSPSafe(t, "domainManagePage/pending", page)
	if strings.Contains(page, "View site") {
		t.Error("a pending Tor site has no address yet — no live-view link")
	}
	if !strings.Contains(page, "Minting .onion…") {
		t.Error("a pending Tor site should show the minting state")
	}
}

// TestOptimizeWebsitesCards verifies the Optimize hub surfaces one "Your
// websites" card per secondary domain, each linking to that site's manager, and
// shows nothing when there are no secondary sites.
func TestOptimizeWebsitesCards(t *testing.T) {
	sites := []optimizeSite{
		{ID: "s1", Host: "shop.example", Label: "Blog"},
		{ID: "s2", Host: "docs.example", Label: "Business site"},
	}
	grid := osOptimizeGrid(accessAdmin, sites)
	assertCSPSafe(t, "osOptimizeGrid/sites", grid)
	for _, want := range []string{
		"Your websites", `href="/os/domains/s1"`, `href="/os/domains/s2"`,
		"shop.example", "docs.example",
	} {
		if !strings.Contains(grid, want) {
			t.Errorf("Optimize hub missing %q", want)
		}
	}

	// No secondary sites → no "Your websites" section at all.
	empty := osOptimizeGrid(accessAdmin, nil)
	if strings.Contains(empty, "Your websites") {
		t.Error("Optimize hub must not show the websites row when there are no sites")
	}
}
