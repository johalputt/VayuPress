package theme

import (
	"fmt"
	"regexp"
	"strings"
)

// hexColorRe matches CSS hex colours: #rgb, #rrggbb, #rgba, #rrggbbaa.
// Values that don't match are rejected so they can never break the served
// stylesheet or carry injection payloads inside a CSS variable block.
var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

// validHex returns the value unchanged when it is a valid hex colour string,
// or returns an empty string and a non-nil error when it is not.
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

// safeFont strips characters that could escape the CSS variable value.
// A font-family stack only ever needs letters, digits, spaces, commas,
// hyphens, and dots, so every CSS-structural character — quotes,
// backslash, angle-brackets, and crucially the block/declaration
// punctuation ; { } ( ) : — is removed. Without that punctuation a value
// cannot break out of the surrounding :root{ … } block to inject rules.
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

// safeDimension allows only digits, dots, and a short set of CSS unit suffixes.
// Anything that doesn't look like a CSS length/ratio is replaced with an empty string.
func safeDimension(s string) string {
	s = strings.TrimSpace(s)
	// Allow e.g. "1rem", "1.6", "72ch", "0", "0.25rem", "1.0625rem"
	allowed := regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(rem|em|px|ch|%|vw|vh)?$`)
	if allowed.MatchString(s) {
		return s
	}
	return ""
}

// CompileCSS converts a Tokens value into a minimal CSS block.
// It validates every hex colour; non-hex values produce an error.
// Font stacks and dimension values are sanitised to prevent injection.
func CompileCSS(t Tokens) (string, error) {
	// Validate all hex colour fields.
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

	// Dark-mode root (default)
	sb.WriteString(":root{")
	writeVar := func(name, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "--vp-%s:%s;", name, val)
		}
	}
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
	sb.WriteString("}")

	// Light-mode override
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
