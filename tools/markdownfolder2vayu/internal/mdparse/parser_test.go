package mdparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestYAMLFrontmatterAllFields(t *testing.T) {
	content := `---
title: My Test Post
slug: my-test-post
date: 2024-01-15
tags:
  - go
  - testing
draft: false
---
Body content here.
`
	path := writeTemp(t, "test.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Title != "My Test Post" {
		t.Errorf("title = %q, want %q", doc.Title, "My Test Post")
	}
	if doc.Slug != "my-test-post" {
		t.Errorf("slug = %q, want %q", doc.Slug, "my-test-post")
	}
	if len(doc.Tags) != 2 || doc.Tags[0] != "go" || doc.Tags[1] != "testing" {
		t.Errorf("tags = %v, want [go testing]", doc.Tags)
	}
	if doc.Draft {
		t.Errorf("draft = true, want false")
	}
	want := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	if !doc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", doc.Date, want)
	}
}

func TestSlugFromFilename(t *testing.T) {
	content := `---
title: Has Title But No Slug
---
Some content.
`
	path := writeTemp(t, "my_awesome post.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Slug != "my-awesome-post" {
		t.Errorf("slug = %q, want %q", doc.Slug, "my-awesome-post")
	}
}

func TestTitleFromH1(t *testing.T) {
	content := `---
slug: no-title-here
---
# Heading From Body

Some paragraph.
`
	path := writeTemp(t, "no-title-here.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Title != "Heading From Body" {
		t.Errorf("title = %q, want %q", doc.Title, "Heading From Body")
	}
}

func TestMarkdownToHTML(t *testing.T) {
	content := `---
title: HTML Test
slug: html-test
---
# My Heading

A paragraph with a [link](https://example.com).
`
	path := writeTemp(t, "html-test.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !strings.Contains(doc.HTML, "<h1") {
		t.Errorf("HTML missing <h1>, got: %s", doc.HTML)
	}
	if !strings.Contains(doc.HTML, "<p>") {
		t.Errorf("HTML missing <p>, got: %s", doc.HTML)
	}
	if !strings.Contains(doc.HTML, `href="https://example.com"`) {
		t.Errorf("HTML missing link href, got: %s", doc.HTML)
	}
}

func TestDraftDetection(t *testing.T) {
	content := `---
title: Draft Article
slug: draft-article
draft: true
---
This is a draft.
`
	path := writeTemp(t, "draft-article.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !doc.Draft {
		t.Errorf("draft = false, want true")
	}
}

func TestDateFromFrontmatter(t *testing.T) {
	content := `---
title: Dated Post
slug: dated-post
date: 2023-06-20T10:30:00Z
---
Content.
`
	path := writeTemp(t, "dated-post.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	want := time.Date(2023, 6, 20, 10, 30, 0, 0, time.UTC)
	if !doc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", doc.Date, want)
	}
}

func TestNoFrontmatterPlainMarkdown(t *testing.T) {
	content := `# Plain Markdown

No frontmatter at all. Just content.
`
	path := writeTemp(t, "plain-markdown.md", content)
	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Title != "Plain Markdown" {
		t.Errorf("title = %q, want %q", doc.Title, "Plain Markdown")
	}
	if doc.Slug != "plain-markdown" {
		t.Errorf("slug = %q, want %q", doc.Slug, "plain-markdown")
	}
	if doc.Draft {
		t.Errorf("draft = true, want false")
	}
	if !strings.Contains(doc.HTML, "<p>") {
		t.Errorf("HTML missing <p>, got: %s", doc.HTML)
	}
	// Date should fall back to file mtime (non-zero).
	if doc.Date.IsZero() {
		t.Errorf("date is zero, expected file mtime")
	}
}
