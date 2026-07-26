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
func TestFlagshipThemesConsumeMotionTokens(t *testing.T) {
	checkedApex, checkedVayu := false, false
	for _, p := range AllPresets() {
		css := p.CustomCSS
		if strings.Contains(css, ".apex-bento__cell") {
			checkedApex = true
			for _, want := range []string{"var(--sh-lg", "var(--sh,", "var(--t-slow", "var(--t,"} {
				if !strings.Contains(css, want) {
					t.Errorf("Apex flagship does not consume sovereign token %q — it should be built on the token system", want)
				}
			}
		}
		if strings.Contains(css, ".vayu-post-card {") {
			checkedVayu = true
			for _, want := range []string{"var(--sh-lg", "var(--t,"} {
				if !strings.Contains(css, want) {
					t.Errorf("Vayu flagship does not consume sovereign token %q", want)
				}
			}
		}
	}
	if !checkedApex {
		t.Error("Apex flagship preset not found among AllPresets()")
	}
	if !checkedVayu {
		t.Error("Vayu flagship preset not found among AllPresets()")
	}
}
