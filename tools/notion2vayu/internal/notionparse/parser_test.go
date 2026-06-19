package notionparse

import (
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Hello_World", "hello-world"},
		{"My Article Title", "my-article-title"},
		{"  leading trailing  ", "leading-trailing"},
		{"special!@#chars", "specialchars"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"", ""},
		{"123 Numbers", "123-numbers"},
		{"UPPERCASE", "uppercase"},
	}
	for _, c := range cases {
		got := Slugify(c.input)
		if got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseHTML_BasicTitle(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head><title>My Test Article | Notion</title></head>
<body>
<article class="page sans">
<h1>My Test Article</h1>
<p>Some content here.</p>
</article>
</body>
</html>`

	mtime := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	a, err := ParseHTML(html, "/some/path/my-test-article.html", mtime)
	if err != nil {
		t.Fatalf("ParseHTML returned error: %v", err)
	}
	if a.Title != "My Test Article" {
		t.Errorf("Title = %q, want %q", a.Title, "My Test Article")
	}
	if a.Slug != "my-test-article" {
		t.Errorf("Slug = %q, want %q", a.Slug, "my-test-article")
	}
	if !strings.Contains(a.Content, "Some content here") {
		t.Errorf("Content should contain article body, got: %q", a.Content)
	}
	if !a.CreatedAt.Equal(mtime) {
		t.Errorf("CreatedAt = %v, want %v", a.CreatedAt, mtime)
	}
}

func TestParseHTML_WithTags(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head><title>Tagged Article | Notion</title></head>
<body>
<article class="page sans">
<h1>Tagged Article</h1>
<table class="properties">
<tbody>
<tr><th>Multi-select</th><td><span>golang</span><span>tutorial</span></td></tr>
</tbody>
</table>
<p>Content with tags.</p>
</article>
</body>
</html>`

	mtime := time.Now()
	a, err := ParseHTML(html, "/path/tagged-article.html", mtime)
	if err != nil {
		t.Fatalf("ParseHTML returned error: %v", err)
	}
	if a.Title != "Tagged Article" {
		t.Errorf("Title = %q, want %q", a.Title, "Tagged Article")
	}
	if len(a.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 tags", a.Tags)
	} else {
		if a.Tags[0] != "golang" {
			t.Errorf("Tags[0] = %q, want %q", a.Tags[0], "golang")
		}
		if a.Tags[1] != "tutorial" {
			t.Errorf("Tags[1] = %q, want %q", a.Tags[1], "tutorial")
		}
	}
}

func TestParseHTML_NoTitle(t *testing.T) {
	html := `<html><body><p>No title here</p></body></html>`
	_, err := ParseHTML(html, "/path/no-title.html", time.Now())
	if err == nil {
		t.Error("Expected error for HTML without title, got nil")
	}
}

func TestParseHTML_UUIDSuffixStripping(t *testing.T) {
	html := `<html><head><title>My Page | Notion</title></head><body><article>content</article></body></html>`
	// Filename with UUID suffix (32 hex chars preceded by space)
	filePath := "/path/My Page abcdef1234567890abcdef1234567890.html"
	mtime := time.Now()
	a, err := ParseHTML(html, filePath, mtime)
	if err != nil {
		t.Fatalf("ParseHTML returned error: %v", err)
	}
	// UUID suffix should be stripped, resulting in slug from "My Page"
	if strings.Contains(a.Slug, "abcdef") {
		t.Errorf("Slug %q should not contain UUID suffix", a.Slug)
	}
	if a.Slug != "my-page" {
		t.Errorf("Slug = %q, want %q", a.Slug, "my-page")
	}
}

func TestParseHTML_H1FallbackTitle(t *testing.T) {
	html := `<html><body><h1>Fallback Title</h1><p>Content</p></body></html>`
	a, err := ParseHTML(html, "/path/fallback.html", time.Now())
	if err != nil {
		t.Fatalf("ParseHTML returned error: %v", err)
	}
	if a.Title != "Fallback Title" {
		t.Errorf("Title = %q, want %q", a.Title, "Fallback Title")
	}
}
