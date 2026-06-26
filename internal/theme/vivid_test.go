package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestVividPresetCompiles proves the Vivid (bold magazine) theme is a valid,
// deployable preset: present in the catalogue, ships its component CSS with the
// six color schemes, and compiles to a valid same-origin stylesheet.
func TestVividPresetCompiles(t *testing.T) {
	v := theme.Vivid()
	if v.Name != "Vivid" {
		t.Fatalf("expected name Vivid, got %q", v.Name)
	}
	if strings.TrimSpace(v.CustomCSS) == "" {
		t.Fatal("Vivid must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(v)
	if err != nil {
		t.Fatalf("Vivid failed to compile: %v", err)
	}
	// Token bridge + shipped components must be present.
	for _, want := range []string{"--vp-accent", "--pico-primary", ".vivid-header", ".vivid-hero", ".vivid-featured", ".vivid-grid"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Vivid CSS missing %q", want)
		}
	}
	// All six color schemes must ship.
	for _, scheme := range []string{"dusty-orange", "whale-blue", "apple-green", "plum-purple", "slate-grey", "sea-blue"} {
		if !strings.Contains(css, ".vivid-scheme--"+scheme) {
			t.Errorf("Vivid color scheme %q missing", scheme)
		}
	}
	// Five featured-image aspect ratios must ship.
	for _, ar := range []string{"natural", "square", "landscape", "wide", "letterbox"} {
		if !strings.Contains(css, ".vivid-card__media--"+ar) {
			t.Errorf("Vivid featured-image ratio %q missing", ar)
		}
	}
}

// TestVividInStore proves Vivid appears in the Theme Store with metadata.
func TestVividInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Vivid" {
			found = true
			if strings.TrimSpace(e.Meta.Category) == "" || strings.TrimSpace(e.Meta.Tagline) == "" {
				t.Error("Vivid is missing store metadata (category/tagline)")
			}
		}
	}
	if !found {
		t.Fatal("Vivid not present in theme.Store()")
	}
}
