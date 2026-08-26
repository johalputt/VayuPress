// SPDX-License-Identifier: Apache-2.0

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
	// Each card is a real miniature page (Wave A): inline-SVG art with the
	// preset's own palette, plus filter/search metadata tags.
	for _, part := range []string{"theme-card__art", "<svg", "data-archetype=", "data-scheme=", "data-search="} {
		if !strings.Contains(out, part) {
			t.Errorf("gallery preview element %q missing from rendered cards", part)
		}
	}
	// Colours ride on SVG presentational attributes (CSP-safe), never as
	// inline style attributes.
	if strings.Contains(out, "style=") {
		t.Error("gallery cards must not carry inline style attributes (CSP-safe rendering)")
	}
	schemes := strings.Count(out, `data-scheme="dark"`) + strings.Count(out, `data-scheme="light"`)
	if schemes != cards {
		t.Errorf("every card must be tagged dark or light: %d tags for %d cards", schemes, cards)
	}
}
