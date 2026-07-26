// SPDX-License-Identifier: Apache-2.0

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCreateBackupPrunesOldArchives proves that creating a backup prunes older
// archives for the same database down to the newest N (default 5), so the
// update-backup directory cannot grow without bound as a site is updated
// repeatedly.
func TestCreateBackupPrunesOldArchives(t *testing.T) {
	t.Setenv("VAYU_UPDATE_BACKUP_KEEP", "3")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vayupress.db")
	if err := os.WriteFile(dbPath, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed five older archives for this DB (distinct, increasing-age mod times).
	base := filepath.Base(dbPath)
	for i := 0; i < 5; i++ {
		p := filepath.Join(destDir, fmt.Sprintf("backup-%s-old%d.tar.gz", base, i))
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-time.Duration(100-i) * time.Minute) // all older than "now"
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	// A fresh backup (newest) should trigger pruning down to keep=3.
	newest, err := CreateBackup(dbPath, destDir)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(destDir, "backup-*.tar.gz"))
	if len(matches) != 3 {
		t.Fatalf("retention: want 3 archives kept, got %d: %v", len(matches), matches)
	}
	// The just-created archive must survive pruning.
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("newest backup was pruned: %v", err)
	}
}

// TestPruneBackupsScopedToBase proves pruning never deletes another database's
// backups (the glob is per-base).
func TestPruneBackupsScopedToBase(t *testing.T) {
	dir := t.TempDir()
	// Two databases' backups share a directory.
	for _, name := range []string{
		"backup-vayupress.db-1.tar.gz", "backup-vayupress.db-2.tar.gz",
		"backup-vayupress.db-3.tar.gz", "backup-mail.db-1.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pruneBackups(dir, "backup-vayupress.db-*.tar.gz", 1); err != nil {
		t.Fatal(err)
	}
	// The unrelated mail.db backup must remain.
	if _, err := os.Stat(filepath.Join(dir, "backup-mail.db-1.tar.gz")); err != nil {
		t.Errorf("pruning deleted an unrelated database's backup: %v", err)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "backup-vayupress.db-*.tar.gz"))
	if len(kept) != 1 {
		t.Errorf("want 1 vayupress.db backup kept, got %d", len(kept))
	}
}
