// SPDX-License-Identifier: Apache-2.0

package theme

// harmony.go — colour intelligence for Theme Studio (Wave B).
//
// Given one accent colour and a mood, derive a full dark+light token set:
// surfaces ramp toward the accent's hue, text stays near-neutral for
// readability, and the second accent rotates the hue by a mood-specific step.
// Pure maths on RGB/HSL so every function is unit-testable without a database.

import (
	"math"
	"strconv"
	"strings"
)

// Mood shapes how far the derived palette strays from the accent.
var Moods = []string{"calm", "vivid", "muted"}

// ParseHex accepts #rgb / #rrggbb and returns 0–255 r,g,b.
func ParseHex(hex string) (r, g, b float64, ok bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64((n >> 16) & 255), float64((n >> 8) & 255), float64(n & 255), true
}

func clamp01(v float64) float64 { return math.Min(1, math.Max(0, v)) }

// HexHSL converts #rrggbb to h∈[0,360), s,l∈[0,1].
func HexHSL(hex string) (h, s, l float64, ok bool) {
	r, g, b, good := ParseHex(hex)
	if !good {
		return 0, 0, 0, false
	}
	rn, gn, bn := r/255, g/255, b/255
	max, min := math.Max(rn, math.Max(gn, bn)), math.Min(rn, math.Min(gn, bn))
	l = (max + min) / 2
	if max == min {
		return 0, 0, l, true
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case rn:
		h = math.Mod((gn-bn)/d, 6)
	case gn:
		h = (bn-rn)/d + 2
	default:
		h = (rn-gn)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l, true
}

func hslHex(h, s, l float64) string {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	s, l = clamp01(s), clamp01(l)
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to := func(v float64) string {
		n := int(math.Round((v + m) * 255))
		if n < 0 {
			n = 0
		}
		if n > 255 {
			n = 255
		}
		s := strconv.FormatInt(int64(n), 16)
		if len(s) == 1 {
			s = "0" + s
		}
		return s
	}
	return "#" + to(r) + to(g) + to(b)
}

// HarmonyRequest is the input to the palette generator.
type HarmonyRequest struct {
	Accent string `json:"accent"` // seed colour, #rrggbb
	Mood   string `json:"mood"`   // calm | vivid | muted
}

// HarmonyPalette is a full Studio-ready token set.
type HarmonyPalette struct {
	BgDark, SurfaceDark, TextDark, MutedDark, AccentDark, Accent2Dark       string
	BgLight, SurfaceLight, TextLight, MutedLight, AccentLight, Accent2Light string
}

// moodTuning returns (sat multiplier, hue2 rotation, surface tint strength).
func moodTuning(mood string) (float64, float64, float64) {
	switch strings.ToLower(mood) {
	case "vivid":
		return 1.35, 200, 0.10
	case "muted":
		return 0.55, 40, 0.05
	default: // calm
		return 0.85, 150, 0.07
	}
}

// Harmony derives a dark+light palette from one accent and a mood. Surfaces
// carry a whisper of the accent hue (tint strength by mood), body text stays
// near-neutral, accent2 rotates the wheel, and light/dark accents are tuned
// for contrast against their own backgrounds.
func Harmony(req HarmonyRequest) (HarmonyPalette, bool) {
	h, s, _, ok := HexHSL(req.Accent)
	if !ok || req.Accent == "" {
		return HarmonyPalette{}, false
	}
	satMul, rot, tint := moodTuning(req.Mood)
	s2 := clamp01(s * satMul)

	p := HarmonyPalette{
		// Dark mode: deep blue-black ramp tinted toward the accent hue.
		BgDark:      hslHex(h, s2*0.35, 0.085+tint*0.4),
		SurfaceDark: hslHex(h, s2*0.30, 0.135+tint*0.5),
		TextDark:    hslHex(h, s2*0.12, 0.93),
		MutedDark:   hslHex(h, s2*0.18, 0.68),
		AccentDark:  hslHex(h, s2, 0.62),
		Accent2Dark: hslHex(h+rot, s2*0.9, 0.66),
		// Light mode: paper tinted the same direction, ink near-black.
		BgLight:      hslHex(h, s2*0.22, 0.975-tint*0.15),
		SurfaceLight: hslHex(h, s2*0.20, 0.94),
		TextLight:    hslHex(h, s2*0.25, 0.14),
		MutedLight:   hslHex(h, s2*0.15, 0.38),
		AccentLight:  hslHex(h, clamp01(s*satMul*0.95), 0.36),
		Accent2Light: hslHex(h+rot, s2*0.85, 0.40),
	}
	return p, true
}

// ContrastRatio returns WCAG contrast (1–21) between two #hex colours.
func ContrastRatio(fg, bg string) float64 {
	lum := func(hex string) float64 {
		r, g, b, ok := ParseHex(hex)
		if !ok {
			return 0
		}
		lin := func(v float64) float64 {
			v /= 255
			if v <= 0.03928 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	}
	l1, l2 := lum(fg), lum(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	if l2 <= 0 {
		return 21
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// NearestAccessible nudges a foreground toward the background until WCAG AA
// (4.5:1) is met, stepping lightness away from the bg — used by the Studio's
// inline contrast fix-it buttons.
func NearestAccessible(fg, bg string) (string, bool) {
	fh, fs, fl, ok1 := HexHSL(fg)
	_, _, bl, ok2 := HexHSL(bg)
	if !ok1 || !ok2 {
		return fg, false
	}
	const step = 0.04
	var dir float64 // +darkens on dark bgs, -lightens on light bgs
	if bl > 0.5 {
		dir = -1.0
	} else {
		dir = 1.0
	}
	for i := 0; i < 24; i++ {
		cand := hslHex(fh, fs, clamp01(fl+dir*step*float64(i)))
		if ContrastRatio(cand, bg) >= 4.5 {
			return cand, true
		}
	}
	return fg, false
}
