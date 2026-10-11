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
