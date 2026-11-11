// SPDX-License-Identifier: Apache-2.0

package mail

// drive.go — a holder's files: folders and files beside their mail.
//
// What a file is called, where it sits and how big it is are rows in
// vayumail_drive; its bytes are a file under <StorageDir>/drive, named by a
// random id, never by anything a person typed and never under a directory
// the web server serves. A file's name is therefore only ever text: it is
// checked for what a file system and a download header cannot carry, and
// never joined into a path.
//
// Files count against the same storage allowance as mail (QuotaFor): a
// write that would pass it is refused, and mail arriving counts the files
// too (inbound.go). Trash keeps a file thirty days; writing over a file
// keeps the five versions before.
//
// A private mailbox's files are sealed to its holder's key as its mail is
// (sealing.go): unreadable on the disk, opened while the key is held open.
// Sealing is done in memory, so a sealed file is at most maxSealedFile.
//
// Files are their holder's alone: driveOf refuses an operator Reader, as
// calendarOf does.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// maxDriveFile is the largest file kept; maxSealedFile the largest in a
	// private mailbox, where sealing holds the file and its sealed form in
	// memory at once.
	maxDriveFile  = 100 << 20
	maxSealedFile = 25 << 20
	// maxDriveEntries bounds one mailbox's files and folders together.
	maxDriveEntries = 50000
	maxDriveDepth   = 32
	driveVersions   = 5
	driveTrashFor   = 30 * 24 * time.Hour
	// sealedAtOnce: see sealSlot.
	sealedAtOnce = 2
)

var (
	ErrDriveHolderOnly = errors.New("vayumail: files are opened only by their holder")
	ErrDriveNotFound   = errors.New("vayumail: there is no such file or folder")
	ErrDriveName       = errors.New("vayumail: a name is 1 to 255 characters with no slash, and one or two dots alone are not a name")
	ErrDriveExists     = errors.New("vayumail: something here already has that name")
	ErrDriveFull       = errors.New("vayumail: this would pass the mailbox's storage allowance")
	ErrDriveTooLarge   = errors.New("vayumail: a file is at most 100 MB, and 25 MB in a private mailbox")
	ErrDriveLocked     = errors.New("vayumail: this file is sealed; unlock the mailbox to open it")
	ErrDriveMove       = errors.New("vayumail: a folder cannot be moved into itself")
	ErrDriveBusy       = errors.New("vayumail: two of this mailbox's sealed files are being kept or opened already; try again when one is done")
)

// DriveEntry is one file or folder.
type DriveEntry struct {
	ID       int64
	Parent   int64 // 0 for the top
	Name     string
	IsDir    bool
	Size     int64
	Type     string // the content type given at upload
	Modified time.Time
	Trashed  time.Time // zero unless in Trash
	Sealed   bool
}

