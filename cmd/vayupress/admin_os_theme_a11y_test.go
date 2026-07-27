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
	// Every SUPPLIED accent is measured (blank ones are skipped), plus the shipped
	// reading-text pairings. Asserting an exact total here would re-encode the old
	// "accents only" contract, which is the gap that let a real contrast failure
	// go unreported.
	accents := 0
	for _, c := range checks {
		if strings.HasPrefix(c.Label, "Accent") {
			accents++
		}
	}
	if accents != 2 {
		t.Fatalf("want a check per supplied accent, got %d", accents)
	}
	if len(checks) <= accents {
		t.Fatal("the panel measures only accents; shipped body and muted text go unchecked")
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

// TestShippedReadingTextClearsAA is the regression test for a contrast failure
// that this panel could not see.
//
// The checker measured only the four accent pairings. Accents are the colours an
// operator picked, so they are already being thought about; body and muted text
// are shipped defaults nobody re-examines. Muted text on a dark background is the
// most common contrast fault in any theme — and it was failing here at 4.14:1 on
// the page background and 3.87:1 on cards, while the panel reported "Readable",
// because the failing pairing was never one of the four it looked at.
//
// A check that covers a subset while presenting as covering the whole is worse
// than no check: it turns an unknown into a false assurance.
func TestShippedReadingTextClearsAA(t *testing.T) {
	for _, c := range []struct{ label, fg, bg string }{
		{"body on dark", bodyTextDark, darkModeBG},
		{"body on light", bodyTextLight, lightModeBG},
		{"muted on dark", mutedTextDark, darkModeBG},
		{"muted on dark card", mutedTextDark, darkModeSurface},
		{"muted on light", mutedTextLight, lightModeBG},
	} {
		if got := contrastRatio(c.fg, c.bg); got < wcagAANormal {
			t.Errorf("%s: %s on %s = %.2f:1, below the WCAG AA bar of %.1f:1",
				c.label, c.fg, c.bg, got, wcagAANormal)
		}
	}
}

// TestA11yPanelMeasuresReadingText — the shipped text tokens must actually be in
// the panel's work list, not merely correct today. Without this, a future token
// change silently reintroduces an unmeasured pairing.
func TestA11yPanelMeasuresReadingText(t *testing.T) {
	checks := themeA11yChecks("#6366f1", "#818cf8", "#4f46e5", "#6366f1")
	seen := map[string]bool{}
	for _, c := range checks {
		seen[c.Label] = true
	}
	for _, want := range []string{
		"Body text on dark background",
		"Muted text on dark background",
		"Muted text on dark card",
		"Muted text on light background",
	} {
		if !seen[want] {
			t.Errorf("the accessibility panel does not measure %q", want)
		}
	}
	// The summary must still grade the WEAKEST pairing, now that more pairings
	// exist — otherwise widening the check would hide the very failure it adds.
	weak := themeA11yChecks("#1a1f2e", "#818cf8", "#4f46e5", "#6366f1")
	if chip := a11ySummaryChip(weak); !strings.Contains(chip, "Hard to read") {
		t.Errorf("summary chip = %q for a failing accent; it must grade the worst pairing", chip)
	}
}
