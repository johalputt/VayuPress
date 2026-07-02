package theme

// typography.go — reusable TYPOGRAPHY pairings.
//
// A theme's personality is at least half typography. Each pairing bundles a
// display stack (headings, post titles, the brand) with a body stack, using
// SYSTEM fonts only — zero external requests, zero CDNs, true to the sovereign
// posture. The pairing is carried as the "fontpair" customization option so it
// flows through the same preview/apply/persist pipeline as every other option,
// and each colour preset ships a default pairing (see fontPairForPreset) so
// deploying a theme changes the typographic voice, not just the palette.

// fontPair bundles a display (headings) stack with a body stack. Either may be
// empty, meaning "keep the theme's current stack" for that role.
type fontPair struct {
	Display string // headings, post titles, brand
	Body    string // running text; applied by mutating Tokens.FontSans
}

// fontPairs are the selectable typography personalities. Stacks are plain
// identifier lists (no quotes — CSS treats spaced names as ident sequences),
// so they survive safeFont intact.
var fontPairs = map[string]fontPair{
	// Serif headings over a quiet sans body — the classic Ghost editorial look.
	"elegant": {
		Display: "Georgia,Iowan Old Style,Palatino Linotype,Palatino,Book Antiqua,serif",
		Body:    "system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif",
	},
	// All-serif, book-like: long-form reading with warmth.
	"literary": {
		Display: "Iowan Old Style,Palatino Linotype,Palatino,Georgia,serif",
		Body:    "Charter,Bitstream Charter,Iowan Old Style,Georgia,Cambria,Times New Roman,serif",
	},
	// Crisp geometric sans throughout — contemporary product feel.
	"modern": {
		Display: "Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif",
		Body:    "Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif",
	},
	// Warm humanist sans — friendly and open.
	"humanist": {
		Display: "Seravek,Gill Sans Nova,Gill Sans,Avenir Next,Avenir,Trebuchet MS,Verdana,sans-serif",
		Body:    "Seravek,Gill Sans Nova,Gill Sans,Avenir Next,Avenir,Trebuchet MS,Verdana,sans-serif",
	},
	// Tight neo-grotesque headlines — confident, Swiss, newsy.
	"grotesk": {
		Display: "Helvetica Neue,Helvetica,Arial Nova,Arial,system-ui,sans-serif",
		Body:    "Helvetica Neue,Helvetica,Arial Nova,Arial,system-ui,sans-serif",
	},
	// Monospaced display over a sans body — technical, terminal-adjacent.
	"typewriter": {
		Display: "ui-monospace,SFMono-Regular,SF Mono,Menlo,Consolas,Liberation Mono,monospace",
		Body:    "system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif",
	},
}

// FontPairChoices lists the selectable typography pairings for the Studio.
func FontPairChoices() []OptionChoice {
	return []OptionChoice{
		{"default", "Theme default"},
		{"elegant", "Elegant — serif headings, sans body"},
		{"literary", "Literary — book serif throughout"},
		{"modern", "Modern — crisp geometric sans"},
		{"humanist", "Humanist — warm, open sans"},
		{"grotesk", "Grotesk — tight Swiss headlines"},
		{"typewriter", "Typewriter — mono display"},
	}
}

// displayFontSelectors are the elements a pairing's display stack retypes:
// hero + article titles, prose headings, and the nav brand — the voice of the
// whole site, home page and article pages alike.
const displayFontSelectors = ".vayu-hero h1,.vayu-post-title,.vayu-article-header h1," +
	"article.vayu-prose h1,article.vayu-prose h2,article.vayu-prose h3,.vayu-nav-brand"

// fontPairForPreset maps each colour preset to its default typography pairing
// so deploying it changes the typographic personality along with the palette.
// Design themes with bespoke CustomCSS return "" — their CSS owns typography.
func fontPairForPreset(name string) string {
	switch name {
	// Classic — serif headings lend the timeless editorial feel.
	case "Default", "Pine":
		return "elegant"
	case "Midnight":
		return "grotesk"
	case "Slate":
		return "modern"
	// Magazine — varied contemporary voices.
	case "Aurora", "Coral":
		return "modern"
	case "Ocean", "Meadow":
		return "humanist"
	case "Plum":
		return "elegant"
	case "Glacier":
		return "grotesk"
	// Minimal — quiet and unadorned.
	case "Mint":
		return "modern"
	case "Fog":
		return "humanist"
	case "Terminal":
		return "typewriter"
	// Editorial — bookish serifs.
	case "Sepia", "Rust":
		return "literary"
	case "Sakura", "Bloom", "Lavender":
		return "elegant"
	// Bold — heavy grotesque headlines.
	case "Carbon", "Solar", "Amber", "Noir":
		return "grotesk"
	default:
		return "" // design themes (Gale/Zephyr/…): bespoke CSS owns typography
	}
}
