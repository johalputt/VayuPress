package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestCompileBridgesPublicSiteVariables proves a deployed theme restyles the
// actual public site — not just the Pico colour variables. The public
// stylesheet (article.css) reads bare variable names (--accent, --bg, --font,
// --max-w, --radius …); the compiler must emit those, mapped from the tokens,
// in addition to the --vp-* and --pico-* families.
func TestCompileBridgesPublicSiteVariables(t *testing.T) {
	tok := theme.Default()
	css, err := theme.CompileCSS(tok)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Public-site variable names must be present and carry the token values.
	mustContain := []string{
		"--accent:" + tok.AccentDark + ";",
		"--bg:" + tok.BgDark + ";",
		"--surface:" + tok.SurfaceDark + ";",
		"--text:" + tok.TextDark + ";",
		"--muted:" + tok.MutedDark + ";",
		"--accent2:" + tok.Accent2Dark + ";",
		"--hi:" + tok.HiDark + ";",
		"--max-w:" + tok.MaxWidth + ";",
		"--radius:" + tok.RadiusSm + ";",
		"--radius2:" + tok.RadiusLg + ";",
		"--font:", // font-family (sanitised) is present
		"--border:color-mix",
	}
	for _, want := range mustContain {
		if !strings.Contains(css, want) {
			t.Errorf("public-site bridge missing %q in compiled CSS", want)
		}
	}

	// The Pico + design-token families must still be present (no regression).
	for _, want := range []string{"--pico-primary:", "--vp-accent:", "--vayu-accent:"} {
		if !strings.Contains(css, want) {
			t.Errorf("expected %q to remain in compiled CSS", want)
		}
	}

	// A manual [data-theme] choice must re-theme the whole site, so explicit
	// dark/light blocks are emitted alongside the system media query.
	for _, want := range []string{`:root[data-theme="dark"]`, `:root[data-theme="light"]`, "@media(prefers-color-scheme:light)"} {
		if !strings.Contains(css, want) {
			t.Errorf("expected %q selector in compiled CSS", want)
		}
	}

	// Light values must appear too (e.g. the light accent).
	if !strings.Contains(css, "--accent:"+tok.AccentLight+";") {
		t.Errorf("light-mode public accent %q missing", tok.AccentLight)
	}
}
