// SPDX-License-Identifier: Apache-2.0

package theme

// validate.go — exported validation primitives for the Vayu Compatibility
// Bible (VCB, ADR-0135). Third-party themes are checked by internal/vcb/validate
// BEFORE import/apply, against the exact same rules CompileCSS enforces at
// apply time. Everything here is a thin wrapper over the compiler's own
// primitives (validHex / safeDimension / safeFont) so the published
// compatibility contract can never drift from what the compiler actually does.

import "strings"

// TokenValue is one named design-token value, for validators that iterate the
// token groups below and report failures by field name.
type TokenValue struct {
	Field string
	Value string
}

// colorFields is the single source of truth for which Tokens fields are hex
// colours. CompileCSS validates exactly this list; ColorTokens exposes the same
// list to the VCB validator.
func colorFields(t *Tokens) []struct {
	Name string
	Ptr  *string
} {
	return []struct {
		Name string
		Ptr  *string
	}{
		{"BgDark", &t.BgDark},
		{"SurfaceDark", &t.SurfaceDark},
		{"TextDark", &t.TextDark},
		{"MutedDark", &t.MutedDark},
		{"AccentDark", &t.AccentDark},
		{"Accent2Dark", &t.Accent2Dark},
		{"HiDark", &t.HiDark},
		{"GreenDark", &t.GreenDark},
		{"BgLight", &t.BgLight},
		{"SurfaceLight", &t.SurfaceLight},
		{"TextLight", &t.TextLight},
		{"MutedLight", &t.MutedLight},
		{"AccentLight", &t.AccentLight},
		{"Accent2Light", &t.Accent2Light},
		{"HiLight", &t.HiLight},
	}
}

// ColorTokens returns the 15 colour tokens CompileCSS validates, in compiler
// order. An empty value is allowed (the variable is simply not emitted); a
// non-empty value must satisfy ValidHexColor or the compile aborts.
func ColorTokens(t Tokens) []TokenValue {
	fs := colorFields(&t)
	out := make([]TokenValue, len(fs))
	for i, f := range fs {
		out[i] = TokenValue{Field: f.Name, Value: *f.Ptr}
	}
	return out
}

// DimensionTokens returns the tokens CompileCSS passes through safeDimension.
// An invalid dimension is SILENTLY DROPPED at compile time — the validator
// turns that silence into an explicit finding.
func DimensionTokens(t Tokens) []TokenValue {
	return []TokenValue{
		{Field: "FontSizeBase", Value: t.FontSizeBase},
		{Field: "LineHeight", Value: t.LineHeight},
		{Field: "MaxWidth", Value: t.MaxWidth},
		{Field: "RadiusSm", Value: t.RadiusSm},
		{Field: "RadiusLg", Value: t.RadiusLg},
	}
}

// FontTokens returns the tokens CompileCSS passes through safeFont. Characters
// outside the sanitiser's allowlist are SILENTLY STRIPPED at compile time.
func FontTokens(t Tokens) []TokenValue {
	return []TokenValue{
		{Field: "FontSans", Value: t.FontSans},
		{Field: "FontMono", Value: t.FontMono},
	}
}

// ValidHexColor reports whether v is empty (allowed) or a hex colour the
// compiler accepts (#rgb … #rrggbbaa).
func ValidHexColor(v string) bool {
	_, err := validHex("color", v)
	return err == nil
}

// SafeDimensionOK reports whether v survives the compiler's dimension filter:
// empty is allowed; otherwise it must match the number+unit shape or the
// compiler drops it.
func SafeDimensionOK(v string) bool {
	return strings.TrimSpace(v) == "" || safeDimension(v) != ""
}

// SafeFontOK reports whether the compiler's font sanitiser preserves the
// declared meaning of v. Quote characters are exempt: stripping them is
// harmless and idiomatic (every built-in preset writes 'Segoe UI'-style
// quoting, and unquoted multi-word family names are valid CSS). Anything else
// the sanitiser removes — backslash, brackets, braces, ;:<> or non-ASCII —
// changes what renders, so the declaration fails the contract.
func SafeFontOK(v string) bool {
	unquoted := strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' {
			return -1
		}
		return r
	}, v)
	return safeFont(v) == unquoted
}
