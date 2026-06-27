package render

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// TestArticleJSONLDUsesSettings proves the BlogPosting JSON-LD reflects the
// operator's site author + name rather than the old hardcoded values, and that
// the share image is included when present.
func TestArticleJSONLDUsesSettings(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme Press", Author: "Jane Writer"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	art := db.Article{
		Title: "Hello", Slug: "hello", Content: "<p>Body.</p>",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{OGImage: "https://example.com/share.png"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `"@type":"BlogPosting"`) {
		t.Error("expected BlogPosting JSON-LD type")
	}
	if !strings.Contains(out, `"name":"Jane Writer"`) {
		t.Error("author name should come from settings (Jane Writer)")
	}
	if !strings.Contains(out, `"name":"Acme Press"`) {
		t.Error("publisher name should come from settings (Acme Press)")
	}
	if strings.Contains(out, "Ankush Choudhary Johal") || strings.Contains(out, `"name":"VayuPress"`) {
		t.Error("JSON-LD must not contain the old hardcoded author/publisher")
	}
	// html/template JSON-escapes forward slashes (/ → \/), so match loosely.
	if !strings.Contains(out, `"image":`) || !strings.Contains(out, "share.png") {
		t.Error("JSON-LD should include the share image when present")
	}
	if !strings.Contains(out, `"mainEntityOfPage"`) {
		t.Error("JSON-LD should include mainEntityOfPage")
	}
}
