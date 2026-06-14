package render

import "strings"

import "testing"

func TestThemeCSSGeneratesVariableOverrides(t *testing.T) {
	SetActiveSettings(SiteSettings{
		PrimaryLight: "#0d9488",
		PrimaryDark:  "#2dd4bf",
		AccentLight:  "#f59e0b",
		AccentDark:   "#fbbf24",
		CustomCSS:    "body{font-family:Georgia}",
	})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	css := ThemeCSS()
	for _, want := range []string{
		`:root,[data-theme="light"]{`,
		"--pico-primary:#0d9488;",
		"--vayu-accent:#f59e0b;",
		`[data-theme="dark"]{`,
		"--pico-primary:#2dd4bf;",
		"body{font-family:Georgia}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("ThemeCSS() missing %q\ngot: %s", want, css)
		}
	}
	if strings.Contains(css, "<style") {
		t.Error("ThemeCSS() must not include <style> wrapper (served as text/css)")
	}
}

func TestThemeCSSETagChangesWithSettings(t *testing.T) {
	SetActiveSettings(SiteSettings{PrimaryLight: "#111111"})
	a := ThemeCSSETag()
	SetActiveSettings(SiteSettings{PrimaryLight: "#222222"})
	b := ThemeCSSETag()
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })
	if a == b {
		t.Error("ETag should change when the palette changes")
	}
}

func TestThemeCSSEmptyWhenUnset(t *testing.T) {
	SetActiveSettings(SiteSettings{})
	if got := ThemeCSS(); got != "" {
		t.Errorf("expected empty CSS for zero settings, got %q", got)
	}
}

func TestHeadMetaRendersAllowlistedTagsEscaped(t *testing.T) {
	got := string(headMetaHTML(SiteSettings{
		Keywords:     `sovereignty, "governance"`,
		ThemeColor:   "#0d9488",
		Robots:       "noindex,nofollow",
		VerifyGoogle: "abc-123_def",
	}))
	for _, want := range []string{
		`<meta name="keywords" content="sovereignty, &#34;governance&#34;">`,
		`<meta name="theme-color" content="#0d9488">`,
		`<meta name="robots" content="noindex,nofollow">`,
		`<meta name="google-site-verification" content="abc-123_def">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("headMetaHTML missing %q\ngot: %s", want, got)
		}
	}
	// Bing was empty → no msvalidate tag emitted.
	if strings.Contains(got, "msvalidate") {
		t.Errorf("expected no bing tag when unset, got: %s", got)
	}
}

func TestHeadMetaEmptyWhenUnset(t *testing.T) {
	if got := string(headMetaHTML(SiteSettings{})); got != "" {
		t.Errorf("expected empty head meta for zero settings, got %q", got)
	}
}

// Guards the core governance property: a value that tries to break out of the
// content="" attribute is escaped, so no arbitrary markup can reach <head>.
func TestHeadMetaCannotInjectMarkup(t *testing.T) {
	got := string(headMetaHTML(SiteSettings{
		Keywords: `"><script>alert(1)</script>`,
	}))
	if strings.Contains(got, "<script>") {
		t.Errorf("head meta must not emit raw <script>, got: %s", got)
	}
}

// TestDefaultPaletteMatchesVendoredCSS guards against first-deploy FOUC: the
// vendored custom.css must declare the same default palette the settings layer
// falls back to (settings.Defaults), or a page rendered before any operator
// save would flash a different hue than /theme.css later applies.
func TestDefaultPaletteMatchesVendoredCSS(t *testing.T) {
	for _, hexColor := range []string{"#0d9488", "#2dd4bf", "#f59e0b", "#fbbf24"} {
		if !strings.Contains(customCSSMin, hexColor) {
			t.Errorf("vendored custom.css missing default palette colour %s (drift vs settings.Defaults)", hexColor)
		}
	}
}

func TestThemeToggleJSLinkIsSameOriginScript(t *testing.T) {
	link := string(ThemeToggleJSLink())
	if !strings.HasPrefix(link, `<script src="/static/js/theme-toggle.js?v=`) {
		t.Errorf("toggle link must be a same-origin <script src>, got: %s", link)
	}
	// Must carry no inline body (would require a CSP nonce that cached HTML lacks).
	if strings.Contains(link, "localStorage") {
		t.Errorf("toggle must be external, not inline: %s", link)
	}
	if !strings.Contains(ThemeToggleJS, "localStorage") {
		t.Error("toggle script should persist preference in localStorage")
	}
}
