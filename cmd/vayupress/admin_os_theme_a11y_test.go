// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestThemeA11yGradesContrastHonestly: the readout must grade by measured WCAG
// ratio, and the collapsed summary must reflect the WEAKEST pairing rather than
// the most flattering one — otherwise it reassures an operator whose accent is
// unreadable on one of the two backgrounds.
func TestThemeA11yGradesContrastHonestly(t *testing.T) {
	// White on the dark background is very high contrast; mid-grey is poor.
	checks := themeA11yChecks("#ffffff", "", "#111827", "")
	if len(checks) != 2 {
		t.Fatalf("want a check per supplied accent, got %d", len(checks))
	}
	for _, c := range checks {
		if c.Ratio < 1 || c.Ratio > 21 {
			t.Errorf("%s ratio %.2f out of range", c.Label, c.Ratio)
		}
		if lbl, _ := c.grade(); lbl == "" {
			t.Errorf("%s produced no grade", c.Label)
		}
	}
	if chip := a11ySummaryChip(checks); !strings.Contains(chip, "Readable") {
		t.Errorf("high-contrast palette chip = %q, want Readable", chip)
	}

	// A washed-out accent on the dark background must be called out, not hidden.
	weak := themeA11yChecks("#1a1f2b", "", "#ffffff", "")
	chip := a11ySummaryChip(weak)
	if strings.Contains(chip, "Readable") {
		t.Errorf("chip = %q, want a low-contrast warning for a near-invisible accent", chip)
	}
	body := themeA11yPanel(weak)
	if !strings.Contains(body, "Readability") || !strings.Contains(body, ":1") {
		t.Error("panel must name itself and show measured ratios")
	}
	assertCSPSafe(t, "themeA11yPanel", body)

	// No colours supplied → nothing to claim.
	if got := a11ySummaryChip(nil); got != "" {
		t.Errorf("empty checks chip = %q, want empty", got)
	}
	if got := themeA11yPanel(nil); got != "" {
		t.Errorf("empty checks panel = %q, want empty", got)
	}
}

// TestThemeA11yGradeBoundaries pins the WCAG thresholds so a refactor cannot
// quietly turn a failing palette green.
func TestThemeA11yGradeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ratio float64
		want  string
	}{
		{7.5, "AAA"},
		{5.0, "AA"},
		{3.5, "AA large only"},
		{2.0, "Fails"},
	} {
		got, _ := a11yCheck{Ratio: tc.ratio}.grade()
		if got != tc.want {
			t.Errorf("ratio %.1f graded %q, want %q", tc.ratio, got, tc.want)
		}
	}
}
