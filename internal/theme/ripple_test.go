package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestRipplePresetCompiles proves the Ripple (wave-styled blog) theme is a
// valid, deployable preset: present in the catalogue, ships its component CSS,
// and compiles to a valid same-origin stylesheet.
func TestRipplePresetCompiles(t *testing.T) {
	r := theme.Ripple()
	if r.Name != "Ripple" {
		t.Fatalf("expected name Ripple, got %q", r.Name)
	}
	if strings.TrimSpace(r.CustomCSS) == "" {
		t.Fatal("Ripple must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(r)
	if err != nil {
		t.Fatalf("Ripple failed to compile: %v", err)
	}
	for _, want := range []string{"--vp-accent", "--pico-primary", ".ripple-wave", ".ripple-layout", ".ripple-sidebar", ".ripple-pricing"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Ripple CSS missing %q", want)
		}
	}
	// All four single-post dispositions must ship.
	for _, d := range []string{"classic", "vertical", "fullcover", "nosidebar"} {
		if !strings.Contains(css, ".ripple-post--"+d) {
			t.Errorf("Ripple post disposition %q missing", d)
		}
	}
	// Sidebar widgets must ship.
	for _, w := range []string{"ripple-widget--about", "ripple-tagcloud", "ripple-social"} {
		if !strings.Contains(css, w) {
			t.Errorf("Ripple sidebar widget %q missing", w)
		}
	}
}

// TestRippleInStore proves Ripple appears in the Theme Store with metadata.
func TestRippleInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Ripple" {
			found = true
			if strings.TrimSpace(e.Meta.Category) == "" || strings.TrimSpace(e.Meta.Tagline) == "" {
				t.Error("Ripple is missing store metadata (category/tagline)")
			}
		}
	}
	if !found {
		t.Fatal("Ripple not present in theme.Store()")
	}
}
