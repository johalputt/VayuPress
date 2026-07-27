// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestPortalScriptShipsOnlyWhenMembershipIsOn — the widget's own init() fetches
// /api/v1/members/me and returns immediately unless membership is enabled. So on
// an install with membership off, shipping it made every visitor download ~10 KiB
// and pay an extra request to learn something the SERVER ALREADY KNEW when it
// rendered the page.
func TestPortalScriptShipsOnlyWhenMembershipIsOn(t *testing.T) {
	t.Cleanup(func() { SetMembershipEnabled(false) })

	SetMembershipEnabled(false)
	if got := PortalJSLink(); got != "" {
		t.Errorf("membership off, but the portal script still ships: %q", got)
	}

	SetMembershipEnabled(true)
	got := string(PortalJSLink())
	if !strings.Contains(got, "/static/js/portal.js") {
		t.Errorf("membership on, but the portal script is missing: %q", got)
	}
	if !strings.Contains(got, "v="+PortalJSVersion()) {
		t.Error("the script tag lost its cache-busting version")
	}
}

// TestActiveSettingsDrivesThePortalGate — the gate must be derived at the single
// chokepoint every settings path already goes through, not set by hand at each
// call site. Otherwise a future save path forgets it and the renderer ships a
// script that contradicts the live setting.
func TestActiveSettingsDrivesThePortalGate(t *testing.T) {
	t.Cleanup(func() { SetMembershipEnabled(false) })

	SetActiveSettings(SiteSettings{ShowMembership: true})
	if PortalJSLink() == "" {
		t.Error("enabling membership through SetActiveSettings did not open the portal gate")
	}

	SetActiveSettings(SiteSettings{ShowMembership: false})
	if PortalJSLink() != "" {
		t.Error("disabling membership through SetActiveSettings did not close the portal gate")
	}
}

// relLuminance / compositeOver / contrast mirror how a browser — and Lighthouse
// — actually measure a colour pair: a translucent background is blended onto
// what sits behind it FIRST, and the text is measured against that result.
func relLuminance(r, g, b int) float64 {
	f := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

func compositeOver(r, g, b int, alpha float64, br, bg, bb int) (int, int, int) {
	mix := func(f, k int) int { return int(math.Round(float64(f)*alpha + float64(k)*(1-alpha))) }
	return mix(r, br), mix(g, bg), mix(b, bb)
}

func hexRGB(t *testing.T, h string) (int, int, int) {
	t.Helper()
	var r, g, b int
	if _, err := fmt.Sscanf(strings.TrimPrefix(h, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("bad hex %q: %v", h, err)
	}
	return r, g, b
}

func contrast(l1, l2 float64) float64 {
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// TestTintedUIClearsAAInBothModes is the regression test for three contrast
// failures that only existed in LIGHT mode, which is why they survived on a site
// that renders dark.
//
// Tags, inline code and the footer badge all draw their text on a TRANSLUCENT
// tint, not on the page background. A tint lightens a light page, so it eats the
// contrast the same colour has against the page itself — every one of these
// measured fine against the raw background and failed against what a reader
// actually sees. Nothing in this codebase measured a composited colour, so
// nothing could catch it.
func TestTintedUIClearsAAInBothModes(t *testing.T) {
	const aa = 4.5
	// Read the values from the SHIPPED stylesheet, never hardcode them. A first
	// version of this test pinned the hex codes inline and therefore kept passing
	// when the real CSS was reverted to a failing colour — it verified the
	// arithmetic, not the product. A check that cannot fail when the thing it
	// guards breaks is not a check.
	// :root is the dark base; the light block overrides only what differs. Model
	// that cascade rather than assuming one block declares everything — the dark
	// override block does not redeclare --green, it inherits it.
	base := themeBlockVars(t, ":root{")
	for _, m := range []struct{ mode, page, block string }{
		{"light", "#fafafa", "html[data-theme=light]{"},
		{"dark", "#080b10", "html[data-theme=dark]{"},
	} {
		vars := map[string]string{}
		for k, v := range base {
			vars[k] = v
		}
		for k, v := range themeBlockVars(t, m.block) {
			vars[k] = v
		}
		accent, green := vars["--accent"], vars["--green"]
		if accent == "" || green == "" {
			t.Fatalf("%s mode: could not read --accent/--green from the shipped stylesheet", m.mode)
		}
		pr, pg, pb := hexRGB(t, m.page)
		for _, c := range []struct {
			what       string
			fg         string
			tr, tg, tb int
			alpha      float64
		}{
			{"tag chip / inline code on the indigo tint", accent, 99, 102, 241, 0.10},
			{"footer badge on the green tint", green, 34, 197, 94, 0.10},
		} {
			br, bg, bb := compositeOver(c.tr, c.tg, c.tb, c.alpha, pr, pg, pb)
			fr, fgc, fb := hexRGB(t, c.fg)
			got := contrast(relLuminance(fr, fgc, fb), relLuminance(br, bg, bb))
			if got < aa {
				t.Errorf("%s mode: %s — %s on the composited tint = %.2f:1, below AA %.1f:1",
					m.mode, c.what, c.fg, got, aa)
			}
		}
	}
}

// TestTagChipIsATappableTarget — a tag was 3px of vertical padding on 12px text,
// roughly 20px tall. Pointer targets want at least 24×24 CSS px with spacing
// between neighbours, and tags render as a dense row, so every one of them
// failed together.
func TestTagChipIsATappableTarget(t *testing.T) {
	css := articleCSSMin
	i := strings.Index(css, ".tags a{")
	if i < 0 {
		t.Fatal("the .tags a rule has disappeared from the article stylesheet")
	}
	rule := css[i : i+strings.Index(css[i:], "}")]
	for _, want := range []string{"min-height:24px", "margin:"} {
		if !strings.Contains(rule, want) {
			t.Errorf("tag chip rule is missing %q — it renders below the minimum tap target:\n  %s", want, rule)
		}
	}
	if strings.Contains(rule, "padding:3px") {
		t.Error("tag chip still uses the 3px padding that made it ~20px tall")
	}
	// Pin the coupling the contrast test above relies on: it measures --accent
	// against the tint, so these rules must actually USE --accent. Without this,
	// swapping them back to the lighter --accent2 would reintroduce the failure
	// while the contrast test kept measuring a colour the CSS no longer applies.
	for _, sel := range []string{".tags a{", ".content code{"} {
		j := strings.Index(css, sel)
		if j < 0 {
			t.Fatalf("%s has disappeared from the article stylesheet", sel)
		}
		r := css[j : j+strings.Index(css[j:], "}")]
		if !strings.Contains(r, "color:var(--accent)") {
			t.Errorf("%s draws on a tinted background but does not use var(--accent):\n  %s", sel, r)
		}
	}
}

// themeBlockVars extracts the custom properties declared in one theme block of
// the shipped article stylesheet, so contrast tests measure what actually ships.
func themeBlockVars(t *testing.T, marker string) map[string]string {
	t.Helper()
	i := strings.Index(articleCSSMin, marker)
	if i < 0 {
		t.Fatalf("theme block %q not found in the article stylesheet", marker)
	}
	body := articleCSSMin[i+len(marker):]
	body = body[:strings.Index(body, "}")]
	out := map[string]string{}
	for _, decl := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
