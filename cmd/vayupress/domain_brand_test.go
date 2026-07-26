// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
)

// TestApplyBrand verifies the overlay semantics that let a secondary domain
// re-brand only the fields it sets while inheriting every other field from the
// primary site's settings.
func TestApplyBrand(t *testing.T) {
	base := render.SiteSettings{
		Name:        "Primary",
		Tagline:     "Primary tagline",
		Description: "Primary description",
		AccentLight: "#111111",
		AccentDark:  "#222222",
		ThemeColor:  "#333333",
		Author:      "Jo", // a field with no brand equivalent — must survive untouched
	}

	// A partial brand overrides only its non-empty fields.
	s := base
	applyBrand(&s, domain.Brand{SiteName: "Shop", AccentLight: "#2563eb"})
	if s.Name != "Shop" {
		t.Errorf("site name = %q, want Shop", s.Name)
	}
	if s.AccentLight != "#2563eb" {
		t.Errorf("accent light = %q, want #2563eb", s.AccentLight)
	}
	// Untouched brand fields inherit the primary.
	if s.Tagline != "Primary tagline" || s.Description != "Primary description" {
		t.Errorf("blank brand fields should inherit primary: %+v", s)
	}
	if s.AccentDark != "#222222" || s.ThemeColor != "#333333" {
		t.Errorf("blank colour fields should inherit primary: %+v", s)
	}
	// A non-brand field is never disturbed by the overlay.
	if s.Author != "Jo" {
		t.Errorf("non-brand field Author changed to %q", s.Author)
	}

	// An empty brand is a no-op — the settings are the primary's, verbatim.
	s2 := base
	applyBrand(&s2, domain.Brand{})
	if s2 != base {
		t.Errorf("empty brand mutated settings: %+v", s2)
	}
}
