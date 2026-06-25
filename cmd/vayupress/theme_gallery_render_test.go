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
	if !strings.Contains(out, `theme-card__sw`) {
		t.Error("preset swatches missing")
	}
}
