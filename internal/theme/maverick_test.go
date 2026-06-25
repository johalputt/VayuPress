package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestMaverickPresetCompiles proves the Maverick (bold/creative) theme is a
// valid, deployable preset: present in the catalogue, ships its component CSS,
// and compiles to a valid same-origin stylesheet.
func TestMaverickPresetCompiles(t *testing.T) {
	m := theme.Maverick()
	if m.Name != "Maverick" {
		t.Fatalf("expected name Maverick, got %q", m.Name)
	}
	if strings.TrimSpace(m.CustomCSS) == "" {
		t.Fatal("Maverick must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(m)
	if err != nil {
		t.Fatalf("Maverick failed to compile: %v", err)
	}
	for _, want := range []string{"--vp-accent", "--pico-primary", ".maverick-hero", ".maverick-slider", ".maverick-circles", ".maverick-tiers"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Maverick CSS missing %q", want)
		}
	}
	// Hero content-position and length options must ship.
	for _, opt := range []string{"pos-bottom-left", "pos-center", "len-narrow", "len-wide", "len-full"} {
		if !strings.Contains(css, ".maverick-hero--"+opt) {
			t.Errorf("Maverick hero option %q missing", opt)
		}
	}
	// All eight post-card aspect ratios must ship.
	for _, ar := range []string{"2-3", "3-4", "4-5", "16-9", "4-3", "3-2", "5-4", "1-1"} {
		if !strings.Contains(css, ".maverick-card__media--"+ar) {
			t.Errorf("Maverick aspect ratio %q missing", ar)
		}
	}
	// Text-style options must ship.
	for _, tt := range []string{"upper", "lower", "caps", "none"} {
		if !strings.Contains(css, ".maverick-tt--"+tt) {
			t.Errorf("Maverick text-style %q missing", tt)
		}
	}
}

// TestMaverickInStore proves Maverick appears in the Theme Store under the
// Creative category, which must be exposed by Categories().
func TestMaverickInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Maverick" {
			found = true
			if e.Meta.Category != theme.CatCreative {
				t.Errorf("Maverick category = %q, want %q", e.Meta.Category, theme.CatCreative)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Maverick is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Maverick not present in theme.Store()")
	}

	var hasCreative bool
	for _, c := range theme.Categories() {
		if c == theme.CatCreative {
			hasCreative = true
		}
	}
	if !hasCreative {
		t.Error("Creative category not exposed by theme.Categories()")
	}
}
