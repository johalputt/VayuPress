package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestAgoraPresetCompiles proves the Agora (community/discussion) theme is a
// valid, deployable preset: present in the catalogue, ships its component CSS,
// and compiles to a valid same-origin stylesheet.
func TestAgoraPresetCompiles(t *testing.T) {
	a := theme.Agora()
	if a.Name != "Agora" {
		t.Fatalf("expected name Agora, got %q", a.Name)
	}
	if strings.TrimSpace(a.CustomCSS) == "" {
		t.Fatal("Agora must ship its component CustomCSS")
	}

	css, err := theme.CompileCSS(a)
	if err != nil {
		t.Fatalf("Agora failed to compile: %v", err)
	}
	for _, want := range []string{"--vp-accent", "--pico-primary", ".agora-layout", ".agora-aside-left", ".agora-aside-right", ".agora-card__comments", ".agora-drawer"} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Agora CSS missing %q", want)
		}
	}
	// All six hero image positions must ship.
	for _, pos := range []string{"none", "top", "bottom", "left", "right", "background"} {
		if !strings.Contains(css, ".agora-hero--img-"+pos) {
			t.Errorf("Agora hero image position %q missing", pos)
		}
	}
	// All four feed view styles must ship.
	for _, v := range []string{"list", "compact", "cards", "articles"} {
		if !strings.Contains(css, ".agora-feed--"+v) {
			t.Errorf("Agora feed view %q missing", v)
		}
	}
	// Both sidebars must be independently toggleable.
	for _, mod := range []string{"agora-layout--no-left", "agora-layout--no-right"} {
		if !strings.Contains(css, mod) {
			t.Errorf("Agora layout modifier %q missing", mod)
		}
	}
	// Refinements: threaded comments, drawer backdrop/close, role badges,
	// reactions, and thread-status markers must ship.
	for _, want := range []string{
		".agora-drawer__backdrop", ".agora-drawer__close",
		".agora-comment__replies", ".agora-comment__reply",
		".agora-badge--admin", ".agora-badge--mod", ".agora-presence--online",
		".agora-react--active",
		".agora-status--pinned", ".agora-status--answered", ".agora-status--locked", ".agora-status--hot",
		":focus-visible",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("Agora refinement %q missing", want)
		}
	}
}

// TestAgoraInStore proves Agora appears in the Theme Store under the Community
// category, which must be exposed by Categories().
func TestAgoraInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Agora" {
			found = true
			if e.Meta.Category != theme.CatCommunity {
				t.Errorf("Agora category = %q, want %q", e.Meta.Category, theme.CatCommunity)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Agora is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Agora not present in theme.Store()")
	}

	var hasCommunity bool
	for _, c := range theme.Categories() {
		if c == theme.CatCommunity {
			hasCommunity = true
		}
	}
	if !hasCommunity {
		t.Error("Community category not exposed by theme.Categories()")
	}
}
