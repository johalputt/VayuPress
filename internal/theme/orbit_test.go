// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"regexp"
	"strings"
	"testing"
)

// Orbit's headline promises are that it is fast, that it stays still, and that
// it is not another glass-card theme. All three are properties of the CSS, so
// all three are checkable here rather than left as a claim in the catalogue
// description — which is exactly the sort of claim this repository has been
// burned by before.

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

// orbitAllCSS is the stylesheet a reader can actually receive: the base plus
// every hero mode, since a mode is emitted rather than selected.
func orbitAllCSS(t *testing.T) string {
	t.Helper()
	css := orbitTokens(t).CustomCSS
	for _, mode := range []string{"grid", "flat", "search"} {
		css += orbitHeroCSS(mode)
	}
	return css
}

// A theme that fetches anything has given up the render-blocking round trip
// that decides LCP, and on a hosted domain an off-origin fetch is refused by the
// Content-Security-Policy outright — so the rule is not merely a performance
// preference, it is the difference between a styled page and an unstyled one.
func TestOrbitMakesNoExternalRequest(t *testing.T) {
	css := orbitAllCSS(t)
	for _, bad := range []string{"http://", "https://", "@import", "//fonts.", "url(//"} {
		if strings.Contains(css, bad) {
			t.Errorf("Orbit CSS contains %q — it must fetch nothing", bad)
		}
	}
	// url() at all is suspect: every ornament in this theme is a gradient or a
	// border.
	if regexp.MustCompile(`url\(\s*['"]?[^'")]`).MatchString(css) {
		t.Error("Orbit CSS references an external asset via url()")
	}
}

