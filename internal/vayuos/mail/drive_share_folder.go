// SPDX-License-Identifier: Apache-2.0

package mail

// drive_share_folder.go — a link to a folder: to look through it and
// download what is in it, or only to upload into it.
//
// A folder link opens the folder and every folder under it, and nothing
// above or beside it: each folder and file it is asked for is walked up to
// the shared folder before it is shown, so an id from elsewhere in the
// mailbox reads as not there. Each file handed out is one of the link's
// downloads.
//
// An upload link lets someone with no account put files into the folder
// without seeing what is in it. Each file is kept under a name of its own,
// numbered when one is taken, so nothing kept is ever written over from
// outside; each counts as one of the link's uses and against the mailbox's
// allowance, and the link must have an end, so an address passed around
// does not take files for ever. A private mailbox's uploads are sealed to
// its holder as they are kept, as mail to it is.
//
// A private mailbox's folder is not shared to look through: its files are
// sealed to the holder, and a link would need a copy of every one in the
// clear. Its files are shared one at a time, each with its copy.

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

// What a link opens.
const (
	ShareFile   = "file"
	ShareFolder = "folder"
	ShareUpload = "upload"
)

var (
	// ErrShareSealedFolder refuses a folder link in a private mailbox.
	ErrShareSealedFolder = errors.New("vayumail: this mailbox's files are sealed to your key, so a folder of them is not shared by link; share its files one at a time")
	// ErrShareUploadEnds refuses an upload link with no end.
	ErrShareUploadEnds = errors.New("vayumail: a link to upload into has an end")
)

