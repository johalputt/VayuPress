// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A Markdown import stores tags as every write does: comma-separated under the
// tag rules. Stored as JSON, they read back as `["news"` and `"go"]`.
func TestMarkdownImportStoresTagsAsEveryWriteDoes(t *testing.T) {
	openMigratedDB(t)
	dir := t.TempDir()
	md := "---\ntitle: Imported\nslug: imported\ntags: [news, with, Go]\n---\n# Imported\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "imported.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrateMarkdown([]string{"--dir=" + dir}); err != nil {
		t.Fatal(err)
	}
	var tags string
	if err := dbpkg.DB.QueryRow(`SELECT tags FROM articles WHERE slug='imported'`).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if tags != "news,Go" {
		t.Errorf("stored tags %q, want news,Go", tags)
	}
}

// The importers store text as they were given it and offer what reads as
// garbled for review: Markdown, and Ghost and WordPress (insertImported).
func TestImportersOfferGarbledTextAndKeepIt(t *testing.T) {
	openMigratedDB(t)
	dir := t.TempDir()
	md := "---\ntitle: Dash\nslug: md-dash\n---\na \u00e2\u20ac\u201d b\n"
	if err := os.WriteFile(filepath.Join(dir, "dash.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrateMarkdown([]string{"--dir=" + dir}); err != nil {
		t.Fatal(err)
	}
	if err := insertImported([]importedPost{{Title: "Ghost", Slug: "ghost-dash", HTML: "<p>a \u00e2\u20ac\u201d b</p>"}}, false, false); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"md-dash", "ghost-dash"} {
		var content, state string
		if err := dbpkg.DB.QueryRow(`SELECT a.content, COALESCE(o.state,'') FROM articles a LEFT JOIN text_repair_offer o ON o.slug=a.slug WHERE a.slug=?`, slug).Scan(&content, &state); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "a \u00e2\u20ac\u201d b") {
			t.Errorf("%s stored %q, not as sent", slug, content)
		}
		if state != "offered" {
			t.Errorf("%s: offer %q", slug, state)
		}
	}
}

// A draft kept with --skip-drafts=false stays a draft, from every built-in
// importer. articles.status defaults to 'published', so an insert that leaves
// it out makes a private draft public.
func TestImportedDraftsStayDrafts(t *testing.T) {
	openMigratedDB(t)
	dir := t.TempDir()
	md := "---\ntitle: Private\nslug: md-private\ndraft: true\n---\nNot yet.\n"
	if err := os.WriteFile(filepath.Join(dir, "private.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrateMarkdown([]string{"--dir=" + dir, "--skip-drafts=false"}); err != nil {
		t.Fatal(err)
	}
	posts := []importedPost{
		{Title: "Private", Slug: "ghost-private", HTML: "<p>Not yet.</p>", Draft: true},
		{Title: "Public", Slug: "ghost-public", HTML: "<p>Out.</p>"},
	}
	if err := insertImported(posts, false, false); err != nil {
		t.Fatal(err)
	}
	for slug, want := range map[string]string{"md-private": "draft", "ghost-private": "draft", "ghost-public": "published"} {
		var status string
		if err := dbpkg.DB.QueryRow(`SELECT status FROM articles WHERE slug=?`, slug).Scan(&status); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		if status != want {
			t.Errorf("%s stored as %q, want %q", slug, status, want)
		}
	}
}
