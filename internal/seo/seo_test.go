package seo

import (
	"strings"
	"testing"
	"time"
)

func TestExtractDescription(t *testing.T) {
	html := `<p>Hello <strong>world</strong>! This is a <a href="#">test</a> article with some content that goes on and on to make sure we properly truncate at one hundred and sixty characters total.</p>`
	got := ExtractDescription(html)
	if strings.Contains(got, "<") {
		t.Errorf("ExtractDescription returned HTML tags: %q", got)
	}
	if len(got) > 160 {
		t.Errorf("ExtractDescription returned %d chars, want <= 160", len(got))
	}
	if !strings.HasPrefix(got, "Hello world!") {
		t.Errorf("ExtractDescription got %q, want prefix 'Hello world!'", got)
	}
}

func TestExtractFirstImage(t *testing.T) {
	html := `<p>Some text</p><img src="https://example.com/photo.jpg" alt="photo"><img src="https://example.com/other.jpg">`
	got := ExtractFirstImage(html)
	if got != "https://example.com/photo.jpg" {
		t.Errorf("ExtractFirstImage = %q, want %q", got, "https://example.com/photo.jpg")
	}
}

func TestExtractFirstImage_None(t *testing.T) {
	html := `<p>No images here.</p>`
	got := ExtractFirstImage(html)
	if got != "" {
		t.Errorf("ExtractFirstImage_None = %q, want %q", got, "")
	}
}

func TestGenerateSitemap(t *testing.T) {
	entries := []SitemapEntry{
		{Loc: "https://example.com/articles/my-post", LastMod: "2024-01-15", ChangeFreq: "monthly", Priority: "0.8"},
	}
	got := GenerateSitemap(entries)
	if !strings.Contains(got, `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`) {
		t.Error("GenerateSitemap missing xmlns")
	}
	if !strings.Contains(got, "https://example.com/articles/my-post") {
		t.Error("GenerateSitemap missing loc")
	}
	if !strings.Contains(got, "2024-01-15") {
		t.Error("GenerateSitemap missing lastmod")
	}
	if !strings.Contains(got, "<changefreq>monthly</changefreq>") {
		t.Error("GenerateSitemap missing changefreq")
	}
	if !strings.Contains(got, "<priority>0.8</priority>") {
		t.Error("GenerateSitemap missing priority")
	}
}

func TestCompute(t *testing.T) {
	created := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC)
	meta := Compute(
		"My Article",
		"my-article",
		`<p>Hello <img src="https://cdn.example.com/img.jpg"> world</p>`,
		created, updated,
		"example.com",
		"Example Site",
	)
	if meta.Title != "My Article" {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.CanonicalURL != "https://example.com/articles/my-article" {
		t.Errorf("CanonicalURL = %q", meta.CanonicalURL)
	}
	if meta.OGImage != "https://cdn.example.com/img.jpg" {
		t.Errorf("OGImage = %q", meta.OGImage)
	}
	if meta.SiteName != "Example Site" {
		t.Errorf("SiteName = %q", meta.SiteName)
	}
	if meta.DatePublished != "2024-01-15T00:00:00Z" {
		t.Errorf("DatePublished = %q", meta.DatePublished)
	}
	if meta.DateModified != "2024-02-20T00:00:00Z" {
		t.Errorf("DateModified = %q", meta.DateModified)
	}
}
