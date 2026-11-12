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
// Every file's bytes are kept in chunks sealed with the file's own key
// (driveblob.go), and that key is wrapped by the install's Drive key or, in
// a private mailbox, sealed to its holder's key as its mail is (sealing.go,
// drivekeys.go): unreadable on the disk, a private file opened only while
// the holder's key is held open. A file is never whole in memory, so a
// private file is as large as any other. A file kept before chunks reads
// as it was kept (in the clear, or sealed whole) until it is rewritten.
//
// Files are their holder's alone: driveOf refuses an operator Reader, as
// calendarOf does.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	// maxDriveEntries bounds one mailbox's files and folders together.
	maxDriveEntries = 50000
	maxDriveDepth   = 32
	driveVersions   = 5
	driveTrashFor   = 30 * 24 * time.Hour
	// sealedAtOnce: see sealSlot.
	sealedAtOnce = 2
)

// maxDriveFile is the largest file kept: a file is sealed and read in
// chunks, never whole in memory, so the bound is the disk's and the
// allowance's, and this one keeps a single file to what a person uploads
// through a page or a file app. A variable so a test of the bound need not
// write two gigabytes.
var maxDriveFile int64 = 2 << 30

var (
	ErrDriveHolderOnly = errors.New("vayumail: files are opened only by their holder")
	ErrDriveNotFound   = errors.New("vayumail: there is no such file or folder")
	ErrDriveName       = errors.New("vayumail: a name is 1 to 255 characters with no slash, and one or two dots alone are not a name")
	ErrDriveExists     = errors.New("vayumail: something here already has that name")
	ErrDriveFull       = errors.New("vayumail: this would pass the mailbox's storage allowance")
	ErrDriveTooLarge   = errors.New("vayumail: a file is at most 2 GB")
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
	// Sum is the SHA-256 of the file's content, in hex; "" for a file kept
	// before sums were. Damaged: its bytes on the disk were found not to be
	// what was kept.
	Sum     string
	Damaged bool
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
	if err != nil {
		return err
	}
	// A link to a file (drive_share.go). blob and size are the unsealed copy
	// kept for a sealed file's link, '' and 0 for any other.
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_shares(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		file_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		pass_hash TEXT NOT NULL DEFAULT '',
		created DATETIME NOT NULL,
		expires DATETIME,
		most INTEGER NOT NULL DEFAULT 0,
		downloads INTEGER NOT NULL DEFAULT 0,
		failures INTEGER NOT NULL DEFAULT 0,
		blob TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL DEFAULT 0);`)
	if err != nil {
		return err
	}
	// A file or folder shared with another mailbox here (drive_grant.go).
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_grants(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		file_id INTEGER NOT NULL,
		grantee TEXT NOT NULL,
		can_write INTEGER NOT NULL DEFAULT 0,
		created DATETIME NOT NULL,
		UNIQUE(mailbox, file_id, grantee))`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS vayumail_drive_grants_grantee ON vayumail_drive_grants(grantee)`); err != nil {
		return err
	}
	// When each link's downloads were handed out (drive_share.go).
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_share_downloads(share_id INTEGER NOT NULL, at INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS vayumail_drive_share_downloads_share ON vayumail_drive_share_downloads(share_id, at)`); err != nil {
		return err
	}
	// Each file's own key and checksums (driveblob.go, drivekeys.go): fkey
	// is the key as kept, '' for a file kept before; sum the SHA-256 of the
	// content, blob_sum of the bytes on the disk; checked when those bytes
	// were last read against it, damaged when they did not match. A link's
	// own copy of a sealed file has its own key. Added in place on an
	// install that made the tables before.
	for _, add := range []string{
		"vayumail_drive ADD COLUMN fkey TEXT NOT NULL DEFAULT ''",
		"vayumail_drive ADD COLUMN sum TEXT NOT NULL DEFAULT ''",
		"vayumail_drive ADD COLUMN blob_sum TEXT NOT NULL DEFAULT ''",
		"vayumail_drive ADD COLUMN checked INTEGER NOT NULL DEFAULT 0",
		"vayumail_drive ADD COLUMN damaged INTEGER NOT NULL DEFAULT 0",
		"vayumail_drive_versions ADD COLUMN fkey TEXT NOT NULL DEFAULT ''",
		"vayumail_drive_versions ADD COLUMN sum TEXT NOT NULL DEFAULT ''",
		"vayumail_drive_versions ADD COLUMN blob_sum TEXT NOT NULL DEFAULT ''",
		"vayumail_drive_versions ADD COLUMN checked INTEGER NOT NULL DEFAULT 0",
		"vayumail_drive_versions ADD COLUMN damaged INTEGER NOT NULL DEFAULT 0",
		"vayumail_drive_shares ADD COLUMN fkey TEXT NOT NULL DEFAULT ''",
		// What a link opens (drive_share_folder.go): a file, a folder to
		// look through, or a folder to upload into.
		"vayumail_drive_shares ADD COLUMN kind TEXT NOT NULL DEFAULT 'file'",
	} {
		if _, err := s.db.Exec(`ALTER TABLE ` + add); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
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

// DriveUsed is the bytes a mailbox's files take: versions, Trash and the
// copies kept for links too.
func (s *AccountStore) DriveUsed(ctx context.Context, mbox string) int64 {
	if s.ensureDriveTables() != nil {
		return 0
	}
	var files, versions, copies int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM vayumail_drive WHERE mailbox=?`, normEmail(mbox)).Scan(&files)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM vayumail_drive_versions WHERE mailbox=?`, normEmail(mbox)).Scan(&versions)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM vayumail_drive_shares WHERE mailbox=?`, normEmail(mbox)).Scan(&copies)
	return files + versions + copies
}

const driveCols = `id,parent,name,is_dir,size,ctype,modified,trashed,sealed,sum,damaged`

func scanDrive(row interface{ Scan(...any) error }) (DriveEntry, error) {
	var d DriveEntry
	var isDir, sealed, damaged int
	var trashed sql.NullTime
	if err := row.Scan(&d.ID, &d.Parent, &d.Name, &isDir, &d.Size, &d.Type, &d.Modified, &trashed, &sealed, &d.Sum, &damaged); err != nil {
		return d, err
	}
	d.IsDir, d.Sealed, d.Damaged = isDir == 1, sealed == 1, damaged == 1
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
	id, _, err := e.drivePut(rd, parent, name, ctype, r, false)
	return id, err
}

// drivePut keeps r as the file name in parent: over a file of that name,
// or, fresh, under a name of its own (DriveKeepNew). It answers the name.
func (e *Engine) drivePut(rd Reader, parent int64, name, ctype string, r io.Reader, fresh bool) (int64, string, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return 0, "", err
	}
	if name, err = driveName(name); err != nil {
		return 0, "", err
	}
	defer e.forgetUsage(mbox)
	ctx := context.Background()
	if err := e.driveFolder(ctx, mbox, parent); err != nil {
		return 0, "", err
	}
	old, replacing := e.driveNamed(ctx, mbox, parent, name)
	if replacing && old.IsDir && !fresh {
		return 0, "", ErrDriveExists
	}
	if !replacing || fresh {
		if err := e.driveRoom(ctx, mbox); err != nil {
			return 0, "", err
		}
	}
	private := e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(mbox)
	key, fkey, err := e.newFileKey(mbox, private)
	if err != nil {
		return 0, "", err
	}
	blob, err := randomHex(16)
	if err != nil {
		return 0, "", err
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, "", err
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path is the mailbox's own directory and a random hex name
	if err != nil {
		return 0, "", err
	}
	discard := func() { _ = f.Close(); _ = os.Remove(tmp) }
	// The content is sealed chunk by chunk as it arrives, so a file of any
	// size, private or not, is never whole in memory; both sums are taken on
	// the way.
	contentSum, storedSum := sha256.New(), sha256.New()
	bw, err := newBlobWriter(io.MultiWriter(f, storedSum), key)
	if err != nil {
		discard()
		return 0, "", err
	}
	size, err := io.Copy(io.MultiWriter(bw, contentSum), io.LimitReader(r, maxDriveFile+1))
	if err != nil {
		discard()
		return 0, "", err
	}
	if size > maxDriveFile {
		discard()
		return 0, "", ErrDriveTooLarge
	}
	if err := bw.Close(); err != nil {
		discard()
		return 0, "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, "", err
	}
	sealed := 0
	if private {
		sealed = 1
	}
	sum, blobSum := hex.EncodeToString(contentSum.Sum(nil)), hex.EncodeToString(storedSum.Sum(nil))
	return e.keepStored(ctx, mbox, parent, name, ctype, tmp, path, storedFile{blob: blob, fkey: fkey, size: size, sealed: sealed, sum: sum, blobSum: blobSum, fresh: fresh})
}

// storedFile is a file's bytes as written, ready to be named by a row.
type storedFile struct {
	blob, fkey, sum, blobSum string
	size                     int64
	sealed                   int
	// fresh: never over a file already there; a name taken is numbered
	// when the row is made, so a name taken while the bytes were arriving
	// is not written over either.
	fresh bool
}

// keepStored makes the bytes written at tmp the file name in parent: a new
// row, or the file of that name again with what it was kept as a version.
// The allowance is checked last, when the size is known. A name taken by a
// folder since the bytes began is refused. It answers the name kept under.
func (e *Engine) keepStored(ctx context.Context, mbox string, parent int64, name, ctype, tmp, path string, f storedFile) (int64, string, error) {
	if quota := e.accounts.QuotaFor(ctx, mbox); quota > 0 && e.mailboxUsage(mbox, false)+f.size > quota {
		_ = os.Remove(tmp)
		return 0, "", ErrDriveFull
	}
	if f.fresh {
		var ok bool
		if name, ok = e.freeName(ctx, mbox, parent, name); !ok {
			_ = os.Remove(tmp)
			return 0, "", ErrDriveExists
		}
	}
	old, replacing := e.driveNamed(ctx, mbox, parent, name)
	if replacing && old.IsDir {
		_ = os.Remove(tmp)
		return 0, "", ErrDriveExists
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, "", err
	}
	defer e.forgetUsage(mbox)
	now := time.Now().UTC()
	if !replacing {
		res, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive(mailbox,parent,name,is_dir,size,blob,sealed,ctype,modified,fkey,sum,blob_sum,checked)
			VALUES(?,?,?,0,?,?,?,?,?,?,?,?,?)`, mbox, parent, name, f.size, f.blob, f.sealed, oneLine(ctype), now, f.fkey, f.sum, f.blobSum, now.Unix())
		if err != nil {
			_ = os.Remove(path)
			return 0, "", err
		}
		id, err := res.LastInsertId()
		return id, name, err
	}
	tx, err := e.accounts.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(path)
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_drive_versions(mailbox,file_id,size,blob,sealed,saved,fkey,sum,blob_sum,checked,damaged)
		SELECT mailbox,id,size,blob,sealed,modified,fkey,sum,blob_sum,checked,damaged FROM vayumail_drive WHERE id=? AND mailbox=?`, old.ID, mbox); err != nil {
		_ = os.Remove(path)
		return 0, "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive SET size=?, blob=?, sealed=?, ctype=?, modified=?, fkey=?, sum=?, blob_sum=?, checked=?, damaged=0 WHERE id=? AND mailbox=?`,
		f.size, f.blob, f.sealed, oneLine(ctype), now, f.fkey, f.sum, f.blobSum, now.Unix(), old.ID, mbox); err != nil {
		_ = os.Remove(path)
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(path)
		return 0, "", err
	}
	e.pruneVersions(ctx, mbox, old.ID)
	return old.ID, name, nil
}

