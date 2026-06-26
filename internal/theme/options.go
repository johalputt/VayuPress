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
			Key: "feedlayout", Label: "Post feed layout", Default: "default",
			Help: "How the home-page post list is arranged.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"list", "List"}, {"grid", "Grid"}, {"cards", "Cards"},
			},
		},
		{
			Key: "headeralign", Label: "Header alignment", Default: "default",
			Help: "Alignment of the hero/title block.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"left", "Left"}, {"center", "Centered"},
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
		{
			Key: "herostyle", Label: "Hero style", Default: "default",
			Help: "Layout of the homepage hero block.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"centered", "Centered"}, {"left", "Left"},
				{"minimal", "Minimal"}, {"boxed", "Boxed"},
			},
		},
		{
			Key: "herobg", Label: "Hero background", Default: "default",
			Help: "Tint, gradient, or an uploaded image behind the hero.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"none", "None"}, {"tint", "Accent tint"},
				{"gradient", "Accent gradient"}, {"image", "Uploaded image"},
			},
		},
		{
			Key: "heroheight", Label: "Hero height", Default: "default",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"compact", "Compact"}, {"regular", "Regular"}, {"tall", "Tall"},
			},
		},
		{
			Key: "navstyle", Label: "Navigation style", Default: "default",
			Help: "Alignment of the top navigation bar.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"spread", "Brand left, links right"},
				{"center", "Centered"}, {"stack", "Stacked"},
			},
		},
		{
			Key: "cardstyle", Label: "Post card style", Default: "default",
			Help: "How post cards look across the blog.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"flat", "Flat"}, {"bordered", "Bordered"},
				{"elevated", "Elevated"}, {"underline", "Underlined"},
			},
		},
		{
			Key: "linkstyle", Label: "Link style", Default: "default",
			Help: "Underlining of links in article content.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"underline", "Always underline"},
				{"hover", "Underline on hover"}, {"clean", "No underline"},
			},
		},
		{
			Key: "articlealign", Label: "Article header", Default: "default",
			Help: "Alignment of the post title and meta on article pages.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"left", "Left"}, {"center", "Centered"},
			},
		},
		{
			Key: "articlemeta", Label: "Article meta", Default: "default",
			Help: "The date / read-time / tags line under a post title.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"full", "Show all"},
				{"notags", "Hide tags"}, {"hidden", "Hide meta"},
			},
		},
		{
			Key: "relatedposts", Label: "Related posts", Default: "default",
			Help: "The related-articles list at the end of a post.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"show", "Show"}, {"hidden", "Hide"},
			},
		},
	}
}

// OptionsFor returns the options for a theme: the shared set (AllOptions) plus
// any per-theme extras that apply to it.
func OptionsFor(name string) []Option {
	out := AllOptions()
	for _, to := range perThemeOptions {
		for _, t := range to.Themes {
			if t == name {
				out = append(out, to.Option)
				break
			}
		}
	}
	return out
}

// ThemedOption is a per-theme extra option plus the themes it applies to.
type ThemedOption struct {
	Themes []string `json:"themes"`
	Option Option   `json:"option"`
}

// perThemeOptions are extra controls layered on top of the shared set for
// specific themes. Their effects (below, in applyThemeOptions) target the real
// vayu-* markup, so they are harmless if ever applied to another theme.
var perThemeOptions = []ThemedOption{
	{
		Themes: []string{"Apex", "Beacon", "Dispatch", "Agora", "Ripple"},
		Option: Option{
			Key: "density", Label: "Density", Default: "default",
			Help: "Vertical rhythm and section spacing.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"compact", "Compact"},
				{"comfortable", "Comfortable"}, {"spacious", "Spacious"},
			},
		},
	},
	{
		Themes: []string{"Maverick", "Vivid", "Gale", "Apex", "Noir"},
		Option: Option{
			Key: "headingscale", Label: "Heading size", Default: "default",
			Help: "Scale of display headings.",
			Choices: []OptionChoice{
				{"default", "Theme default"}, {"sm", "Small"}, {"md", "Medium"},
				{"lg", "Large"}, {"xl", "Extra large"},
			},
		},
	},
}

