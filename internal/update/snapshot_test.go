package update

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB creates a SQLite database carrying the minimal VayuPress schema the
// snapshot validator requires, seeded with one article and one setting.
func newTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, checksum TEXT)`,
		`CREATE TABLE articles(id TEXT PRIMARY KEY, title TEXT, slug TEXT, content TEXT)`,
		`CREATE TABLE site_settings(key TEXT PRIMARY KEY, value TEXT, updated_at DATETIME)`,
		`INSERT INTO schema_migrations(version,checksum) VALUES('001','abc')`,
		`INSERT INTO articles(id,title,slug,content) VALUES('1','Hello','hello','hi')`,
		`INSERT INTO site_settings(key,value) VALUES('site.name','My Site')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db
}

func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.db")
	db := newTestDB(t, srcPath)
	defer db.Close()

	// Export to an in-memory buffer.
	var buf bytes.Buffer
	if err := ExportSnapshot(context.Background(), &buf, db, srcPath, dir, "9.9.9"); err != nil {
		t.Fatalf("ExportSnapshot: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty snapshot")
	}

	// Stage a restore against a fresh DB path.
	destPath := filepath.Join(dir, "restored.db")
	manifest, err := StageRestore(context.Background(), &buf, destPath, dir)
	if err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if manifest.AppVersion != "9.9.9" {
		t.Errorf("manifest app version = %q, want 9.9.9", manifest.AppVersion)
	}
	if manifest.SettingsCount != 1 {
		t.Errorf("settings count = %d, want 1", manifest.SettingsCount)
	}
	if _, err := os.Stat(destPath + PendingRestoreSuffix); err != nil {
		t.Fatalf("pending restore not staged: %v", err)
	}

	// Apply the pending restore (no live DB at destPath yet).
	applied, err := ApplyPendingRestore(destPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("ApplyPendingRestore: %v", err)
	}
	if !applied {
		t.Fatal("expected a restore to be applied")
	}

	// The restored DB must carry the original data.
	rdb, err := sql.Open("sqlite3", destPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer rdb.Close()
	var name string
	if err := rdb.QueryRow(`SELECT value FROM site_settings WHERE key='site.name'`).Scan(&name); err != nil {
		t.Fatalf("read restored setting: %v", err)
	}
	if name != "My Site" {
		t.Errorf("restored setting = %q, want %q", name, "My Site")
	}
}

func TestStageRestoreRejectsNonSnapshot(t *testing.T) {
	dir := t.TempDir()
	// Random gzip-of-garbage is not a valid snapshot archive.
	junk := bytes.NewBufferString("this is not a tar.gz at all")
	if _, err := StageRestore(context.Background(), junk, filepath.Join(dir, "x.db"), dir); err == nil {
		t.Error("expected error for invalid snapshot input")
	}
}

func TestApplyPendingRestoreNoop(t *testing.T) {
	dir := t.TempDir()
	applied, err := ApplyPendingRestore(filepath.Join(dir, "absent.db"), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("expected no restore when nothing is staged")
	}
}
