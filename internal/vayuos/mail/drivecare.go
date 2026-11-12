// SPDX-License-Identifier: Apache-2.0

package mail

// drivecare.go — Drive's own upkeep, a little at a time behind the worker:
// files kept in the clear before files were kept in chunks are rewritten in
// chunks, and every stored file's bytes are read against the SHA-256 kept
// for them, so a file the disk changed or damaged is found and said to be,
// not served as if it were sound. A private file is checked too: its sum
// is of the bytes on the disk, which need no key to read.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"
)

const (
	// driveCheckEvery is how often each file's bytes are read against
	// their sum; a pass reads at most driveCareBytes and driveCareFiles,
	// the longest unchecked first, and rewrites driveCareRewrites.
	driveCheckEvery   = 7 * 24 * time.Hour
	driveCareBytes    = 256 << 20
	driveCareFiles    = 200
	driveCareRewrites = 10
	// driveCareGap is the least time between two passes.
	driveCareGap = 10 * time.Minute
)

// careForDrive is one pass of the upkeep.
func (e *Engine) careForDrive(now time.Time) (rewritten, checked, damaged int) {
	if e.accounts == nil || e.accounts.ensureDriveTables() != nil {
		return 0, 0, 0
	}
	ctx := context.Background()
	rewritten = e.rewriteClearFiles(ctx)
	checked, damaged = e.checkStoredFiles(ctx, now)
	return rewritten, checked, damaged
}

// rewriteClearFiles rewrites a few files kept in the clear in chunks: under
// the install's key, or sealed to the holder in a private mailbox.
func (e *Engine) rewriteClearFiles(ctx context.Context) int {
	type clear struct{ mbox, blob string }
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT mailbox,blob FROM vayumail_drive WHERE is_dir=0 AND blob<>'' AND fkey='' AND sealed=0
		UNION ALL SELECT mailbox,blob FROM vayumail_drive_versions WHERE fkey='' AND sealed=0 LIMIT ?`, driveCareRewrites)
	if err != nil {
		return 0
	}
	var all []clear
	for rows.Next() {
		var c clear
		if rows.Scan(&c.mbox, &c.blob) == nil {
			all = append(all, c)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return 0
	}
	n := 0
	for _, c := range all {
		private := e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(c.mbox)
		e.sealingStored.Lock()
		fkey, sealed, _, err := e.keptRow(ctx, c.mbox, c.blob)
		if err == nil && fkey == "" && sealed == 0 && e.rewriteKept(ctx, c.mbox, c.blob, private) == nil {
			n++
		}
		e.sealingStored.Unlock()
	}
	return n
}

// checkStoredFiles reads the files longest unchecked against their sums,
// recording a sum for a file kept before sums were.
func (e *Engine) checkStoredFiles(ctx context.Context, now time.Time) (checked, damaged int) {
	type stored struct {
		table, mbox, blob, sum string
		size                   int64
	}
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT 'vayumail_drive',mailbox,blob,blob_sum,size,checked FROM vayumail_drive WHERE is_dir=0 AND blob<>'' AND checked<?
		UNION ALL SELECT 'vayumail_drive_versions',mailbox,blob,blob_sum,size,checked FROM vayumail_drive_versions WHERE checked<?
		ORDER BY checked LIMIT ?`, now.Add(-driveCheckEvery).Unix(), now.Add(-driveCheckEvery).Unix(), driveCareFiles)
	if err != nil {
		return 0, 0
	}
	var all []stored
	for rows.Next() {
		var s stored
		var at int64
		if rows.Scan(&s.table, &s.mbox, &s.blob, &s.sum, &s.size, &at) == nil {
			all = append(all, s)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return 0, 0
	}
	var read int64
	for _, s := range all {
		// Decided by the size each file is kept at, before it is read: a
		// pass reads its share and no more, and a file larger than the whole
		// share is read on a pass of its own.
		if checked > 0 && read+blobSize(s.size) > driveCareBytes {
			break
		}
		got, err := e.storedSum(s.mbox, s.blob)
		read += blobSize(s.size)
		if errors.Is(err, os.ErrNotExist) {
			// Gone between the list and the read (deleted, or rewritten).
			continue
		}
		bad := err != nil || (s.sum != "" && got != s.sum)
		if s.sum == "" && !bad {
			// A file kept before sums were: its bytes as they are now are
			// what is checked from here on.
			_, _ = e.accounts.db.ExecContext(ctx, `UPDATE `+s.table+` SET blob_sum=? WHERE mailbox=? AND blob=? AND blob_sum=''`, got, s.mbox, s.blob)
		}
		flag := 0
		if bad {
			flag = 1
			damaged++
		}
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE `+s.table+` SET checked=?, damaged=? WHERE mailbox=? AND blob=?`, now.Unix(), flag, s.mbox, s.blob)
		checked++
	}
	return checked, damaged
}

// storedSum is the SHA-256 of a stored file's bytes on the disk.
func (e *Engine) storedSum(mbox, blob string) (string, error) {
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

// DriveDamaged is how many of rd's files and versions were found damaged.
func (e *Engine) DriveDamaged(rd Reader) (int, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return 0, err
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		return 0, err
	}
	var n int
	err = e.accounts.db.QueryRowContext(context.Background(), `SELECT (SELECT COUNT(1) FROM vayumail_drive WHERE mailbox=? AND damaged=1) +
		(SELECT COUNT(1) FROM vayumail_drive_versions WHERE mailbox=? AND damaged=1)`, mbox, mbox).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
