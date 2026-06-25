package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestDesignThemesStyleRealMarkup proves the design themes restyle the ACTUAL
// public-site markup (the vayu-* classes the Go templates emit) — not invented
// component classes — so deploying one visibly changes layout/typography, not
// just colour. /theme.css is served last, so these selectors win.
func TestDesignThemesStyleRealMarkup(t *testing.T) {
	cases := map[string]func() theme.Tokens{
		"Gale":     theme.Gale,
		"Zephyr":   theme.Zephyr,
		"Dispatch": theme.Dispatch,
		"Vivid":    theme.Vivid,
		"Beacon":   theme.Beacon,
	}
	// Real selectors emitted by the public templates (render.go).
	wantSelectors := []string{".vayu-hero", ".vayu-post-card", ".vayu-post-title", ".vayu-footer"}

	for name, ctor := range cases {
		css, err := theme.CompileCSS(ctor())
		if err != nil {
			t.Fatalf("%s failed to compile: %v", name, err)
		}
		for _, sel := range wantSelectors {
			if !strings.Contains(css, sel) {
				t.Errorf("%s theme does not style real markup selector %q — it would only recolour, not restyle", name, sel)
			}
		}
	}
}
