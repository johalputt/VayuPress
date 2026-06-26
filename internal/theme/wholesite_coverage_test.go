package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// wholeSiteSelectors are public-markup elements that a design theme must style
// for a theme switch to transform the WHOLE blog (not just the home hero/cards):
// the post author box, the multi-column footer, and cover-image post cards.
var wholeSiteSelectors = []string{
	".vayu-author-box",
	".vayu-footer-col-links",
	".vayu-post-card--media",
}

// TestDesignThemesCoverWholeSite guards against the "only the homepage changes"
// regression: every preset that ships CustomCSS must restyle the author box,
// the footer columns, and cover-image cards — so applying it visibly changes
// every section of the site.
func TestDesignThemesCoverWholeSite(t *testing.T) {
	design := 0
	for _, p := range theme.AllPresets() {
		if strings.TrimSpace(p.CustomCSS) == "" {
			continue // colour-palette preset — exempt
		}
		design++
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Fatalf("%s: CompileCSS failed: %v", p.Name, err)
		}
		for _, sel := range wholeSiteSelectors {
			if !strings.Contains(css, sel) {
				t.Errorf("design theme %q does not style %q — applying it would leave that section unthemed", p.Name, sel)
			}
		}
	}
	if design < 9 {
		t.Fatalf("expected at least 9 design themes, found %d", design)
	}
}

// TestDesignThemesAreMutuallyDistinct guards against design themes collapsing
// into look-alikes: no two design themes may compile to identical CSS.
func TestDesignThemesAreMutuallyDistinct(t *testing.T) {
	seen := map[string]string{} // compiled CSS -> theme name
	for _, p := range theme.AllPresets() {
		if strings.TrimSpace(p.CustomCSS) == "" {
			continue
		}
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Fatalf("%s: CompileCSS failed: %v", p.Name, err)
		}
		if other, dup := seen[css]; dup {
			t.Errorf("design themes %q and %q compile to identical CSS — they would look the same", p.Name, other)
		}
		seen[css] = p.Name
	}
}
