package theme

// archetypes.go — reusable LAYOUT archetypes.
//
// A theme is more than a palette: applying one should change structure, spacing,
// cards and headings — not just colour. To deliver that for every colour preset
// without authoring bespoke CSS per theme, each preset is assigned a layout
// "archetype" (Minimal / Classic / Magazine / Editorial / Bold). The archetype's
// CSS targets the real public markup (the vayu-* classes) and is appended to the
// compiled stylesheet, so switching presets visibly restyles the whole blog.
//
// The archetype is carried as the "archetype" customization option (see
// options.go), so it flows through the same preview/apply/persist pipeline as
// every other option and can be overridden by the operator in the Studio.

// ArchetypeChoices lists the selectable layout archetypes for the Studio control.
func ArchetypeChoices() []OptionChoice {
	return []OptionChoice{
		{"default", "Theme default"},
		{"minimal", "Minimal"},
		{"classic", "Classic"},
		{"magazine", "Magazine"},
		{"editorial", "Editorial"},
		{"bold", "Bold"},
	}
}

// archetypeForPreset maps a colour preset to a layout archetype so applying it
// transforms the layout, not just the palette. Design themes that ship their own
// component CSS (Gale, Apex, …) return "" — their bespoke CSS owns the layout.
func archetypeForPreset(name string) string {
	switch name {
	case "Default", "Midnight", "Pine", "Slate":
		return "classic"
	case "Aurora", "Ocean", "Plum", "Meadow", "Coral", "Glacier":
		return "magazine"
	case "Terminal", "Mint", "Fog":
		return "minimal"
	case "Sepia", "Sakura", "Bloom", "Lavender", "Rust":
		return "editorial"
	case "Carbon", "Solar", "Amber", "Noir":
		return "bold"
	default:
		return "" // design themes (Gale/Zephyr/…): keep their own CustomCSS layout
	}
}

// ArchetypeCSS returns the scoped stylesheet for a layout archetype, or "" for
// "default"/unknown. Rules target the real vayu-* markup and lean on the public
// CSS variables (with fallbacks) so they adapt to whatever palette is active.
func ArchetypeCSS(key string) string {
	switch key {
	case "minimal":
		return `/* archetype: minimal */
.vayu-section-label{font-size:.7rem;letter-spacing:.18em;text-transform:uppercase;opacity:.55}
.vayu-post-list{display:flex;flex-direction:column;gap:0}
.vayu-post-card{border:0;border-top:1px solid var(--border,rgba(125,125,125,.16));border-radius:0;padding:1.7rem 0;box-shadow:none}
.vayu-post-card:first-child{border-top:0}
.vayu-post-card--media{display:flex;gap:1.1rem;align-items:flex-start}
.vayu-post-card--media .vayu-post-thumb{flex:0 0 34%;border-radius:8px;overflow:hidden}
.vayu-post-title{font-size:1.2rem;font-weight:600;letter-spacing:-.01em}
.vayu-post-excerpt{opacity:.68}
.vayu-hero h1{font-weight:600;letter-spacing:-.02em}`
	case "classic":
		return `/* archetype: classic */
.vayu-section-label{font-size:.8rem;letter-spacing:.12em;text-transform:uppercase;opacity:.7}
.vayu-post-list{display:flex;flex-direction:column;gap:1.4rem}
.vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.2));border-radius:var(--radius2,10px);padding:1.4rem 1.5rem;box-shadow:none}
.vayu-post-card--media{display:flex;gap:1.2rem;align-items:flex-start}
.vayu-post-card--media .vayu-post-thumb{flex:0 0 40%;border-radius:8px;overflow:hidden}
.vayu-post-title{font-size:1.4rem}
.vayu-hero{border-bottom:1px solid var(--border,rgba(125,125,125,.2));padding-bottom:2rem}`
	case "magazine":
		return `/* archetype: magazine */
.vayu-section-label{font-size:.78rem;letter-spacing:.12em;text-transform:uppercase;opacity:.7}
.vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:1.5rem;align-items:start}
.vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.16));border-radius:var(--radius2,14px);overflow:hidden;display:flex;flex-direction:column;background:color-mix(in srgb,var(--surface,#111827) 55%,transparent);transition:transform .14s,box-shadow .14s}
.vayu-post-card:hover{transform:translateY(-3px);box-shadow:0 12px 32px rgba(0,0,0,.22)}
.vayu-post-card .vayu-post-body{padding:1.1rem 1.2rem 1.35rem}
.vayu-post-thumb img{width:100%;aspect-ratio:16/9;object-fit:cover;display:block}
.vayu-post-title{font-size:1.2rem;line-height:1.25}
.vayu-post-card:first-child{grid-column:1/-1}
.vayu-post-card:first-child .vayu-post-title{font-size:1.9rem}`
	case "editorial":
		return `/* archetype: editorial */
.vayu-section-label{text-align:center;font-size:.74rem;letter-spacing:.2em;text-transform:uppercase;opacity:.6}
.vayu-post-list{display:flex;flex-direction:column;gap:2.6rem;max-width:46rem;margin-left:auto;margin-right:auto}
.vayu-post-card{border:0;border-radius:0;padding:0 0 2.6rem;border-bottom:1px solid var(--border,rgba(125,125,125,.18));box-shadow:none}
.vayu-post-card--media .vayu-post-thumb{border-radius:10px;overflow:hidden;margin-bottom:1rem}
.vayu-post-title{font-size:2rem;line-height:1.15;letter-spacing:-.02em}
.vayu-post-excerpt{font-size:1.05rem;opacity:.8}
.vayu-hero h1{font-size:clamp(2.4rem,5vw,3.6rem);letter-spacing:-.03em}
article.vayu-prose h1{font-size:2.6rem;letter-spacing:-.02em}`
	case "bold":
		return `/* archetype: bold */
.vayu-section-label{font-size:.8rem;font-weight:800;letter-spacing:.16em;text-transform:uppercase;color:var(--accent,#2dd4bf)}
.vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:1.25rem;align-items:start}
.vayu-post-card{border:2px solid var(--text,#e5e7eb);border-radius:0;padding:1.25rem;box-shadow:6px 6px 0 var(--accent,#2dd4bf);transition:transform .12s,box-shadow .12s}
.vayu-post-card:hover{transform:translate(-2px,-2px);box-shadow:8px 8px 0 var(--accent,#2dd4bf)}
.vayu-post-title{font-size:1.35rem;font-weight:800;letter-spacing:-.01em}
.vayu-hero h1{font-weight:800;text-transform:uppercase;letter-spacing:-.01em}
article.vayu-prose h1,article.vayu-prose h2{font-weight:800}`
	default:
		return ""
	}
}