func (s *AccountStore) ensureDriveTables() error {
	if s.db == nil {
		return sql.ErrConnDone
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		parent INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		is_dir INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0,
		blob TEXT NOT NULL DEFAULT '',
		sealed INTEGER NOT NULL DEFAULT 0,
		ctype TEXT NOT NULL DEFAULT '',
		modified DATETIME NOT NULL,
		trashed DATETIME);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS vayumail_drive_parent ON vayumail_drive(mailbox, parent)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_versions(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		file_id INTEGER NOT NULL,
		size INTEGER NOT NULL,
		blob TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0,
		saved DATETIME NOT NULL);`)
	return err
}

// driveName is a name as kept, or ErrDriveName.
func driveName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) {
		return "", ErrDriveName
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return "", ErrDriveName
		}
	}
	return name, nil
}

// driveOf is the mailbox whose files rd opens: its holder's, or the
// product's on their behalf, never an administrator's.
func (e *Engine) driveOf(rd Reader) (string, error) {
	if rd.kind != readerOwner && rd.kind != readerSystem {
		return "", ErrDriveHolderOnly
	}
	if e.accounts == nil {
		return "", errors.New("vayumail: no account store")
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		return "", err
	}
	dom, local := e.mailboxKey(rd.Key())
	return normEmail(local + "@" + dom), nil
}

// driveDir is where a mailbox's file bytes are kept.
func (e *Engine) driveDir(mbox string) string {
	local, dom := splitAddress(mbox)
	return filepath.Join(e.cfg.StorageDir, "drive", safeSegment(dom), safeSegment(local))
}

// blobPath is a blob's file. A blob's name is hex this package made; any
// other is refused, so a row cannot name a file outside the mailbox's own.
func (e *Engine) blobPath(mbox, blob string) (string, error) {
	if len(blob) != 32 || strings.Trim(blob, "0123456789abcdef") != "" {
		return "", ErrDriveNotFound
	}
	return filepath.Join(e.driveDir(mbox), blob[:2], blob), nil
}

// DriveUsed is the bytes a mailbox's files take, versions and Trash too.
func (s *AccountStore) DriveUsed(ctx context.Context, mbox string) int64 {
	if s.ensureDriveTables() != nil {
		return 0
	}
	var files, versions int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM vayumail_drive WHERE mailbox=?`, normEmail(mbox)).Scan(&files)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM vayumail_drive_versions WHERE mailbox=?`, normEmail(mbox)).Scan(&versions)
	return files + versions
}

const driveCols = `id,parent,name,is_dir,size,ctype,modified,trashed,sealed`

func scanDrive(row interface{ Scan(...any) error }) (DriveEntry, error) {
	var d DriveEntry
	var isDir, sealed int
	var trashed sql.NullTime
	if err := row.Scan(&d.ID, &d.Parent, &d.Name, &isDir, &d.Size, &d.Type, &d.Modified, &trashed, &sealed); err != nil {
		return d, err
	}
	d.IsDir, d.Sealed = isDir == 1, sealed == 1
	if trashed.Valid {
		d.Trashed = trashed.Time
	}
	return d, nil
}

// driveEntry is one of mbox's entries.
func (e *Engine) driveEntry(ctx context.Context, mbox string, id int64) (DriveEntry, error) {
	d, err := scanDrive(e.accounts.db.QueryRowContext(ctx, `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND id=?`, mbox, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrDriveNotFound
	}
	return d, err
}

// driveFolder checks parent is the top or one of mbox's folders, not in
// Trash.
func (e *Engine) driveFolder(ctx context.Context, mbox string, parent int64) error {
	if parent == 0 {
		return nil
	}
	d, err := e.driveEntry(ctx, mbox, parent)
	if err != nil {
		return err
	}
	if !d.IsDir || !d.Trashed.IsZero() {
		return ErrDriveNotFound
	}
	return nil
}

// driveNamed is the live entry called name in parent, if any.
func (e *Engine) driveNamed(ctx context.Context, mbox string, parent int64, name string) (DriveEntry, bool) {
	d, err := scanDrive(e.accounts.db.QueryRowContext(ctx, `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND parent=? AND name=? AND trashed IS NULL`, mbox, parent, name))
	return d, err == nil
}

// DriveList is a folder's contents (parent 0 for the top), folders first,
// then by name.
func (e *Engine) DriveList(rd Reader, parent int64) ([]DriveEntry, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := e.driveFolder(ctx, mbox, parent); err != nil {
		return nil, err
	}
	return e.driveRows(ctx, `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND parent=? AND trashed IS NULL ORDER BY is_dir DESC, name COLLATE NOCASE`, mbox, parent)
}

// DriveTrashed is what is in Trash, the latest first.
func (e *Engine) DriveTrashed(rd Reader) ([]DriveEntry, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	return e.driveRows(context.Background(), `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND trashed IS NOT NULL ORDER BY trashed DESC`, mbox)
}

func (e *Engine) driveRows(ctx context.Context, query string, args ...any) ([]DriveEntry, error) {
	rows, err := e.accounts.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveEntry
	for rows.Next() {
		d, err := scanDrive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DrivePath is the folders from the top down to id (the entry itself last).
func (e *Engine) DrivePath(rd Reader, id int64) ([]DriveEntry, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	var out []DriveEntry
	for n := 0; id != 0; n++ {
		if n > maxDriveDepth {
			return nil, ErrDriveNotFound
		}
		d, err := e.driveEntry(context.Background(), mbox, id)
		if err != nil {
			return nil, err
		}
		out = append([]DriveEntry{d}, out...)
		id = d.Parent
	}
	return out, nil
}

func (e *Engine) driveDepth(ctx context.Context, mbox string, id int64) (int, error) {
	n := 0
	for ; id != 0; n++ {
		if n > maxDriveDepth {
			return 0, ErrDriveName
		}
		d, err := e.driveEntry(ctx, mbox, id)
		if err != nil {
			return 0, err
		}
		id = d.Parent
	}
	return n, nil
}

func (e *Engine) driveRoom(ctx context.Context, mbox string) error {
	var n int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive WHERE mailbox=?`, mbox).Scan(&n)
	if n >= maxDriveEntries {
		return fmt.Errorf("vayumail: a mailbox keeps at most %d files and folders", maxDriveEntries)
	}
	return nil
}

