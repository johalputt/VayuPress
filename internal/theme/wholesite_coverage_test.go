// SPDX-License-Identifier: Apache-2.0

package theme_test

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

// wholeSiteSelectors are public-markup elements that a design theme must style
// for a theme switch to transform the WHOLE blog (not just the home hero/cards):
// the article byline, the author card, the multi-column footer, and cover-image
// post cards.
//
// EVERY ENTRY HERE MUST BE MARKUP THE RENDERER ACTUALLY EMITS. This list carried
// only .vayu-author-box for a long time, and nothing emitted it — the class was
// designed (admin field, design-studio option, twelve stylesheets, ADR-0086) and
// never rendered, so a gate meant to prove "the theme reaches the whole site"
// was satisfied by twelve rule sets that could never match. Meanwhile
// .vayu-byline, which IS emitted under every article headline, went unstyled in
// nine of the twelve themes and the gate had nothing to say about it.
// TestWholeSiteSelectorsAreRealMarkup below now holds that line.
var wholeSiteSelectors = []string{
	".vayu-byline",
	".vayu-author-box",
	".vayu-footer-col-links",
	".vayu-post-card--media",
}

// TestWholeSiteSelectorsAreRealMarkup is the gate on the gate: a selector may
// only be required of every theme if the renderer emits it. Without this, the
// coverage list can quietly drift back into demanding CSS for markup that does
// not exist — which is how it spent its whole life until now.
func TestWholeSiteSelectorsAreRealMarkup(t *testing.T) {
	// Rendered with everything switched on that the article template gates on,
	// so byline, author card, tags and related list are all present.
	markup := renderFullArticle(t)
	for _, sel := range wholeSiteSelectors {
		class := strings.TrimPrefix(sel, ".")
		if !strings.Contains(markup, class) {
			t.Errorf("%q is required of every theme but the renderer emits no such class — the coverage gate would be checking rules that can never match", sel)
		}
	}
}

// TestDesignThemesCoverWholeSite guards against the "only the homepage changes"
// regression: every preset that ships CustomCSS must restyle the byline, the
// author card, the footer columns, and cover-image cards — so applying it
// visibly changes every section of the site.
func TestDesignThemesCoverWholeSite(t *testing.T) {
	design := 0
	for _, p := range theme.AllPresets() {
		if strings.TrimSpace(p.CustomCSS) == "" {
			continue // colour-palette preset — exempt
		}
		design++
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Fatalf("%s: CompileCSS failed: %v", p.Name, err)
		}
		for _, sel := range wholeSiteSelectors {
			if !strings.Contains(css, sel) {
				t.Errorf("design theme %q does not style %q — applying it would leave that section unthemed", p.Name, sel)
			}
		}
	}
	if design < 9 {
		t.Fatalf("expected at least 9 design themes, found %d", design)
	}
}

// renderFullArticle returns the real HTML of every public page, rendered with
// everything the templates gate on switched on: a hero, a search box, cover
// images, an author with a slug and a bio, tags, and related posts.
//
// This is the only honest way to ask "does the renderer emit this class?" —
// reading the template by eye is what let .vayu-author-box sit in twelve
// stylesheets, a design-studio option, an ADR and this very gate without any
// page ever containing it.
func renderFullArticle(t *testing.T) string {
	t.Helper()
	render.Init(t.TempDir())
	s := render.SiteSettings{
		Name: "Example", Tagline: "T", Description: "D",
		Author: "A Person", AuthorBio: "One line about the author.", ShowHero: true,
		// The premium footer's link columns only exist when the operator has
		// configured one — leaving this out is what made the first run of
		// TestWholeSiteSelectorsAreRealMarkup report .vayu-footer-col-links as
		// dead markup when it is merely conditional. A gate that renders less
		// than the templates can produce reports live selectors as dead.
		FooterJSON: `{"tagline":"T","columns":[{"title":"More","links":[{"label":"About","href":"/about"}]}],` +
			`"social":[{"label":"Feed","href":"/feed.xml"}],"legal":[{"label":"Privacy","href":"/privacy"}]}`,
	}
	prev := render.GetActiveSettings()
	render.SetActiveSettings(s)
	t.Cleanup(func() { render.SetActiveSettings(prev) })
	render.SetSearchEnabled(true)

	now := time.Now()
	home, err := render.RenderHomeWithSettings(s, "example.com", "0", []render.HomeArticle{{
		Title: "One", Slug: "one", Excerpt: "E", CreatedAt: now, Author: "A Person",
		Image: "/x.png",
	}}, 1, 1, 2)
	if err != nil {
		t.Fatalf("render home: %v", err)
	}

	article, err := render.RenderArticleWithMetaSettings(s, db.Article{
		ID: "1", Title: "One", Slug: "one", Content: "<p>x</p>",
		Tags: []string{"a"}, CreatedAt: now,
	}, render.ArticleLayoutDefault, []render.RelatedArticle{{Title: "Two", Slug: "two", CreatedAt: now}},
		render.ArticleMetaOverrides{})
	if err != nil {
		t.Fatalf("render article: %v", err)
	}

	search, err := render.RenderSearch("example.com", "0", "q", []render.SearchHit{
		{Title: "One", Slug: "one", CreatedAt: now, Tags: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("render search: %v", err)
	}

	return home + article + search + render.Render404("example.com", "0")
}

// TestRealMarkupSelectorsAreReal applies the same rule to the other coverage
// list in this package (realmarkup_test.go), which likewise describes itself as
// "class names the PUBLIC templates actually emit" without ever checking.
func TestRealMarkupSelectorsAreReal(t *testing.T) {
	markup := renderFullArticle(t)
	for _, sel := range realMarkupSelectors {
		if !strings.Contains(markup, strings.TrimPrefix(sel, ".")) {
			t.Errorf("%q is required of every theme but no public page emits it", sel)
		}
	}
}

// TestDesignThemesAreMutuallyDistinct guards against design themes collapsing
// into look-alikes: no two design themes may compile to identical CSS.
func TestDesignThemesAreMutuallyDistinct(t *testing.T) {
	seen := map[string]string{} // compiled CSS -> theme name
	for _, p := range theme.AllPresets() {
		if strings.TrimSpace(p.CustomCSS) == "" {
			continue
		}
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Fatalf("%s: CompileCSS failed: %v", p.Name, err)
		}
		if other, dup := seen[css]; dup {
			t.Errorf("design themes %q and %q compile to identical CSS — they would look the same", p.Name, other)
		}
		seen[css] = p.Name
	}
}
