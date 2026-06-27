package render

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// TestRenderArticleWithMetaOverrides proves the per-post publishing options flow
// into the rendered <head> and body: SEO title, canonical, description, the OG /
// Twitter cards, and the hero feature image, with site-root paths resolved to
// absolute share URLs.
func TestRenderArticleWithMetaOverrides(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{
		Title:     "Hello",
		Slug:      "hello",
		Content:   "<p>Body text here for the post.</p>",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	ov := ArticleMetaOverrides{
		MetaTitle:       "Custom SEO Title",
		MetaDescription: "Custom description.",
		CanonicalURL:    "https://canonical.example/orig",
		FeatureImage:    "/media/feat.jpg",
		OGTitle:         "OG Title",
		TwitterImage:    "/media/tw.jpg",
	}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ov)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<title>Custom SEO Title</title>",
		`<link rel="canonical" href="https://canonical.example/orig">`,
		`<meta name="description" content="Custom description.">`,
		`property="og:title" content="OG Title"`,
		`property="og:image" content="https://example.com/media/feat.jpg"`,
		`name="twitter:image" content="https://example.com/media/tw.jpg"`,
		`class="vayu-feature-image" src="https://example.com/media/feat.jpg"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

// TestRenderArticleAsPageHidesChrome verifies that marking a post as a page drops
// the date/tags meta, author box, and related list, and keeps it out of the
// blog-post chrome.
func TestRenderArticleAsPageHidesChrome(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme", Author: "Jo"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{Title: "About", Slug: "about", Content: "<p>x</p>", Tags: []string{"a"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, []RelatedArticle{{Slug: "q", Title: "Q"}}, ArticleMetaOverrides{IsPage: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"vayu-article-meta", "vayu-author-box", "vayu-related"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("page render should not contain %q", unwanted)
		}
	}
}

// TestRenderArticleDefaultsUnchanged confirms a post that sets no overrides
// still renders the derived title/canonical exactly as before.
func TestRenderArticleDefaultsUnchanged(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{Title: "Plain", Slug: "plain", Content: "<p>Some body.</p>", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	out, err := RenderArticleWithLayout(art, ArticleLayoutDefault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<title>Plain — example.com</title>") {
		t.Errorf("default title tag missing: %q", out[:200])
	}
	if !strings.Contains(out, `<link rel="canonical" href="https://example.com/plain">`) {
		t.Error("default canonical missing")
	}
}