// DriveMkdir makes a folder.
func (e *Engine) DriveMkdir(rd Reader, parent int64, name string) (int64, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return 0, err
	}
	if name, err = driveName(name); err != nil {
		return 0, err
	}
	ctx := context.Background()
	if err := e.driveFolder(ctx, mbox, parent); err != nil {
		return 0, err
	}
	if depth, err := e.driveDepth(ctx, mbox, parent); err != nil || depth >= maxDriveDepth {
		return 0, errors.New("vayumail: folders go at most 32 deep")
	}
	if _, taken := e.driveNamed(ctx, mbox, parent, name); taken {
		return 0, ErrDriveExists
	}
	if err := e.driveRoom(ctx, mbox); err != nil {
		return 0, err
	}
	res, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive(mailbox,parent,name,is_dir,modified) VALUES(?,?,?,1,?)`, mbox, parent, name, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DrivePut keeps r as the file name in parent. A file of that name already
// there is written over, and what it was is kept as a version.
func (e *Engine) DrivePut(rd Reader, parent int64, name, ctype string, r io.Reader) (int64, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return 0, err
	}
	if name, err = driveName(name); err != nil {
		return 0, err
	}
	defer e.forgetUsage(mbox)
	ctx := context.Background()
	if err := e.driveFolder(ctx, mbox, parent); err != nil {
		return 0, err
	}
	old, replacing := e.driveNamed(ctx, mbox, parent, name)
	if replacing && old.IsDir {
		return 0, ErrDriveExists
	}
	if !replacing {
		if err := e.driveRoom(ctx, mbox); err != nil {
			return 0, err
		}
	}
	private := e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(mbox)
	limit := int64(maxDriveFile)
	if private {
		limit = maxSealedFile
		done, ok := e.sealSlot(mbox)
		if !ok {
			return 0, ErrDriveBusy
		}
		defer done()
	}
	blob, err := randomHex(16)
	if err != nil {
		return 0, err
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path is the mailbox's own directory and a random hex name
	if err != nil {
		return 0, err
	}
	discard := func() { _ = f.Close(); _ = os.Remove(tmp) }
	size, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		discard()
		return 0, err
	}
	if size > limit {
		discard()
		return 0, ErrDriveTooLarge
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	sealed := 0
	if private {
		plain, rerr := os.ReadFile(tmp) // #nosec G304 -- the file just written
		if rerr != nil {
			_ = os.Remove(tmp)
			return 0, rerr
		}
		out, serr := e.maildir.keys.SealTo(mbox, plain)
		if serr != nil {
			// As with mail: a file that cannot be sealed is not kept in the
			// clear in a mailbox whose holder was told it is sealed.
			_ = os.Remove(tmp)
			return 0, serr
		}
		if werr := os.WriteFile(tmp, out, 0o600); werr != nil {
			_ = os.Remove(tmp)
			return 0, werr
		}
		sealed = 1
	}
	if quota := e.accounts.QuotaFor(ctx, mbox); quota > 0 && e.mailboxUsage(mbox, false)+size > quota {
		_ = os.Remove(tmp)
		return 0, ErrDriveFull
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	now := time.Now().UTC()
	if !replacing {
		res, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive(mailbox,parent,name,is_dir,size,blob,sealed,ctype,modified) VALUES(?,?,?,0,?,?,?,?,?)`,
			mbox, parent, name, size, blob, sealed, oneLine(ctype), now)
		if err != nil {
			_ = os.Remove(path)
			return 0, err
		}
		return res.LastInsertId()
	}
	tx, err := e.accounts.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_drive_versions(mailbox,file_id,size,blob,sealed,saved) SELECT mailbox,id,size,blob,sealed,modified FROM vayumail_drive WHERE id=? AND mailbox=?`, old.ID, mbox); err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive SET size=?, blob=?, sealed=?, ctype=?, modified=? WHERE id=? AND mailbox=?`, size, blob, sealed, oneLine(ctype), now, old.ID, mbox); err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	e.pruneVersions(ctx, mbox, old.ID)
	return old.ID, nil
}

