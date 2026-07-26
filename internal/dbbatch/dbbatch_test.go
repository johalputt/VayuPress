// SPDX-License-Identifier: Apache-2.0

package dbbatch

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "b.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE t(v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestWriterBatchesAndPersists proves submitted ops are flushed (in batches) and
// that draining happens on shutdown.
func TestWriterBatchesAndPersists(t *testing.T) {
	db := newDB(t)
	w := New(db, 4096, 256, 20*time.Millisecond)
	done := make(chan struct{})
	go w.Run(done)

	const n = 100
	for i := 0; i < n; i++ {
		i := i
		w.Submit(func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO t(v) VALUES(?)`, i)
			return err
		})
	}
	// Stop and drain, then verify everything landed.
	close(done)
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		_ = db.QueryRow(`SELECT COUNT(1) FROM t`).Scan(&got)
		if got == n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != n {
		t.Fatalf("persisted %d rows, want %d", got, n)
	}
	if w.Dropped() != 0 {
		t.Errorf("no ops should have dropped, got %d", w.Dropped())
	}
}

// TestWriterDropsWhenFull proves Submit is non-blocking and sheds (counts) ops
// once the bounded buffer is full — telemetry never blocks a request.
func TestWriterDropsWhenFull(t *testing.T) {
	db := newDB(t)
	w := New(db, 1, 256, time.Hour) // tiny buffer; loop NOT started so nothing drains
	noop := func(ctx context.Context, tx *sql.Tx) error { return nil }
	for i := 0; i < 5; i++ {
		w.Submit(noop)
	}
	if w.Dropped() != 4 { // 1 buffered, 4 shed
		t.Fatalf("dropped = %d, want 4", w.Dropped())
	}
}

// TestNilWriterSafe proves a nil Writer is a safe no-op.
func TestNilWriterSafe(t *testing.T) {
	var w *Writer
	w.Submit(func(ctx context.Context, tx *sql.Tx) error { return nil })
	if w.Dropped() != 0 {
		t.Error("nil writer Dropped should be 0")
	}
}