// DriveKeepNew keeps r as a new file in parent, never over one already
// there: a name that is taken gets a number, "plan (2).pdf". What Mail
// saves comes by a sender's name for it, and two messages' "invoice.pdf"
// are two files, not one written over. It answers the name it used.
func (e *Engine) DriveKeepNew(rd Reader, parent int64, name, ctype string, r io.Reader) (int64, string, error) {
	return e.drivePut(rd, parent, name, ctype, r, true)
}

// freeName is name, or the first numbered name after it, that nothing in
// parent has.
func (e *Engine) freeName(ctx context.Context, mbox string, parent int64, name string) (string, bool) {
	free := name
	for n := 2; n <= 1000; n++ {
		if _, taken := e.driveNamed(ctx, mbox, parent, free); !taken {
			return free, true
		}
		free = numberedName(name, n)
	}
	return "", false
}

// numberedName is name with (n) before its extension, kept inside the 255
// bytes a name has.
func numberedName(name string, n int) string {
	ext := ""
	if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 10 {
		name, ext = name[:i], name[i:]
	}
	tail := fmt.Sprintf(" (%d)%s", n, ext)
	for len(name)+len(tail) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name + tail
}

// pruneVersions keeps as many of a file's latest versions as its holder
// chose (driveVersions unless they chose).
func (e *Engine) pruneVersions(ctx context.Context, mbox string, fileID int64) {
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT id,blob FROM vayumail_drive_versions WHERE mailbox=? AND file_id=? ORDER BY id DESC LIMIT -1 OFFSET ?`, mbox, fileID, e.driveSettingsOf(ctx, mbox).Versions)
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

// sealStoredFiles seals the files a mailbox kept before it was private, and
// their earlier versions, as SealStored does its mail. A file kept in
// chunks has only its key sealed to the holder; one kept before chunks is
// rewritten in chunks under a key sealed to the holder. Either way no file
// is held whole in memory, so every file is sealed, whatever its size.
// left counts those that could not be sealed this time and will be on the
// next run.
//
// A link made while a file was in the clear has no copy of its own, so it
// ends when the file is sealed (shareByToken): the holder makes it again,
// and is told then that a readable copy is kept for it.
func (e *Engine) sealStoredFiles(mbox string) (sealed, left int) {
	if e.accounts == nil || e.maildir == nil || e.maildir.keys == nil || e.accounts.ensureDriveTables() != nil {
		return 0, 0
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT blob FROM vayumail_drive WHERE mailbox=? AND is_dir=0 AND sealed=0 AND blob<>''
		UNION ALL SELECT blob FROM vayumail_drive_versions WHERE mailbox=? AND sealed=0`, mbox, mbox)
	if err != nil {
		return 0, 0
	}
	var blobs []string
	for rows.Next() {
		var b string
		if rows.Scan(&b) == nil {
			blobs = append(blobs, b)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return 0, 0
	}
	for _, b := range blobs {
		if e.sealBlob(ctx, mbox, b) != nil {
			left++
			continue
		}
		sealed++
	}
	return sealed, left
}

