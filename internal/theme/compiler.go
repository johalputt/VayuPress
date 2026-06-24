package theme

import (
	"fmt"
	"regexp"
	"strings"
)

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func validHex(field, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if !hexColorRe.MatchString(v) {
		return "", fmt.Errorf("theme: %s %q is not a valid hex colour", field, v)
	}
	return v, nil
}

func safeFont(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '\'', '\\', '<', '>', '"', ';', '{', '}', '(', ')', ':':
			continue
		}
		if r < 0x20 || r > 0x7e {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func safeDimension(s string) string {
	s = strings.TrimSpace(s)
	allowed := regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(rem|em|px|ch|%|vw|vh)?$`)
	if allowed.MatchString(s) {
		return s
	}
	return ""
}

// CompileCSS converts Tokens into a CSS block with --vp-* variables plus a
// Pico-bridge that maps --vp-* tokens to --pico-* variables so presets
// actually change the public site appearance.
func CompileCSS(t Tokens) (string, error) {
	type colorField struct {
		name string
		ptr  *string
	}
	fields := []colorField{
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
	for _, f := range fields {
		v, err := validHex(f.name, *f.ptr)
		if err != nil {
			return "", err
		}
		*f.ptr = v
	}

	fontSans := safeFont(t.FontSans)
	fontMono := safeFont(t.FontMono)
	fontSize := safeDimension(t.FontSizeBase)
	lineH := safeDimension(t.LineHeight)
	maxW := safeDimension(t.MaxWidth)
	radSm := safeDimension(t.RadiusSm)
	radLg := safeDimension(t.RadiusLg)

	var sb strings.Builder
	writeVar := func(name, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "--vp-%s:%s;", name, val)
		}
	}

	// ── Dark-mode root ─────────────────────────────────────────────────────
	sb.WriteString(":root{")
	writeVar("bg", t.BgDark)
	writeVar("surface", t.SurfaceDark)
	writeVar("text", t.TextDark)
	writeVar("muted", t.MutedDark)
	writeVar("accent", t.AccentDark)
	writeVar("accent2", t.Accent2Dark)
	writeVar("hi", t.HiDark)
	writeVar("green", t.GreenDark)
	if fontSans != "" {
		fmt.Fprintf(&sb, "--vp-font-sans:%s;", fontSans)
	}
	if fontMono != "" {
		fmt.Fprintf(&sb, "--vp-font-mono:%s;", fontMono)
	}
	writeVar("font-size-base", fontSize)
	writeVar("line-height", lineH)
	writeVar("max-width", maxW)
	writeVar("radius-sm", radSm)
	writeVar("radius-lg", radLg)

	// ── Pico bridge (dark) — maps --vp-* to --pico-* for site-wide effect ──
	writePicoDark := func() {
		if t.BgDark != "" {
			fmt.Fprintf(&sb, "--pico-background-color:%s;", t.BgDark)
		}
		if t.SurfaceDark != "" {
			fmt.Fprintf(&sb, "--pico-card-background-color:%s;", t.SurfaceDark)
		}
		if t.TextDark != "" {
			fmt.Fprintf(&sb, "--pico-color:%s;--pico-h1-color:%s;--pico-h2-color:%s;", t.TextDark, t.TextDark, t.TextDark)
		}
		if t.MutedDark != "" {
			fmt.Fprintf(&sb, "--pico-muted-color:%s;--pico-muted-border-color:%s;", t.MutedDark, t.MutedDark)
		}
		if t.AccentDark != "" {
			fmt.Fprintf(&sb, "--pico-primary:%s;--pico-primary-hover:%s;--vayu-accent:%s;--vayu-accent-hover:%s;",
				t.AccentDark, t.AccentDark, t.Accent2Dark, t.Accent2Dark)
		}
		if t.SurfaceDark != "" {
			fmt.Fprintf(&sb, "--pico-code-background-color:%s;", t.SurfaceDark)
		}
	}
	writePicoDark()
	sb.WriteString("}")

	// ── Light-mode override ───────────────────────────────────────────────
	sb.WriteString("@media(prefers-color-scheme:light){:root{")
	writeVar("bg", t.BgLight)
	writeVar("surface", t.SurfaceLight)
	writeVar("text", t.TextLight)
	writeVar("muted", t.MutedLight)
	writeVar("accent", t.AccentLight)
	writeVar("accent2", t.Accent2Light)
	writeVar("hi", t.HiLight)

	// Pico bridge (light)
	if t.BgLight != "" {
		fmt.Fprintf(&sb, "--pico-background-color:%s;", t.BgLight)
	}
	if t.SurfaceLight != "" {
		fmt.Fprintf(&sb, "--pico-card-background-color:%s;", t.SurfaceLight)
	}
	if t.TextLight != "" {
		fmt.Fprintf(&sb, "--pico-color:%s;--pico-h1-color:%s;--pico-h2-color:%s;", t.TextLight, t.TextLight, t.TextLight)
	}
	if t.MutedLight != "" {
		fmt.Fprintf(&sb, "--pico-muted-color:%s;--pico-muted-border-color:%s;", t.MutedLight, t.MutedLight)
	}
	if t.AccentLight != "" {
		fmt.Fprintf(&sb, "--pico-primary:%s;--pico-primary-hover:%s;--vayu-accent:%s;--vayu-accent-hover:%s;",
			t.AccentLight, t.AccentLight, t.Accent2Light, t.Accent2Light)
	}
	if t.SurfaceLight != "" {
		fmt.Fprintf(&sb, "--pico-card-sectioning-background-color:%s;", t.SurfaceLight)
	}
	sb.WriteString("}}")

	// Per-preset / per-site custom CSS is appended verbatim after the token
	// blocks so presets like Gale and Zephyr can ship their own layout rules.
	// It is served same-origin via /theme.css (CSP style-src 'self') and is
	// validated/length-capped at the API boundary before it ever reaches here.
	if cssExtra := strings.TrimSpace(t.CustomCSS); cssExtra != "" {
		sb.WriteString("\n")
		sb.WriteString(cssExtra)
	}

	return sb.String(), nil
}

// CompileVPOnly returns --vp-* variables only (no Pico bridge) for the live preview panel.
func CompileVPOnly(t Tokens) (string, error) {
	type colorField struct {
		name string
		ptr  *string
	}
	fields := []colorField{
		{"BgDark", &t.BgDark}, {"SurfaceDark", &t.SurfaceDark}, {"TextDark", &t.TextDark},
		{"MutedDark", &t.MutedDark}, {"AccentDark", &t.AccentDark}, {"Accent2Dark", &t.Accent2Dark},
		{"HiDark", &t.HiDark}, {"GreenDark", &t.GreenDark},
		{"BgLight", &t.BgLight}, {"SurfaceLight", &t.SurfaceLight}, {"TextLight", &t.TextLight},
		{"MutedLight", &t.MutedLight}, {"AccentLight", &t.AccentLight}, {"Accent2Light", &t.Accent2Light},
		{"HiLight", &t.HiLight},
	}
	for _, f := range fields {
		v, err := validHex(f.name, *f.ptr)
		if err != nil {
			return "", err
		}
		*f.ptr = v
	}
	fontSans := safeFont(t.FontSans)
	fontMono := safeFont(t.FontMono)

	var sb strings.Builder
	writeVar := func(name, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "--vp-%s:%s;", name, val)
		}
	}
	sb.WriteString(":root{")
	writeVar("bg", t.BgDark)
	writeVar("surface", t.SurfaceDark)
	writeVar("text", t.TextDark)
	writeVar("muted", t.MutedDark)
	writeVar("accent", t.AccentDark)
	writeVar("accent2", t.Accent2Dark)
	writeVar("hi", t.HiDark)
	writeVar("green", t.GreenDark)
	if fontSans != "" {
		fmt.Fprintf(&sb, "--vp-font-sans:%s;", fontSans)
	}
	if fontMono != "" {
		fmt.Fprintf(&sb, "--vp-font-mono:%s;", fontMono)
	}
	sb.WriteString("}")
	sb.WriteString("@media(prefers-color-scheme:light){:root{")
	writeVar("bg", t.BgLight)
	writeVar("surface", t.SurfaceLight)
	writeVar("text", t.TextLight)
	writeVar("muted", t.MutedLight)
	writeVar("accent", t.AccentLight)
	writeVar("accent2", t.Accent2Light)
	writeVar("hi", t.HiLight)
	sb.WriteString("}}")
	return sb.String(), nil
}
