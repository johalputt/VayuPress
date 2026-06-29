package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedStaticHasAdminAssets proves the admin CSS/JS the panel serves are
// actually compiled into the binary, so a one-click self-update ships them.
func TestEmbeddedStaticHasAdminAssets(t *testing.T) {
	for _, p := range []string{"css/admin-os.css", "js/admin-os.js", "js/admin-os-update.js"} {
		if _, err := fs.Stat(embeddedStaticFS, p); err != nil {
			t.Errorf("embedded static is missing %q: %v", p, err)
		}
	}
}

// TestSyncEmbeddedStaticWritesAndSkips proves the boot-time sync writes the
// embedded admin assets into an empty STATIC_DIR and then leaves an unchanged
// file untouched on the next pass.
func TestSyncEmbeddedStaticWritesAndSkips(t *testing.T) {
	dir := t.TempDir()
	syncEmbeddedStatic(dir)

	css := filepath.Join(dir, "css", "admin-os.css")
	fi, err := os.Stat(css)
	if err != nil {
		t.Fatalf("admin-os.css not written: %v", err)
	}
	want, err := fs.ReadFile(embeddedStaticFS, "css/admin-os.css")
	if err != nil {
		t.Fatalf("read embedded css: %v", err)
	}
	got, err := os.ReadFile(css)
	if err != nil {
		t.Fatalf("read written css: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("written admin-os.css does not match the embedded copy")
	}

	// Second sync must be a no-op for the unchanged file (mtime preserved).
	before := fi.ModTime()
	syncEmbeddedStatic(dir)
	fi2, err := os.Stat(css)
	if err != nil {
		t.Fatalf("stat after second sync: %v", err)
	}
	if !fi2.ModTime().Equal(before) {
		t.Error("unchanged file should not be rewritten on the second sync")
	}
}
