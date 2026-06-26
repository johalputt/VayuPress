package main

import (
	"strings"
	"testing"
)

// TestThemePresetCardsRender proves the Theme Studio gallery is emitted
// server-side: one card button per preset, including Gale and Zephyr.
func TestThemePresetCardsRender(t *testing.T) {
	out := themePresetCards()
	cards := strings.Count(out, `class="theme-card"`)
	if cards < 20 {
		t.Fatalf("expected 20+ preset cards rendered, got %d", cards)
	}
	for _, name := range []string{"Default", "Gale", "Zephyr"} {
		if !strings.Contains(out, `data-preset="`+name+`"`) {
			t.Errorf("preset %q not rendered in gallery", name)
		}
	}
	// Each card is a Tumblr-style visual preview: a coloured page with an accent
	// bar, body text lines and accent pills — not bare swatches.
	for _, part := range []string{"theme-card__preview", "theme-card__bar", "theme-card__body", "theme-card__line", "theme-card__pills"} {
		if !strings.Contains(out, part) {
			t.Errorf("gallery preview element %q missing from rendered cards", part)
		}
	}
	// Colours must ride on data-color attributes (applied via CSSOM, CSP-safe),
	// never as inline style attributes.
	if strings.Contains(out, "style=") {
		t.Error("gallery cards must not carry inline style attributes (CSP-safe rendering)")
	}
	if !strings.Contains(out, `data-color="`) {
		t.Error("preset preview colours missing (expected data-color attributes)")
	}
}
