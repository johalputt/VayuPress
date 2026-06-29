package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestManagedFileByPathAuthorises proves the download/delete authorisation
// gate: only files actually enumerated as managed artefacts resolve, while the
// live DB, traversal attempts and arbitrary system files are rejected.
func TestManagedFileByPathAuthorises(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vayupress.db")
	cacheDir := filepath.Join(dir, "cache")
	tmpDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(filepath.Join(cacheDir, "update-backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Point config at the temp layout.
	origDB, origCache, origTmp, origMedia := config.Cfg.DBPath, config.Cfg.CacheDir, config.Cfg.TmpDir, config.Cfg.MediaDir
	t.Setenv("VAYU_LOG_DIR", filepath.Join(dir, "logs"))    // non-existent → skipped
	t.Setenv("VAYU_BACKUP_DIR", filepath.Join(dir, "bkup")) // non-existent → skipped
	config.Cfg.DBPath = dbPath
	config.Cfg.CacheDir = cacheDir
	config.Cfg.TmpDir = tmpDir
	config.Cfg.MediaDir = filepath.Join(dir, "media")
	t.Cleanup(func() {
		config.Cfg.DBPath, config.Cfg.CacheDir, config.Cfg.TmpDir, config.Cfg.MediaDir = origDB, origCache, origTmp, origMedia
	})

	// Create the live DB (+WAL) and a legitimate backup.
	mustWrite(t, dbPath, "live")
	mustWrite(t, dbPath+"-wal", "wal")
	backup := filepath.Join(cacheDir, "update-backups", "pre-update-123.db")
	mustWrite(t, backup, "backup")

	// The backup is a managed artefact.
	if _, ok := managedFileByPath(backup); !ok {
		t.Errorf("expected backup %q to be managed", backup)
	}
	// The live DB and WAL must NEVER be managed (no download/delete).
	if _, ok := managedFileByPath(dbPath); ok {
		t.Error("live DB must not be a managed file")
	}
	if _, ok := managedFileByPath(dbPath + "-wal"); ok {
		t.Error("live WAL must not be a managed file")
	}
	// Arbitrary system files and traversal are rejected.
	if _, ok := managedFileByPath("/etc/passwd"); ok {
		t.Error("/etc/passwd must not be managed")
	}
	if _, ok := managedFileByPath(filepath.Join(cacheDir, "update-backups", "..", "..", "vayupress.db")); ok {
		t.Error("traversal to the live DB must not resolve as managed")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KiB",
		1536:       "1.5 KiB",
		1048576:    "1.0 MiB",
		1073741824: "1.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
