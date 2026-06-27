package render

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// TestContactMarkerInjectsWidget proves the [[contact-form]] marker is stripped
// from the visible prose and replaced by the CSP-safe widget container + loader,
// while a page without the marker gets neither.
func TestContactMarkerInjectsWidget(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	withMarker := db.Article{
		Title: "Contact", Slug: "contact",
		Content:   "<p>Reach us.</p>[[contact-form]]",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out, err := RenderArticleWithMeta(withMarker, ArticleLayoutDefault, nil, ArticleMetaOverrides{IsPage: true})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "[[contact-form]]") {
		t.Error("marker text must be stripped from the rendered page")
	}
	if !strings.Contains(out, `id="vayu-contact"`) {
		t.Error("contact widget container must be injected when the marker is present")
	}
	if !strings.Contains(out, "/static/js/contact.js") {
		t.Error("contact loader script must be linked when the marker is present")
	}

	plain := db.Article{
		Title: "About", Slug: "about", Content: "<p>Just prose.</p>",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out2, err := RenderArticleWithMeta(plain, ArticleLayoutDefault, nil, ArticleMetaOverrides{IsPage: true})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out2, `id="vayu-contact"`) || strings.Contains(out2, "/static/js/contact.js") {
		t.Error("a page without the marker must not get the contact widget")
	}
}
