package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestDispatchPresetCompiles proves the Dispatch (newsletter) theme is a valid,
// deployable preset: it is present in the catalogue, ships its component CSS,
// and compiles to a valid same-origin stylesheet (colours validated).
func TestDispatchPresetCompiles(t *testing.T) {
	d := theme.Dispatch()
	if d.Name != "Dispatch" {
		t.Fatalf("expected name Dispatch, got %q", d.Name)
	}
	if strings.TrimSpace(d.CustomCSS) == "" {
		t.Fatal("Dispatch must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(d)
	if err != nil {
		t.Fatalf("Dispatch failed to compile: %v", err)
	}
	// The compiled stylesheet must include the token bridge and the shipped
	// component classes (proving CustomCSS is appended).
	for _, want := range []string{"--vp-accent", "--pico-primary", ".dispatch-hero", ".dispatch-inbox", ".dispatch-feed"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Dispatch CSS missing %q", want)
		}
	}
}

// TestDispatchInStore proves Dispatch appears in the Theme Store with its
// metadata and the newsletter category.
func TestDispatchInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Dispatch" {
			found = true
			if e.Meta.Category != theme.CatNewsletter {
				t.Errorf("Dispatch category = %q, want %q", e.Meta.Category, theme.CatNewsletter)
			}
			if len(e.Meta.Tags) == 0 || strings.TrimSpace(e.Meta.Tagline) == "" {
				t.Error("Dispatch is missing store metadata (tags/tagline)")
			}
		}
	}
	if !found {
		t.Fatal("Dispatch not present in theme.Store()")
	}

	var hasNewsletter bool
	for _, c := range theme.Categories() {
		if c == theme.CatNewsletter {
			hasNewsletter = true
		}
	}
	if !hasNewsletter {
		t.Error("Newsletter category not exposed by theme.Categories()")
	}
}