// pruneVersions keeps a file's latest driveVersions versions.
func (e *Engine) pruneVersions(ctx context.Context, mbox string, fileID int64) {
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT id,blob FROM vayumail_drive_versions WHERE mailbox=? AND file_id=? ORDER BY id DESC LIMIT -1 OFFSET ?`, mbox, fileID, driveVersions)
	if err != nil {
		return
	}
	type old struct {
		id   int64
		blob string
	}
	var gone []old
	for rows.Next() {
		var o old
		if rows.Scan(&o.id, &o.blob) == nil {
			gone = append(gone, o)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, o := range gone {
		if _, err := e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive_versions WHERE id=?`, o.id); err == nil {
			e.removeBlob(mbox, o.blob)
		}
	}
}

// DriveVersion is what a file was before it was written over.
type DriveVersion struct {
	ID    int64
	Size  int64
	Saved time.Time // when those bytes were first kept
}

// DriveVersions is a file's earlier versions, newest first.
func (e *Engine) DriveVersions(rd Reader, id int64) ([]DriveVersion, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	rows, err := e.accounts.db.QueryContext(context.Background(), `SELECT id,size,saved FROM vayumail_drive_versions WHERE mailbox=? AND file_id=? ORDER BY id DESC LIMIT ?`, mbox, id, driveVersions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveVersion
	for rows.Next() {
		var v DriveVersion
		if err := rows.Scan(&v.ID, &v.Size, &v.Saved); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DriveRestoreVersion makes an earlier version the file again. What the
// file is now takes that version's place, so going back can be undone and
// the bytes kept do not grow.
func (e *Engine) DriveRestoreVersion(rd Reader, id, version int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil || d.IsDir {
		return ErrDriveNotFound
	}
	tx, err := e.accounts.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var cur, old struct {
		size   int64
		blob   string
		sealed int
		at     time.Time
	}
	if err := tx.QueryRowContext(ctx, `SELECT size,blob,sealed,modified FROM vayumail_drive WHERE id=? AND mailbox=?`, id, mbox).Scan(&cur.size, &cur.blob, &cur.sealed, &cur.at); err != nil {
		return ErrDriveNotFound
	}
	// The version is asked for by its file and its mailbox as well as its
	// number: a number from another file, or another holder's, finds nothing.
	if err := tx.QueryRowContext(ctx, `SELECT size,blob,sealed,saved FROM vayumail_drive_versions WHERE id=? AND file_id=? AND mailbox=?`, version, id, mbox).Scan(&old.size, &old.blob, &old.sealed, &old.at); err != nil {
		return ErrDriveNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive SET size=?, blob=?, sealed=?, modified=? WHERE id=? AND mailbox=?`, old.size, old.blob, old.sealed, time.Now().UTC(), id, mbox); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive_versions SET size=?, blob=?, sealed=?, saved=? WHERE id=? AND mailbox=?`, cur.size, cur.blob, cur.sealed, cur.at, version, mbox); err != nil {
		return err
	}
	return tx.Commit()
}

func (e *Engine) removeBlob(mbox, blob string) {
	e.forgetUsage(mbox)
	if p, err := e.blobPath(mbox, blob); err == nil {
		_ = os.Remove(p)
	}
}

// bytesFile is an opened sealed file; closing it gives its slot back.
type bytesFile struct {
	*bytes.Reader
	done func()
}

func (b *bytesFile) Close() error { b.done(); return nil }

// sealSlot bounds the sealed files one mailbox has in memory at once to
// sealedAtOnce: sealing and opening both hold a whole file and its other
// form, and a download holds it for as long as its reader is slow. The
// bound is the mailbox's own, so one holder's transfers never make another
// wait. It answers at once: false when the mailbox's slots are taken. The
// function it returns gives the slot back, and is safe to call twice.
func (e *Engine) sealSlot(mbox string) (func(), bool) {
	v, _ := e.driveSealing.LoadOrStore(mbox, make(chan struct{}, sealedAtOnce))
	slots, _ := v.(chan struct{})
	select {
	case slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots }) }, true
	default:
		return nil, false
	}
}

