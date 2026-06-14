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
