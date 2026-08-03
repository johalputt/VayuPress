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
	cfg, err := domain.EncodeBrandConfigInto("", brand)
	if err != nil {
		t.Fatalf("EncodeBrandConfigInto: %v", err)
	}
	d := domain.Domain{
		ID: "s1", Host: "shop.example", SiteType: domain.SiteBlog,
		Status: domain.StatusActive, ConfigJSON: cfg,
	}

	page := scopedConsolePage(d, 3, 2, 0, true, nil, nil, nil)
	assertCSPSafe(t, "scopedConsolePage", page)

	// The hostile value must be escaped — the `">` breakout sequence must not
	// survive into the markup, and the raw <script> must never appear.
	if strings.Contains(page, `"><script>`) {
		t.Fatalf("hostile brand value broke out of the attribute:\n%s", page)
	}
	// ADR-0154 D3 — one editor per field. The console used to carry a second
	// Branding editor writing name/tagline/description/accents into the
	// config_json overlay, while /os/d/{id}/settings and Theme Studio wrote the
	// same fields into the scoped store. Two editors for one field means the
	// value depends on which page an operator happened to use last, and nothing
	// on either page said so.
	for _, gone := range []string{"data-site-brand-save", "data-site-brand-clear", "data-site-assign"} {
		if strings.Contains(page, gone) {
			t.Errorf("the console still carries %q — a second editor for fields that already "+
				"have a scoped one, so which value wins depends on which page was used last", gone)
		}
	}
	// It must instead POINT AT the scoped editors, or the fields become
	// unreachable and this is a removal rather than a consolidation.
	for _, want := range []string{`href="/os/d/s1/settings"`, `href="/os/d/s1/theme"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the console does not link %s, so retiring the duplicate editor left "+
				"those fields with no way in", want)
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
		"[data-site-sync]", "[data-site-toggle]", "[data-site-delete]",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing handler for %q", want)
		}
	}
	// The handlers for the retired branding and assign controls go with the
	// markup. A listener that can never bind is not harmless: it is the reason
	// the next reader believes an editor exists somewhere on this page.
	for _, gone := range []string{
		"[data-site-brand-save]", "[data-site-brand-clear]", "[data-site-assign]",
	} {
		if strings.Contains(script, gone) {
			t.Errorf("the script still wires %q, whose markup was removed — dead code that "+
				"reads as evidence of a control that is not there", gone)
		}
	}
}

// TestDomainManagePrimaryHidesEditor is a light guard that the manager never
// renders the branding editor's breakout risk for a pending Tor site (its host
// is a placeholder, not a real address, so no live-view link is offered).
func TestDomainManagePendingTorSite(t *testing.T) {
	d := domain.Domain{ID: "t1", Host: torSitePending + "abc.local", SiteType: domain.SiteBlog, Status: domain.StatusActive}
	page := scopedConsolePage(d, 0, 0, 0, false, nil, nil, nil)
	assertCSPSafe(t, "scopedConsolePage/pending", page)
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
		"Your websites", `href="/os/d/s1"`, `href="/os/d/s2"`,
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
