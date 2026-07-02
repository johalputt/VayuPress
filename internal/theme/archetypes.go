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
//
// Each archetype is a WHOLE-SITE design — navigation, hero, feed, article page
// and footer — so switching themes transforms every page, not just the home
// cards. The aesthetic is deliberately Ghost-like: flat colour, hairline rules,
// generous whitespace. No gradients, no glows, no neon.
func ArchetypeCSS(key string) string {
	switch key {
	case "minimal":
		// Quiet, centered, air everywhere — Ghost "Solo/Edition" spirit.
		return `/* archetype: minimal */
.vayu-nav{display:flex;flex-direction:column;align-items:center;gap:.6rem;padding:2.2rem 0 1.6rem;border-bottom:0}
.vayu-nav-brand{font-weight:600;letter-spacing:-.01em;font-size:1.15rem}
.vayu-nav-links{display:flex;gap:1.4rem;font-size:.92rem;opacity:.75}
.vayu-hero{text-align:center;background:none;border-bottom:0;padding:3.2rem 0 2.6rem}
.vayu-hero h1{font-weight:600;letter-spacing:-.02em;font-size:clamp(1.9rem,4vw,2.6rem)}
.vayu-hero .vayu-hero-tagline{margin-left:auto;margin-right:auto;max-width:34rem;opacity:.7}
.vayu-hero .vayu-stats{justify-content:center}
.vayu-hero-eyebrow{background:none;border:0;letter-spacing:.22em;text-transform:uppercase;font-size:.68rem;opacity:.55}
.vayu-section-label{font-size:.7rem;letter-spacing:.18em;text-transform:uppercase;opacity:.55;text-align:center}
.vayu-post-list{display:flex;flex-direction:column;gap:0;max-width:44rem;margin-left:auto;margin-right:auto}
.vayu-post-card{border:0;border-top:1px solid var(--border,rgba(125,125,125,.16));border-radius:0;padding:1.8rem 0;box-shadow:none;background:none}
.vayu-post-card:first-child{border-top:0}
.vayu-post-card:hover{transform:none;box-shadow:none}
.vayu-post-card--media{display:flex;gap:1.1rem;align-items:flex-start}
.vayu-post-card--media .vayu-post-thumb{flex:0 0 32%;border-radius:6px;overflow:hidden}
.vayu-post-title{font-size:1.22rem;font-weight:600;letter-spacing:-.01em}
.vayu-post-excerpt{opacity:.66}
.vayu-post-arrow{display:none}
.vayu-article-header{text-align:center}
.vayu-article-header h1{font-weight:600;letter-spacing:-.02em}
.vayu-article-meta{justify-content:center}
.vayu-byline{justify-content:center}
article.vayu-prose{font-size:1.05rem;line-height:1.75}
article.vayu-prose blockquote{border-left:2px solid var(--text,#e5e7eb);background:none;font-style:italic;opacity:.85}
.vayu-footer{border-top:1px solid var(--border,rgba(125,125,125,.14));text-align:center;padding-top:2.2rem;opacity:.8}`
	case "classic":
		// Structured and calm — Ghost "Casper" spirit: hairline order, serif-ready.
		return `/* archetype: classic */
.vayu-nav{display:flex;align-items:center;justify-content:space-between;padding:1.4rem 0;border-bottom:1px solid var(--border,rgba(125,125,125,.16))}
.vayu-nav-brand{font-weight:700;letter-spacing:-.01em}
.vayu-nav-links{display:flex;gap:1.3rem;font-size:.95rem}
.vayu-hero{background:none;border-bottom:1px solid var(--border,rgba(125,125,125,.2));padding:3rem 0 2.4rem}
.vayu-hero h1{font-weight:700;letter-spacing:-.02em;font-size:clamp(2.1rem,4.5vw,3rem)}
.vayu-hero .vayu-hero-tagline{max-width:38rem;opacity:.75}
.vayu-hero-eyebrow{background:none;border:0;padding:0;letter-spacing:.16em;text-transform:uppercase;font-size:.72rem;opacity:.6;color:var(--accent,#2dd4bf)}
.vayu-section-label{font-size:.78rem;letter-spacing:.12em;text-transform:uppercase;opacity:.7}
.vayu-post-list{display:flex;flex-direction:column;gap:1.4rem}
.vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.2));border-radius:var(--radius2,10px);padding:1.4rem 1.5rem;box-shadow:none;background:none;transition:border-color .15s}
.vayu-post-card:hover{border-color:var(--accent,#2dd4bf);transform:none;box-shadow:none}
.vayu-post-card--media{display:flex;gap:1.2rem;align-items:flex-start}
.vayu-post-card--media .vayu-post-thumb{flex:0 0 38%;border-radius:8px;overflow:hidden}
.vayu-post-title{font-size:1.4rem;line-height:1.3}
.vayu-article-header h1{letter-spacing:-.02em}
article.vayu-prose{line-height:1.7}
article.vayu-prose blockquote{border-left:3px solid var(--accent,#2dd4bf);background:none}
.vayu-footer{border-top:1px solid var(--border,rgba(125,125,125,.16));padding-top:2rem}`
	case "magazine":
		// Newsroom grid with a lead story — flat surfaces, tight titles.
		return `/* archetype: magazine */
.vayu-nav{display:flex;align-items:center;justify-content:space-between;padding:1.1rem 0;border-bottom:2px solid var(--text,#e5e7eb)}
.vayu-nav-brand{font-weight:800;letter-spacing:-.02em;text-transform:uppercase;font-size:1.05rem}
.vayu-nav-links{display:flex;gap:1.2rem;font-size:.9rem;text-transform:uppercase;letter-spacing:.06em}
.vayu-hero{background:none;border-bottom:0;padding:2.6rem 0 1.8rem}
.vayu-hero h1{font-weight:800;letter-spacing:-.025em;font-size:clamp(2rem,4.5vw,3.1rem)}
.vayu-hero .vayu-hero-tagline{opacity:.72}
.vayu-hero-eyebrow{background:none;border:0;padding:0;font-weight:700;text-transform:uppercase;letter-spacing:.14em;font-size:.7rem;color:var(--accent,#2dd4bf)}
.vayu-section-label{font-size:.74rem;font-weight:700;letter-spacing:.14em;text-transform:uppercase;border-bottom:2px solid var(--text,#e5e7eb);padding-bottom:.45rem}
.vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:1.5rem;align-items:start}
.vayu-post-card{border:1px solid var(--border,rgba(125,125,125,.16));border-radius:var(--radius2,12px);overflow:hidden;display:flex;flex-direction:column;background:var(--surface,#111827);transition:transform .14s,box-shadow .14s}
.vayu-post-card:hover{transform:translateY(-3px);box-shadow:0 10px 28px rgba(0,0,0,.16)}
.vayu-post-card .vayu-post-body{padding:1.1rem 1.2rem 1.35rem}
.vayu-post-thumb img{width:100%;aspect-ratio:16/9;object-fit:cover;display:block}
.vayu-post-title{font-size:1.2rem;line-height:1.25;font-weight:700}
.vayu-post-card:first-child{grid-column:1/-1}
.vayu-post-card:first-child .vayu-post-title{font-size:1.9rem}
.vayu-article-header h1{font-weight:800;letter-spacing:-.025em}
article.vayu-prose blockquote{border-left:4px solid var(--accent,#2dd4bf);background:none;font-weight:500}
.vayu-footer{border-top:2px solid var(--text,#e5e7eb);padding-top:1.8rem}`
	case "editorial":
		// Literary journal — big serif-friendly titles, centered, hairlines only.
		return `/* archetype: editorial */
.vayu-nav{display:flex;flex-direction:column;align-items:center;gap:.7rem;padding:2.4rem 0 1.4rem;border-bottom:1px solid var(--border,rgba(125,125,125,.16))}
.vayu-nav-brand{font-size:1.5rem;font-weight:600;letter-spacing:-.01em}
.vayu-nav-links{display:flex;gap:1.5rem;font-size:.88rem;letter-spacing:.08em;text-transform:uppercase;opacity:.7}
.vayu-hero{text-align:center;background:none;border-bottom:0;padding:3.6rem 0 3rem}
.vayu-hero h1{font-size:clamp(2.4rem,5vw,3.6rem);letter-spacing:-.03em;font-weight:600;line-height:1.12}
.vayu-hero .vayu-hero-tagline{margin-left:auto;margin-right:auto;max-width:36rem;font-size:1.08rem;opacity:.75}
.vayu-hero .vayu-stats{justify-content:center}
.vayu-hero-eyebrow{background:none;border:0;letter-spacing:.24em;text-transform:uppercase;font-size:.68rem;opacity:.55}
.vayu-section-label{text-align:center;font-size:.72rem;letter-spacing:.22em;text-transform:uppercase;opacity:.6}
.vayu-post-list{display:flex;flex-direction:column;gap:2.6rem;max-width:46rem;margin-left:auto;margin-right:auto}
.vayu-post-card{border:0;border-radius:0;padding:0 0 2.6rem;border-bottom:1px solid var(--border,rgba(125,125,125,.18));box-shadow:none;background:none;text-align:center}
.vayu-post-card:hover{transform:none;box-shadow:none}
.vayu-post-card--media .vayu-post-thumb{border-radius:8px;overflow:hidden;margin-bottom:1.1rem}
.vayu-post-meta{justify-content:center}
.vayu-post-title{font-size:1.9rem;line-height:1.16;letter-spacing:-.02em;font-weight:600}
.vayu-post-excerpt{font-size:1.05rem;opacity:.78;max-width:38rem;margin-left:auto;margin-right:auto}
.vayu-post-arrow{display:none}
.vayu-article-header{text-align:center;padding-bottom:1.6rem}
.vayu-article-header h1{font-size:clamp(2.2rem,5vw,3.2rem);letter-spacing:-.025em;font-weight:600;line-height:1.14}
.vayu-article-meta{justify-content:center}
.vayu-byline{justify-content:center}
article.vayu-prose{font-size:1.1rem;line-height:1.8}
article.vayu-prose h2,article.vayu-prose h3{letter-spacing:-.015em;font-weight:600}
article.vayu-prose blockquote{border:0;background:none;text-align:center;font-size:1.25rem;font-style:italic;opacity:.85;padding:1rem 2rem}
.vayu-footer{border-top:1px solid var(--border,rgba(125,125,125,.14));text-align:center;padding-top:2.4rem;opacity:.85}`
	case "bold":
		// Confident poster energy — flat ink, heavy weights, honest edges.
		return `/* archetype: bold */
.vayu-nav{display:flex;align-items:center;justify-content:space-between;padding:1.2rem 0;border-bottom:3px solid var(--text,#e5e7eb)}
.vayu-nav-brand{font-weight:800;letter-spacing:-.02em;text-transform:uppercase}
.vayu-nav-links{display:flex;gap:1.2rem;font-weight:700;font-size:.9rem;text-transform:uppercase;letter-spacing:.05em}
.vayu-hero{background:none;border-bottom:0;padding:3rem 0 2.2rem}
.vayu-hero h1{font-weight:800;text-transform:uppercase;letter-spacing:-.01em;font-size:clamp(2.2rem,5.5vw,3.6rem);line-height:1.05}
.vayu-hero .vayu-hero-tagline{font-weight:500;opacity:.8}
.vayu-hero-eyebrow{background:var(--text,#e5e7eb);color:var(--bg,#0b0f19);border:0;font-weight:800;text-transform:uppercase;letter-spacing:.12em;font-size:.68rem;padding:.3rem .6rem}
.vayu-section-label{font-size:.8rem;font-weight:800;letter-spacing:.16em;text-transform:uppercase;color:var(--accent,#2dd4bf)}
.vayu-post-list{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:1.4rem;align-items:start}
.vayu-post-card{border:2px solid var(--text,#e5e7eb);border-radius:0;padding:1.25rem;background:none;box-shadow:5px 5px 0 var(--accent,#2dd4bf);transition:transform .12s,box-shadow .12s}
.vayu-post-card:hover{transform:translate(-2px,-2px);box-shadow:7px 7px 0 var(--accent,#2dd4bf)}
.vayu-post-title{font-size:1.35rem;font-weight:800;letter-spacing:-.01em}
.vayu-article-header h1{font-weight:800;text-transform:uppercase;letter-spacing:-.01em}
article.vayu-prose h1,article.vayu-prose h2{font-weight:800}
article.vayu-prose blockquote{border:2px solid var(--text,#e5e7eb);background:none;font-weight:600}
.vayu-footer{border-top:3px solid var(--text,#e5e7eb);padding-top:1.8rem;font-weight:600}`
	default:
		return ""
	}
}
