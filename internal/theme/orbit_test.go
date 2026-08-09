// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"regexp"
	"strings"
	"testing"
)

// Orbit's headline promise is that it is fast and that it stays still. Both are
// properties of the CSS, so both are checkable here rather than left as a claim
// in the catalogue description — which is exactly the sort of claim this
// repository has been burned by before.

func orbitTokens(t *testing.T) Tokens {
	t.Helper()
	for _, p := range AllPresets() {
		if p.Name == "Orbit" {
			return p
		}
	}
	t.Fatal("Orbit is not in AllPresets()")
	return Tokens{}
}

// A theme that fetches anything has given up the render-blocking round trip
// that decides LCP, and on a hosted domain an off-origin fetch is refused by the
// Content-Security-Policy outright — so the rule is not merely a performance
// preference, it is the difference between a styled page and an unstyled one.
func TestOrbitMakesNoExternalRequest(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	for _, bad := range []string{"http://", "https://", "@import", "//fonts.", "url(//"} {
		if strings.Contains(css, bad) {
			t.Errorf("Orbit CSS contains %q — it must fetch nothing", bad)
		}
	}
	// url() at all is suspect: every ornament in this theme is a gradient.
	if regexp.MustCompile(`url\(\s*['"]?[^'")]`).MatchString(css) {
		t.Error("Orbit CSS references an external asset via url()")
	}
}

// The CLS guarantee, made mechanical. Core Web Vitals scores layout shift, and
// layout shift is caused by animating properties that affect layout. Keyframes
// here may touch transform and opacity and nothing else.
func TestOrbitAnimatesOnlyCompositedProperties(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	for _, mode := range []string{"beam", "flat", "search"} {
		css += orbitHeroCSS(mode)
	}

	blocks := keyframeBodies(css)
	if len(blocks) == 0 {
		t.Fatal("no @keyframes found — the signature ring drift should be one")
	}
	layoutProps := []string{
		"width", "height", "top:", "left:", "right:", "bottom:",
		"margin", "padding", "font-size", "inset:", "border-width",
	}
	for _, b := range blocks {
		for _, p := range layoutProps {
			if strings.Contains(b, p) {
				t.Errorf("a keyframe animates %q — that is a layout shift, which is what CLS measures", p)
			}
		}
	}

	// Every transition must name its properties. `transition: all` is the other
	// common way to animate layout by accident.
	if regexp.MustCompile(`transition:\s*all\b`).MatchString(css) {
		t.Error("Orbit uses `transition: all` — it can animate a layout property by accident")
	}
}

// Motion has to be switchable off, and the guard has to cover the one animation
// that runs forever.
func TestOrbitRespectsReducedMotion(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	i := strings.Index(css, "prefers-reduced-motion")
	if i < 0 {
		t.Fatal("Orbit has no prefers-reduced-motion block")
	}
	if !strings.Contains(css[i:], "animation: none") {
		t.Error("the reduced-motion block does not stop the infinite ring animation")
	}
	if !strings.Contains(css, "vayuOrbitDrift") {
		t.Error("the signature ring animation is missing")
	}
}

// ADR-0136: themes are built ON the sovereign token system, so a scheme change
// moves elevation and timing with it instead of leaving hardcoded values behind.
func TestOrbitConsumesSovereignTokens(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	for _, want := range []string{"var(--sh-lg", "var(--sh-sm", "var(--t,"} {
		if !strings.Contains(css, want) {
			t.Errorf("Orbit does not consume sovereign token %q", want)
		}
	}
}

// The hero mode is an Orbit-only control. herostyle already exists as a SHARED
// option on a different axis (centered/left/minimal/boxed), and overloading that
// key would have silently changed every theme that uses it.
func TestOrbitHeroModeIsScopedToOrbit(t *testing.T) {
	var found *Option
	for _, to := range PerThemeOptions() {
		if to.Option.Key != "orbithero" {
			continue
		}
		if len(to.Themes) != 1 || to.Themes[0] != "Orbit" {
			t.Errorf("orbithero applies to %v — it must be Orbit only", to.Themes)
		}
		o := to.Option
		found = &o
	}
	if found == nil {
		t.Fatal("orbithero is not registered as a per-theme option")
	}
	// The collision this test exists to prevent: herostyle is shared, and an
	// Orbit-only key must not shadow any of the shared ones.
	for _, shared := range AllOptions() {
		if shared.Key == "orbithero" {
			t.Error("orbithero collides with a shared option key")
		}
	}

	got := map[string]bool{}
	for _, c := range found.Choices {
		got[c.Value] = true
	}
	for _, want := range []string{"default", "search", "beam", "flat"} {
		if !got[want] {
			t.Errorf("hero mode %q is missing from the option", want)
		}
	}
}

// Each mode must actually emit something, and the search mode has one job
// beyond styling: it must hide the nav's search so the page never carries two
// search forms at once.
func TestOrbitHeroModesEmitDistinctCSS(t *testing.T) {
	seen := map[string]string{}
	for _, mode := range []string{"beam", "flat", "search"} {
		css := orbitHeroCSS(mode)
		if strings.TrimSpace(css) == "" {
			t.Errorf("hero mode %q emits no CSS", mode)
			continue
		}
		for other, prev := range seen {
			if prev == css {
				t.Errorf("hero modes %q and %q emit identical CSS", mode, other)
			}
		}
		seen[mode] = css
	}
	if orbitHeroCSS("default") != "" {
		t.Error(`the "default" mode must emit nothing — the base CSS already is the default`)
	}
	if orbitHeroCSS("nonsense") != "" {
		t.Error("an unknown mode must emit nothing rather than guess")
	}

	search := orbitHeroCSS("search")
	if !strings.Contains(search, ".vayu-hero-search") {
		t.Error("the search mode does not reveal the hero search form")
	}
	if !strings.Contains(search, ".vayu-nav .vayu-search") {
		t.Error("the search mode does not hide the nav search — the page would carry two search forms")
	}
}

// The store card is what an operator picks from; a theme with no metadata falls
// back to a generated stub and looks unfinished beside the rest of the catalogue.
func TestOrbitHasStoreMetadata(t *testing.T) {
	for _, e := range Store() {
		if e.Meta.Name != "Orbit" {
			continue
		}
		if e.Meta.Tagline == "" || len(e.Meta.Description) < 120 {
			t.Error("Orbit's store metadata is thin")
		}
		if e.Meta.Category == "" {
			t.Error("Orbit has no store category")
		}
		return
	}
	t.Fatal("Orbit is missing from Store()")
}

// keyframeBodies returns the body of every @keyframes rule, matched by counting
// braces rather than by regex.
//
// The regex this replaces was `@keyframes[^{]*\{(.*?)\n\}`, which assumes the
// rule ends at the first newline-then-brace. Orbit's ring keyframe is written on
// a single line, so that pattern ran straight past it and captured a hundred
// lines of ordinary CSS — then reported width, height and font-size as animated
// properties. The gate was failing on rules that are not keyframes at all, which
// would have been "fixed" by loosening it and losing the check entirely.
func keyframeBodies(css string) []string {
	var out []string
	for i := 0; ; {
		k := strings.Index(css[i:], "@keyframes")
		if k < 0 {
			return out
		}
		k += i
		open := strings.Index(css[k:], "{")
		if open < 0 {
			return out
		}
		open += k
		depth, j := 0, open
		for ; j < len(css); j++ {
			switch css[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if j >= len(css) {
			return out
		}
		out = append(out, css[open+1:j])
		i = j + 1
	}
}