// keptRow is how one stored file's bytes are kept, from whichever table
// holds its blob now.
func (e *Engine) keptRow(ctx context.Context, mbox, blob string) (fkey string, sealed int, size int64, err error) {
	err = e.accounts.db.QueryRowContext(ctx, `SELECT fkey,sealed,size FROM vayumail_drive WHERE mailbox=? AND blob=?
		UNION ALL SELECT fkey,sealed,size FROM vayumail_drive_versions WHERE mailbox=? AND blob=? LIMIT 1`, mbox, blob, mbox, blob).Scan(&fkey, &sealed, &size)
	return fkey, sealed, size, err
}

// sealBlob seals one stored file to the holder, and marks the row that has
// it.
func (e *Engine) sealBlob(ctx context.Context, mbox, blob string) error {
	// One at a time, and how the file is kept read again inside: two runs
	// over one mailbox (the page's, and a second press) would otherwise both
	// read a file as unsealed and both rewrite it.
	e.sealingStored.Lock()
	defer e.sealingStored.Unlock()
	fkey, sealed, _, err := e.keptRow(ctx, mbox, blob)
	if errors.Is(err, sql.ErrNoRows) {
		// Rewritten by the run before this one (a new blob), or gone.
		return nil
	}
	if err != nil || sealed == 1 {
		return err
	}
	if fkey == "" {
		return e.rewriteKept(ctx, mbox, blob, true)
	}
	key, err := e.openFileKey(mbox, fkey)
	if err != nil {
		return err
	}
	wrapped, err := e.wrapFileKey(mbox, key, true)
	if err != nil {
		return err
	}
	// By the blob, in both tables: a file saved over meanwhile has moved its
	// row to the versions, and the blob's name is this package's own random
	// one, so it names one row wherever that row now is.
	if _, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET fkey=?, sealed=1 WHERE mailbox=? AND blob=? AND fkey=?`, wrapped, mbox, blob, fkey); err != nil {
		return err
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive_versions SET fkey=?, sealed=1 WHERE mailbox=? AND blob=? AND fkey=?`, wrapped, mbox, blob, fkey)
	return err
}

