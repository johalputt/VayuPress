// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"fmt"
	"strconv"
	"strings"
)

// Halcyon is the first theme that brings its own layout rather than restyling
// the shared page. Every other preset is CSS over one set of templates, which
// is why none could have a lead story, an outline, reading settings or a
// topic index: those are markup, not paint. Layout names the template set the
// renderer uses; it is empty for every other preset, which therefore renders
// exactly as before.
const LayoutHalcyon = "halcyon"

// Halcyon's surfaces, in each scheme: canvas, surface, raised (popovers and
// sheets) and sunken (code and fields). Accent text can sit on any of them, so
// the accent check measures it against each. The theme's stylesheet paints
// these, not the token backgrounds, so the check measures what a reader sees.
var (
	halcyonPaper = []string{"#f7f5f0", "#fbfaf6", "#ffffff", "#efece5"}
	halcyonDusk  = []string{"#171614", "#1d1c19", "#24221f", "#12110f"}
)

// halcyonAccents are the fixed accent choices. "site" is johal.in's teal,
// quietened for long reading; "stillair" is the console's blue, for an
// operator who wants the site and VayuOS to read as one product.
var halcyonAccents = map[string][2]string{
	"site":     {"#0f6b63", "#7cc4b8"},
	"stillair": {"#2d5bd7", "#7aa2ff"},
}

// Halcyon returns the Halcyon preset: warm paper and dusk, one quiet accent,
// a reading serif. Its tokens feed /theme.css, which the member portal and the
// operator's custom CSS read; the theme's own stylesheet carries the rest.
func Halcyon() Tokens {
	return Tokens{
		Name: "Halcyon", BgDark: "#171614", SurfaceDark: "#1d1c19", TextDark: "#ece7dd",
		MutedDark: "#999284", AccentDark: "#7cc4b8", Accent2Dark: "#bdb6a8", HiDark: "#7cc4b8", GreenDark: "#7fb069",
		BgLight: "#f7f5f0", SurfaceLight: "#fbfaf6", TextLight: "#1c1a16", MutedLight: "#6b665c",
		AccentLight: "#0f6b63", Accent2Light: "#4d4940", HiLight: "#0f6b63",
		FontSans:     "Inter, ui-sans-serif, system-ui, sans-serif",
		FontMono:     "JetBrains Mono, ui-monospace, monospace",
		FontSizeBase: "1.25rem", LineHeight: "1.7", MaxWidth: "680px", RadiusSm: "0.75rem", RadiusLg: "1.125rem",
		Layout:  LayoutHalcyon,
		Options: DefaultHalcyonOptions(),
	}
}

// onOff is the choice list for a section or reading tool that can be hidden.
var onOff = []OptionChoice{{"on", "Shown"}, {"off", "Hidden"}}

// halcyonOptions are Halcyon's Theme Studio controls, in the order the Studio
// shows them. Each default is the operator's decision of 2026-10-10.
var halcyonOptions = []Option{
	{Key: "home", Label: "Home", Default: "front",
		Help:    "The front page leads with the newest post; the river is a calm list with a side column; the index lists by month.",
		Choices: []OptionChoice{{"front", "Front page"}, {"river", "River"}, {"index", "Index"}}},
	{Key: "archives", Label: "Archive pages and topics", Default: "index",
		Help:    "How a topic and the later pages of Home are listed.",
		Choices: []OptionChoice{{"index", "Index"}, {"home", "Same as Home"}}},
	{Key: "article", Label: "Articles", Default: "column",
		Help:    "Column centres the text with the outline to its right; Margin sets the facts and outline in a left margin.",
		Choices: []OptionChoice{{"column", "Column"}, {"margin", "Margin"}}},
	{Key: "face", Label: "Reading face", Default: "serif",
		Help:    "The face articles are read in. Readers can switch it for themselves in Aa.",
		Choices: []OptionChoice{{"serif", "Serif (Newsreader)"}, {"sans", "Sans (Inter)"}}},
	{Key: "textsize", Label: "Text size", Default: "20",
		Help:    "The body size of an article. Readers can change it for themselves in Aa.",
		Choices: []OptionChoice{{"17", "17 px"}, {"18", "18 px"}, {"19", "19 px"}, {"20", "20 px"}, {"21", "21 px"}, {"22", "22 px"}, {"23", "23 px"}}},
	{Key: "smallcaps", Label: "Running text", Default: "on",
		Help:    "Sets runs of capitals (HTML, SQL, NASA) in small caps so they sit quietly in a sentence. The stored post is never changed.",
		Choices: []OptionChoice{{"on", "Small caps"}, {"off", "As written"}}},
	{Key: "accent", Label: "Accent", Default: "site",
		Help:    "Custom uses the accent colours below; every accent must reach 4.5:1 on both schemes before it can be saved.",
		Choices: []OptionChoice{{"site", "Site colour"}, {"stillair", "Still Air blue"}, {"custom", "Custom"}}},
	{Key: "appearance", Label: "Appearance for new readers", Default: "auto",
		Help:    "Auto follows the reader's device. A reader's own choice in Aa always wins.",
		Choices: []OptionChoice{{"auto", "Auto"}, {"light", "Light"}, {"dark", "Dark"}}},
	{Key: "mostread", Label: "Most read", Default: "on", Help: "The most-read list on Home and at the end of a post.", Choices: onOff},
	{Key: "pinned", Label: "Pinned posts", Default: "on", Help: "Featured posts, first in the most-read list.", Choices: onOff},
	{Key: "topics", Label: "Topics", Default: "on", Help: "The numbered topic desks on Home and the topic row on archive pages.", Choices: onOff},
	{Key: "subscribe", Label: "Subscribe", Default: "on", Help: "The one-line subscribe on Home, shown while membership is on.", Choices: onOff},
	{Key: "outline", Label: "Outline", Default: "on", Help: "An article's headings beside the text, the current one marked.", Choices: onOff},
	{Key: "selection", Label: "Selection toolbar", Default: "on", Help: "Copy, quote and share a selected passage.", Choices: onOff},
	{Key: "dock", Label: "Phone reading dock", Default: "on", Help: "Outline, Aa and back to the top, at the foot of a phone screen.", Choices: onOff},
	{Key: "comments", Label: "Comments", Default: "on", Help: "Shown while comments are switched on for the site.", Choices: onOff},
}

