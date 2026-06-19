package mediumparse

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"Go is great!", "go-is-great"},
		{"  spaces  ", "spaces"},
		{"multiple---hyphens", "multiple-hyphens"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFilenameToSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2024-01-15_my-cool-post_abc123.html", "my-cool-post"},
		{"plain-post.html", "plain-post"},
		{"2024-01-15_no-hash.html", "no-hash"},
	}
	for _, c := range cases {
		if got := filenameToSlug(c.in); got != c.want {
			t.Errorf("filenameToSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseHTML_Basic(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>My Post Title</title></head><body>
<h1>My Post Title</h1>
<time datetime="2024-03-10T12:00:00Z">March 10, 2024</time>
<a rel="tag">golang</a>
<a rel="tag">programming</a>
<article><section><p>Hello world content here.</p></section></article>
</body></html>`

	a, err := ParseHTML(html, "2024-03-10_my-post_abc123.html")
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if a.Title != "My Post Title" {
		t.Errorf("title = %q", a.Title)
	}
	if a.Slug != "my-post" {
		t.Errorf("slug = %q", a.Slug)
	}
	if a.CreatedAt.Year() != 2024 {
		t.Errorf("year = %d", a.CreatedAt.Year())
	}
	if len(a.Tags) != 2 {
		t.Errorf("tags = %v", a.Tags)
	}
	if !contains(a.Content, "Hello world content here.") {
		t.Errorf("content missing body: %q", a.Content)
	}
}

func TestParseHTML_SectionBody(t *testing.T) {
	html := `<html><head><title>Section Test</title></head><body>
<h1>Section Test</h1>
<time datetime="2024-01-01T00:00:00Z">Jan 1</time>
<section><p>Section body.</p></section>
</body></html>`

	a, err := ParseHTML(html, "section-test.html")
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if !contains(a.Content, "Section body.") {
		t.Errorf("content = %q", a.Content)
	}
}

func TestParseHTML_NoBody(t *testing.T) {
	html := `<html><body><h1>No Body</h1></body></html>`
	_, err := ParseHTML(html, "no-body.html")
	if err == nil {
		t.Error("expected error for missing body")
	}
}

func TestParseHTML_DraftDetection(t *testing.T) {
	html := `<html><body><h1>Draft</h1><div class="draft"><section><p>draft</p></section></div></body></html>`
	a, _ := ParseHTML(html, "draft-post.html")
	if !a.IsDraft {
		t.Error("expected IsDraft=true")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
