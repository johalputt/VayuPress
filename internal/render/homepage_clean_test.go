package render

import (
	"strings"
	"testing"
)

// TestHomepageCleanByDefault locks the "clean homepage" behaviour: with the
// hero toggle off (the default) the homepage shows NO hero, and none of the
// removed runtime/stats chrome ever appears.
func TestHomepageCleanByDefault(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme", Tagline: "A tagline", Description: "A description"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderHome("example.com", "1.0.0", nil, 0)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	for _, banned := range []string{
		"vayu-hero",                    // hero block hidden by default
		"vayu-stats",                   // published/stats wall removed
		"Sovereign Publishing Runtime", // old eyebrow default
		"runtime · normal",             // nav status pill removed
		"vayu-footer-badge",            // runtime · governed badge removed
	} {
		if strings.Contains(out, banned) {
			t.Errorf("clean homepage must not contain %q", banned)
		}
	}
}

// TestHomepageHeroOptIn proves the hero renders once the operator turns it on.
func TestHomepageHeroOptIn(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme", Tagline: "Welcome", Description: "Words.", ShowHero: true})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderHome("example.com", "1.0.0", nil, 0)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	if !strings.Contains(out, "vayu-hero") {
		t.Error("hero should render when ShowHero is true")
	}
	if !strings.Contains(out, "Welcome") {
		t.Error("hero should show the tagline as the headline")
	}
}
