// SPDX-License-Identifier: Apache-2.0

package mail

// drive_share.go — a link to one of a holder's files, for someone with no
// account here.
//
// The link is a 256-bit random token; its hash is what is kept, so the
// store cannot be read for working links. A link may have a password, an
// end and a most number of downloads. It hands out the file as it is when
// it is asked for, so a file saved over is shared as its newest version,
// and a file in Trash, or in a folder that is, is not shared at all.
//
// A private mailbox's file is sealed to its holder's key, which a stranger
// with a link does not have. Sharing one makes a copy that is not sealed,
// kept beside the mailbox's files and counted in its allowance, removed
// when the link ends, is stopped, or its file is deleted. The holder is
// told so before the link is made (the page's words), because that copy is
// readable on the server's disk as the sealed file is not.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

const (
	// ShareMinPassword is the shortest password a link takes. It is tried
	// only against this server, shareMaxFailures times, never offline.
	ShareMinPassword = 8
	// ShareMaxTTL is the longest end a link is given; zero is no end.
	ShareMaxTTL = 365 * 24 * time.Hour
	// ShareMaxDownloads is the largest most-downloads a link is given.
	ShareMaxDownloads = 10000
	// shareMaxFailures wrong passwords and a link is gone.
	shareMaxFailures = 10
	// maxSharesPerFile bounds the links one file has at once.
	maxSharesPerFile = 20
)

var (
	// ErrShareGone is a link that hands out nothing: never made, ended,
	// stopped, used up, closed after wrong passwords, its file in Trash or
	// deleted, its mailbox suspended. One error for all, so the page says
	// nothing about which.
	ErrShareGone = errors.New("vayumail: this link is not open")
	// ErrSharePassword is the wrong password for a link that has one.
	ErrSharePassword = errors.New("vayumail: that is not this link's password")
	// ErrShareDetails refuses a link's own settings.
	ErrShareDetails = errors.New("vayumail: a password is at least 8 characters, an end at most a year away, and downloads at most 10000")
	// ErrShareTooMany refuses a link past maxSharesPerFile.
	ErrShareTooMany = errors.New("vayumail: this file has 20 links already; stop one to make another")
)

// DriveShare is a link as its holder's list shows it.
type DriveShare struct {
	ID        int64
	File      int64
	Created   time.Time
	Expires   time.Time // zero: until it is stopped
	Password  bool
	Most      int // 0: any number
	Downloads int
	Copy      bool // an unsealed copy of a sealed file is kept for it
}

// SharedFile is what a link's page says about its file.
type SharedFile struct {
	Name     string
	Size     int64
	From     string // the mailbox that shared it
	Password bool
}

// driveLive reports whether a file is one a link may hand out: a file, not
// in Trash, and in no folder that is.
func (e *Engine) driveLive(ctx context.Context, mbox string, id int64) (DriveEntry, bool) {
	file, err := e.driveEntry(ctx, mbox, id)
	if err != nil || file.IsDir || !file.Trashed.IsZero() {
		return file, false
	}
	for n, up := 0, file.Parent; up != 0; n++ {
		d, err := e.driveEntry(ctx, mbox, up)
		if err != nil || !d.Trashed.IsZero() || n > maxDriveDepth {
			return file, false
		}
		up = d.Parent
	}
	return file, true
}

