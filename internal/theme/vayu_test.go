// SPDX-License-Identifier: Apache-2.0

package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestVayuPresetCompiles proves the Vayu theme (the vayupress.com look) is a
// valid, deployable design theme: present, ships component CSS, compiles to a
// same-origin stylesheet that restyles the real public markup, and carries the
// brand palette (teal + saffron).
func TestVayuPresetCompiles(t *testing.T) {
	v := theme.Vayu()
	if v.Name != "Vayu" {
		t.Fatalf("expected name Vayu, got %q", v.Name)
	}
	if strings.TrimSpace(v.CustomCSS) == "" {
		t.Fatal("Vayu must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(v)
	if err != nil {
		t.Fatalf("Vayu failed to compile: %v", err)
	}

	// Token bridge + the whole-site markup a design theme must restyle.
	for _, want := range []string{
		"--vp-accent", "--accent", "--pico-primary",
		".vayu-hero", ".vayu-post-card", ".vayu-post-card--media", ".vayu-post-title",
		".vayu-author-box", ".vayu-footer", ".vayu-footer-col-links",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Vayu CSS missing %q", want)
		}
	}

	// The brand palette: bright teal accent + saffron secondary.
	if !strings.Contains(css, "#2dd4bf") {
		t.Error("Vayu missing its teal accent (#2dd4bf)")
	}
	if !strings.Contains(css, "#f59e0b") {
		t.Error("Vayu missing its saffron secondary (#f59e0b)")
	}
	// Fast + sovereign: no external web-font or CDN requests baked into the CSS.
	for _, forbidden := range []string{"@import", "fonts.googleapis", "fonts.gstatic", "cdn.", "http://", "https://"} {
		if strings.Contains(css, forbidden) {
			t.Errorf("Vayu CSS must be self-contained (no external requests), found %q", forbidden)
		}
	}
}

// TestVayuInStore proves Vayu is deployable from the Theme Store as a Flagship
// theme with complete card metadata (one-click "same as the website").
func TestVayuInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Vayu" {
			found = true
			if e.Meta.Category != theme.CatFlagship {
				t.Errorf("Vayu category = %q, want %q", e.Meta.Category, theme.CatFlagship)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Vayu is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Vayu not present in theme.Store()")
	}
}
