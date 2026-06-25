package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// realMarkupSelectors are class names the PUBLIC VayuPress templates actually
// emit (render.go). A "design theme" — any preset that ships CustomCSS — must
// restyle these, otherwise deploying it would only recolour the site (the exact
// bug users hit). Token-only presets (no CustomCSS) are colour palettes and are
// intentionally exempt.
var realMarkupSelectors = []string{".vayu-hero", ".vayu-post-card"}

// TestDesignThemesTargetRealMarkup is the foolproof guard: it fails the build if
// any preset that ships CustomCSS does not target the real public markup. This
// makes "theme switch only changes colour" impossible to reintroduce.
func TestDesignThemesTargetRealMarkup(t *testing.T) {
	var designThemes int
	for _, p := range theme.AllPresets() {
		if strings.TrimSpace(p.CustomCSS) == "" {
			continue // token-only palette preset — exempt
		}
		designThemes++
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Errorf("%s: compile failed: %v", p.Name, err)
			continue
		}
		for _, sel := range realMarkupSelectors {
			if !strings.Contains(css, sel) {
				t.Errorf("%s ships CustomCSS but does NOT style real markup %q — "+
					"deploying it would only recolour, not restyle. Add a 'Live "+
					"public-site styling' section targeting the vayu-* selectors.",
					p.Name, sel)
			}
		}
	}
	if designThemes < 9 {
		t.Fatalf("expected at least 9 design themes (with CustomCSS), found %d", designThemes)
	}
	t.Logf("verified %d design themes restyle the real public markup", designThemes)
}