// DriveOpen is a file and its bytes, opened if it is sealed.
func (e *Engine) DriveOpen(rd Reader, id int64) (DriveEntry, io.ReadSeekCloser, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return DriveEntry{}, nil, err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil || d.IsDir {
		return d, nil, ErrDriveNotFound
	}
	var blob string
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT blob FROM vayumail_drive WHERE id=? AND mailbox=?`, id, mbox).Scan(&blob); err != nil {
		return d, nil, ErrDriveNotFound
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return d, nil, err
	}
	if !d.Sealed {
		f, err := os.Open(path) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
		if err != nil {
			return d, nil, ErrDriveNotFound
		}
		return d, f, nil
	}
	if e.maildir == nil || e.maildir.keys == nil {
		return d, nil, ErrDriveLocked
	}
	// The opened file is in memory until its reader is closed, so the slot
	// is held that long, not only while it is opened.
	done, ok := e.sealSlot(mbox)
	if !ok {
		return d, nil, ErrDriveBusy
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- as above
	if err != nil {
		done()
		return d, nil, ErrDriveNotFound
	}
	plain, err := e.maildir.keys.DecryptForEmail(raw, mbox)
	if err != nil {
		done()
		return d, nil, ErrDriveLocked
	}
	return d, &bytesFile{Reader: bytes.NewReader(plain), done: done}, nil
}

// DriveRename gives an entry another name where it is.
func (e *Engine) DriveRename(rd Reader, id int64, name string) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if name, err = driveName(name); err != nil {
		return err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil {
		return err
	}
	if other, taken := e.driveNamed(ctx, mbox, d.Parent, name); taken && other.ID != id {
		return ErrDriveExists
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET name=? WHERE id=? AND mailbox=?`, name, id, mbox)
	return err
}

