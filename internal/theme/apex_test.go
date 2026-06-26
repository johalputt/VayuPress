package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestApexPresetCompiles proves the flagship Apex theme is a valid, deployable
// preset: present in the catalogue, ships its extensive component CSS, and
// compiles to a valid same-origin stylesheet.
func TestApexPresetCompiles(t *testing.T) {
	a := theme.Apex()
	if a.Name != "Apex" {
		t.Fatalf("expected name Apex, got %q", a.Name)
	}
	if strings.TrimSpace(a.CustomCSS) == "" {
		t.Fatal("Apex must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(a)
	if err != nil {
		t.Fatalf("Apex failed to compile: %v", err)
	}
	// Token bridge + a representative slice of the flagship component kit.
	for _, want := range []string{
		"--vp-accent", "--pico-primary",
		".apex-header", ".apex-bento", ".apex-pricing", ".apex-faq", ".apex-timeline",
		".apex-testimonials", ".apex-toc", ".apex-progress", ".apex-footer",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Apex CSS missing %q", want)
		}
	}
	// All four hero styles must ship.
	for _, h := range []string{"split", "center", "media", "mesh"} {
		if !strings.Contains(css, ".apex-hero--"+h) {
			t.Errorf("Apex hero style %q missing", h)
		}
	}
	// All four feed layouts must ship.
	for _, f := range []string{"grid", "list", "magazine", "masonry"} {
		if !strings.Contains(css, ".apex-feed--"+f) {
			t.Errorf("Apex feed layout %q missing", f)
		}
	}
	// All ten color schemes must ship.
	for _, s := range []string{"indigo", "violet", "cyan", "emerald", "rose", "amber", "slate", "crimson", "teal", "mono"} {
		if !strings.Contains(css, ".apex-scheme--"+s) {
			t.Errorf("Apex color scheme %q missing", s)
		}
	}
	// Density and shape modes must ship.
	for _, mod := range []string{".apex--compact", ".apex--spacious", ".apex--sharp", ".apex--rounded", ".apex--pill", ".apex--gradient"} {
		if !strings.Contains(css, mod) {
			t.Errorf("Apex mode %q missing", mod)
		}
	}
}

// TestApexInStore proves Apex appears in the Theme Store under the Flagship
// category, which must be exposed first by Categories().
func TestApexInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Apex" {
			found = true
			if e.Meta.Category != theme.CatFlagship {
				t.Errorf("Apex category = %q, want %q", e.Meta.Category, theme.CatFlagship)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Apex is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Apex not present in theme.Store()")
	}

	cats := theme.Categories()
	if len(cats) == 0 || cats[0] != theme.CatFlagship {
		t.Errorf("expected Flagship to lead Categories(), got %v", cats)
	}
}
