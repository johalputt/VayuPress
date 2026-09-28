// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
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

// The Storage page opens on how much room is left. Each case trips one rule
// and asserts its own sentence; disk outranks memory because a full disk stops
// the site where full memory slows it.
func TestTheStorageSentenceSaysWhatIsRunningOut(t *testing.T) {
	const gb = 1 << 30
	room := sysStats{DiskTotal: 100 * gb, DiskUsed: 40 * gb, DiskFree: 60 * gb, MemTotal: 8 * gb, MemUsed: 2 * gb}
	for _, c := range []struct {
		name  string
		trip  func(*sysStats)
		tone  string
		state string
	}{
		{"room", func(*sysStats) {}, "ok", "60.0 GiB free on the disk"},
		{"disk full", func(s *sysStats) { s.DiskUsed, s.DiskFree = 95*gb, 5*gb }, "danger", "The disk is 95% full: 5.0 GiB left"},
		{"memory full", func(s *sysStats) { s.MemUsed = 15 * gb / 2 }, "warn", "Memory is 93% used"},
		{"disk filling", func(s *sysStats) { s.DiskUsed, s.DiskFree = 80*gb, 20*gb }, "warn", "20.0 GiB left on the disk, 20% of it"},
		{"disk over memory", func(s *sysStats) { s.DiskUsed, s.DiskFree, s.MemUsed = 95*gb, 5*gb, 15*gb/2 }, "danger", "The disk is 95% full"},
	} {
		st := room
		c.trip(&st)
		if tone, state := storageState(st); tone != c.tone || !strings.HasPrefix(state, c.state) {
			t.Errorf("%s: got (%s) %q, want (%s) %q…", c.name, tone, state, c.tone, c.state)
		}
	}
}
