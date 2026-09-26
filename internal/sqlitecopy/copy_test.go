// SPDX-License-Identifier: Apache-2.0

package sqlitecopy

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedPacer lets every step copy the same number of pages, after a pause long
// enough for a concurrent writer to commit between any two steps.
type fixedPacer struct {
	pages int
	pause time.Duration
	calls atomic.Int32
}

func (f *fixedPacer) Next(ctx context.Context) (int, error) {
	f.calls.Add(1)
	time.Sleep(f.pause)
	return f.pages, ctx.Err()
}

// liveDB is a WAL database of rows rows, big enough to take many steps.
func liveDB(t *testing.T, rows int) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 1000)
	for range rows {
		if _, err := tx.Exec(`INSERT INTO t(body) VALUES(?)`, pad); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, db
}

// writeThroughout commits a row every millisecond until the returned stop is
// called, as the Shield trail and analytics do on a live install.
func writeThroughout(t *testing.T, db *sql.DB) (stop func() int) {
	var wg sync.WaitGroup
	done := make(chan struct{})
	var n atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := db.Exec(`INSERT INTO t(body) VALUES('live')`); err == nil {
				n.Add(1)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return func() int { close(done); wg.Wait(); return int(n.Load()) }
}

func count(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ic string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ic); err != nil || ic != "ok" {
		t.Fatalf("integrity_check of the copy = %q, %v", ic, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A copy taken in many paced steps while another connection writes throughout
// is one moment's database: it finishes, it is intact, and it holds the rows
// of the moment it began, not the rows written during it.
func TestAPacedCopyUnderAWriterIsOneMoment(t *testing.T) {
	src, db := liveDB(t, 4000)
	stop := writeThroughout(t, db)
	time.Sleep(20 * time.Millisecond) // the writer is committing before the copy starts

	dest := filepath.Join(t.TempDir(), "copy.db")
	p := &fixedPacer{pages: 16, pause: 3 * time.Millisecond}
	var last Progress
	err := Copy(context.Background(), src, dest, p, func(pr Progress) { last = pr })
	written := stop()
	if err != nil {
		t.Fatalf("the copy failed under a writer: %v", err)
	}
	if written == 0 {
		t.Fatal("the writer committed nothing, so this test proved nothing about a live database")
	}
	got := count(t, dest)
	var live int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if got < 4000 || got >= live {
		t.Errorf("the copy has %d rows; the database had at least 4000 when it began and %d now, so it was not one moment", got, live)
	}
	if calls := int(p.calls.Load()); calls < 10 {
		t.Errorf("the copy asked the pacer %d times; at 16 pages a step it should have asked before each of many steps", calls)
	}
	if last.Total == 0 || last.Copied != last.Total || last.PageSize != 4096 {
		t.Errorf("the last progress was %+v; want every page of 4096 bytes copied", last)
	}
}

// fakeBackup is SQLite's backup handle restarting after its second step, which
// is what it does when the source changes under an unpinned copy. It reports
// done by its sixth step, so a copy that stopped checking would return nil
// rather than run forever.
type fakeBackup struct{ step int }

func (f *fakeBackup) Step(int) (bool, error) { f.step++; return f.step >= 6, nil }
func (f *fakeBackup) PageCount() int         { return 100 }
func (f *fakeBackup) Remaining() int {
	if f.step == 3 {
		return 100
	}
	return 100 - 10*f.step
}

// A restart is refused by name rather than retried: it means the copy is no
// longer of one moment.
func TestARestartedCopyFailsSayingSo(t *testing.T) {
	err := steps(context.Background(), &fakeBackup{}, &fixedPacer{pages: 10}, 4096, nil)
	if !errors.Is(err, ErrRestarted) {
		t.Fatalf("err = %v; want ErrRestarted", err)
	}
}

// Copy never overwrites: a backup landing on an existing file is a caller bug
// that must not destroy the file.
func TestACopyNeverOverwrites(t *testing.T) {
	src, _ := liveDB(t, 10)
	if err := Copy(context.Background(), src, src, &fixedPacer{pages: 8}, nil); err == nil {
		t.Fatal("Copy wrote over an existing file")
	}
	if count(t, src) != 10 {
		t.Fatal("the existing file was damaged")
	}
}
