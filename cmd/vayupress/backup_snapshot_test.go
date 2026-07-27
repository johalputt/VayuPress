// SPDX-License-Identifier: Apache-2.0

package main

// backup_snapshot_test.go — proof that `vayupress backup` captures a CONSISTENT
// database, not whatever bytes happened to be on disk (ADR-0145).
//
// The test is deliberately deterministic rather than timing-based. In WAL mode a
// committed row lives in the `-wal` sidecar until a checkpoint folds it into the
// main database file. So a run that commits rows and does NOT checkpoint leaves
// the main file provably missing them.
//
// That gives an exact discriminator between the three possible implementations:
//
//   - copy the live `.db` and skip the sidecars → the rows are LOST
//   - copy the live `.db` and its `-wal` byte-for-byte → restorable only if the
//     pair happened to be caught in a consistent state, which is the defect
//   - snapshot with `VACUUM INTO` → the WAL is folded in and the rows are there
//
// Asserting the rows survive therefore fails on the old behaviour and passes
// only on the new one.

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// seedWALHeavyDB creates a WAL-mode database whose most recent commits are still
// in the sidecar, and returns the still-open handle plus how many rows were
// written. The handle MUST stay open across the backup: closing the last
// connection makes SQLite checkpoint and delete the `-wal`, which would fold the
// rows into the main file and quietly destroy the very condition under test.
func seedWALHeavyDB(t *testing.T, dbPath string) (*sql.DB, int) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	// Keep SQLite from auto-checkpointing behind our back, so the rows below are
	// guaranteed to still be in the -wal when the backup runs.
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE articles (id INTEGER PRIMARY KEY, slug TEXT, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	const rows = 400
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := tx.Exec(`INSERT INTO articles(slug, body) VALUES(?,?)`,
			fmt.Sprintf("post-%d", i), "body that only exists in the write-ahead log"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Confirm the premise: the sidecar must be non-empty, otherwise the test
	// would pass for the wrong reason.
	if st, err := os.Stat(dbPath + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("premise broken: expected a non-empty -wal, got err=%v", err)
	}
	return db, rows
}

// rowsInFileAlone counts articles in a COPY of the main database file taken
// without its sidecars — i.e. exactly what a naive hot copy would have captured.
func rowsInFileAlone(t *testing.T, dbPath string) int {
	t.Helper()
	lone := filepath.Join(t.TempDir(), "lone.db")
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lone, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", lone+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&n); err != nil {
		return 0 // table not even checkpointed yet
	}
	return n
}

func countRestoredArticles(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("restored database would not answer integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("restored database failed integrity_check: %s", integrity)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&n); err != nil {
		t.Fatalf("restored database is missing the articles table: %v", err)
	}
	return n
}

// TestBackupCapturesConsistentDatabase runs the real CLI end to end.
func TestBackupCapturesConsistentDatabase(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "vayupress.db")
	live, want := seedWALHeavyDB(t, dbPath)
	defer live.Close()

	// A second file so the archive covers more than the database.
	if err := os.MkdirAll(filepath.Join(dataDir, "media"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "media", "hero.bin"), []byte("MEDIA"), 0o640); err != nil {
		t.Fatal(err)
	}

	prevDB := config.Cfg.DBPath
	t.Cleanup(func() { config.Cfg.DBPath = prevDB })
	config.Cfg.DBPath = dbPath
	t.Setenv("VAYU_BACKUP_PASSPHRASE", "a passphrase for the test")

	archive := filepath.Join(t.TempDir(), "out.vpbk")
	var out bytes.Buffer
	if err := runBackupCLI("backup", []string{"-data", dataDir, "-out", archive}, &out); err != nil {
		t.Fatalf("backup: %v\n%s", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("consistent database snapshot")) {
		t.Errorf("backup did not report taking a consistent snapshot:\n%s", out.String())
	}

	// Verify-only must accept it before we trust a restore.
	var vout bytes.Buffer
	if err := runBackupCLI("restore", []string{"-in", archive, "-verify"}, &vout); err != nil {
		t.Fatalf("verify: %v\n%s", err, vout.String())
	}

	dest := filepath.Join(t.TempDir(), "restored")
	var rout bytes.Buffer
	if err := runBackupCLI("restore", []string{"-in", archive, "-dest", dest}, &rout); err != nil {
		t.Fatalf("restore: %v\n%s", err, rout.String())
	}

	// The discriminator. The live main file on its own is missing rows that are
	// still in the write-ahead log, so if this is not strictly less than `want`
	// the test has stopped proving anything and must be fixed rather than
	// trusted.
	lone := rowsInFileAlone(t, dbPath)
	if lone >= want {
		t.Fatalf("premise broken: the main database file alone already holds %d/%d rows, so this test cannot distinguish a snapshot from a hot copy", lone, want)
	}

	// The payload assertion: every committed row is present, including the ones
	// that existed ONLY in the write-ahead log when the backup ran.
	if got := countRestoredArticles(t, filepath.Join(dest, "vayupress.db")); got != want {
		t.Errorf("restored database has %d rows, want %d (the main file alone had %d) — commits still in the WAL were not captured", got, want, lone)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "media", "hero.bin")); err != nil || string(b) != "MEDIA" {
		t.Errorf("non-database files were not restored: %q err=%v", b, err)
	}
	// The sidecars belong to a checkpoint state the snapshot already folded in;
	// restoring them alongside a vacuumed database is how you resurrect a
	// mismatched pair.
	for _, side := range []string{"vayupress.db-wal", "vayupress.db-shm"} {
		if _, err := os.Stat(filepath.Join(dest, side)); err == nil {
			t.Errorf("%s was archived — a snapshot must not carry stale sidecars", side)
		}
	}
}

// TestBackupRefusesTamperedArchiveBeforeTouchingDest confirms the CLI inherits
// the staged-restore guarantee: a corrupted archive must not leave the operator
// with a half-replaced data directory.
func TestBackupRefusesTamperedArchiveBeforeTouchingDest(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "vayupress.db")
	live, _ := seedWALHeavyDB(t, dbPath)
	defer live.Close()

	prevDB := config.Cfg.DBPath
	t.Cleanup(func() { config.Cfg.DBPath = prevDB })
	config.Cfg.DBPath = dbPath
	t.Setenv("VAYU_BACKUP_PASSPHRASE", "pw")

	archive := filepath.Join(t.TempDir(), "out.vpbk")
	if err := runBackupCLI("backup", []string{"-data", dataDir, "-out", archive}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	raw = raw[:len(raw)-64] // a cut-short upload
	if err := os.WriteFile(archive, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	canary := filepath.Join(dest, "live.txt")
	if err := os.WriteFile(canary, []byte("LIVE"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := runBackupCLI("restore", []string{"-in", archive, "-dest", dest}, &bytes.Buffer{}); err == nil {
		t.Fatal("a truncated archive restored successfully")
	}
	if got, err := os.ReadFile(canary); err != nil || string(got) != "LIVE" {
		t.Fatalf("a failed restore damaged the destination: %q err=%v", got, err)
	}
	if err := runBackupCLI("restore", []string{"-in", archive, "-verify"}, &bytes.Buffer{}); err == nil {
		t.Fatal("verify accepted a truncated archive")
	}
}
