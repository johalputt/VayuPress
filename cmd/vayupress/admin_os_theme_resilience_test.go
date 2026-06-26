package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestThemeStudioRendersWithoutSettingsStore guards the Theme Studio surface
// against a regression where the gallery "doesn't show anywhere": handleOSTheme
// used to dereference a.siteSettings unconditionally, so if the settings store
// was not yet ready (startup race / init failure) the page panicked → HTTP 500
// → blank page, while every other VayuOS page (which guards the nil store) kept
// working. The handler must instead degrade to Defaults and still render the
// full Studio, including the theme preset gallery.
func TestThemeStudioRendersWithoutSettingsStore(t *testing.T) {
	// siteSettings intentionally left nil — the worst-case "not ready" state.
	a := &App{}

	req := httptest.NewRequest("GET", "/os/theme", nil)
	rec := httptest.NewRecorder()

	// Must not panic.
	a.handleOSTheme(rec, req)

	if rec.Code != 200 {
		t.Fatalf("Theme Studio status = %d, want 200 (page must render even without the settings store)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data-theme-presets") {
		t.Error("Theme Studio is missing the preset gallery container (data-theme-presets)")
	}
	if n := strings.Count(body, `class="theme-card"`); n < 20 {
		t.Errorf("Theme Studio rendered %d gallery cards, want >= 20 (the full preset gallery)", n)
	}
}
