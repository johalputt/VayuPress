package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestBeaconPresetCompiles proves the Beacon (startup/company blog) theme is a
// valid, deployable preset: present in the catalogue, ships its component CSS,
// and compiles to a valid same-origin stylesheet.
func TestBeaconPresetCompiles(t *testing.T) {
	b := theme.Beacon()
	if b.Name != "Beacon" {
		t.Fatalf("expected name Beacon, got %q", b.Name)
	}
	if strings.TrimSpace(b.CustomCSS) == "" {
		t.Fatal("Beacon must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(b)
	if err != nil {
		t.Fatalf("Beacon failed to compile: %v", err)
	}
	for _, want := range []string{"--vp-accent", "--pico-primary", ".beacon-header", ".beacon-hero", ".beacon-featured", ".beacon-feed--grid", ".beacon-toc"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Beacon CSS missing %q", want)
		}
	}
	// Hero's four background styles must ship.
	for _, bg := range []string{"bg-none", "bg-cover", "bg-cover-full", "bg-accent"} {
		if !strings.Contains(css, ".beacon-hero--"+bg) {
			t.Errorf("Beacon hero background style %q missing", bg)
		}
	}
	// The three post-card styles must ship.
	for _, st := range []string{"minimal", "bordered", "shadowed"} {
		if !strings.Contains(css, ".beacon-card--"+st) {
			t.Errorf("Beacon post-card style %q missing", st)
		}
	}
	// The three topic-list layouts must ship.
	for _, ly := range []string{"list", "cards"} {
		if !strings.Contains(css, ".beacon-topics--"+ly) {
			t.Errorf("Beacon topic layout %q missing", ly)
		}
	}
}

// TestBeaconInStore proves Beacon appears in the Theme Store under the Business
// category, which must be exposed by Categories().
func TestBeaconInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Beacon" {
			found = true
			if e.Meta.Category != theme.CatBusiness {
				t.Errorf("Beacon category = %q, want %q", e.Meta.Category, theme.CatBusiness)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Beacon is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Beacon not present in theme.Store()")
	}

	var hasBusiness bool
	for _, c := range theme.Categories() {
		if c == theme.CatBusiness {
			hasBusiness = true
		}
	}
	if !hasBusiness {
		t.Error("Business category not exposed by theme.Categories()")
	}
}
