// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"strings"
	"testing"
)

func TestHexHSLRoundTrip(t *testing.T) {
	h, s, l, ok := HexHSL("#2dd4bf")
	if !ok {
		t.Fatal("parse failed")
	}
	if h < 170 || h > 180 || s < 0.5 || l < 0.45 {
		t.Fatalf("teal parsed wrong: h=%.1f s=%.2f l=%.2f", h, s, l)
	}
}

func TestHarmonyPalettesValid(t *testing.T) {
	for _, mood := range Moods {
		p, ok := Harmony(HarmonyRequest{Accent: "#e0562f", Mood: mood})
		if !ok {
			t.Fatalf("%s: harmony failed", mood)
		}
		for _, c := range []string{p.BgDark, p.SurfaceDark, p.TextDark, p.AccentDark,
			p.BgLight, p.TextLight, p.AccentLight, p.Accent2Dark} {
			if len(c) != 7 || !strings.HasPrefix(c, "#") {
				t.Fatalf("%s: bad colour %q", mood, c)
			}
		}
	}
	// Dark bg must actually be dark; light bg light.
	p, _ := Harmony(HarmonyRequest{Accent: "#7c3aed", Mood: "calm"})
	lum := func(hex string) float64 {
		r, g, b, ok := ParseHex(hex)
		if !ok {
			return -1
		}
		return 0.299*r + 0.587*g + 0.114*b
	}
	if lum(p.BgDark) > 90 || lum(p.BgLight) < 165 {
		t.Fatalf("ramps inverted: dark=%v light=%v", lum(p.BgDark), lum(p.BgLight))
	}
}

func TestNearestAccessibleReachesAA(t *testing.T) {
	fixed, ok := NearestAccessible("#8a8a8a", "#ffffff")
	if !ok || ContrastRatio(fixed, "#ffffff") < 4.5 {
		t.Fatalf("grey on white not fixed: %q ratio=%.2f", fixed, ContrastRatio(fixed, "#ffffff"))
	}
	fixed2, _ := NearestAccessible("#5566aa", "#0b0f19")
	if ContrastRatio(fixed2, "#0b0f19") < 4.5 {
		t.Fatalf("navy on near-black not fixed: %.2f", ContrastRatio(fixed2, "#0b0f19"))
	}
}
