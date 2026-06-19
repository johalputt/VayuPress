package update

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.db")
	content := []byte("SQLite format 3\x00 fake db")
	if err := os.WriteFile(dbPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// also a -wal sidecar
	if err := os.WriteFile(dbPath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "backups")
	archive, err := CreateBackup(dbPath, destDir)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	found := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		found[hdr.Name] = b
	}
	if string(found["data.db"]) != string(content) {
		t.Errorf("db content mismatch: %q", found["data.db"])
	}
	if string(found["data.db-wal"]) != "wal" {
		t.Errorf("wal sidecar missing: %q", found["data.db-wal"])
	}
}

func TestCreateBackupMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBackup(filepath.Join(dir, "nope.db"), filepath.Join(dir, "out")); err == nil {
		t.Error("expected error for missing db")
	}
}
