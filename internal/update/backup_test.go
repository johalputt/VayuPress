// SPDX-License-Identifier: Apache-2.0

package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/pacedio"
)

// walHeavyDB is a live WAL database whose last commits are still only in the
// write-ahead log: the main file alone is missing them, so a byte copy of the
// main file (or a torn file-and-log pair) is told apart from a consistent copy
// by the row count alone. The connection stays open, holding the log.
func walHeavyDB(t *testing.T, path string) (live *sql.DB, rows int) {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_wal_autocheckpoint=0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE articles(id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	for range 300 {
		if _, err := db.Exec(`INSERT INTO articles(body) VALUES (?)`, strings.Repeat("x", 500)); err != nil {
			t.Fatal(err)
		}
	}
	return db, 300
}

// archived reads every member of a .tar.gz.
func archived(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = b
	}
}

type countingPacer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingPacer) Next(ctx context.Context) (int, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return 1, ctx.Err()
}

// The backup taken before an update used to tar the live database file and its
// write-ahead log byte for byte while the site wrote. It is now one consistent
// copy: every committed row, including those still only in the log, and no
// sidecar in the archive to resurrect a mismatched pair from.
func TestThePreUpdateBackupIsOneConsistentCopy(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.db")
	_, want := walHeavyDB(t, dbPath)

	var pages, chunks countingPacer
	archive, err := CreateBackup(context.Background(), dbPath, filepath.Join(dir, "backups"), Pacing{
		Pages:  func() pacedio.Pacer { return &pages },
		Chunks: func() pacedio.Pacer { return &chunks },
	})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	members := archived(t, archive)
	if len(members) != 1 || members["data.db"] == nil {
		names := make([]string, 0, len(members))
		for n := range members {
			names = append(names, n)
		}
		t.Fatalf("the archive holds %v; want the database alone, as data.db", names)
	}
	restored := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(restored, members["data.db"], 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", restored+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := db.QueryRow(`SELECT count(*) FROM articles`).Scan(&got); err != nil || got != want {
		t.Errorf("the backup restores %d rows (%v); want all %d, including those only in the write-ahead log", got, err, want)
	}
	if pages.calls == 0 || chunks.calls == 0 {
		t.Errorf("the copy asked its pacer %d times and the archive %d; both are passes over the database", pages.calls, chunks.calls)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "backups", ".snapshot-*")); len(leftovers) != 0 {
		t.Errorf("the working copy was left behind: %v", leftovers)
	}
}

func TestCreateBackupMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBackup(context.Background(), filepath.Join(dir, "nope.db"), filepath.Join(dir, "out"), Pacing{}); err == nil {
		t.Error("expected error for missing db")
	}
}
