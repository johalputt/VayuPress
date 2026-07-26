// SPDX-License-Identifier: Apache-2.0

package theme_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestEditorPresetCompiles proves the Editor newspaper flagship is a valid,
// deployable preset: present in the catalogue, ships its broadsheet component
// CSS, and compiles to a valid same-origin stylesheet.
func TestEditorPresetCompiles(t *testing.T) {
	e := theme.Editor()
	if e.Name != "Editor" {
		t.Fatalf("expected name Editor, got %q", e.Name)
	}
	if strings.TrimSpace(e.CustomCSS) == "" {
		t.Fatal("Editor must ship its broadsheet component CustomCSS")
	}

	css, err := theme.CompileCSS(e)
	if err != nil {
		t.Fatalf("Editor failed to compile: %v", err)
	}
	// Token bridge + a representative slice of the newsroom component kit.
	for _, want := range []string{
		"--vp-accent", "--pico-primary",
		".editor-masthead", ".editor-navbar", ".editor-topbar", ".editor-front",
		".editor-lead", ".editor-mostread", ".editor-package--pinned",
		".editor-pullquote", ".editor-standfirst", ".editor-keypoints",
		".editor-share-rail", ".editor-paywall-fade", ".editor-prompt",
		".editor-footer", ".editor-ticker", ".editor-progress",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("compiled Editor CSS missing %q", want)
		}
	}
	// The print rule hierarchy (double / story / soft) must ship.
	for _, r := range []string{"section", "story", "soft"} {
		if !strings.Contains(css, ".editor-rule--"+r) {
			t.Errorf("Editor rule tier %q missing", r)
		}
	}
	// All four teaser densities must ship.
	for _, v := range []string{"--compact", "--headline", "--opinion"} {
		if !strings.Contains(css, ".editor-teaser"+v) {
			t.Errorf("Editor teaser variant %q missing", v)
		}
	}
	// Newsroom kickers: news, breaking, live (with the pulse-free fallback).
	for _, k := range []string{".editor-kicker", ".editor-kicker--breaking", ".editor-kicker--live"} {
		if !strings.Contains(css, k) {
			t.Errorf("Editor kicker %q missing", k)
		}
	}
	// Paper tones must ship as wrapper hooks.
	for _, p := range []string{"bright", "ivory", "salmon"} {
		if !strings.Contains(css, ".editor-paper--"+p) {
			t.Errorf("Editor paper tone %q missing", p)
		}
	}
	// Accessibility affordances: focus rings and a reduced-motion story.
	for _, want := range []string{":focus-visible", "prefers-reduced-motion"} {
		if !strings.Contains(css, want) {
			t.Errorf("Editor CSS missing accessibility affordance %q", want)
		}
	}
}

// TestEditorStylesRealMarkup proves Editor restyles the actual public markup —
// the front page becomes a broadsheet, the trending widget a numbered
// most-read rail, and articles get newspaper reading typography.
func TestEditorStylesRealMarkup(t *testing.T) {
	css, err := theme.CompileCSS(theme.Editor())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, sel := range []string{
		".vayu-nav", ".vayu-hero", ".vayu-section-label",
		".vayu-post-list", ".vayu-post-card", ".vayu-post-title", ".vayu-post-card--media",
		".vayu-trending-rank", ".vayu-trending-pin", ".vayu-trending-tab",
		".vayu-article-header", ".vayu-byline", ".vayu-related-list",
		".vayu-author-box", ".vayu-footer-col-links", ".vayu-pagination",
		".vayu-comments", ".vayu-err-code",
	} {
		if !strings.Contains(css, sel) {
			t.Errorf("Editor does not style real markup %q", sel)
		}
	}
}

