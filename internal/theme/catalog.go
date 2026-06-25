package theme

import "strings"

// catalog.go — presentational metadata for the VayuOS Theme Store.
//
// AllPresets() (presets.go) remains the single source of truth for a theme's
// design tokens and any per-preset CustomCSS. This file layers a thin,
// store-only metadata record on top — a tagline, a longer description, a set of
// tags, and a primary category — so the Theme Store can render rich showcase
// cards and offer category/search filtering.
//
// Adding a new theme to the store is two steps and nothing else:
//  1. add its Tokens constructor to presets.go and to AllPresets(); and
//  2. add a ThemeMeta entry to catalogMeta below (optional — a sensible
//     fallback is generated when an entry is missing).
//
// Categories are intentionally a small, fixed vocabulary so the filter UI stays
// legible. See Categories().

// ThemeMeta is store-facing metadata for one deployable theme. It carries no
// colours or tokens — those live on the matching Tokens from AllPresets().
type ThemeMeta struct {
	Name        string   `json:"name"`
	Tagline     string   `json:"tagline"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category"`
}

// StoreEntry bundles a deployable theme's design Tokens with its store Meta.
// Deploying an entry is exactly "apply the preset whose Name == Meta.Name".
type StoreEntry struct {
	Tokens Tokens    `json:"tokens"`
	Meta   ThemeMeta `json:"meta"`
}

// Store-card category vocabulary (kept small and stable for the filter UI).
const (
	CatDark       = "Dark"
	CatLight      = "Light"
	CatMinimal    = "Minimal"
	CatVibrant    = "Vibrant"
	CatEditorial  = "Editorial"
	CatReading    = "Reading"
	CatMono       = "Mono"
	CatNewsletter = "Newsletter"
)

