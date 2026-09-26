// SPDX-License-Identifier: Apache-2.0

// Package sqlitecopy copies a live SQLite database in steps, one consistent
// snapshot, pausing between steps as long as the pacer asks.
//
// It replaces `VACUUM INTO`, which reads and writes the whole database in one
// statement that nothing can pause. On johal.in's 17 GB database that is minutes
// of full disk bandwidth, taken whenever a backup falls due. SQLite's backup API
// copies a given number of pages per step instead, so the copy can yield to
// visitors between any two steps.
//
// A paced copy is only a backup if it is one moment's database. The backup API
// restarts from the first page whenever another connection writes to the source
// between steps, and a live install always has a writer, so an unpinned paced
// copy never finishes. Copy pins one read transaction on the source connection
// for the whole copy: in WAL mode that connection keeps seeing the database as
// it was when the transaction began, whatever is written meanwhile, and the
// backup reads through it. A restart is therefore a broken invariant, not
// something to retry, and Copy fails on the first one.
//
// The price of the pin: no checkpoint can move past the snapshot while the copy
// runs, so the write-ahead log keeps everything the site writes meanwhile and
// is folded back in only after the copy ends. A copy held at its slowest pace
// for a day holds a day of writes there; the site's periodic checkpoint reports
// the log's size as it grows.
package sqlitecopy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/johalputt/vayupress/internal/pacedio"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// Progress is how far a copy has got, in database pages of PageSize bytes.
type Progress struct {
	Copied, Total, PageSize int
}

// ErrRestarted is returned when the source changed under the copy, which the
// pinned snapshot exists to prevent.
var ErrRestarted = errors.New("the copy restarted: the database changed under it, so it would not have been one moment's data")

// Copy writes a consistent copy of the SQLite database at src to dest, which
// must not exist. onStep, when not nil, is told the progress after each step.
// The pacer is asked before every step how many pages it may copy.
func Copy(ctx context.Context, src, dest string, p pacedio.Pacer, onStep func(Progress)) (err error) {
	if _, statErr := os.Stat(dest); statErr == nil {
		return fmt.Errorf("sqlitecopy: %s already exists", dest)
	}
	// Opening a path that does not exist creates an empty database there,
	// which would then copy "successfully": a backup of nothing, and a stray
	// database file where the live one belongs.
	if _, statErr := os.Stat(src); statErr != nil {
		return fmt.Errorf("sqlitecopy: no database to copy: %w", statErr)
	}
	srcDB, err := sql.Open("sqlite3", src+"?_busy_timeout=15000&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer srcDB.Close()
	srcConn, err := srcDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer srcConn.Close()

	// Pin the snapshot: a read transaction is only opened by the first read
	// inside it, so BEGIN alone would pin nothing.
	if _, err := srcConn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() { _, _ = srcConn.ExecContext(context.Background(), "ROLLBACK") }()
	var n int
	if err := srcConn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		return err
	}
	var pageSize int
	if err := srcConn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}

	dstDB, err := sql.Open("sqlite3", dest)
	if err != nil {
		return err
	}
	defer dstDB.Close()
	dstConn, err := dstDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer dstConn.Close()

	err = dstConn.Raw(func(d any) error {
		return srcConn.Raw(func(s any) error {
			bk, err := d.(*sqlite3.SQLiteConn).Backup("main", s.(*sqlite3.SQLiteConn), "main")
			if err != nil {
				return err
			}
			if err := steps(ctx, bk, p, pageSize, onStep); err != nil {
				_ = bk.Finish()
				return err
			}
			return bk.Finish()
		})
	})
	if err != nil {
		_ = os.Remove(dest)
	}
	return err
}

// stepper is the part of the backup handle steps uses, so a test can stand in
// for SQLite's restart without racing a writer.
type stepper interface {
	Step(pages int) (bool, error)
	Remaining() int
	PageCount() int
}

func steps(ctx context.Context, bk stepper, p pacedio.Pacer, pageSize int, onStep func(Progress)) error {
	last := -1
	for {
		pages, err := p.Next(ctx)
		if err != nil {
			return err
		}
		done, err := bk.Step(pages)
		if err != nil {
			return err
		}
		rem, total := bk.Remaining(), bk.PageCount()
		if last >= 0 && rem > last {
			return ErrRestarted
		}
		last = rem
		if onStep != nil {
			onStep(Progress{Copied: total - rem, Total: total, PageSize: pageSize})
		}
		if done {
			return nil
		}
	}
}