// TestEditorIsSovereign pins the speed contract: pure CSS, system fonts, no
// external requests of any kind — the properties that keep it 100/100 fast.
func TestEditorIsSovereign(t *testing.T) {
	css, err := theme.CompileCSS(theme.Editor())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, banned := range []string{"@import", "fonts.googleapis", "fonts.gstatic", "cdn.", "http://", "https://"} {
		if strings.Contains(css, banned) {
			t.Errorf("Editor CSS must not reference %q — it must stay sovereign and external-request-free", banned)
		}
	}
	// Motion must be opt-in (no-preference), and the sovereign motion tokens
	// consumed rather than hardcoded (ADR-0136).
	for _, want := range []string{"prefers-reduced-motion:no-preference", "var(--t,", "var(--sh-lg", "var(--ease-out"} {
		if !strings.Contains(css, want) {
			t.Errorf("Editor CSS missing motion-contract marker %q", want)
		}
	}
}

// TestEditorInStore proves Editor appears in the Theme Store under the
// Editorial category with real metadata, and the category is exposed.
func TestEditorInStore(t *testing.T) {
	var found bool
	for _, e := range theme.Store() {
		if e.Meta.Name == "Editor" {
			found = true
			if e.Meta.Category != theme.CatEditorial {
				t.Errorf("Editor category = %q, want %q", e.Meta.Category, theme.CatEditorial)
			}
			if strings.TrimSpace(e.Meta.Tagline) == "" || len(e.Meta.Tags) == 0 {
				t.Error("Editor is missing store metadata (tagline/tags)")
			}
		}
	}
	if !found {
		t.Fatal("Editor not present in theme.Store()")
	}

	var hasEditorial bool
	for _, c := range theme.Categories() {
		if c == theme.CatEditorial {
			hasEditorial = true
		}
	}
	if !hasEditorial {
		t.Error("Editorial category not exposed by Categories()")
	}
}

// TestEditorStudioOptions proves the Editor-specific Theme Studio controls
// exist and realise through CompileCSS, on top of the shared option set.
func TestEditorStudioOptions(t *testing.T) {
	// Shared set + density, headingscale, paper, dropcap, columnrules,
	// readingprogress, pagefade.
	if got, want := len(theme.OptionsFor("Editor")), len(theme.AllOptions())+7; got != want {
		t.Errorf("Editor should expose %d options (shared + 7 extras), got %d", want, got)
	}

	// Paper tone mutates the light canvas through every bridge.
	e := theme.Editor()
	e.Options = map[string]string{"paper": "salmon"}
	css, err := theme.CompileCSS(e)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(css, "#fff1e0") {
		t.Error("paper=salmon should re-tint the light background to FT salmon")
	}

	// Drop cap hide must neutralise the shipped initial letter.
	e2 := theme.Editor()
	e2.Options = map[string]string{"dropcap": "hidden"}
	css2, _ := theme.CompileCSS(e2)
	if !strings.Contains(css2, "initial-letter:normal") {
		t.Error("dropcap=hidden should neutralise the drop cap")
	}

	// Column rules toggle swaps lattice for airy grid and back.
	e3 := theme.Editor()
	e3.Options = map[string]string{"columnrules": "hidden"}
	css3, _ := theme.CompileCSS(e3)
	if !strings.Contains(css3, ".vayu-post-card{box-shadow:none}") {
		t.Error("columnrules=hidden should remove the hairline lattice")
	}

	// Reading progress + page fades realise as strictly-progressive CSS.
	e4 := theme.Editor()
	e4.Options = map[string]string{"readingprogress": "hidden", "pagefade": "off"}
	css4, _ := theme.CompileCSS(e4)
	if !strings.Contains(css4, "article.vayu-prose::before{content:none}") {
		t.Error("readingprogress=hidden should suppress the progress bar")
	}
	if !strings.Contains(css4, "@view-transition{navigation:none}") {
		t.Error("pagefade=off should disable view transitions")
	}

	// Defaults stay a strict no-op (the shared contract).
	plain, _ := theme.CompileCSS(theme.Editor())
	withDefaults := theme.Editor()
	withDefaults.Options = theme.DefaultOptions()
	got, _ := theme.CompileCSS(withDefaults)
	if plain != got {
		t.Error("Editor with DefaultOptions() must compile identically to no options")
	}
}
