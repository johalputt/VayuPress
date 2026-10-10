// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
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
