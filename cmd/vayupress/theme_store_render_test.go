package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestThemeStoreCardsRender proves the Theme Store gallery is emitted
// server-side: one deployable card per catalogue theme, each with a Deploy
// action, a visual preview, descriptive metadata, and CSP-safe colours.
func TestThemeStoreCardsRender(t *testing.T) {
	out := themeStoreCards("")

	if got, want := strings.Count(out, "data-store-item"), len(theme.Store()); got != want {
		t.Fatalf("expected %d store cards, got %d", want, got)
	}
	if cards := strings.Count(out, "data-store-item"); cards < 20 {
		t.Fatalf("expected 20+ deployable themes in the store, got %d", cards)
	}

	for _, name := range []string{"Default", "Gale", "Zephyr", "Terminal", "Sepia"} {
		if !strings.Contains(out, `data-name="`+name+`"`) {
			t.Errorf("theme %q not rendered in the store", name)
		}
		if !strings.Contains(out, `data-store-deploy="`+name+`"`) {
			t.Errorf("theme %q has no Deploy action", name)
		}
	}

	// Each card carries a visual preview and metadata for the showcase.
	for _, part := range []string{"store-card__preview", "store-card__pills", "store-card__tagline", "store-card__desc", "store-card__tags", "store-card__cat"} {
		if !strings.Contains(out, part) {
			t.Errorf("store card element %q missing", part)
		}
	}

	// Colours must ride on data-color attributes (applied via CSSOM, CSP-safe),
	// never as inline style attributes.
	if strings.Contains(out, "style=") {
		t.Error("store cards must not carry inline style attributes (CSP-safe rendering)")
	}
	if !strings.Contains(out, `data-color="`) {
		t.Error("store preview colours missing (expected data-color attributes)")
	}
}

// TestThemeStoreActiveBadge proves the active theme is badged and its Deploy
// button is rendered in the disabled "Active" state.
func TestThemeStoreActiveBadge(t *testing.T) {
	out := themeStoreCards("Aurora")

	if !strings.Contains(out, "store-card--active") {
		t.Error("expected the active theme card to be marked store-card--active")
	}
	// The Aurora deploy button should be the disabled active variant.
	if !strings.Contains(out, `data-store-deploy="Aurora" data-store-active="true" disabled`) {
		t.Error("active theme Deploy button should be disabled and marked active")
	}
}

// TestThemeStoreCatalogIntegrity proves every deployable preset has store
// metadata (via the fallback at minimum) and a non-empty category.
func TestThemeStoreCatalogIntegrity(t *testing.T) {
	for _, e := range theme.Store() {
		if e.Meta.Name != e.Tokens.Name {
			t.Errorf("store entry meta name %q != tokens name %q", e.Meta.Name, e.Tokens.Name)
		}
		if strings.TrimSpace(e.Meta.Tagline) == "" {
			t.Errorf("theme %q has an empty tagline", e.Meta.Name)
		}
		if strings.TrimSpace(e.Meta.Category) == "" {
			t.Errorf("theme %q has an empty category", e.Meta.Name)
		}
	}
	if len(theme.Categories()) == 0 {
		t.Error("expected at least one store category")
	}
}
