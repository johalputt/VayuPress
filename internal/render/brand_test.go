// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// TestRenderHomeWithSettingsOverlay proves a secondary domain's branded settings
// reach the homepage while the package-global active settings are untouched — the
// mechanism that lets one binary serve many domains, each as its own site.
func TestRenderHomeWithSettingsOverlay(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Primary", Tagline: "Primary tagline"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	branded := SiteSettings{Name: "Shop", Tagline: "Great deals", Description: "The shop."}
	out, err := RenderHomeWithSettings(branded, "shop.example", "vtest", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHomeWithSettings: %v", err)
	}
	if !strings.Contains(out, "Shop") {
		t.Error("branded site name missing from homepage")
	}
	if strings.Contains(out, "Primary tagline") {
		t.Error("primary tagline leaked into a branded homepage")
	}

	// The global active settings must be unchanged: RenderHome (used by the primary
	// domain) still renders the primary identity, so a single-host install is
	// byte-identical.
	prim, err := RenderHome("example.com", "vtest", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	if !strings.Contains(prim, "Primary") || strings.Contains(prim, "Shop") {
		t.Error("global RenderHome should still render the primary identity")
	}
}

// TestThemeCSSForAccent proves the accent overlay reaches a domain's /theme.css
// and that ThemeCSS() (the primary's stylesheet) is unaffected.
func TestThemeCSSForAccent(t *testing.T) {
	SetActiveSettings(SiteSettings{AccentLight: "#111111"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	branded := ThemeCSSFor(SiteSettings{AccentLight: "#2563eb", AccentDark: "#60a5fa"})
	if !strings.Contains(branded, "--vayu-accent:#2563eb;") {
		t.Errorf("branded light accent missing:\n%s", branded)
	}
	if !strings.Contains(branded, "--vayu-accent:#60a5fa;") {
		t.Errorf("branded dark accent missing:\n%s", branded)
	}

	// The primary stylesheet keeps its own accent, and its ETag differs from the
	// branded one so browsers revalidate per domain.
	if got := ThemeCSS(); !strings.Contains(got, "--vayu-accent:#111111;") {
		t.Errorf("primary theme.css lost its accent:\n%s", got)
	}
	if ThemeCSSETag() == ThemeCSSETagFor(SiteSettings{AccentLight: "#2563eb"}) {
		t.Error("branded and primary stylesheets should have distinct ETags")
	}
}
