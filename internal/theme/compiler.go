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
	// Realise theme-level customization options first: scheme/width/corners
	// mutate the tokens below (so the choice flows through every bridge), and
	// the rest return scoped CSS appended at the end.
	optionCSS := applyThemeOptions(&t)

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

	// emit writes one fully-themed context. Crucially it sets THREE families of
	// variables from the same token set so a deployed theme restyles the entire
	// site — not just the Pico colour bridge:
	//   1. --vp-*   : the design tokens used by per-theme component CSS.
	//   2. --pico-* : the Pico base theme (admin, signup and Pico-based pages).
	//   3. bare     : the names the PUBLIC stylesheet (article.css) actually
	//                 reads — --bg/--surface/--text/--muted/--accent/--accent2/
	//                 --hi/--green/--font/--mono/--max-w/--radius/--radius2 — so
	//                 the public palette, typography, reading measure and corner
	//                 radius all change on a theme switch (theme.css is loaded
	//                 last, after article.css, so these overrides win).
	emit := func(bg, surface, text, muted, accent, accent2, hi, green string) {
		set := func(name, val string) {
			if val != "" {
				fmt.Fprintf(&sb, "%s:%s;", name, val)
			}
		}
		// 1. --vp-* design tokens
		set("--vp-bg", bg)
		set("--vp-surface", surface)
		set("--vp-text", text)
		set("--vp-muted", muted)
		set("--vp-accent", accent)
		set("--vp-accent2", accent2)
		set("--vp-hi", hi)
		set("--vp-green", green)
		set("--vp-font-sans", fontSans)
		set("--vp-font-mono", fontMono)
		set("--vp-font-size-base", fontSize)
		set("--vp-line-height", lineH)
		set("--vp-max-width", maxW)
		set("--vp-radius-sm", radSm)
		set("--vp-radius-lg", radLg)

		// 2. Pico bridge
		set("--pico-background-color", bg)
		set("--pico-card-background-color", surface)
		set("--pico-card-sectioning-background-color", surface)
		set("--pico-code-background-color", surface)
		if text != "" {
			fmt.Fprintf(&sb, "--pico-color:%s;--pico-h1-color:%s;--pico-h2-color:%s;--pico-h3-color:%s;", text, text, text, text)
		}
		if muted != "" {
			fmt.Fprintf(&sb, "--pico-muted-color:%s;--pico-muted-border-color:%s;", muted, muted)
		}
		if accent != "" {
			fmt.Fprintf(&sb, "--pico-primary:%s;--pico-primary-hover:%s;--pico-a-color:%s;", accent, accent, accent)
		}
		if accent2 != "" {
			fmt.Fprintf(&sb, "--vayu-accent:%s;--vayu-accent-hover:%s;", accent2, accent2)
		}

		// 3. Public-site variable bridge (article.css and friends)
		set("--bg", bg)
		set("--surface", surface)
		set("--surface2", surface)
		set("--text", text)
		set("--muted", muted)
		set("--accent", accent)
		set("--accent2", accent2)
		set("--hi", hi)
		set("--green", green)
		if muted != "" {
			fmt.Fprintf(&sb, "--border:color-mix(in srgb,%s 22%%,transparent);--border2:color-mix(in srgb,%s 40%%,transparent);", muted, muted)
		}
		set("--font", fontSans)
		set("--mono", fontMono)
		set("--max-w", maxW)
		set("--radius", radSm)
		set("--radius2", radLg)
	}

	// Default (dark) under :root; light under the system media query — matching
	// how the public stylesheet (article.css) is authored.
	sb.WriteString(":root{")
	emit(t.BgDark, t.SurfaceDark, t.TextDark, t.MutedDark, t.AccentDark, t.Accent2Dark, t.HiDark, t.GreenDark)
	sb.WriteString("}")
	sb.WriteString("@media(prefers-color-scheme:light){:root{")
	emit(t.BgLight, t.SurfaceLight, t.TextLight, t.MutedLight, t.AccentLight, t.Accent2Light, t.HiLight, t.GreenDark)
	sb.WriteString("}}")

	// Manual sun/moon toggle: an explicit [data-theme] / [data-color-scheme]
	// choice overrides the media query so the WHOLE site (public + Pico) flips,
	// not just Pico pages.
	sb.WriteString(`:root[data-theme="dark"],:root[data-color-scheme="dark"]{`)
	emit(t.BgDark, t.SurfaceDark, t.TextDark, t.MutedDark, t.AccentDark, t.Accent2Dark, t.HiDark, t.GreenDark)
	sb.WriteString("}")
	sb.WriteString(`:root[data-theme="light"],:root[data-color-scheme="light"]{`)
	emit(t.BgLight, t.SurfaceLight, t.TextLight, t.MutedLight, t.AccentLight, t.Accent2Light, t.HiLight, t.GreenDark)
	sb.WriteString("}")

	// Per-preset / per-site custom CSS is appended verbatim after the token
	// blocks so presets like Gale and Zephyr can ship their own layout rules.
	// It is served same-origin via /theme.css (CSP style-src 'self') and is
	// validated/length-capped at the API boundary before it ever reaches here.
	if cssExtra := strings.TrimSpace(t.CustomCSS); cssExtra != "" {
		sb.WriteString("\n")
		sb.WriteString(cssExtra)
	}

	// Theme-option scoped CSS (heading case / accent fill) — appended last so it
	// overrides the component CSS above.
	if optionCSS != "" {
		sb.WriteString("\n")
		sb.WriteString(optionCSS)
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