// DriveShareFile makes a link to file id and gives its token, which is
// shown to the holder once and kept nowhere. ttl zero is no end, most zero
// any number of downloads, password "" none.
func (e *Engine) DriveShareFile(rd Reader, id int64, password string, ttl time.Duration, most int) (string, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return "", err
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		return "", err
	}
	if (password != "" && len(password) < ShareMinPassword) || len(password) > 256 || ttl < 0 || ttl > ShareMaxTTL || most < 0 || most > ShareMaxDownloads {
		return "", ErrShareDetails
	}
	ctx := context.Background()
	file, ok := e.driveLive(ctx, mbox, id)
	if !ok {
		return "", ErrDriveNotFound
	}
	var n int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive_shares WHERE mailbox=? AND file_id=?`, mbox, id).Scan(&n)
	if n >= maxSharesPerFile {
		return "", ErrShareTooMany
	}
	passHash := ""
	if password != "" {
		if passHash, err = auth.HashSecretArgon2id(password); err != nil {
			return "", err
		}
	}
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	copyBlob, copySize := "", int64(0)
	if file.Sealed {
		if copyBlob, copySize, err = e.unsealedCopy(rd, mbox, id); err != nil {
			return "", err
		}
	}
	var expires any
	now := time.Now().UTC()
	if ttl > 0 {
		expires = now.Add(ttl)
	}
	if _, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive_shares(mailbox,file_id,token_hash,pass_hash,created,expires,most,blob,size) VALUES(?,?,?,?,?,?,?,?,?)`,
		mbox, id, hashRecoveryToken(token), passHash, now, expires, most, copyBlob, copySize); err != nil {
		if copyBlob != "" {
			e.removeBlob(mbox, copyBlob)
		}
		return "", err
	}
	e.forgetUsage(mbox)
	return token, nil
}

// unsealedCopy writes a sealed file's opened bytes as a blob of its own.
// The key must be held open, as for any reading of the file.
func (e *Engine) unsealedCopy(rd Reader, mbox string, id int64) (string, int64, error) {
	_, f, err := e.DriveOpen(rd, id)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	blob, err := randomHex(16)
	if err != nil {
		return "", 0, err
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return "", 0, err
	}
	size, err := io.Copy(out, f)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		if quota := e.accounts.QuotaFor(context.Background(), mbox); quota > 0 && e.mailboxUsage(mbox, false)+size > quota {
			err = ErrDriveFull
		}
	}
	if err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return blob, size, nil
}

// DriveShares is a file's links, newest first.
func (e *Engine) DriveShares(rd Reader, id int64) ([]DriveShare, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	e.sweepShares(ctx, time.Now())
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT id,file_id,created,expires,pass_hash<>'',most,downloads,blob<>'' FROM vayumail_drive_shares WHERE mailbox=? AND file_id=? ORDER BY id DESC`, mbox, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveShare
	for rows.Next() {
		var s DriveShare
		var expires sql.NullTime
		if err := rows.Scan(&s.ID, &s.File, &s.Created, &expires, &s.Password, &s.Most, &s.Downloads, &s.Copy); err != nil {
			return nil, err
		}
		if expires.Valid {
			s.Expires = expires.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DriveUnshare stops one of the holder's links.
func (e *Engine) DriveUnshare(rd Reader, share int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		return err
	}
	return e.dropShares(context.Background(), `SELECT id,mailbox,blob FROM vayumail_drive_shares WHERE mailbox=? AND id=?`, mbox, share)
}

// dropShares removes the links a query names (their id, mailbox and blob),
// and the copies kept for them. Every way a link ends goes through here, so
// no copy outlives its link.
func (e *Engine) dropShares(ctx context.Context, query string, args ...any) error {
	rows, err := e.accounts.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	type gone struct {
		id         int64
		mbox, blob string
	}
	var all []gone
	for rows.Next() {
		var g gone
		if rows.Scan(&g.id, &g.mbox, &g.blob) == nil {
			all = append(all, g)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, g := range all {
		if _, err := e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive_shares WHERE id=?`, g.id); err != nil {
			return err
		}
		if g.blob != "" {
			e.removeBlob(g.mbox, g.blob)
		}
	}
	return nil
}

// sweepShares removes the links whose end has passed.
func (e *Engine) sweepShares(ctx context.Context, now time.Time) {
	if e.accounts == nil || e.accounts.ensureDriveTables() != nil {
		return
	}
	_ = e.dropShares(ctx, `SELECT id,mailbox,blob FROM vayumail_drive_shares WHERE expires IS NOT NULL AND expires<=?`, now.UTC())
}

