// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"strings"
	"testing"
)

// TestCompileEmitsMotionTokens is the ADR-0136 regression guard: every compiled
// theme must expose the shared motion / elevation / spacing token vocabulary so
// public themes inherit the same premium primitives the admin uses. These are
// additive — no existing rule references them — so their only contract is
// "present and well-formed".
func TestCompileEmitsMotionTokens(t *testing.T) {
	css, err := CompileCSS(Default())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, tok := range []string{
		// scheme-adaptive elevation
		"--vp-sh-sm:", "--vp-sh:", "--vp-sh-lg:",
		// easing + durations + composed transitions
		"--vp-ease:", "--vp-ease-spring:", "--vp-dur:", "--vp-dur-fast:", "--vp-t:", "--vp-t-spring:",
		// spacing + z-index scales
		"--vp-sp-4:", "--vp-z-modal:",
		// bare aliases for CustomCSS authors
		"--sh:var(--vp-sh)", "--t:var(--vp-t)", "--ease:var(--vp-ease)",
	} {
		if !strings.Contains(css, tok) {
			t.Errorf("compiled theme is missing token %q", tok)
		}
	}

	// Shadows must adapt to the colour scheme: the light blocks carry the
	// slate-tinted shadow, the dark blocks the black one.
	if !strings.Contains(css, "rgba(15,23,42,.10)") {
		t.Error("light-scheme shadow token not emitted")
	}
	if !strings.Contains(css, "rgba(0,0,0,.45)") {
		t.Error("dark-scheme shadow token not emitted")
	}

	// The primitives must not disturb the existing colour/radius contract the
	// VCB validator and public stylesheet depend on.
	for _, tok := range []string{"--accent:", "--radius:", "--vp-bg:"} {
		if !strings.Contains(css, tok) {
			t.Errorf("existing token %q regressed out of the output", tok)
		}
	}
}

// TestMotionTokensSurviveCustomCSS proves the primitive block is emitted even
// when a theme ships CustomCSS (it is appended after, not in place of, tokens).
func TestMotionTokensSurviveCustomCSS(t *testing.T) {
	tk := Default()
	tk.CustomCSS = ".hero{letter-spacing:.02em}"
	css, err := CompileCSS(tk)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(css, "--vp-t:") || !strings.Contains(css, ".hero{letter-spacing:.02em}") {
		t.Error("motion tokens or CustomCSS missing when both present")
	}
}

// TestMotionTokensAreCompilerBacked guards the exported ADR-0136 vocabulary
// against drift: every canonical --vp-* name MotionTokens() advertises must be
// a token the compiler actually defines, and must appear in VPTokenNames().
func TestMotionTokensAreCompilerBacked(t *testing.T) {
	css, err := CompileCSS(Default())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	vp := VPTokenNames()
	for _, tok := range MotionTokens() {
		if !strings.HasPrefix(tok, "--vp-") {
			continue // bare aliases (--sh, --t, …) are not in the --vp- namespace
		}
		if !strings.Contains(css, tok+":") {
			t.Errorf("MotionTokens() advertises %q but the compiler never defines it", tok)
		}
		if !vp[tok] {
			t.Errorf("VPTokenNames() is missing compiler token %q", tok)
		}
	}
}

// TestVPTokenNamesSurface pins that the derived --vp-* set covers colour,
// layout and the new motion families, and excludes names the compiler never
// emits (so the VCB typo check is sound).
func TestVPTokenNamesSurface(t *testing.T) {
	vp := VPTokenNames()
	for _, want := range []string{"--vp-accent", "--vp-text", "--vp-radius-lg", "--vp-sh-lg", "--vp-t", "--vp-sp-4", "--vp-z-modal"} {
		if !vp[want] {
			t.Errorf("VPTokenNames() should include %q", want)
		}
	}
	for _, bogus := range []string{"--vp-nope", "--vp-shadow", "--vp-transition"} {
		if vp[bogus] {
			t.Errorf("VPTokenNames() should NOT include non-existent token %q", bogus)
		}
	}
}

// TestFlagshipThemesConsumeMotionTokens is the deliverable guard for UX P4:
// the flagship themes must be built ON the sovereign token system — consuming
// the scheme-adaptive elevation + motion tokens — not hardcoding their own.
//
// Themes are identified by preset NAME, not by a signature selector. The
// earlier version fingerprinted Vayu by `.vayu-post-card {`, which is not a
// signature at all — it is the shared public markup, and the moment a second
// theme restyled that card it inherited Vayu's expectations. Orbit did, and it
// is a deliberately flat design with no elevation to assert on; the check would
// have forced a shadow into it purely to satisfy a matcher.
func TestFlagshipThemesConsumeMotionTokens(t *testing.T) {
	// Each flagship is checked for the token families its design actually uses.
	// A theme that declines elevation is not thereby excused from the token
	// system — see the hardcoded-shadow check below, which is the rule that
	// applies to it.
	want := map[string][]string{
		"Apex":  {"var(--sh-lg", "var(--sh,", "var(--t-slow", "var(--t,"},
		"Vayu":  {"var(--sh-lg", "var(--t,"},
		"Orbit": {"var(--t,"},
	}
	seen := map[string]bool{}
	for _, p := range AllPresets() {
		toks, ok := want[p.Name]
		if !ok {
			continue
		}
		seen[p.Name] = true
		for _, tok := range toks {
			if !strings.Contains(p.CustomCSS, tok) {
				t.Errorf("%s flagship does not consume sovereign token %q — it should be built on the token system", p.Name, tok)
			}
		}
		// Orbit is exempt from the elevation tokens because it has no elevation
		// at all — hairlines and rules, never a raised surface. That is a claim
		// about the design, so it is checked rather than trusted: the moment a
		// shadow appears the exemption above stops being honest and this fails.
		//
		// Apex and Vayu are not held to the same line: both cast an
		// accent-tinted glow, which is a coloured effect rather than the neutral
		// elevation --sh* provides, and forcing it through the token would flatten
		// a deliberate part of their look.
		if p.Name != "Orbit" {
			continue
		}
		for _, decl := range shadowDecls(p.CustomCSS) {
			if strings.TrimSpace(decl) == "none" {
				continue
			}
			t.Errorf("Orbit declares an elevation (%q) — it is exempted from the --sh* tokens on the grounds that it has none", decl)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s flagship preset not found among AllPresets()", name)
		}
	}
}

// shadowDecls returns the value of every box-shadow declaration in css.
func shadowDecls(css string) []string {
	var out []string
	for i := 0; ; {
		k := strings.Index(css[i:], "box-shadow:")
		if k < 0 {
			return out
		}
		k += i + len("box-shadow:")
		end := strings.IndexAny(css[k:], ";}")
		if end < 0 {
			return append(out, strings.TrimSpace(css[k:]))
		}
		out = append(out, strings.TrimSpace(css[k:k+end]))
		i = k + end
	}
}
