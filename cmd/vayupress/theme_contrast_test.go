package main

import (
	"math"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

func TestContrastRatioKnownValues(t *testing.T) {
	if cr := contrastRatio("#000000", "#ffffff"); math.Abs(cr-21.0) > 0.05 {
		t.Errorf("black/white should be 21:1, got %.2f", cr)
	}
	if cr := contrastRatio("#abcdef", "#abcdef"); math.Abs(cr-1.0) > 0.001 {
		t.Errorf("identical colours should be 1:1, got %.2f", cr)
	}
	// #rgb shorthand must expand identically to #rrggbb.
	if a, b := contrastRatio("#fff", "#000"), contrastRatio("#ffffff", "#000000"); math.Abs(a-b) > 0.001 {
		t.Errorf("#rgb and #rrggbb must agree: %.2f vs %.2f", a, b)
	}
}

func TestDefaultPalettePassesWCAGAA(t *testing.T) {
	// The shipped defaults must clear AA, or the checker would flag its own
	// defaults. Light primary #0f766e and dark primary #2dd4bf are the defaults.
	if w := contrastWarnings("#0f766e", "#2dd4bf"); len(w) != 0 {
		t.Errorf("default palette must pass WCAG AA, got warnings: %v", w)
	}
}

// TestThemeEditorCoversSettingsAllowlist is a drift guard: every key in the
// settings allowlist must appear in the rendered editor — both as an input id and
// in the import key list — so export/import and the editor can never fall out of
// sync with the allowlist as keys are added or removed.
func TestThemeEditorCoversSettingsAllowlist(t *testing.T) {
	// Branding keys are managed out-of-band by the multipart favicon upload
	// handler (POST /admin/theme/favicon), not the JSON Save form, so they are
	// deliberately absent from the form-field / import-key drift guard below.
	outOfBand := map[string]bool{
		settings.KeyBrandFavicon:     true,
		settings.KeyBrandFaviconType: true,
		// Feature flags are toggled through the Tools & Plugins panel
		// (POST /admin/v3/api/tools/toggle), not the theme editor form.
		settings.KeyFeatureComments:    true,
		settings.KeyFeatureNewsletter:  true,
		settings.KeyFeatureWebmentions: true,
	}
	page := themeEditorPage(map[string]string{}, "NORMAL", "test-nonce", "")
	for key := range settings.AllKeys {
		if outOfBand[key] {
			continue
		}
		if !strings.Contains(page, `id="`+key+`"`) {
			t.Errorf("settings key %q has no input field in the theme editor", key)
		}
		if !strings.Contains(page, `'`+key+`'`) {
			t.Errorf("settings key %q is missing from the import/save key list", key)
		}
	}
	// The export and import sides must agree on the bundle schema version.
	if themeExportVersion != 1 {
		t.Errorf("import JS pins vayupress_theme===1; bump it in lockstep with themeExportVersion (%d)", themeExportVersion)
	}
}

func TestContrastWarningsFlagLowContrast(t *testing.T) {
	// A near-white light primary on the light background must warn; a bright
	// dark primary on the dark background must not.
	w := contrastWarnings("#fefefe", "#ffffff")
	if len(w) == 0 {
		t.Error("expected a contrast warning for near-white light primary")
	}
	if w := contrastWarnings("", ""); len(w) != 0 {
		t.Errorf("empty colours should produce no warnings, got: %v", w)
	}
}
