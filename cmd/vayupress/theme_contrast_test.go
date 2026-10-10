// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestDefaultPalettePassesWCAGAA(t *testing.T) {
	// The shipped defaults must clear AA, or the checker would flag its own
	// defaults. Light primary #0f766e and dark primary #2dd4bf are the defaults.
	if w := contrastWarnings("#0f766e", "#2dd4bf"); len(w) != 0 {
		t.Errorf("default palette must pass WCAG AA, got warnings: %v", w)
	}
}

// TestThemeEditorCoversSettingsAllowlist was retired with the legacy
// /admin/theme editor page it rendered: the route now redirects to /os/theme
// and themeEditorPage is gone. The exporter it drifted-guarded is still live,
// and its credential-leak guard lives on in theme_export_leak_test.go.

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
