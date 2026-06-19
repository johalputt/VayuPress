package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func createTestDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE articles (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		slug TEXT UNIQUE NOT NULL,
		content TEXT NOT NULL,
		tags TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	_, err = db.Exec(`INSERT INTO articles VALUES ('1','Test','test','<p>Hello</p>','tag1','2024-01-01 00:00:00','2024-01-01 00:00:00')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	return dbPath
}

func TestBackupCreatesValidArchive(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir)
	outPath := filepath.Join(dir, "backup.tar.gz")

	result, err := Create(Options{
		DBPath:   dbPath,
		OutPath:  outPath,
		Compress: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result != outPath {
		t.Errorf("expected out path %q, got %q", outPath, result)
	}

	// Check file exists and is non-empty.
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() == 0 {
		t.Error("archive is empty")
	}

	// Verify the archive.
	if err := Verify(outPath); err != nil {
		t.Errorf("Verify: %v", err)
	}

	// List contents.
	manifest, err := List(outPath)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if manifest.ArticleCount != 1 {
		t.Errorf("expected article count 1, got %d", manifest.ArticleCount)
	}
	if len(manifest.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(manifest.Files))
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir)
	outPath := filepath.Join(dir, "backup.tar.gz")

	if _, err := Create(Options{
		DBPath:   dbPath,
		OutPath:  outPath,
		Compress: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read archive, flip a byte in the middle.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	// Flip byte somewhere in the middle of the payload (past gzip header).
	mid := len(data) / 2
	data[mid] ^= 0xFF
	corruptPath := filepath.Join(dir, "corrupt.tar.gz")
	if err := os.WriteFile(corruptPath, data, 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	// Verify should fail (either can't parse or checksum mismatch).
	if err := Verify(corruptPath); err == nil {
		t.Error("expected error for corrupt archive, got nil")
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir)
	outPath := filepath.Join(dir, "backup.tar.gz")

	if _, err := Create(Options{
		DBPath:   dbPath,
		OutPath:  outPath,
		Compress: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	restorePath := filepath.Join(dir, "restored.db")
	if err := Restore(RestoreOptions{
		BackupPath: outPath,
		DBPath:     restorePath,
		Force:      false,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Open restored DB and check article count.
	db, err := sql.Open("sqlite3", restorePath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&count); err != nil {
		t.Fatalf("count articles: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 article, got %d", count)
	}
}

func TestRestoreForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir)
	outPath := filepath.Join(dir, "backup.tar.gz")

	if _, err := Create(Options{
		DBPath:   dbPath,
		OutPath:  outPath,
		Compress: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	restorePath := filepath.Join(dir, "restored.db")
	// First restore.
	if err := Restore(RestoreOptions{BackupPath: outPath, DBPath: restorePath}); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	// Second restore without force should fail.
	if err := Restore(RestoreOptions{BackupPath: outPath, DBPath: restorePath}); err == nil {
		t.Error("expected error restoring without force, got nil")
	}
	// With force should succeed.
	if err := Restore(RestoreOptions{BackupPath: outPath, DBPath: restorePath, Force: true}); err != nil {
		t.Errorf("restore with force: %v", err)
	}
}
