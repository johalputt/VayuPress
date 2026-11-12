// SPDX-License-Identifier: Apache-2.0

package mail

// drive_grant.go — a file or folder shared with another mailbox on this
// install: to read, or for a folder also to add files to.
//
// The holder names an address here; that mailbox's Drive lists it under
// Shared with me, opens it and the folders under it, and nothing above or
// beside it (within, as for a folder link). Sharing to write lets it upload
// files and make folders in the shared folder, kept in the holder's
// mailbox and counted in the holder's allowance; a file of a name already
// there is saved over, with what it was kept as a version, so nothing is
// lost to a mistake from the other side. It deletes nothing and moves
// nothing.
//
// A private mailbox's files are sealed to its holder's key, which the
// other mailbox does not have, so a private mailbox shares nothing this
// way; a file sealed after it was shared is not handed out. A suspended
// holder shares nothing, and what was shared with an address goes when the
// address does, so an address issued again inherits none of it.

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// maxGrantsPerEntry bounds the mailboxes one file or folder is shared with.
const maxGrantsPerEntry = 50

var (
	// ErrGrantWho refuses a share with an address that is not another
	// mailbox here.
	ErrGrantWho = errors.New("vayumail: share with another mailbox on this server, by its address")
	// ErrGrantSealed refuses a private mailbox's share.
	ErrGrantSealed = errors.New("vayumail: this mailbox's files are sealed to your key, which no other mailbox has, so they are not shared this way")
	// ErrGrantWrite refuses writing into a share that is to read, or a file.
	ErrGrantWrite = errors.New("vayumail: this is shared with you to read")
	// ErrGrantTooMany refuses a share past maxGrantsPerEntry.
	ErrGrantTooMany = errors.New("vayumail: this is shared with 50 mailboxes already; stop one to add another")
)

// DriveGrant is one share with a mailbox: as its holder's sheet lists it,
// or as the mailbox it is shared with sees it.
type DriveGrant struct {
	ID      int64
	Owner   string // the holder's address
	Grantee string // the address it is shared with
	Write   bool   // a folder the grantee may add files and folders to
	Created time.Time
	Entry   DriveEntry // the file or folder, as it is now
}

// DriveGrantTo shares entry id with the mailbox at address, to read, or
// with write (a folder only) also to add to. Sharing again with the same
// address changes what it may do.
func (e *Engine) DriveGrantTo(rd Reader, id int64, address string, write bool) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(mbox) {
		return ErrGrantSealed
	}
	who := normEmail(strings.TrimSpace(address))
	if who != "" && !strings.Contains(who, "@") {
		who = normEmail(who + "@" + e.cfg.Domain)
	}
	ctx := context.Background()
	var active int
	if who == mbox || e.accounts.db.QueryRowContext(ctx, `SELECT active FROM vayumail_accounts WHERE email=?`, who).Scan(&active) != nil {
		return ErrGrantWho
	}
	entry, ok := e.driveLiveEntry(ctx, mbox, id)
	if !ok {
		return ErrDriveNotFound
	}
	if write && !entry.IsDir {
		return ErrGrantWrite
	}
	var n int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive_grants WHERE mailbox=? AND file_id=? AND grantee<>?`, mbox, id, who).Scan(&n)
	if n >= maxGrantsPerEntry {
		return ErrGrantTooMany
	}
	_, err = e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive_grants(mailbox,file_id,grantee,can_write,created) VALUES(?,?,?,?,?)
		ON CONFLICT(mailbox,file_id,grantee) DO UPDATE SET can_write=excluded.can_write`, mbox, id, who, boolInt(write), time.Now().UTC())
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DriveGrants is who entry id is shared with, as its holder's sheet lists
// them.
func (e *Engine) DriveGrants(rd Reader, id int64) ([]DriveGrant, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	return e.grantRows(context.Background(), `SELECT id,mailbox,grantee,can_write,created,file_id FROM vayumail_drive_grants WHERE mailbox=? AND file_id=? ORDER BY grantee`, mbox, id)
}

// DriveUngrant stops one of the holder's shares.
func (e *Engine) DriveUngrant(rd Reader, grant int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	_, err = e.accounts.db.ExecContext(context.Background(), `DELETE FROM vayumail_drive_grants WHERE id=? AND mailbox=?`, grant, mbox)
	return err
}

// DriveSharedWithMe is what other mailboxes here share with this one and
// can be opened now: live, from a holder who is not suspended.
func (e *Engine) DriveSharedWithMe(rd Reader) ([]DriveGrant, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return nil, err
	}
	all, err := e.grantRows(context.Background(), `SELECT g.id,g.mailbox,g.grantee,g.can_write,g.created,g.file_id FROM vayumail_drive_grants g
		JOIN vayumail_accounts a ON a.email=g.mailbox AND a.active=1 WHERE g.grantee=? ORDER BY g.created DESC`, mbox)
	if err != nil {
		return nil, err
	}
	var out []DriveGrant
	for _, g := range all {
		if entry, ok := e.driveLiveEntry(context.Background(), g.Owner, g.Entry.ID); ok {
			g.Entry = entry
			out = append(out, g)
		}
	}
	return out, nil
}