// catalogMeta maps a preset Name to its store metadata. Names must match the
// Tokens.Name returned by the corresponding AllPresets() constructor exactly.
var catalogMeta = map[string]ThemeMeta{
	"Default": {
		Tagline:     "Balanced neutral tones, ready for anything.",
		Description: "The stock VayuPress look — calm slate-and-teal palette with comfortable typography. A dependable starting point you can deploy as-is or fine-tune in the Studio.",
		Tags:        []string{"neutral", "teal", "balanced"},
		Category:    CatDark,
	},
	"Aurora": {
		Tagline:     "Aurora-inspired purple and teal.",
		Description: "Deep indigo canvas lit with violet and mint accents, evoking the northern lights. Generous line-height makes long reads feel airy.",
		Tags:        []string{"purple", "teal", "atmospheric"},
		Category:    CatVibrant,
	},
	"Slate": {
		Tagline:     "Cool, quiet, and minimal.",
		Description: "A restrained cool-grey palette with a single sky-blue accent. Tight radii and modest contrast keep the focus squarely on your words.",
		Tags:        []string{"grey", "blue", "clean"},
		Category:    CatMinimal,
	},
	"Terminal": {
		Tagline:     "Green-on-black, pure hacker.",
		Description: "Phosphor-green text on true black with a monospace stack throughout. Zero border radius and wide measure — a love letter to the CRT.",
		Tags:        []string{"green", "monospace", "retro"},
		Category:    CatMono,
	},
	"Sepia": {
		Tagline:     "Warm parchment for long reads.",
		Description: "Soft sepia tones, a serif body face, and roomy 1.8 line-height tuned for sustained reading comfort, day or night.",
		Tags:        []string{"warm", "serif", "comfortable"},
		Category:    CatReading,
	},
	"Carbon": {
		Tagline:     "High-contrast technical dark.",
		Description: "An IBM-Carbon-flavoured palette: near-black surfaces, crisp blue and pink accents, and sharp corners for a precise, engineered feel.",
		Tags:        []string{"contrast", "blue", "technical"},
		Category:    CatDark,
	},
	"Ocean": {
		Tagline:     "Deep-sea blues and cyans.",
		Description: "An immersive midnight-ocean background with bright cyan highlights. Rounded corners and easy spacing give it a fluid, modern feel.",
		Tags:        []string{"blue", "cyan", "deep"},
		Category:    CatDark,
	},
	"Sakura": {
		Tagline:     "Soft cherry-blossom pinks.",
		Description: "Gentle rose and blush tones over a plum-dark base. Large radii and a relaxed measure make it feel light and welcoming.",
		Tags:        []string{"pink", "soft", "rounded"},
		Category:    CatVibrant,
	},
	"Midnight": {
		Tagline:     "Deep indigo journal.",
		Description: "Inky indigo surfaces with indigo-violet accents and a narrow journal measure. Inter + IBM Plex Mono for a focused writing mood.",
		Tags:        []string{"indigo", "journal", "focused"},
		Category:    CatDark,
	},
	"Bloom": {
		Tagline:     "Warm rose, soft and welcoming.",
		Description: "A friendly rose-and-coral palette over a warm dark base. Soft corners and a cosy measure suit personal blogs and newsletters.",
		Tags:        []string{"rose", "warm", "friendly"},
		Category:    CatVibrant,
	},
	"Mint": {
		Tagline:     "Fresh green, crisp and modern.",
		Description: "Cool mint and emerald accents on a deep forest base. Tight radii and a compact measure give a clean, contemporary edge.",
		Tags:        []string{"green", "mint", "modern"},
		Category:    CatMinimal,
	},
	"Solar": {
		Tagline:     "Amber-gold and energetic.",
		Description: "Warm amber and gold over a dark espresso base, with a large body size and generous spacing — bold, sunny, and confident.",
		Tags:        []string{"amber", "gold", "bold"},
		Category:    CatVibrant,
	},
	"Plum": {
		Tagline:     "Rich purple, sophisticated.",
		Description: "Luxe purple surfaces with orchid accents and a refined measure. A sophisticated dark theme for essays and editorial writing.",
		Tags:        []string{"purple", "rich", "elegant"},
		Category:    CatDark,
	},
	"Fog": {
		Tagline:     "Misty grey, calm and minimal.",
		Description: "Muted greys with a whisper of blue. The lowest-contrast, quietest theme in the catalogue — ideal when the content is the star.",
		Tags:        []string{"grey", "muted", "calm"},
		Category:    CatMinimal,
	},
	"Amber": {
		Tagline:     "Golden hour, retro film.",
		Description: "Warm golden tones with a Georgia serif and extra-loose 1.85 line-height. Square corners lend a vintage, printed-page character.",
		Tags:        []string{"amber", "serif", "retro"},
		Category:    CatReading,
	},
	"Pine": {
		Tagline:     "Forest green, grounded.",
		Description: "Evergreen surfaces with emerald accents and a touch of gold. Natural, grounded, and easy on the eyes for daily reading.",
		Tags:        []string{"green", "forest", "natural"},
		Category:    CatDark,
	},
	"Lavender": {
		Tagline:     "Soft purple, gentle and dreamy.",
		Description: "Pale violet accents over a muted purple base, with rounded corners and a relaxed measure — quiet, dreamy, and soft-spoken.",
		Tags:        []string{"purple", "soft", "dreamy"},
		Category:    CatVibrant,
	},
	"Noir": {
		Tagline:     "True black, high-contrast editorial.",
		Description: "Pure black and white with no chrome at all — zero radius, maximum contrast, large type. Stark, confident, and unmistakably editorial.",
		Tags:        []string{"black", "contrast", "editorial"},
		Category:    CatEditorial,
	},
	"Meadow": {
		Tagline:     "Green-yellow, fresh and organic.",
		Description: "Lime and chartreuse accents on a deep green base with a bright yellow highlight. Light, organic, and energetic.",
		Tags:        []string{"lime", "fresh", "organic"},
		Category:    CatVibrant,
	},
	"Rust": {
		Tagline:     "Terracotta earth tones.",
		Description: "Warm terracotta and clay over a dark earthen base, paired with a Georgia serif. Grounded warmth for storytelling.",
		Tags:        []string{"orange", "earth", "serif"},
		Category:    CatReading,
	},
	"Glacier": {
		Tagline:     "Icy blue, clean and crisp.",
		Description: "Cool glacier blues and cyans with soft corners. Crisp and refreshing, with a comfortable measure for technical writing.",
		Tags:        []string{"blue", "icy", "crisp"},
		Category:    CatMinimal,
	},
	"Coral": {
		Tagline:     "Orange-pink tropical energy.",
		Description: "Vibrant coral and rose accents over a warm dark base. Lively and inviting, with rounded corners and a relaxed measure.",
		Tags:        []string{"coral", "pink", "vibrant"},
		Category:    CatVibrant,
	},
	"Gale": {
		Tagline:     "Editorial magazine, bold typography.",
		Description: "A full editorial layout: large hero sections, card-based grids, and bold amber accents over sophisticated dark surfaces. Ships its own layout CSS for a true magazine feel.",
		Tags:        []string{"magazine", "amber", "layout"},
		Category:    CatEditorial,
	},
	"Zephyr": {
		Tagline:     "Bright, creative, open layout.",
		Description: "An airy, creative theme with coral-rose accents, prominent calls-to-action, and a multi-column footer. Ships its own layout CSS for a modern marketing-site feel.",
		Tags:        []string{"creative", "coral", "layout"},
		Category:    CatEditorial,
	},
	"Dispatch": {
		Tagline:     "Modern newsletter & email publication.",
		Description: "A sovereign re-imagining of the Brief newsletter theme. A clean neutral canvas with a confident emerald accent and an email-width measure, shipping a complete component kit: a customizable hero with inline/section subscribe, a 'Featured in' logos strip, a story section, inbox-style featured issues, a reviews slider, a latest-issues feed with a sticky subscribe sidebar, a topic list, newsletter cards, membership tiers, archive, and web/email post formats — light & dark.",
		Tags:        []string{"newsletter", "email", "subscribe", "emerald", "layout"},
		Category:    CatNewsletter,
	},
}

