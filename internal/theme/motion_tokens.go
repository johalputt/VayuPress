package theme

// motion_tokens.go — the sanctioned motion/elevation/spacing design-primitive
// vocabulary (ADR-0136) plus the canonical --vp-* token set, exported so the
// VCB theme contract (internal/vcb/validate) can validate third-party themes
// that reference them from custom_css. Deriving the --vp-* set from a real
// compile keeps the published contract from ever drifting away from what the
// compiler actually emits.

import (
	"regexp"
	"strings"
	"sync"
)

// MotionTokens returns the ADR-0136 premium-UX design primitives every compiled
// theme shares: the scheme-adaptive elevation scale, easing curves, motion
// durations, composed transitions, the spacing scale and the z-index scale —
// as both the canonical --vp-* names and the bare aliases exposed for
// custom_css authors. This is the motion/elevation vocabulary the VCB theme
// contract documents for third-party themes.
func MotionTokens() []string {
	return []string{
		// elevation (scheme-adaptive)
		"--vp-sh-sm", "--vp-sh", "--vp-sh-lg", "--sh-sm", "--sh", "--sh-lg",
		// easing curves
		"--vp-ease", "--vp-ease-out", "--vp-ease-in", "--vp-ease-spring",
		"--ease", "--ease-out", "--ease-spring",
		// durations
		"--vp-dur-fast", "--vp-dur", "--vp-dur-slow", "--dur-fast", "--dur", "--dur-slow",
		// composed transitions (duration + easing)
		"--vp-t-fast", "--vp-t", "--vp-t-slow", "--vp-t-spring",
		"--t", "--t-fast", "--t-slow", "--t-spring",
		// spacing scale
		"--vp-sp-1", "--vp-sp-2", "--vp-sp-3", "--vp-sp-4", "--vp-sp-5",
		"--vp-sp-6", "--vp-sp-8", "--vp-sp-10", "--vp-sp-12",
		// z-index scale
		"--vp-z-base", "--vp-z-sticky", "--vp-z-overlay", "--vp-z-modal", "--vp-z-toast",
	}
}

// vpTokenDefRE matches a --vp-* custom-property DEFINITION (name followed by a
// colon). A var(--vp-…) reference is followed by ')' or ',', so it is not
// matched — only names the compiler actually defines are collected.
var vpTokenDefRE = regexp.MustCompile(`(--vp-[a-z0-9-]+)\s*:`)

var (
	vpTokenOnce  sync.Once
	vpTokenNames map[string]bool
)

// VPTokenNames returns the set of canonical --vp-* custom-property names the
// theme compiler emits — colour, typography and layout tokens plus the
// ADR-0136 motion/elevation/spacing/z primitives. It is derived by compiling
// every built-in preset and collecting the token definitions, so it can never
// drift from what the compiler provides. A theme never defines --vp-* itself
// (those are compiler-owned), so the VCB validator can treat a custom_css
// var(--vp-…) reference to a name absent from this set as a typo that silently
// resolves to nothing.
func VPTokenNames() map[string]bool {
	vpTokenOnce.Do(func() {
		names := map[string]bool{}
		// Seed with the advertised ADR-0136 motion/elevation vocabulary so the
		// published contract's --vp-* tokens are always accepted by the VCB
		// validator, then union in every --vp-* the compiler actually defines.
		for _, tok := range MotionTokens() {
			if strings.HasPrefix(tok, "--vp-") {
				names[tok] = true
			}
		}
		for _, p := range AllPresets() {
			css, err := CompileCSS(p)
			if err != nil {
				continue
			}
			for _, m := range vpTokenDefRE.FindAllStringSubmatch(css, -1) {
				names[m[1]] = true
			}
		}
		vpTokenNames = names
	})
	out := make(map[string]bool, len(vpTokenNames))
	for k := range vpTokenNames {
		out[k] = true
	}
	return out
}