// PerThemeOptions exposes the per-theme extras (with their target themes) so the
// Studio can render them and show/hide per the active theme.
func PerThemeOptions() []ThemedOption { return perThemeOptions }

// OptionKeys returns every option key (shared + per-theme), deduplicated. Used
// to read option values from preview query strings.
func OptionKeys() []string {
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, o := range AllOptions() {
		add(o.Key)
	}
	for _, to := range perThemeOptions {
		add(to.Option.Key)
	}
	return keys
}

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

	// Post feed layout — arrange the home-page post list. Falls back gracefully
	// (the var() fallbacks keep cards readable even if a theme omits the vars).
	switch t.Options["feedlayout"] {
	case "list":
		extra.WriteString(".vayu-post-list{display:flex;flex-direction:column;gap:1rem}")
	case "grid":
		extra.WriteString(".vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:1.5rem;align-items:start}")
	case "cards":
		extra.WriteString(".vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:1.25rem;align-items:start}.vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.22));border-radius:var(--radius2,12px);overflow:hidden}")
	}
	switch t.Options["headeralign"] {
	case "left":
		extra.WriteString(".vayu-hero{text-align:left}")
	case "center":
		extra.WriteString(".vayu-hero{text-align:center}.vayu-hero .vayu-stats{justify-content:center}.vayu-hero .vayu-hero-tagline{margin-left:auto;margin-right:auto}")
	}

	// ── Hero section ──────────────────────────────────────────────────────────
	switch t.Options["herostyle"] {
	case "centered":
		extra.WriteString(".vayu-hero{text-align:center}.vayu-hero h1{margin-left:auto;margin-right:auto}.vayu-hero .vayu-hero-tagline{margin-left:auto;margin-right:auto}.vayu-hero .vayu-stats{justify-content:center}")
	case "left":
		extra.WriteString(".vayu-hero{text-align:left}")
	case "minimal":
		extra.WriteString(".vayu-hero{border-bottom:0;padding-bottom:1.5rem}.vayu-hero .vayu-stats{display:none}")
	case "boxed":
		extra.WriteString(".vayu-hero{border:1px solid var(--border,rgba(125,125,125,.22));border-radius:var(--radius2,14px);padding:2.5rem 1.75rem}")
	}
	switch t.Options["heroheight"] {
	case "compact":
		extra.WriteString(".vayu-hero{padding-top:1.75rem;padding-bottom:1.5rem}")
	case "regular":
		extra.WriteString(".vayu-hero{padding-top:3.5rem;padding-bottom:3rem}")
	case "tall":
		extra.WriteString(".vayu-hero{padding-top:6rem;padding-bottom:5rem}")
	}
	switch t.Options["herobg"] {
	case "none":
		extra.WriteString(".vayu-hero{background:none}")
	case "tint":
		extra.WriteString(".vayu-hero{background:color-mix(in srgb,var(--accent,#2dd4bf) 10%,transparent);border-radius:var(--radius2,14px);padding:2.5rem 1.75rem;border-bottom:0}")
	case "gradient":
		extra.WriteString(".vayu-hero{background:linear-gradient(135deg,color-mix(in srgb,var(--accent,#2dd4bf) 22%,transparent),color-mix(in srgb,var(--accent2,#6366f1) 16%,transparent));border-radius:var(--radius2,14px);padding:2.75rem 1.75rem;border-bottom:0}")
	case "image":
		// References a fixed same-origin URL; if no image is uploaded the request
		// 404s and CSS simply shows no background (no broken-image artefact).
		extra.WriteString(`.vayu-hero{background-image:linear-gradient(180deg,rgba(0,0,0,.35),rgba(0,0,0,.72)),url(/theme-assets/hero);background-size:cover;background-position:center;background-repeat:no-repeat;border-radius:var(--radius2,14px);border-bottom:0;padding:4rem 1.75rem;color:#fff}.vayu-hero .vayu-hero-tagline{color:rgba(255,255,255,.9)}.vayu-hero .vayu-stat-label{color:rgba(255,255,255,.75)}`)
	}

	// ── Navigation style ──────────────────────────────────────────────────────
	switch t.Options["navstyle"] {
	case "spread":
		extra.WriteString(".vayu-nav{display:flex;align-items:center;justify-content:space-between}.vayu-nav .vayu-nav-links{margin-left:auto;display:flex;gap:1.25rem}")
	case "center":
		extra.WriteString(".vayu-nav{display:flex;flex-direction:column;align-items:center;gap:.5rem}.vayu-nav .vayu-nav-links{display:flex;gap:1.25rem;justify-content:center}")
	case "stack":
		extra.WriteString(".vayu-nav{display:flex;flex-direction:column;align-items:flex-start;gap:.5rem}.vayu-nav .vayu-nav-links{display:flex;gap:1rem;flex-wrap:wrap}")
	}

	// ── Post card style (applies wherever .vayu-post-card appears) ─────────────
	switch t.Options["cardstyle"] {
	case "flat":
		extra.WriteString(".vayu-post-card{border:0;box-shadow:none;padding:0}")
	case "bordered":
		extra.WriteString(".vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.22));border-radius:var(--radius2,12px);padding:1.25rem}")
	case "elevated":
		extra.WriteString(".vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.16));border-radius:var(--radius2,14px);padding:1.25rem;box-shadow:0 6px 24px rgba(0,0,0,.18);transition:transform .15s,box-shadow .15s}.vayu-post-card:hover{transform:translateY(-3px);box-shadow:0 12px 36px rgba(0,0,0,.26)}")
	case "underline":
		extra.WriteString(".vayu-post-card{border:0;border-bottom:1px solid var(--border,rgba(125,125,125,.2));border-radius:0;padding:0 0 1.25rem}")
	}

	// ── Link style (article content) ──────────────────────────────────────────
	switch t.Options["linkstyle"] {
	case "underline":
		extra.WriteString("article.vayu-prose a,.vayu-prose a{text-decoration:underline}")
	case "hover":
		extra.WriteString("article.vayu-prose a,.vayu-prose a{text-decoration:none}article.vayu-prose a:hover,.vayu-prose a:hover{text-decoration:underline}")
	case "clean":
		extra.WriteString("article.vayu-prose a,.vayu-prose a{text-decoration:none}")
	}

	// ── Article page layout (operates on the real article markup) ─────────────
	switch t.Options["articlealign"] {
	case "left":
		extra.WriteString(".vayu-article-header{text-align:left}")
	case "center":
		extra.WriteString(".vayu-article-header{text-align:center}.vayu-article-meta{justify-content:center}")
	}
	switch t.Options["articlemeta"] {
	case "notags":
		extra.WriteString(".vayu-article-meta a.vayu-tag{display:none}")
	case "hidden":
		extra.WriteString(".vayu-article-meta{display:none}")
	}
	if t.Options["relatedposts"] == "hidden" {
		extra.WriteString(".vayu-related{display:none}")
	}

	// ── Per-theme extras ────────────────────────────────────────────────────
	switch t.Options["density"] {
	case "compact":
		extra.WriteString("body{line-height:1.5}.vayu-hero{padding-top:2.5rem;padding-bottom:2rem}.vayu-section{margin:2rem 0}")
	case "comfortable":
		extra.WriteString("body{line-height:1.7}.vayu-hero{padding-top:4rem;padding-bottom:3rem}")
	case "spacious":
		extra.WriteString("body{line-height:1.85}.vayu-hero{padding-top:6rem;padding-bottom:4.5rem}.vayu-section{margin:4.5rem 0}")
	}
	switch t.Options["headingscale"] {
	case "sm":
		extra.WriteString(".vayu-hero h1{font-size:2rem}.vayu-post-title{font-size:1.1rem}article.vayu-prose h1,.vayu-article-header h1{font-size:1.9rem}")
	case "md":
		extra.WriteString(".vayu-hero h1{font-size:2.7rem}.vayu-post-title{font-size:1.3rem}article.vayu-prose h1,.vayu-article-header h1{font-size:2.4rem}")
	case "lg":
		extra.WriteString(".vayu-hero h1{font-size:3.6rem}.vayu-post-title{font-size:1.55rem}article.vayu-prose h1,.vayu-article-header h1{font-size:3.1rem}")
	case "xl":
		extra.WriteString(".vayu-hero h1{font-size:4.6rem}.vayu-post-title{font-size:1.8rem}article.vayu-prose h1,.vayu-article-header h1{font-size:3.9rem}")
	}
	return extra.String()
}