// rewriteKept rewrites a file kept before chunks (in the clear) in chunks,
// under a new key, sealed to the holder when private: as a new blob, which
// its row then names, so a reader that has the old one open reads it to
// its end and one that comes after reads the new. The caller holds
// sealingStored.
func (e *Engine) rewriteKept(ctx context.Context, mbox, blob string, private bool) error {
	oldPath, err := e.blobPath(mbox, blob)
	if err != nil {
		return err
	}
	in, err := os.Open(oldPath) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return err
	}
	defer in.Close()
	key, fkey, err := e.newFileKey(mbox, private)
	if err != nil {
		return err
	}
	newBlob, err := randomHex(16)
	if err != nil {
		return err
	}
	path, err := e.blobPath(mbox, newBlob)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- as above
	if err != nil {
		return err
	}
	contentSum, storedSum := sha256.New(), sha256.New()
	bw, err := newBlobWriter(io.MultiWriter(f, storedSum), key)
	if err == nil {
		_, err = io.Copy(io.MultiWriter(bw, contentSum), in)
	}
	if err == nil {
		err = bw.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	sealed := 0
	if private {
		sealed = 1
	}
	sum, blobSum, now := hex.EncodeToString(contentSum.Sum(nil)), hex.EncodeToString(storedSum.Sum(nil)), time.Now().Unix()
	moved := int64(0)
	for _, table := range []string{"vayumail_drive", "vayumail_drive_versions"} {
		res, err := e.accounts.db.ExecContext(ctx, `UPDATE `+table+` SET blob=?, fkey=?, sealed=?, sum=?, blob_sum=?, checked=? WHERE mailbox=? AND blob=? AND fkey=''`,
			newBlob, fkey, sealed, sum, blobSum, now, mbox, blob)
		if err != nil {
			_ = os.Remove(path)
			return err
		}
		n, _ := res.RowsAffected()
		moved += n
	}
	if moved == 0 {
		// Gone while it was rewritten (deleted, or pruned as a version).
		_ = os.Remove(path)
		return nil
	}
	_ = os.Remove(oldPath)
	return nil
}

