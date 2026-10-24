// SPDX-License-Identifier: Apache-2.0

package dbbatch

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
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

// refusing makes the database roll back any transaction that inserts into t
// while refuse holds 1: a database that refuses a batch whole, as a failing
// disk or a lock held past the deadline does, until it is told otherwise.
func refusing(t *testing.T, db *sql.DB) func(on bool) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE refuse(on_ INTEGER)`, `INSERT INTO refuse VALUES(0)`,
		`CREATE TRIGGER refuse_t BEFORE INSERT ON t WHEN (SELECT on_ FROM refuse)=1 BEGIN SELECT RAISE(ROLLBACK, 'refused'); END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return func(on bool) {
		v := 0
		if on {
			v = 1
		}
		if _, err := db.Exec(`UPDATE refuse SET on_=?`, v); err != nil {
			t.Fatal(err)
		}
	}
}

func insert(v int) Op {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO t(v) VALUES(?)`, v)
		return err
	}
}

func rows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// run starts w and returns a func that stops it and waits for Run to return.
func run(w *Writer) (stop func()) {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() { w.Run(done); close(finished) }()
	return func() { close(done); <-finished }
}

// A batch the database refuses is kept and written once it is taken: a
// refusal shorter than MaxAttempts flushes loses nothing. The hold check runs
// once before each try, so it counts the tries and ends the refusal after
// the first.
func TestARefusedBatchIsTriedAgain(t *testing.T) {
	db := newDB(t)
	refuse := refusing(t, db)
	refuse(true)
	w := New(db, 64, 16, 5*time.Millisecond)
	var tries atomic.Int64
	w.HoldWhile(func() bool {
		if tries.Add(1) == 2 {
			refuse(false)
		}
		return false
	})
	for i := 0; i < 5; i++ {
		w.Submit(insert(i))
	}
	stop := run(w)
	eventually(t, "the kept batch was never written", func() bool { return rows(t, db) == 5 })
	stop()
	if w.Lost() != 0 {
		t.Errorf("%d lost, want none: the batch was written on its second try", w.Lost())
	}
}

// A batch refused MaxAttempts times in a row is discarded and counted, so a
// row the database will never take cannot hold the rows behind it for ever;
// before the last try nothing is counted lost.
func TestABatchRefusedEveryTimeIsCountedLost(t *testing.T) {
	db := newDB(t)
	refusing(t, db)(true)
	w := New(db, 64, 16, 5*time.Millisecond)
	var tries atomic.Int64
	lostBefore := make([]int64, MaxAttempts+1)
	w.HoldWhile(func() bool {
		if n := tries.Add(1); n <= MaxAttempts {
			lostBefore[n] = w.Lost()
		}
		return false
	})
	for i := 0; i < 5; i++ {
		w.Submit(insert(i))
	}
	stop := run(w)
	eventually(t, "the refused batch was never counted lost", func() bool { return w.Lost() == 5 })
	stop()
	for n := 1; n <= MaxAttempts; n++ {
		if lostBefore[n] != 0 {
			t.Errorf("before try %d, %d already counted lost", n, lostBefore[n])
		}
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("%d rows written through a refusing database", got)
	}
}

// While the install refuses writes the batch is held, not written, and
// nothing is lost; released, it is written.
func TestAHeldBatchWaitsForTheRelease(t *testing.T) {
	db := newDB(t)
	w := New(db, 64, 16, 5*time.Millisecond)
	var held atomic.Bool
	held.Store(true)
	var checks atomic.Int64
	w.HoldWhile(func() bool { checks.Add(1); return held.Load() })
	for i := 0; i < 5; i++ {
		w.Submit(insert(i))
	}
	stop := run(w)
	defer stop()
	eventually(t, "the hold was never asked", func() bool { return checks.Load() >= MaxAttempts+2 })
	if got := rows(t, db); got != 0 || w.Lost() != 0 {
		t.Fatalf("held: %d rows written, %d lost; want none of either", got, w.Lost())
	}
	held.Store(false)
	eventually(t, "the released batch was never written", func() bool { return rows(t, db) == 5 })
}

// At shutdown what is still held is counted lost, not dropped in silence.
func TestWhatIsHeldAtShutdownIsCountedLost(t *testing.T) {
	db := newDB(t)
	w := New(db, 64, 4, time.Hour)
	w.HoldWhile(func() bool { return true })
	for i := 0; i < 10; i++ {
		w.Submit(insert(i))
	}
	run(w)()
	if w.Lost() != 10 || rows(t, db) != 0 {
		t.Errorf("held at shutdown: %d lost, %d written; want 10 and 0", w.Lost(), rows(t, db))
	}
}

// An op that fails inside a batch that is written is counted lost; the rest
// of its batch is written.
func TestAFailedOpIsCountedLost(t *testing.T) {
	db := newDB(t)
	w := New(db, 64, 16, time.Hour)
	w.Submit(insert(1))
	w.Submit(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO no_such_table VALUES(1)`)
		return err
	})
	w.Submit(func(context.Context, *sql.Tx) error { panic("op") })
	w.Submit(insert(2))
	run(w)()
	if w.Lost() != 2 || rows(t, db) != 2 {
		t.Errorf("%d lost, %d written; want the two failed ops lost and the two inserts written", w.Lost(), rows(t, db))
	}
}

// A batch that waits to be written is not grown while it waits: ops back up
// into the bounded buffer, where an overflow is dropped and counted, never
// into memory without a bound.
func TestAWaitingBatchDoesNotGrow(t *testing.T) {
	db := newDB(t)
	refusing(t, db)(true)
	w := New(db, 4, 2, time.Hour)
	var tries atomic.Int64
	w.HoldWhile(func() bool { tries.Add(1); return false })
	for i := 0; i < 4; i++ {
		w.Submit(insert(i))
	}
	stop := run(w)
	defer stop()
	eventually(t, "the full batch was never tried", func() bool { return tries.Load() >= 1 })
	before := w.Dropped()
	for i := 0; i < 5; i++ {
		w.Submit(insert(10 + i))
	}
	if got := w.Dropped() - before; got != 3 {
		t.Errorf("%d of 5 dropped while a batch of 2 waited with 2 in a buffer of 4; want 3 (the buffer took 2)", got)
	}
}
