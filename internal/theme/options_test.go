package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestThemeOptionsApply proves customization options realise through CompileCSS:
// scheme re-tints the accent everywhere, width/corners mutate tokens, and
// heading case / accent fill append scoped rules targeting the real markup.
func TestThemeOptionsApply(t *testing.T) {
	g := theme.Gale()
	g.Options = map[string]string{
		"scheme": "violet", "width": "wide", "corners": "sharp",
		"headingcase": "uppercase", "accentfill": "gradient",
	}
	css, err := theme.CompileCSS(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{
		"#8b5cf6",                      // violet dark accent applied
		"--vp-accent:#8b5cf6",          // flows into the vp bridge
		"--accent:#8b5cf6",             // and the public-site bridge
		"--max-w:58rem",                // wide reading width
		"--radius:0;",                  // sharp corners
		"text-transform:uppercase",     // heading case
		"-webkit-background-clip:text", // accent gradient fill
	} {
		if !strings.Contains(css, want) {
			t.Errorf("options-compiled CSS missing %q", want)
		}
	}
}

// TestLayoutArchetypes proves (a) colour presets are tagged with an archetype so
// applying them restyles layout, and (b) the archetype option emits its scoped
// CSS through CompileCSS, while design themes keep their own CSS (no archetype).
func TestLayoutArchetypes(t *testing.T) {
	byName := map[string]theme.Tokens{}
	for _, p := range theme.AllPresets() {
		byName[p.Name] = p
	}
	// A colour preset carries an archetype option.
	if got := byName["Aurora"].Options["archetype"]; got != "magazine" {
		t.Errorf("Aurora archetype = %q, want magazine", got)
	}
	// A design theme keeps its own layout (no archetype tag).
	if got := byName["Apex"].Options["archetype"]; got != "" {
		t.Errorf("Apex should not be tagged with an archetype, got %q", got)
	}

	// The archetype option realises distinct layout CSS via CompileCSS.
	g := theme.Gale()
	g.Options = map[string]string{"archetype": "magazine"}
	css, err := theme.CompileCSS(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(css, "archetype: magazine") || !strings.Contains(css, ".vayu-post-list{display:grid") {
		t.Errorf("magazine archetype CSS not applied")
	}
	// Each archetype produces different CSS.
	seen := map[string]bool{}
	for _, k := range []string{"minimal", "classic", "magazine", "editorial", "bold"} {
		c := theme.ArchetypeCSS(k)
		if c == "" || seen[c] {
			t.Errorf("archetype %q has empty or duplicate CSS", k)
		}
		seen[c] = true
	}
}

// TestArticleLayoutOptions proves the article-page options emit scoped CSS that
// targets the real article markup (header, meta, related) — so they restyle
// every post page, not just the homepage.
func TestArticleLayoutOptions(t *testing.T) {
	g := theme.Gale()
	g.Options = map[string]string{"articlealign": "center", "articlemeta": "hidden", "relatedposts": "hidden"}
	css, err := theme.CompileCSS(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{
		".vayu-article-header{text-align:center}",
		".vayu-article-meta{display:none}",
		".vayu-related{display:none}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("article-option CSS missing %q", want)
		}
	}
	// "notags" hides only the tag links, not the whole meta line.
	g2 := theme.Gale()
	g2.Options = map[string]string{"articlemeta": "notags"}
	css2, _ := theme.CompileCSS(g2)
	if !strings.Contains(css2, ".vayu-article-meta a.vayu-tag{display:none}") {
		t.Errorf("notags should hide only tag links, got: %s", css2)
	}
}

// TestHeroAndDesignOptions proves the hero, navigation, card and link options
// emit scoped CSS targeting the real public markup — so they restyle the live
// site (and preview), not just a section.
func TestHeroAndDesignOptions(t *testing.T) {
	g := theme.Gale()
	g.Options = map[string]string{
		"herostyle": "boxed", "herobg": "image", "heroheight": "tall",
		"navstyle": "spread", "cardstyle": "elevated", "linkstyle": "underline",
	}
	css, err := theme.CompileCSS(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{
		"url(/theme-assets/hero)",   // hero image background
		".vayu-hero{",               // hero restyled
		".vayu-nav{display:flex",    // nav style
		".vayu-post-card{",          // card style
		"text-decoration:underline", // link style
	} {
		if !strings.Contains(css, want) {
			t.Errorf("hero/design option CSS missing %q", want)
		}
	}
}

// TestLayoutOptions proves the post-feed layout and header-alignment options
// emit scoped CSS targeting the real public markup, so they change structure
// (not just colours) in both the live site and the preview.
func TestLayoutOptions(t *testing.T) {
	g := theme.Gale()
	g.Options = map[string]string{"feedlayout": "grid", "headeralign": "center"}
	css, err := theme.CompileCSS(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{
		".vayu-post-list{display:grid",
		".vayu-hero{text-align:center}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("layout-option CSS missing %q", want)
		}
	}

	// "cards" adds card chrome on top of the grid.
	g2 := theme.Gale()
	g2.Options = map[string]string{"feedlayout": "cards"}
	css2, _ := theme.CompileCSS(g2)
	if !strings.Contains(css2, ".vayu-post-card{border:") {
		t.Errorf("cards feed layout should add card chrome, got: %s", css2)
	}
}

// TestDefaultOptionsAreNoop proves applying the default option set produces the
// exact same CSS as no options at all — so the controls never surprise users.
func TestDefaultOptionsAreNoop(t *testing.T) {
	plain, _ := theme.CompileCSS(theme.Beacon())
	withDefaults := theme.Beacon()
	withDefaults.Options = theme.DefaultOptions()
	got, _ := theme.CompileCSS(withDefaults)
	if plain != got {
		t.Error("DefaultOptions() must compile identically to no options")
	}
}

// TestOptionsForEveryTheme proves the studio can offer the full option set for
// every catalogue theme.
func TestOptionsForEveryTheme(t *testing.T) {
	for _, p := range theme.AllPresets() {
		if len(theme.OptionsFor(p.Name)) < 5 {
			t.Errorf("theme %q exposes too few options", p.Name)
		}
	}
}

// TestPerThemeExtras proves per-theme extras layer on top of the shared set and
// realise through CompileCSS (density + heading scale emit scoped rules).
func TestPerThemeExtras(t *testing.T) {
	// Apex gets both density and headingscale on top of the 5 shared options.
	if got := len(theme.OptionsFor("Apex")); got < 7 {
		t.Errorf("Apex should expose shared + extras (>=7), got %d", got)
	}
	// A theme with no extras keeps exactly the shared set.
	if got, want := len(theme.OptionsFor("Default")), len(theme.AllOptions()); got != want {
		t.Errorf("Default should expose exactly the %d shared options, got %d", want, got)
	}
	if len(theme.PerThemeOptions()) == 0 {
		t.Fatal("expected at least one per-theme option")
	}

	ap := theme.Apex()
	ap.Options = map[string]string{"density": "spacious", "headingscale": "xl"}
	css, err := theme.CompileCSS(ap)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{"line-height:1.85", ".vayu-hero h1{font-size:4.6rem}"} {
		if !strings.Contains(css, want) {
			t.Errorf("per-theme extra CSS missing %q", want)
		}
	}
}
