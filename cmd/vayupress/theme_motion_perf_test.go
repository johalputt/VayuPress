// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// styleSheets are the two hand-written stylesheets that carry the public site's
// premium motion layer: the flagship theme and the member portal overlay.
var styleSheets = map[string]string{
	"vayu theme":     "../../internal/theme/vayu.css",
	"portal overlay": "../../static/css/portal.css",
}

// TestPublicMotionLayerStaysFastAndSovereign pins the promises the motion layer
// makes. A flagship theme that costs speed is not a flagship theme, and a
// stylesheet that reaches off-origin breaks both sovereignty and the strict CSP.
func TestPublicMotionLayerStaysFastAndSovereign(t *testing.T) {
	// An off-origin request in CSS is a blocking dependency the operator did not
	// ask for, and it defeats the "no external assets" guarantee.
	external := regexp.MustCompile(`(?i)@import|url\(\s*['"]?(https?:)?//`)
	// Animating a layout property forces reflow on every frame. Composited
	// properties (transform/opacity/filter/background-position/box-shadow/color)
	// do not. This catches a transition or animation naming a layout property.
	layoutAnim := regexp.MustCompile(`(?i)(transition|animation)[^;{}]*\b(width|height|top|left|right|bottom|margin|padding)\b[^;{}]*;`)

	for name, path := range styleSheets {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			css := string(b)

			if m := external.FindString(css); m != "" {
				t.Errorf("stylesheet reaches off-origin (%q) — it must stay self-contained", m)
			}
			if m := layoutAnim.FindString(css); m != "" {
				t.Errorf("animates a layout property (%q) — use transform/opacity so it stays composited", strings.TrimSpace(m))
			}
			// Motion must be optional. Anyone who has asked their system to reduce
			// motion gets the design without the movement.
			if !strings.Contains(css, "prefers-reduced-motion") {
				t.Error("no prefers-reduced-motion block — motion must be dismissable")
			}
			// Balanced braces: an unclosed rule silently swallows everything after it.
			if strings.Count(css, "{") != strings.Count(css, "}") {
				t.Errorf("unbalanced braces: %d open vs %d close", strings.Count(css, "{"), strings.Count(css, "}"))
			}
		})
	}
}

// TestGradientHeadingCannotRenderInvisible: the flagship hero heading shows its
// gradient through the glyphs, which needs color:transparent. On an engine without
// background-clip:text that would erase the single most important piece of text on
// the page, so the rule must sit behind an @supports guard.
func TestGradientHeadingCannotRenderInvisible(t *testing.T) {
	b, err := os.ReadFile(styleSheets["vayu theme"])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	css := string(b)
	// The risky declaration is a BARE `color: transparent`, which every engine
	// honours — so where background-clip:text is unsupported the glyphs are simply
	// invisible. `-webkit-text-fill-color: transparent` is the safe equivalent:
	// an engine that cannot clip a background to text ignores it too, leaving the
	// solid colour. Match only the bare form (a declaration boundary before it, so
	// the -webkit-text-fill-color property name is not a false hit).
	bare := regexp.MustCompile(`(?i)(^|[;{]|\n)\s*color:\s*transparent`)
	for _, m := range bare.FindAllString(css, -1) {
		// Allow it only inside an @supports background-clip guard.
		i := strings.Index(css, m)
		before := css[:i]
		j := strings.LastIndex(before, "@supports")
		if j < 0 || !strings.Contains(before[j:], "background-clip") {
			t.Errorf("bare %q must use -webkit-text-fill-color, or sit inside an "+
				"@supports background-clip guard — otherwise the heading disappears "+
				"where the technique is unsupported", strings.TrimSpace(m))
		}
	}
}

// TestFlagshipThemeSkipsOffscreenWork: content-visibility on the feed cards is the
// one change here that measurably helps Core Web Vitals rather than just looking
// good, and it needs an intrinsic size or the scrollbar jumps as cards render.
func TestFlagshipThemeSkipsOffscreenWork(t *testing.T) {
	b, err := os.ReadFile(styleSheets["vayu theme"])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	css := string(b)
	if !strings.Contains(css, "content-visibility: auto") {
		t.Error("feed cards should skip rendering work while offscreen")
	}
	if !strings.Contains(css, "contain-intrinsic-size") {
		t.Error("content-visibility needs contain-intrinsic-size, else the page height jumps")
	}
}