// The CLS guarantee, made mechanical. Core Web Vitals scores layout shift, and
// layout shift is caused by animating properties that affect layout. Keyframes
// here may touch transform and opacity and nothing else.
func TestOrbitAnimatesOnlyCompositedProperties(t *testing.T) {
	css := orbitAllCSS(t)

	blocks := keyframeBodies(css)
	if len(blocks) == 0 {
		t.Fatal("no @keyframes found — the scroll-driven entry arrival should be one")
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

	// Transitions are the other half of the same rule, and the easier half to
	// get wrong: the hover rule under a log entry is the obvious candidate for a
	// width transition, and width is a layout property. It is written as a
	// scaleX so it composites.
	for _, prop := range transitionedProperties(css) {
		switch prop {
		case "opacity", "transform", "filter", "color", "border-color",
			"border-bottom-color", "background", "background-color", "box-shadow", "none":
		default:
			t.Errorf("Orbit transitions %q — only compositable/paint properties belong in a transition here", prop)
		}
	}

	// `transition: all` is the third way to animate layout by accident, and it
	// slips past the list above because it names no property at all.
	if regexp.MustCompile(`transition:\s*all\b`).MatchString(css) {
		t.Error("Orbit uses `transition: all` — it can animate a layout property by accident")
	}
}

// THE FALLBACK, not the animation, is what this pins.
//
// A scroll timeline the browser does not implement leaves its animation parked
// in the FROM state. Orbit's FROM state is opacity 0, so an unguarded
// declaration means every entry is invisible on Firefox and Safari — a blank
// archive that looks like a broken site, not a degraded one. The @supports is
// therefore load-bearing and has to be asserted, not assumed.
func TestOrbitScrollAnimationIsGuardedBySupports(t *testing.T) {
	css := orbitTokens(t).CustomCSS

	const guardHead = "@supports (animation-timeline: view())"
	guard := strings.Index(css, guardHead)
	if guard < 0 {
		t.Fatal("Orbit has no @supports (animation-timeline: view()) guard")
	}
	lo, hi := blockExtent(css, guard+len(guardHead))
	if lo < 0 {
		t.Fatal("the @supports guard is not a closed block")
	}

	// EVERY declaration of the property has to sit inside that block, not just
	// the first: one guarded declaration plus one unguarded one is the same bug
	// with extra steps.
	decls := 0
	for i := 0; ; {
		k := strings.Index(css[i:], "animation-timeline:")
		if k < 0 {
			break
		}
		k += i
		i = k + 1
		if k >= guard && k < guard+len(guardHead) {
			continue // the @supports condition itself, not a declaration
		}
		decls++
		if k < lo || k > hi {
			t.Errorf("animation-timeline is declared at offset %d, outside the @supports guard — unsupported browsers would render a blank archive", k)
		}
	}
	if decls == 0 {
		t.Fatal("Orbit declares no scroll-driven animation")
	}
	// Paper is the other surface with no scroll position. A printed page (or a
	// full-page screenshot, which fails identically) would otherwise carry a
	// single entry, because every other one is still parked at opacity 0.
	pr := strings.Index(css, "@media print")
	if pr < 0 {
		t.Fatal("no @media print override — printing the archive would produce one entry on a blank page")
	}
	if lo, hi := blockExtent(css, pr+len("@media print")); lo < 0 || !strings.Contains(css[lo:hi], "opacity: 1") {
		t.Error("the print block does not restore the entries to opacity 1")
	}
	// It has to come AFTER the animation, or it loses the cascade to it.
	if pr < guard {
		t.Error("the print override is declared before the animation it overrides")
	}

	// The FROM state this is protecting against. If a later edit made the
	// keyframe start visible, the guard would stop being load-bearing and this
	// test would be pinning nothing — so assert the reason as well as the fix.
	if !strings.Contains(css, "from { opacity: 0;") {
		t.Error("the arrival keyframe no longer starts hidden; re-check whether the @supports guard is still the thing preventing a blank page")
	}
}

// Motion has to be switchable off.
func TestOrbitRespectsReducedMotion(t *testing.T) {
	css := orbitTokens(t).CustomCSS

	// The arrival animation is switched off by CONSTRUCTION rather than by
	// override: it is only ever declared inside a no-preference query, so there
	// is nothing to un-declare.
	decl := strings.Index(css, "animation-timeline:")
	if decl >= 0 {
		decl = strings.Index(css[decl+1:], "animation-timeline:") + decl + 1
	}
	if decl <= 0 {
		t.Fatal("Orbit declares no scroll-driven animation")
	}
	mq := strings.LastIndex(css[:decl], "@media (prefers-reduced-motion: no-preference)")
	if mq < 0 {
		t.Error("the scroll-driven animation is not inside a prefers-reduced-motion: no-preference query")
	} else if lo, hi := blockExtent(css, mq+len("@media (prefers-reduced-motion: no-preference)")); decl < lo || decl > hi {
		t.Error("the scroll-driven animation sits after the no-preference query rather than inside it")
	}

	// The transitions do need an explicit override.
	r := strings.Index(css, "prefers-reduced-motion: reduce")
	if r < 0 {
		t.Fatal("Orbit has no prefers-reduced-motion: reduce block")
	}
	if !strings.Contains(css[r:], "transition: none") {
		t.Error("the reduced-motion block does not stop the transitions")
	}
	// Switching motion off must not switch the hover AFFORDANCE off: with the
	// transition gone the rule has to land at its end state instantly, not stay
	// invisible.
	if !strings.Contains(css[r:], "transform: scaleX(1)") {
		t.Error("under reduced motion the hover rule never appears — the affordance was removed rather than the motion")
	}
}

// ADR-0136: themes are built ON the sovereign token system, so a scheme change
// moves timing with it instead of leaving hardcoded values behind. Orbit is a
// flat design and consumes no elevation token; that it never HARDCODES one is
// checked in motion_tokens_test.go, which is where the rule belongs.
func TestOrbitConsumesSovereignTokens(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	if !strings.Contains(css, "var(--t,") {
		t.Error("Orbit does not consume the sovereign timing token var(--t, …)")
	}
	// Every transition must carry a literal fallback after the token: theme.css
	// is a separate stylesheet, and a var() with no fallback resolves to nothing
	// if it has not arrived, which drops the duration and makes the transition
	// instant.
	for _, m := range regexp.MustCompile(`var\(--t[a-z-]*\)`).FindAllString(css, -1) {
		t.Errorf("%s has no literal fallback — it degrades to an instant transition if theme.css is absent", m)
	}
}

// The redesign gate. Orbit exists because the catalogue's dark themes had
// collapsed into one look; a version of it that reaches for the same shapes has
// no reason to ship. These are the specific decisions that make it different,
// pinned so a later "tidy-up" cannot quietly reintroduce the card grid.
func TestOrbitIsNotAnotherCardGridTheme(t *testing.T) {
	css := orbitTokens(t).CustomCSS

	// It must REPLACE the base feed layout, not decorate it. The base is
	// `.vayu-post-list { display: grid; grid-template-columns: repeat(auto-fill,
	// minmax(300px, 1fr)) }` — a theme that does not override that is a
	// recolour whatever else it does.
	list := ruleBody(css, ".vayu-post-list {")
	if list == "" {
		t.Fatal("Orbit does not restyle .vayu-post-list — the feed would keep the base card grid")
	}
	if !strings.Contains(list, "display: block") {
		t.Error(".vayu-post-list is not switched off the base auto-fill card grid")
	}
	if !strings.Contains(list, "counter-reset: vayu-entry") {
		t.Error("the entry counter is not reset on the list — numbering would continue across sections")
	}

	card := ruleBody(css, ".vayu-post-card {")
	if card == "" {
		t.Fatal("Orbit does not restyle .vayu-post-card")
	}
	for _, want := range []string{
		"counter-increment: vayu-entry", // numbered log entries
		"border-radius: 0",              // sharp, not the rounded-glass family
		"border: 0",                     // hairline rules, not an outlined card
		"background: none",              // the page is the surface
	} {
		if !strings.Contains(card, want) {
			t.Errorf(".vayu-post-card is missing %q — that is one of the decisions that separate Orbit from the card themes", want)
		}
	}

	// Sharp corners have to reach the shared rules this stylesheet never names,
	// which they only do through the compiled radius tokens.
	tk := orbitTokens(t)
	if tk.RadiusSm != "0" || tk.RadiusLg != "0" {
		t.Errorf("Orbit radii are %q/%q — they must be 0 so --radius/--radius2 carry the sharpness to shared components", tk.RadiusSm, tk.RadiusLg)
	}

	// The date rail is built inside .vayu-post-body on purpose: the renderer
	// puts .vayu-post-meta INSIDE the body, so a rail declared on the card
	// matches nothing and fails silently — which is how the first draft of this
	// stylesheet was wrong.
	body := ruleBody(css, ".vayu-post-body {")
	if !strings.Contains(body, "display: grid") {
		t.Error(".vayu-post-body is not a grid — the date rail only works if the rail is built where .vayu-post-meta actually lives")
	}
}

// Pico's form-group rounding is applied with LONGHANDS from
// `[role="search"] > :first-child` at (0,2,0), which outranks anything selecting
// the field by class. Without a rule of matching shape the search box stays a
// 5rem pill while every other surface in the theme is square — and it does so
// silently, because the theme's own `border-radius: 0` is present and simply
// loses. This pins the override that actually lands.
func TestOrbitDefeatsPicosSearchPill(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	if !strings.Contains(css, `.vayu-search[role="search"] > :first-child`) ||
		!strings.Contains(css, `.vayu-search[role="search"] > :last-child`) {
		t.Error("the [role=search] group override is missing — Pico's pill wins and the field is round")
	}
	// Both ends: the nav form has one child, the search page's has a field and a
	// button, so :last-child alone would leave the button rounded.
	i := strings.Index(css, `.vayu-search[role="search"] > :first-child`)
	if lo, hi := blockExtent(css, i); lo < 0 || !strings.Contains(css[lo:hi], "border-radius: 0") {
		t.Error("the group override does not actually set a zero radius")
	}
}

// The catalogue promises a theme that restyles the whole site, and the shared
// coverage gate only checks three selectors. These are the sections a reader
// actually walks through.
func TestOrbitStylesEverySection(t *testing.T) {
	css := orbitTokens(t).CustomCSS
	for _, sel := range []string{
		".vayu-nav", ".vayu-hero", ".vayu-section-label", ".vayu-post-list",
		".vayu-pagination", ".vayu-empty", ".vayu-article-header",
		".vayu-article-meta", ".vayu-byline", ".vayu-tag", ".vayu-prose .content h2",
		".vayu-related-list", ".vayu-trending-card", ".vayu-err-code", ".vayu-footer",
	} {
		if !strings.Contains(css, sel) {
			t.Errorf("Orbit leaves %s unstyled — applying it would not transform that section", sel)
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
	for _, want := range []string{"default", "search", "grid", "flat"} {
		if !got[want] {
			t.Errorf("hero mode %q is missing from the option", want)
		}
	}
	// A choice with no CSS behind it is a control that does nothing.
	for _, c := range found.Choices {
		if c.Value != "default" && strings.TrimSpace(orbitHeroCSS(c.Value)) == "" {
			t.Errorf("hero mode %q is offered in the admin but emits no CSS", c.Value)
		}
	}
}

// Each mode must actually emit something distinct, and the search mode has one
// job beyond styling: it must hide the nav's search so the page never carries
// two search forms at once.
func TestOrbitHeroModesEmitDistinctCSS(t *testing.T) {
	seen := map[string]string{}
	for _, mode := range []string{"grid", "flat", "search"} {
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
	// The mode is layout only; the field's appearance lives in orbit.css. A
	// second copy here would drift from it.
	if strings.Contains(search, "border-bottom:") {
		t.Error("the search mode restates the field's borders — that rule belongs in orbit.css, once")
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
		// The description names hero modes an operator will look for in the
		// dropdown. Describing a mode that no longer exists is how the old
		// "concentric rings" copy outlived the rings themselves.
		for _, claim := range []string{"tick rail", "grid", "search"} {
			if !strings.Contains(strings.ToLower(e.Meta.Description), claim) {
				t.Errorf("the store description does not mention the %q hero mode", claim)
			}
		}
		for _, gone := range []string{"orbit rings", "light beam", "glass card"} {
			if strings.Contains(strings.ToLower(e.Meta.Description), gone) {
				t.Errorf("the store description still promises %q, which the theme no longer has", gone)
			}
		}
		return
	}
	t.Fatal("Orbit is missing from Store()")
}

// keyframeBodies returns the body of every @keyframes rule, matched by counting
// braces rather than by regex.
//
// The regex this replaces was `@keyframes[^{]*\{(.*?)\n\}`, which assumes the
// rule ends at the first newline-then-brace. A keyframe written on a single line
// ran straight past that pattern and captured a hundred lines of ordinary CSS —
// then reported width, height and font-size as animated properties. The gate was
// failing on rules that are not keyframes at all, which would have been "fixed"
// by loosening it and losing the check entirely.
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

// transitionedProperties returns the property named at the head of each
// comma-separated part of every `transition:` declaration.
func transitionedProperties(css string) []string {
	var out []string
	for i := 0; ; {
		k := strings.Index(css[i:], "transition:")
		if k < 0 {
			return out
		}
		k += i + len("transition:")
		end := strings.IndexAny(css[k:], ";}")
		if end < 0 {
			end = len(css) - k
		}
		// Split on the commas that separate transitions, not the ones inside a
		// timing function's argument list.
		val, depth, part := css[k:k+end], 0, strings.Builder{}
		flush := func() {
			if f := strings.Fields(part.String()); len(f) > 0 {
				out = append(out, f[0])
			}
			part.Reset()
		}
		for _, r := range val {
			switch {
			case r == '(':
				depth++
			case r == ')':
				depth--
			case r == ',' && depth == 0:
				flush()
				continue
			}
			part.WriteRune(r)
		}
		flush()
		i = k + end
	}
}

// blockExtent returns the byte range strictly inside the braced block that
// opens at or after from, or (-1, -1) if there is no closed block there.
func blockExtent(css string, from int) (lo, hi int) {
	open := strings.Index(css[from:], "{")
	if open < 0 {
		return -1, -1
	}
	open += from
	depth := 0
	for j := open; j < len(css); j++ {
		switch css[j] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth == 0 {
			return open + 1, j - 1
		}
	}
	return -1, -1
}

// ruleBody returns the declarations of the first rule whose selector text
// matches sel exactly (including the trailing " {"), or "" if there is none.
func ruleBody(css, sel string) string {
	i := strings.Index(css, sel)
	if i < 0 {
		return ""
	}
	i += len(sel)
	j := strings.Index(css[i:], "}")
	if j < 0 {
		return ""
	}
	return css[i : i+j]
}