// HalcyonOptions returns Halcyon's Theme Studio controls.
func HalcyonOptions() []Option { return halcyonOptions }

// DefaultHalcyonOptions returns every Halcyon option at its default.
func DefaultHalcyonOptions() map[string]string {
	out := make(map[string]string, len(halcyonOptions))
	for _, o := range halcyonOptions {
		out[o.Key] = o.Default
	}
	return out
}

// HalcyonConfig is Halcyon's options resolved: every value valid, every
// missing one at its default. The renderer reads this and never the raw map,
// so an unknown or hand-edited value cannot reach a template.
type HalcyonConfig struct {
	Home, Archives, Article, Face, Appearance string
	TextSize                                  int
	SmallCaps                                 bool
	MostRead, Pinned, Topics, Subscribe       bool
	Outline, Selection, Dock, Comments        bool
}

// ResolveHalcyon resolves an options map to a HalcyonConfig. A value that is
// not one of an option's choices falls back to that option's default.
func ResolveHalcyon(opts map[string]string) HalcyonConfig {
	get := func(key string) string {
		for _, o := range halcyonOptions {
			if o.Key != key {
				continue
			}
			v := opts[key]
			for _, c := range o.Choices {
				if c.Value == v {
					return v
				}
			}
			return o.Default
		}
		panic("theme: no Halcyon option " + key)
	}
	size, _ := strconv.Atoi(get("textsize"))
	return HalcyonConfig{
		Home: get("home"), Archives: get("archives"), Article: get("article"),
		Face: get("face"), Appearance: get("appearance"), TextSize: size,
		SmallCaps: get("smallcaps") == "on",
		MostRead:  get("mostread") == "on", Pinned: get("pinned") == "on",
		Topics: get("topics") == "on", Subscribe: get("subscribe") == "on",
		Outline: get("outline") == "on", Selection: get("selection") == "on",
		Dock: get("dock") == "on", Comments: get("comments") == "on",
	}
}

// applyHalcyonAccent sets Halcyon's accent from its option and refuses one
// that a reader could not read. A fixed choice replaces the accent tokens; a
// custom one keeps them, so the Studio's accent fields are the custom colour.
//
// The refusal is here, in the compiler, rather than in the Studio, because
// every path that applies a theme (the Studio, the API, the connector's
// apply_theme) compiles first and persists only what compiled.
func applyHalcyonAccent(t *Tokens) error {
	choice := ResolveHalcyonAccent(t.Options)
	if pair, ok := halcyonAccents[choice]; ok {
		t.AccentLight, t.AccentDark = pair[0], pair[1]
		t.HiLight, t.HiDark = pair[0], pair[1]
	}
	for _, c := range []struct {
		scheme, accent string
		canvases       []string
	}{{"Paper (light)", t.AccentLight, halcyonPaper}, {"Dusk (dark)", t.AccentDark, halcyonDusk}} {
		if !strings.HasPrefix(c.accent, "#") {
			return fmt.Errorf("theme: the %s accent %q must be a #rrggbb colour", c.scheme, c.accent)
		}
		if _, _, _, ok := ParseHex(c.accent); !ok {
			return fmt.Errorf("theme: the %s accent %q must be a #rrggbb colour", c.scheme, c.accent)
		}
		for _, bg := range c.canvases {
			if r := ContrastRatio(c.accent, bg); r < 4.5 {
				return fmt.Errorf("theme: the %s accent %s reaches %.2f:1 on %s; Halcyon needs 4.5:1 so links and labels stay readable", c.scheme, c.accent, r, bg)
			}
		}
	}
	return nil
}

// ResolveHalcyonAccent returns the accent choice in force: one of
// halcyonAccents' keys or "custom".
func ResolveHalcyonAccent(opts map[string]string) string {
	switch v := opts["accent"]; v {
	case "stillair", "custom":
		return v
	}
	return "site"
}