// DriveVersion is what a file was before it was written over.
type DriveVersion struct {
	ID      int64
	Size    int64
	Saved   time.Time // when those bytes were first kept
	Sum     string    // the SHA-256 of its content, "" when not recorded
	Damaged bool
}

// DriveVersions is a file's earlier versions, newest first.
func (e *Engine) DriveVersions(rd Reader, id int64) ([]DriveVersion, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	rows, err := e.accounts.db.QueryContext(context.Background(), `SELECT id,size,saved,sum,damaged FROM vayumail_drive_versions WHERE mailbox=? AND file_id=? ORDER BY id DESC LIMIT ?`, mbox, id, e.driveSettingsOf(context.Background(), mbox).Versions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveVersion
	for rows.Next() {
		var v DriveVersion
		if err := rows.Scan(&v.ID, &v.Size, &v.Saved, &v.Sum, &v.Damaged); err != nil {
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
	type kept struct {
		size            int64
		blob            string
		sealed          int
		at              time.Time
		fkey, sum, bsum string
		checked         int64
		damaged         int
	}
	var cur, old kept
	if err := tx.QueryRowContext(ctx, `SELECT size,blob,sealed,modified,fkey,sum,blob_sum,checked,damaged FROM vayumail_drive WHERE id=? AND mailbox=?`, id, mbox).
		Scan(&cur.size, &cur.blob, &cur.sealed, &cur.at, &cur.fkey, &cur.sum, &cur.bsum, &cur.checked, &cur.damaged); err != nil {
		return ErrDriveNotFound
	}
	// The version is asked for by its file and its mailbox as well as its
	// number: a number from another file, or another holder's, finds nothing.
	if err := tx.QueryRowContext(ctx, `SELECT size,blob,sealed,saved,fkey,sum,blob_sum,checked,damaged FROM vayumail_drive_versions WHERE id=? AND file_id=? AND mailbox=?`, version, id, mbox).
		Scan(&old.size, &old.blob, &old.sealed, &old.at, &old.fkey, &old.sum, &old.bsum, &old.checked, &old.damaged); err != nil {
		return ErrDriveNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive SET size=?, blob=?, sealed=?, modified=?, fkey=?, sum=?, blob_sum=?, checked=?, damaged=? WHERE id=? AND mailbox=?`,
		old.size, old.blob, old.sealed, time.Now().UTC(), old.fkey, old.sum, old.bsum, old.checked, old.damaged, id, mbox); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vayumail_drive_versions SET size=?, blob=?, sealed=?, saved=?, fkey=?, sum=?, blob_sum=?, checked=?, damaged=? WHERE id=? AND mailbox=?`,
		cur.size, cur.blob, cur.sealed, cur.at, cur.fkey, cur.sum, cur.bsum, cur.checked, cur.damaged, version, mbox); err != nil {
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

// sealSlots is a mailbox's slots themselves (sealSlot).
func (e *Engine) sealSlots(mbox string) chan struct{} {
	v, _ := e.driveSealing.LoadOrStore(mbox, make(chan struct{}, sealedAtOnce))
	slots, _ := v.(chan struct{})
	return slots
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
	slots := e.sealSlots(mbox)
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
	var blob, fkey string
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT blob,fkey FROM vayumail_drive WHERE id=? AND mailbox=?`, id, mbox).Scan(&blob, &fkey); err != nil {
		return d, nil, ErrDriveNotFound
	}
	r, err := e.openKept(mbox, blob, fkey, d.Sealed, d.Size)
	return d, r, err
}

// openKept opens one stored file's bytes however they were kept: in chunks
// under the key its row keeps, or, kept before chunks, as they are or
// sealed whole to the holder.
func (e *Engine) openKept(mbox, blob, fkey string, sealed bool, size int64) (io.ReadSeekCloser, error) {
	if fkey != "" {
		return e.openBlob(mbox, blob, fkey, size, func() { e.markDamaged(mbox, blob) })
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return nil, err
	}
	if !sealed {
		f, err := os.Open(path) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
		if err != nil {
			return nil, ErrDriveNotFound
		}
		return f, nil
	}
	if e.maildir == nil || e.maildir.keys == nil {
		return nil, ErrDriveLocked
	}
	// The opened file is in memory until its reader is closed, so the slot
	// is held that long, not only while it is opened.
	done, ok := e.sealSlot(mbox)
	if !ok {
		return nil, ErrDriveBusy
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- as above
	if err != nil {
		done()
		return nil, ErrDriveNotFound
	}
	plain, err := e.maildir.keys.DecryptForEmail(raw, mbox)
	if err != nil {
		done()
		return nil, ErrDriveLocked
	}
	return &bytesFile{Reader: bytes.NewReader(plain), done: done}, nil
}

// DriveRename gives an entry another name where it is.
func (e *Engine) DriveRename(rd Reader, id int64, name string) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	d, err := e.driveEntry(context.Background(), mbox, id)
	if err != nil {
		return err
	}
	return e.DriveMoveAs(rd, id, d.Parent, name)
}

// DriveMove puts an entry in another folder (0 for the top).
func (e *Engine) DriveMove(rd Reader, id, to int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	d, err := e.driveEntry(context.Background(), mbox, id)
	if err != nil {
		return err
	}
	return e.DriveMoveAs(rd, id, to, d.Name)
}

// DriveMoveAs puts an entry in folder to (0 for the top) under name, in one
// step: an app's MOVE renames and moves at once, and two steps would leave
// the entry half done when the second is refused.
func (e *Engine) DriveMoveAs(rd Reader, id, to int64, name string) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if name, err = driveName(name); err != nil {
		return err
	}
	ctx := context.Background()
	if _, err := e.driveEntry(ctx, mbox, id); err != nil {
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
	if other, taken := e.driveNamed(ctx, mbox, to, name); taken && other.ID != id {
		return ErrDriveExists
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive SET parent=?, name=? WHERE id=? AND mailbox=?`, to, name, id, mbox)
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
		if err := e.dropShares(ctx, `SELECT id,mailbox,blob FROM vayumail_drive_shares WHERE mailbox=? AND file_id=?`, mbox, x); err != nil {
			return err
		}
		if _, err := e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive_grants WHERE mailbox=? AND file_id=?`, mbox, x); err != nil {
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

// emptyOldTrash removes what has been in Trash as long as its holder's
// Trash keeps things (driveTrashFor unless they chose), mailbox by mailbox.
func (e *Engine) emptyOldTrash(now time.Time) {
	if e.accounts == nil || e.accounts.ensureDriveTables() != nil {
		return
	}
	ctx := context.Background()
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT DISTINCT mailbox FROM vayumail_drive WHERE trashed IS NOT NULL`)
	if err != nil {
		return
	}
	var boxes []string
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil {
			boxes = append(boxes, m)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, mbox := range boxes {
		keep := time.Duration(e.driveSettingsOf(ctx, mbox).TrashDays) * 24 * time.Hour
		old, err := e.driveRows(ctx, `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND trashed IS NOT NULL AND trashed<? LIMIT 500`, mbox, now.Add(-keep).UTC())
		if err != nil {
			continue
		}
		for _, d := range old {
			_ = e.driveRemove(ctx, mbox, d.ID)
		}
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
