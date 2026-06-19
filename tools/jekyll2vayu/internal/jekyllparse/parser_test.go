package jekyllparse_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johalputt/jekyll2vayu/internal/jekyllparse"
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

func TestDateAndSlugFromFilename(t *testing.T) {
	path := writeTemp(t, "2024-01-15-my-awesome-post.md", `---
title: My Awesome Post
---
Body text.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Slug != "my-awesome-post" {
		t.Errorf("slug = %q, want %q", doc.Slug, "my-awesome-post")
	}
	want := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	if !doc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", doc.Date, want)
	}
}

func TestYAMLFrontmatterOverride(t *testing.T) {
	path := writeTemp(t, "2024-01-15-old-title.md", `---
title: Override Title
date: 2024-03-20
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Title != "Override Title" {
		t.Errorf("title = %q, want %q", doc.Title, "Override Title")
	}
	want := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)
	if !doc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", doc.Date, want)
	}
}

func TestCategoriesMergedIntoTags(t *testing.T) {
	path := writeTemp(t, "2024-02-01-merged.md", `---
title: Merged
tags:
  - go
  - programming
categories:
  - programming
  - tutorial
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// Should have go, programming, tutorial (programming deduplicated)
	tagSet := make(map[string]bool)
	for _, tag := range doc.Tags {
		tagSet[tag] = true
	}
	for _, expected := range []string{"go", "programming", "tutorial"} {
		if !tagSet[expected] {
			t.Errorf("missing tag %q in %v", expected, doc.Tags)
		}
	}
	if len(doc.Tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(doc.Tags), doc.Tags)
	}
}

func TestPublishedFalse(t *testing.T) {
	path := writeTemp(t, "2024-02-10-draft-post.md", `---
title: Draft Post
published: false
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !doc.Draft {
		t.Errorf("expected Draft=true for published: false")
	}
}

func TestSlugCleanup(t *testing.T) {
	path := writeTemp(t, "2024-03-10-hello---world.md", `---
title: Hello World
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Slug != "hello-world" {
		t.Errorf("slug = %q, want %q", doc.Slug, "hello-world")
	}
}

func TestNoDraftPrefix(t *testing.T) {
	path := writeTemp(t, "my-draft-post.md", `---
title: My Draft Post
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if doc.Slug != "my-draft-post" {
		t.Errorf("slug = %q, want %q", doc.Slug, "my-draft-post")
	}
}

func TestTagsOnly(t *testing.T) {
	path := writeTemp(t, "2024-04-01-tags-only.md", `---
title: Tags Only
tags:
  - alpha
  - beta
---
Body.
`)
	doc, err := jekyllparse.Parse(path)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(doc.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(doc.Tags), doc.Tags)
	}
	if doc.Tags[0] != "alpha" || doc.Tags[1] != "beta" {
		t.Errorf("tags = %v, want [alpha beta]", doc.Tags)
	}
}