// DriveLeave takes a share off this mailbox's list; its holder sees it gone.
func (e *Engine) DriveLeave(rd Reader, grant int64) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	_, err = e.accounts.db.ExecContext(context.Background(), `DELETE FROM vayumail_drive_grants WHERE id=? AND grantee=?`, grant, mbox)
	return err
}

func (e *Engine) grantRows(ctx context.Context, query string, args ...any) ([]DriveGrant, error) {
	if err := e.accounts.ensureDriveTables(); err != nil {
		return nil, err
	}
	rows, err := e.accounts.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveGrant
	for rows.Next() {
		var g DriveGrant
		if err := rows.Scan(&g.ID, &g.Owner, &g.Grantee, &g.Write, &g.Created, &g.Entry.ID); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// grantOf is share grant as the mailbox it is shared with may use it now:
// its holder not suspended, its entry live.
func (e *Engine) grantOf(ctx context.Context, grantee string, grant int64) (DriveGrant, error) {
	gs, err := e.grantRows(ctx, `SELECT g.id,g.mailbox,g.grantee,g.can_write,g.created,g.file_id FROM vayumail_drive_grants g
		JOIN vayumail_accounts a ON a.email=g.mailbox AND a.active=1 WHERE g.id=? AND g.grantee=?`, grant, grantee)
	if err != nil || len(gs) != 1 {
		return DriveGrant{}, ErrDriveNotFound
	}
	g := gs[0]
	entry, ok := e.driveLiveEntry(ctx, g.Owner, g.Entry.ID)
	if !ok {
		return DriveGrant{}, ErrDriveNotFound
	}
	g.Entry = entry
	return g, nil
}

// grantIn is share grant for rd's mailbox, and entry id inside it with the
// folders from the shared one down to it.
func (e *Engine) grantIn(rd Reader, grant, id int64) (DriveGrant, []DriveEntry, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return DriveGrant{}, nil, err
	}
	ctx := context.Background()
	g, err := e.grantOf(ctx, mbox, grant)
	if err != nil {
		return DriveGrant{}, nil, err
	}
	if id == 0 {
		id = g.Entry.ID
	}
	path, ok := e.within(ctx, g.Owner, g.Entry.ID, id)
	if !ok {
		return DriveGrant{}, nil, ErrDriveNotFound
	}
	return g, path, nil
}

// holderOf is the holder's mailbox as the store reads it for a share.
func holderOf(g DriveGrant) Reader {
	return ReadAsSystem(g.Owner, "shared with "+g.Grantee)
}

// DriveGrantList is folder dir of a shared folder (0 for the shared one),
// with the folders from it down, and what is in it.
func (e *Engine) DriveGrantList(rd Reader, grant, dir int64) (DriveGrant, []DriveEntry, []DriveEntry, error) {
	g, path, err := e.grantIn(rd, grant, dir)
	if err != nil {
		return DriveGrant{}, nil, nil, err
	}
	if !path[len(path)-1].IsDir {
		return DriveGrant{}, nil, nil, ErrDriveNotFound
	}
	list, err := e.DriveList(holderOf(g), path[len(path)-1].ID)
	return g, path, list, err
}

// DriveGrantOpen opens file id of a share: the shared file, or one in a
// shared folder. A sealed file is not handed out: its key is its holder's.
func (e *Engine) DriveGrantOpen(rd Reader, grant, id int64) (DriveEntry, io.ReadSeekCloser, error) {
	g, path, err := e.grantIn(rd, grant, id)
	if err != nil {
		return DriveEntry{}, nil, err
	}
	// A folder is refused by DriveOpen itself.
	if path[len(path)-1].Sealed {
		return DriveEntry{}, nil, ErrDriveNotFound
	}
	return e.DriveOpen(holderOf(g), path[len(path)-1].ID)
}

// grantWrite is the holder's reader for writing into folder dir of a share
// shared to write.
func (e *Engine) grantWrite(rd Reader, grant, dir int64) (DriveGrant, int64, error) {
	g, path, err := e.grantIn(rd, grant, dir)
	if err != nil {
		return DriveGrant{}, 0, err
	}
	at := path[len(path)-1]
	if !g.Write || !at.IsDir {
		return DriveGrant{}, 0, ErrGrantWrite
	}
	return g, at.ID, nil
}

// DriveGrantMkdir makes a folder in folder dir of a share shared to write.
func (e *Engine) DriveGrantMkdir(rd Reader, grant, dir int64, name string) (int64, error) {
	g, at, err := e.grantWrite(rd, grant, dir)
	if err != nil {
		return 0, err
	}
	return e.DriveMkdir(holderOf(g), at, name)
}

// DriveGrantUploadStart opens an upload into folder dir of a share shared
// to write. Its pieces go on as the mailbox's own uploads do; each is
// refused once the share is stopped or no longer to write.
func (e *Engine) DriveGrantUploadStart(rd Reader, grant, dir int64, name, ctype string, size int64, now time.Time) (DriveUpload, error) {
	g, at, err := e.grantWrite(rd, grant, dir)
	if err != nil {
		return DriveUpload{}, err
	}
	return e.driveUploadStart(holderOf(g), at, name, ctype, size, now, uploadOpener{mbox: g.Grantee, grant: g.ID})
}