// shareRow is a link as the public side reads it.
type shareRow struct {
	id                   int64
	mbox, passHash, blob string
	file                 int64
	most, downloads      int
	size                 int64
}

// shareByToken is the open link a token names, with its live file.
func (e *Engine) shareByToken(ctx context.Context, token string) (shareRow, DriveEntry, error) {
	var s shareRow
	if e.accounts == nil || len(token) != 64 || e.accounts.ensureDriveTables() != nil {
		return s, DriveEntry{}, ErrShareGone
	}
	e.sweepShares(ctx, time.Now())
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT id,mailbox,file_id,pass_hash,most,downloads,blob,size FROM vayumail_drive_shares WHERE token_hash=?`, hashRecoveryToken(token)).
		Scan(&s.id, &s.mbox, &s.file, &s.passHash, &s.most, &s.downloads, &s.blob, &s.size); err != nil {
		return s, DriveEntry{}, ErrShareGone
	}
	// A suspended mailbox shares nothing, as it books nothing.
	var active int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT active FROM vayumail_accounts WHERE email=?`, s.mbox).Scan(&active)
	file, live := e.driveLive(ctx, s.mbox, s.file)
	// A sealed file is handed out only as the copy made for this link.
	if active != 1 || !live || (file.Sealed && s.blob == "") {
		return s, DriveEntry{}, ErrShareGone
	}
	return s, file, nil
}

// DriveShared is what a link's page shows before anything is handed out.
func (e *Engine) DriveShared(token string) (SharedFile, error) {
	s, file, err := e.shareByToken(context.Background(), token)
	if err != nil {
		return SharedFile{}, err
	}
	// Used up: the page says so before a press finds out. The press itself
	// is decided by the count it takes (DriveSharedOpen), not by this.
	if s.most > 0 && s.downloads >= s.most {
		return SharedFile{}, ErrShareGone
	}
	size := file.Size
	if s.blob != "" {
		size = s.size
	}
	return SharedFile{Name: file.Name, Size: size, From: s.mbox, Password: s.passHash != ""}, nil
}

// DriveSharedOpen hands out a link's file, counted as one download. A wrong
// password is counted, and at shareMaxFailures the link is gone. The count
// of downloads is taken before the bytes are opened, so two people at the
// last download do not both get it.
func (e *Engine) DriveSharedOpen(token, password string) (SharedFile, io.ReadCloser, error) {
	ctx := context.Background()
	s, file, err := e.shareByToken(ctx, token)
	if err != nil {
		return SharedFile{}, nil, err
	}
	if s.passHash != "" && !auth.VerifySecretArgon2id(password, s.passHash) {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive_shares SET failures=failures+1 WHERE id=?`, s.id)
		_ = e.dropShares(ctx, `SELECT id,mailbox,blob FROM vayumail_drive_shares WHERE id=? AND failures>=?`, s.id, shareMaxFailures)
		return SharedFile{}, nil, ErrSharePassword
	}
	res, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive_shares SET downloads=downloads+1 WHERE id=? AND (most=0 OR downloads<most)`, s.id)
	if err != nil {
		return SharedFile{}, nil, ErrShareGone
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SharedFile{}, nil, ErrShareGone
	}
	blob, size := s.blob, s.size
	if blob == "" {
		size = file.Size
		if err := e.accounts.db.QueryRowContext(ctx, `SELECT blob FROM vayumail_drive WHERE id=? AND mailbox=?`, s.file, s.mbox).Scan(&blob); err != nil {
			return SharedFile{}, nil, ErrShareGone
		}
	}
	path, err := e.blobPath(s.mbox, blob)
	if err != nil {
		return SharedFile{}, nil, ErrShareGone
	}
	f, err := os.Open(path) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return SharedFile{}, nil, ErrShareGone
	}
	return SharedFile{Name: file.Name, Size: size, From: s.mbox, Password: s.passHash != ""}, f, nil
}
