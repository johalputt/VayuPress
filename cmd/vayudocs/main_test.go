package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateStaticDocs builds a tiny docs tree and checks the generator emits
// clean dir-per-doc pages, skips the image-only folders, renders ADRs, and copies
// the shared stylesheet — the contract the GitHub Pages build depends on.
func TestGenerateStaticDocs(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "OPERATIONS.md"), "# Operations\n\nRun the thing.")
	mustWrite(t, filepath.Join(src, "security", "trust-model.md"), "# Trust model\n\nDon't trust input.")
	mustWrite(t, filepath.Join(src, "adr", "ADR-0001-sqlite-first.md"), "# ADR-0001: SQLite first\n\nDecided.")
	mustWrite(t, filepath.Join(src, "adr", "INDEX.md"), "# index (must be skipped)")
	mustWrite(t, filepath.Join(src, "screenshots", "shot.png"), "PNGDATA") // must be skipped

	css := filepath.Join(src, "docs.css")
	mustWrite(t, css, ".doc-body{}")

	out := filepath.Join(t.TempDir(), "docs")
	groups, adrs, err := index(src)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(adrs) != 1 {
		t.Fatalf("want 1 ADR (INDEX.md skipped), got %d", len(adrs))
	}
	if err := generate(out, css, groups, adrs); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Clean dir-per-doc URLs, the ADR, the copied stylesheet.
	for _, f := range []string{
		"index.html",
		"OPERATIONS/index.html",
		"security/trust-model/index.html",
		"adr/index.html",
		"adr/ADR-0001-sqlite-first/index.html",
		"docs.css",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing expected output %s: %v", f, err)
		}
	}
	// The image-only folder must not become a page.
	if _, err := os.Stat(filepath.Join(out, "screenshots")); err == nil {
		t.Errorf("screenshots/ should have been skipped, not rendered")
	}

	// A rendered page must carry the shared shell + link the stylesheet.
	page, _ := os.ReadFile(filepath.Join(out, "OPERATIONS", "index.html"))
	body := string(page)
	for _, want := range []string{`class="doc-body"`, `href="/docs/docs.css"`, `<article class="doc-prose">`, "Run the thing."} {
		if !strings.Contains(body, want) {
			t.Errorf("OPERATIONS page missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Errorf("static docs must not emit inline script")
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