// DriveMove puts an entry in another folder (0 for the top).
func (e *Engine) DriveMove(rd Reader, id, to int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil {
		return err
	}
	if err := e.driveFolder(ctx, mbox, to); err != nil {
		return err
	}
	// A folder moved into itself, or into anything inside it, would be cut
	// off from the top with everything in it.
	for at, n := to, 0; at != 0; n++ {
		if at == id || n > maxDriveDepth {
			return ErrDriveMove
		}
		up, err := e.driveEntry(ctx, mbox, at)
		if err != nil {
			return err
		}
		at = up.Parent
	}
	if _, taken := e.driveNamed(ctx, mbox, to, d.Name); taken {
		return ErrDriveExists
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET parent=? WHERE id=? AND mailbox=?`, to, id, mbox)
	return err
}

// DriveTrash puts an entry in Trash, or with restore brings it back (to the
// top, under another name, when its folder or its name has gone).
func (e *Engine) DriveTrash(rd Reader, id int64, restore bool) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil {
		return err
	}
	if !restore {
		_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET trashed=? WHERE id=? AND mailbox=? AND trashed IS NULL`, time.Now().UTC(), id, mbox)
		return err
	}
	parent := d.Parent
	if e.driveFolder(ctx, mbox, parent) != nil {
		parent = 0
	}
	name := d.Name
	for n := 2; ; n++ {
		if _, taken := e.driveNamed(ctx, mbox, parent, name); !taken {
			break
		}
		if n > 100 {
			return ErrDriveExists
		}
		name = fmt.Sprintf("%s (%d)", d.Name, n)
		if len(name) > 255 {
			return ErrDriveExists
		}
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET trashed=NULL, parent=?, name=? WHERE id=? AND mailbox=?`, parent, name, id, mbox)
	return err
}

// DriveDelete removes an entry for good, with everything in it and every
// version, and their bytes.
func (e *Engine) DriveDelete(rd Reader, id int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if _, err := e.driveEntry(context.Background(), mbox, id); err != nil {
		return err
	}
	return e.driveRemove(context.Background(), mbox, id)
}

func (e *Engine) driveRemove(ctx context.Context, mbox string, id int64) error {
	ids, queue := []int64{id}, []int64{id}
	for len(queue) > 0 && len(ids) <= maxDriveEntries {
		rows, err := e.accounts.db.QueryContext(ctx, `SELECT id FROM vayumail_drive WHERE mailbox=? AND parent=?`, mbox, queue[0])
		if err != nil {
			return err
		}
		queue = queue[1:]
		for rows.Next() {
			var c int64
			if rows.Scan(&c) == nil {
				ids, queue = append(ids, c), append(queue, c)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	for _, x := range ids {
		var blobs []string
		rows, err := e.accounts.db.QueryContext(ctx, `SELECT blob FROM vayumail_drive WHERE mailbox=? AND id=? AND blob<>'' UNION ALL SELECT blob FROM vayumail_drive_versions WHERE mailbox=? AND file_id=?`, mbox, x, mbox, x)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b string
			if rows.Scan(&b) == nil {
				blobs = append(blobs, b)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive_versions WHERE mailbox=? AND file_id=?`, mbox, x); err != nil {
			return err
		}
		if _, err := e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive WHERE mailbox=? AND id=?`, mbox, x); err != nil {
			return err
		}
		for _, b := range blobs {
			e.removeBlob(mbox, b)
		}
	}
	return nil
}

// emptyOldTrash removes what has been in Trash for driveTrashFor.
func (e *Engine) emptyOldTrash(now time.Time) {
	if e.accounts == nil || e.accounts.ensureDriveTables() != nil {
		return
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT mailbox,id FROM vayumail_drive WHERE trashed IS NOT NULL AND trashed<? LIMIT 500`, now.Add(-driveTrashFor).UTC())
	if err != nil {
		return
	}
	type gone struct {
		mbox string
		id   int64
	}
	var old []gone
	for rows.Next() {
		var g gone
		if rows.Scan(&g.mbox, &g.id) == nil {
			old = append(old, g)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, g := range old {
		_ = e.driveRemove(ctx, g.mbox, g.id)
	}
}

// retireDrive sets a deleted mailbox's file bytes aside, as its mail is,
// so the next holder of the address starts with none.
func (e *Engine) retireDrive(mbox, stamp string) {
	dir := e.driveDir(mbox)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	_ = os.Rename(dir, filepath.Join(filepath.Dir(dir), ".retired-"+filepath.Base(dir)+"-"+stamp))
}
