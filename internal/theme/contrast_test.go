// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Every store card in this catalogue ends with the words "WCAG-AA". Nothing
// enforced that — the claim was made twelve times over and measured zero times,
// which is the failure mode this repository keeps rediscovering under a
// different name. These tests measure it.
//
// WCAG 2.1 §1.4.3: normal-size text needs 4.5:1 against its background, and
// large text (≥18.66px bold or ≥24px) needs 3:1. Only the foregrounds a theme
// actually paints on backgrounds it actually uses are checked — a palette entry
// no rule pairs is not a contrast failure, it is an unused colour.

// aaNormal and aaLarge are the WCAG 2.1 thresholds.
const (
	aaNormal = 4.5
	aaLarge  = 3.0
)

// pairsFor returns every foreground/background combination a preset genuinely
// renders, with the threshold that applies to it.
//
// The muted colour carries dates, section labels, excerpts, footer links and
// captions — body-size text throughout, so it is held to 4.5:1 and not excused
// as decoration. Accent2 and Hi are used for large display type and for
// non-text accents (rules, underlines, gradient stops), so they take the
// large-text threshold.
func pairsFor(t Tokens) []struct {
	what      string
	fg, bg    string
	threshold float64
} {
	return []struct {
		what      string
		fg, bg    string
		threshold float64
	}{
		{"body text on the page (dark)", t.TextDark, t.BgDark, aaNormal},
		{"muted text on the page (dark)", t.MutedDark, t.BgDark, aaNormal},
		{"links/accent on the page (dark)", t.AccentDark, t.BgDark, aaNormal},
		{"body text on a card (dark)", t.TextDark, t.SurfaceDark, aaNormal},
		{"muted text on a card (dark)", t.MutedDark, t.SurfaceDark, aaNormal},
		{"links/accent on a card (dark)", t.AccentDark, t.SurfaceDark, aaNormal},
		{"secondary accent (dark)", t.Accent2Dark, t.BgDark, aaLarge},
		{"highlight (dark)", t.HiDark, t.BgDark, aaLarge},

		{"body text on the page (light)", t.TextLight, t.BgLight, aaNormal},
		{"muted text on the page (light)", t.MutedLight, t.BgLight, aaNormal},
		{"links/accent on the page (light)", t.AccentLight, t.BgLight, aaNormal},
		{"body text on a card (light)", t.TextLight, t.SurfaceLight, aaNormal},
		{"muted text on a card (light)", t.MutedLight, t.SurfaceLight, aaNormal},
		{"links/accent on a card (light)", t.AccentLight, t.SurfaceLight, aaNormal},
		{"secondary accent (light)", t.Accent2Light, t.BgLight, aaLarge},
		{"highlight (light)", t.HiLight, t.BgLight, aaLarge},
	}
}

// TestEveryPresetMeetsAA is the control behind the catalogue's accessibility
// claim. It covers colour-palette presets as well as design themes: a palette
// with no CSS still decides every colour on the page.
func TestEveryPresetMeetsAA(t *testing.T) {
	presets := AllPresets()
	if len(presets) < 12 {
		t.Fatalf("expected the full catalogue, got %d presets", len(presets))
	}
	for _, p := range presets {
		t.Run(p.Name, func(t *testing.T) {
			for _, c := range pairsFor(p) {
				if c.fg == "" || c.bg == "" {
					continue // the preset does not define that role
				}
				got := contrastRatio(t, c.fg, c.bg)
				if got < c.threshold {
					t.Errorf("%s: %s on %s is %.2f:1, below %.1f:1", c.what, c.fg, c.bg, got, c.threshold)
				}
			}
		})
	}
}

// TestAAClaimIsOnlyMadeWhereMeasured closes the loop from the other direction.
// A store card may say "WCAG-AA" only for a preset TestEveryPresetMeetsAA
// actually covers — otherwise the wording could outlive the check by being
// applied to something the gate never sees.
func TestAAClaimIsOnlyMadeWhereMeasured(t *testing.T) {
	measured := map[string]bool{}
	for _, p := range AllPresets() {
		measured[p.Name] = true
	}
	for _, e := range Store() {
		if !strings.Contains(e.Meta.Description, "WCAG-AA") {
			continue
		}
		if !measured[e.Meta.Name] {
			t.Errorf("%q claims WCAG-AA in its store card but is not in AllPresets(), so nothing measures it", e.Meta.Name)
		}
	}
}

// contrastRatio implements the WCAG 2.1 contrast formula.
func contrastRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	l1, l2 := relLuminance(t, fg), relLuminance(t, bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// relLuminance implements WCAG 2.1 relative luminance for a #rrggbb colour.
func relLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		t.Fatalf("not a six-digit hex colour: %q", hex)
	}
	ch := [3]float64{}
	for i := range ch {
		var v int
		if _, err := fmt.Sscanf(h[i*2:i*2+2], "%02x", &v); err != nil {
			t.Fatalf("bad hex %q: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			ch[i] = c / 12.92
		} else {
			ch[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2]
}
