package hugoparse_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johalputt/hugo2vayu/internal/hugoparse"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestYAMLFrontmatter(t *testing.T) {
	content := `---
title: "Hello World"
slug: hello-world
date: "2024-03-15"
tags:
  - go
  - web
---
# Hello World

Some content here.
`
	path := writeTempFile(t, "hello-world.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Title != "Hello World" {
		t.Errorf("Title = %q, want %q", doc.Title, "Hello World")
	}
	if doc.Slug != "hello-world" {
		t.Errorf("Slug = %q, want %q", doc.Slug, "hello-world")
	}
	if !doc.Date.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v, want 2024-03-15", doc.Date)
	}
	if len(doc.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 tags", doc.Tags)
	}
}

func TestTOMLFrontmatter(t *testing.T) {
	content := `+++
title = "TOML Post"
date = "2024-06-01"
draft = true
tags = ["rust", "systems"]
+++

Body content.
`
	path := writeTempFile(t, "toml-post.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Title != "TOML Post" {
		t.Errorf("Title = %q, want %q", doc.Title, "TOML Post")
	}
	if !doc.Draft {
		t.Errorf("Draft = false, want true")
	}
	if len(doc.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 tags", doc.Tags)
	}
	if !doc.Date.Equal(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v, want 2024-06-01", doc.Date)
	}
}

func TestSlugFromDatedFilename(t *testing.T) {
	content := `---
title: "My Post"
date: "2024-01-15"
---
Content.
`
	path := writeTempFile(t, "2024-01-15-my-post.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Slug != "my-post" {
		t.Errorf("Slug = %q, want %q", doc.Slug, "my-post")
	}
}

func TestCategoriesMergedIntoTags(t *testing.T) {
	content := `---
title: "Mixed"
date: "2024-01-01"
tags:
  - go
  - web
categories:
  - programming
  - go
---
Content.
`
	path := writeTempFile(t, "mixed.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// go (from tags), web (from tags), programming (from categories); "go" from categories is duplicate
	if len(doc.Tags) != 3 {
		t.Errorf("Tags = %v (len=%d), want 3 deduplicated tags", doc.Tags, len(doc.Tags))
	}
}

func TestDraftDetection(t *testing.T) {
	content := `---
title: "Draft Post"
date: "2024-01-01"
draft: true
---
Content.
`
	path := writeTempFile(t, "draft-post.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Draft {
		t.Errorf("Draft = false, want true")
	}
}

func TestTOMLArrayTags(t *testing.T) {
	content := `+++
title = "API Guide"
date = "2024-05-10"
tags = ["go", "web", "api"]
+++

Content here.
`
	path := writeTempFile(t, "api-guide.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Tags) != 3 {
		t.Errorf("Tags = %v, want 3 tags", doc.Tags)
	}
	tagMap := make(map[string]bool)
	for _, tag := range doc.Tags {
		tagMap[tag] = true
	}
	for _, want := range []string{"go", "web", "api"} {
		if !tagMap[want] {
			t.Errorf("missing tag %q in %v", want, doc.Tags)
		}
	}
}

func TestSlugFromSimpleFilename(t *testing.T) {
	content := `---
title: "Simple"
date: "2024-01-01"
---
Content.
`
	path := writeTempFile(t, "my-post.md", content)
	doc, err := hugoparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Slug != "my-post" {
		t.Errorf("Slug = %q, want %q", doc.Slug, "my-post")
	}
}