// Meta returns the store metadata for a theme by name. When no explicit entry
// exists (e.g. a freshly added preset), it synthesises a reasonable fallback so
// the store still renders a complete card.
func Meta(name string) ThemeMeta {
	m, ok := catalogMeta[name]
	if !ok {
		m = ThemeMeta{
			Tagline:     name + " theme.",
			Description: "A built-in VayuPress theme. Deploy it site-wide, or open it in the Theme Studio to fine-tune every design token.",
			Tags:        []string{"built-in"},
			Category:    CatDark,
		}
	}
	m.Name = name
	if m.Category == "" {
		m.Category = CatDark
	}
	return m
}

// Store returns every deployable theme as a StoreEntry (tokens + metadata), in
// AllPresets() display order. This is what the Theme Store renders.
func Store() []StoreEntry {
	presets := AllPresets()
	out := make([]StoreEntry, 0, len(presets))
	for _, p := range presets {
		out = append(out, StoreEntry{Tokens: p, Meta: Meta(p.Name)})
	}
	return out
}

// Categories returns the distinct category labels present in the store, in a
// stable, curated order (categories not present in the catalogue are omitted).
func Categories() []string {
	order := []string{CatDark, CatLight, CatMinimal, CatReading, CatEditorial, CatNewsletter, CatVibrant, CatMono}
	present := map[string]bool{}
	for _, e := range Store() {
		present[e.Meta.Category] = true
	}
	out := make([]string, 0, len(order))
	for _, c := range order {
		if present[c] {
			out = append(out, c)
		}
	}
	return out
}

// MatchesQuery reports whether a theme matches a free-text search query across
// its name, tagline, description, tags, and category (case-insensitive).
func (m ThemeMeta) MatchesQuery(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	hay := strings.ToLower(m.Name + " " + m.Tagline + " " + m.Description + " " + m.Category + " " + strings.Join(m.Tags, " "))
	return strings.Contains(hay, q)
}
