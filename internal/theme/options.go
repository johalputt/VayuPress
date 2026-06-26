package theme

import "strings"

// options.go — theme-level customization options.
//
// Beyond the raw design tokens (colours, fonts, sizes), every theme can be
// customized along a shared set of high-level dimensions — colour scheme,
// reading width, corner style, heading case, accent fill. These are exposed in
// the Theme Studio as friendly controls and persisted on Tokens.Options.
//
// applyThemeOptions (called first thing in CompileCSS) realises them by
// MUTATING the tokens — so a colour scheme actually re-tints --vp-accent,
// --pico-primary and the public-site --accent in one place — plus a little
// scoped CSS for the selector-based options (heading case, accent fill). This
// keeps customization uniform across all themes and routes everything through
// the existing apply/compile/persist pipeline.

// OptionChoice is one selectable value for a select-type option.
type OptionChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Option describes one customization control rendered in the Theme Studio.
type Option struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Help    string         `json:"help,omitempty"`
	Default string         `json:"default"`
	Choices []OptionChoice `json:"choices"`
}

// schemePalette is an accent pair for light & dark, applied by the "scheme"
// option. Values are fixed, valid hex (validated again downstream).
type schemePalette struct {
	AccentLight, Accent2Light, AccentDark, Accent2Dark string
}

var schemePalettes = map[string]schemePalette{
	"indigo":  {"#4f46e5", "#0891b2", "#6366f1", "#22d3ee"},
	"violet":  {"#7c3aed", "#db2777", "#8b5cf6", "#ec4899"},
	"cyan":    {"#0891b2", "#2563eb", "#06b6d4", "#3b82f6"},
	"emerald": {"#059669", "#65a30d", "#10b981", "#84cc16"},
	"rose":    {"#e11d48", "#ea580c", "#f43f5e", "#fb923c"},
	"amber":   {"#d97706", "#dc2626", "#f59e0b", "#ef4444"},
	"crimson": {"#dc2626", "#7c3aed", "#ef4444", "#a78bfa"},
	"teal":    {"#0d9488", "#0284c7", "#14b8a6", "#38bdf8"},
	"slate":   {"#475569", "#64748b", "#64748b", "#94a3b8"},
	"mono":    {"#111827", "#374151", "#e5e7eb", "#9ca3af"},
}

// AllOptions returns the customization controls available for every theme, in
// display order. The set is intentionally uniform so any theme can be tuned
// along the same dimensions.
func AllOptions() []Option {
	return []Option{
		{
			Key: "scheme", Label: "Color scheme", Default: "default",
			Help: "Re-tint the accent across the whole site.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"indigo", "Indigo"}, {"violet", "Violet"},
				{"cyan", "Cyan"}, {"emerald", "Emerald"}, {"rose", "Rose"},
				{"amber", "Amber"}, {"crimson", "Crimson"}, {"teal", "Teal"},
				{"slate", "Slate"}, {"mono", "Mono"},
			},
		},
		{
			Key: "width", Label: "Reading width", Default: "default",
			Help: "Width of the content measure.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"narrow", "Narrow"}, {"normal", "Normal"}, {"wide", "Wide"},
			},
		},
		{
			Key: "corners", Label: "Corner style", Default: "default",
			Help: "Roundness of cards, buttons and inputs.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"sharp", "Sharp"}, {"soft", "Soft"}, {"round", "Round"},
			},
		},
		{
			Key: "headingcase", Label: "Heading case", Default: "default",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"normal", "Normal"}, {"uppercase", "Uppercase"},
			},
		},
		{
			Key: "accentfill", Label: "Heading accent fill", Default: "default",
			Help: "Paint large headings with the accent gradient.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"solid", "Solid"}, {"gradient", "Gradient"},
			},
		},
	}
}

// OptionsFor returns the options for a theme. Today the set is uniform across
// themes; the per-name signature lets us specialise later without touching
// callers.
func OptionsFor(_ string) []Option { return AllOptions() }

// DefaultOptions returns the default value for every option key.
func DefaultOptions() map[string]string {
	out := make(map[string]string, len(AllOptions()))
	for _, o := range AllOptions() {
		out[o.Key] = o.Default
	}
	return out
}

// headingSelectors are the public + theme heading elements that case/fill
// options retint. Kept in one place so both options target identical markup.
const headingSelectors = ".vayu-hero h1,.vayu-post-title,article.vayu-prose h1,.vayu-article-header h1"

// applyThemeOptions realises t.Options: it mutates the tokens for scheme/width/
// corners and returns extra scoped CSS for the selector-based options. Called at
// the very start of CompileCSS, before token validation, so the chosen colours
// flow through every bridge (--vp-*, --pico-*, public --accent). Unknown or
// "default" values are no-ops, so it is always safe.
func applyThemeOptions(t *Tokens) string {
	if len(t.Options) == 0 {
		return ""
	}
	if v := t.Options["scheme"]; v != "" && v != "default" {
		if p, ok := schemePalettes[v]; ok {
			t.AccentLight, t.Accent2Light = p.AccentLight, p.Accent2Light
			t.AccentDark, t.Accent2Dark = p.AccentDark, p.Accent2Dark
		}
	}
	switch t.Options["width"] {
	case "narrow":
		t.MaxWidth = "40rem"
	case "normal":
		t.MaxWidth = "46rem"
	case "wide":
		t.MaxWidth = "58rem"
	}
	switch t.Options["corners"] {
	case "sharp":
		t.RadiusSm, t.RadiusLg = "0", "0"
	case "soft":
		t.RadiusSm, t.RadiusLg = "0.5rem", "0.875rem"
	case "round":
		t.RadiusSm, t.RadiusLg = "0.75rem", "1.5rem"
	}

	var extra strings.Builder
	if t.Options["headingcase"] == "uppercase" {
		extra.WriteString(headingSelectors + "{text-transform:uppercase;letter-spacing:-.01em}")
	} else if t.Options["headingcase"] == "normal" {
		extra.WriteString(headingSelectors + "{text-transform:none}")
	}
	if t.Options["accentfill"] == "gradient" {
		extra.WriteString(headingSelectors + "{background:linear-gradient(135deg,var(--vp-accent),var(--vp-accent2,var(--vp-hi)));-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent;color:transparent}")
	}
	return extra.String()
}
