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
	if got := len(theme.OptionsFor("Default")); got != 13 {
		t.Errorf("Default should expose exactly the 13 shared options, got %d", got)
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