// DriveShareFolder makes a link to folder id and gives its token, shown to
// the holder once and kept nowhere: to look through the folder and download
// from it, or with upload only to upload into it. Its settings are a file
// link's, and an upload link must have an end.
func (e *Engine) DriveShareFolder(rd Reader, id int64, upload bool, password string, ttl time.Duration, most int) (string, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return "", err
	}
	if (password != "" && len(password) < ShareMinPassword) || len(password) > 256 || ttl < 0 || ttl > ShareMaxTTL || most < 0 || most > ShareMaxDownloads {
		return "", ErrShareDetails
	}
	kind := ShareFolder
	if upload {
		kind = ShareUpload
		if ttl == 0 {
			return "", ErrShareUploadEnds
		}
	} else if e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(mbox) {
		return "", ErrShareSealedFolder
	}
	ctx := context.Background()
	if dir, ok := e.driveLiveEntry(ctx, mbox, id); !ok || !dir.IsDir {
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
	now := time.Now().UTC()
	if _, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive_shares(mailbox,file_id,token_hash,pass_hash,created,expires,most,kind) VALUES(?,?,?,?,?,?,?,?)`,
		mbox, id, hashRecoveryToken(token), passHash, now, expiresAt(now, ttl), most, kind); err != nil {
		return "", err
	}
	return token, nil
}

// expiresAt is a link's end, nil for none.
func expiresAt(now time.Time, ttl time.Duration) any {
	if ttl > 0 {
		return now.Add(ttl)
	}
	return nil
}

// SharedFolder is one folder of a folder link as its page shows it: where
// it is under the shared folder, and what is in it.
type SharedFolder struct {
	Link SharedFile
	// Path is the folders from the shared one down to this one, the shared
	// one first.
	Path    []DriveEntry
	Entries []DriveEntry
}

// sharedWithin reports whether entry id lies in the link's folder: the
// folder itself or anything under it, out of Trash. The walk goes up from
// the entry, so nothing outside the shared folder is ever listed.
func (e *Engine) sharedWithin(ctx context.Context, s shareRow, id int64) ([]DriveEntry, bool) {
	var path []DriveEntry
	for n, up := 0, id; n <= maxDriveDepth; n++ {
		d, err := e.driveEntry(ctx, s.mbox, up)
		if err != nil || !d.Trashed.IsZero() {
			return nil, false
		}
		path = append([]DriveEntry{d}, path...)
		if up == s.file {
			return path, true
		}
		if d.Parent == 0 {
			return nil, false
		}
		up = d.Parent
	}
	return nil, false
}

// DriveSharedList is a folder link's folder dir (0 for the shared folder
// itself) and what is in it. A wrong password is counted as for a file.
func (e *Engine) DriveSharedList(token, password string, dir int64) (SharedFolder, error) {
	ctx := context.Background()
	s, root, err := e.shareByToken(ctx, token, ShareFolder)
	if err != nil {
		return SharedFolder{}, err
	}
	if err := e.shareAdmits(ctx, s, password); err != nil {
		return SharedFolder{}, err
	}
	if s.most > 0 && s.downloads >= s.most {
		return SharedFolder{}, ErrShareGone
	}
	if dir == 0 {
		dir = s.file
	}
	path, ok := e.sharedWithin(ctx, s, dir)
	if !ok || !path[len(path)-1].IsDir {
		return SharedFolder{}, ErrDriveNotFound
	}
	list, err := e.driveRows(ctx, `SELECT `+driveCols+` FROM vayumail_drive WHERE mailbox=? AND parent=? AND trashed IS NULL ORDER BY is_dir DESC, name COLLATE NOCASE`, s.mbox, dir)
	if err != nil {
		return SharedFolder{}, err
	}
	link := SharedFile{Name: root.Name, From: s.mbox, Password: s.passHash != "", Kind: ShareFolder}
	return SharedFolder{Link: link, Path: path, Entries: list}, nil
}

// DriveSharedFolderOpen hands out file id from a folder link's folder,
// counted as one of its downloads. A file sealed since the link was made
// (its mailbox made private) is not handed out.
func (e *Engine) DriveSharedFolderOpen(token, password string, id int64) (SharedFile, io.ReadCloser, error) {
	ctx := context.Background()
	s, _, err := e.shareByToken(ctx, token, ShareFolder)
	if err != nil {
		return SharedFile{}, nil, err
	}
	if err := e.shareAdmits(ctx, s, password); err != nil {
		return SharedFile{}, nil, err
	}
	path, ok := e.sharedWithin(ctx, s, id)
	if !ok {
		return SharedFile{}, nil, ErrDriveNotFound
	}
	file := path[len(path)-1]
	if file.IsDir || file.Sealed || file.Damaged {
		return SharedFile{}, nil, ErrDriveNotFound
	}
	if err := e.shareTake(ctx, s); err != nil {
		return SharedFile{}, nil, err
	}
	var blob, fkey string
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT blob,fkey FROM vayumail_drive WHERE id=? AND mailbox=?`, id, s.mbox).Scan(&blob, &fkey); err != nil {
		return SharedFile{}, nil, ErrShareGone
	}
	f, err := e.sharedBytes(s.mbox, blob, fkey, file.Size)
	if err != nil {
		return SharedFile{}, nil, err
	}
	return SharedFile{Name: file.Name, Size: file.Size, From: s.mbox, Kind: ShareFolder}, f, nil
}

// DriveSharedUpload keeps one file from an upload link in its folder and
// gives the name it was kept under. The use is counted before the file is
// read and given back when it is not kept, so the link's most is never
// passed by uploads at once.
func (e *Engine) DriveSharedUpload(token, password, name, ctype string, r io.Reader) (string, error) {
	ctx := context.Background()
	s, _, err := e.shareByToken(ctx, token, ShareUpload)
	if err != nil {
		return "", err
	}
	if err := e.shareAdmits(ctx, s, password); err != nil {
		return "", err
	}
	if err := e.shareTake(ctx, s); err != nil {
		return "", err
	}
	_, kept, err := e.DriveKeepNew(ReadAsSystem(s.mbox, "a link to upload into"), s.file, name, ctype, r)
	if err != nil {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive_shares SET downloads=downloads-1 WHERE id=? AND downloads>0`, s.id)
		return "", err
	}
	return kept, nil
}
