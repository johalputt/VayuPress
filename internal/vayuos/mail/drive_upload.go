// SPDX-License-Identifier: Apache-2.0

package mail

// drive_upload.go — a file sent in pieces, and taken up again where it
// stopped.
//
// The page opens an upload with the file's name and size, then sends its
// bytes in pieces of uploadPiece, each sealed into chunks as it arrives
// (driveblob.go) and written after the last. Between pieces the server
// keeps only how much has come, the two SHA-256 states (content and stored
// bytes), and the part-written file: a connection that drops loses at most
// the piece in flight, and the page asks where to go on from. The file is
// a file of Drive only when its last byte has come, and it is checked
// against the allowance when it is opened and when it is done.
//
// Bounds: uploadsPerMailbox open at once; a piece at most uploadPiece and,
// but for the last, a whole number of chunks; one piece at a time per
// upload; an upload left a day is removed by the worker.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	uploadPiece       = 8 << 20
	uploadsPerMailbox = 5
	uploadKeptFor     = 24 * time.Hour
)

var (
	ErrUploadGone   = errors.New("vayumail: that upload is not open; start it again")
	ErrUploadPiece  = errors.New("vayumail: a piece is at most 8 MB, and a whole number of 64 KB but for the last")
	ErrUploadBusy   = errors.New("vayumail: a piece of this upload is already being kept; send the next when it is done")
	ErrUploadsOpen  = errors.New("vayumail: five uploads are open already; finish one first")
	ErrUploadOffset = errors.New("vayumail: that piece is not the next one")
)

// DriveUpload is an open upload: how much of how much has come.
type DriveUpload struct {
	ID       string
	Name     string
	Size     int64
	Received int64
	// File is the Drive file it became, once it is done.
	File int64
}

func (s *AccountStore) ensureUploadTable() error {
	if err := s.ensureDriveTables(); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_uploads(
		id TEXT PRIMARY KEY,
		mailbox TEXT NOT NULL,
		parent INTEGER NOT NULL,
		name TEXT NOT NULL,
		ctype TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL,
		received INTEGER NOT NULL DEFAULT 0,
		blob TEXT NOT NULL,
		fkey TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0,
		content_state BLOB,
		stored_state BLOB,
		touched INTEGER NOT NULL);`)
	return err
}

// uploadLocks keeps one piece of an upload at a time.
var uploadLocks sync.Map

// DriveUploadStart opens an upload of a file of size bytes called name in
// parent.
func (e *Engine) DriveUploadStart(rd Reader, parent int64, name, ctype string, size int64, now time.Time) (DriveUpload, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return DriveUpload{}, err
	}
	if name, err = driveName(name); err != nil {
		return DriveUpload{}, err
	}
	if err := e.accounts.ensureUploadTable(); err != nil {
		return DriveUpload{}, err
	}
	if size < 0 || size > maxDriveFile {
		return DriveUpload{}, ErrDriveTooLarge
	}
	ctx := context.Background()
	if err := e.driveFolder(ctx, mbox, parent); err != nil {
		return DriveUpload{}, err
	}
	if old, taken := e.driveNamed(ctx, mbox, parent, name); taken && old.IsDir {
		return DriveUpload{}, ErrDriveExists
	} else if !taken {
		if err := e.driveRoom(ctx, mbox); err != nil {
			return DriveUpload{}, err
		}
	}
	if quota := e.accounts.QuotaFor(ctx, mbox); quota > 0 && e.mailboxUsage(mbox, false)+size > quota {
		return DriveUpload{}, ErrDriveFull
	}
	var open int
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive_uploads WHERE mailbox=? AND touched>?`, mbox, now.Add(-uploadKeptFor).Unix()).Scan(&open); err != nil {
		return DriveUpload{}, err
	}
	if open >= uploadsPerMailbox {
		return DriveUpload{}, ErrUploadsOpen
	}
	private := e.maildir != nil && e.maildir.keys != nil && e.maildir.keys.HolderKeeps(mbox)
	key, fkey, err := e.newFileKey(mbox, private)
	if err != nil {
		return DriveUpload{}, err
	}
	id, err := randomHex(16)
	if err != nil {
		return DriveUpload{}, err
	}
	blob, err := randomHex(16)
	if err != nil {
		return DriveUpload{}, err
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return DriveUpload{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return DriveUpload{}, err
	}
	f, err := os.OpenFile(path+".part", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return DriveUpload{}, err
	}
	// The header goes down now; the chunks follow piece by piece.
	storedSum := sha256.New()
	_, err = newBlobWriter(io.MultiWriter(f, storedSum), key)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path + ".part")
		return DriveUpload{}, err
	}
	contentState, err := hashState(sha256.New())
	if err != nil {
		return DriveUpload{}, err
	}
	storedState, err := hashState(storedSum)
	if err != nil {
		return DriveUpload{}, err
	}
	sealed := 0
	if private {
		sealed = 1
	}
	if _, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive_uploads(id,mailbox,parent,name,ctype,size,blob,fkey,sealed,content_state,stored_state,touched)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, mbox, parent, name, oneLine(ctype), size, blob, fkey, sealed, contentState, storedState, now.Unix()); err != nil {
		_ = os.Remove(path + ".part")
		return DriveUpload{}, err
	}
	return DriveUpload{ID: id, Name: name, Size: size}, nil
}

func hashState(h hash.Hash) ([]byte, error) {
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("vayumail: a hash that cannot be kept")
	}
	return m.MarshalBinary()
}

func hashFrom(state []byte) (hash.Hash, error) {
	h := sha256.New()
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, errors.New("vayumail: a hash that cannot be taken up")
	}
	return h, u.UnmarshalBinary(state)
}

type openUpload struct {
	mbox, name, ctype, blob, fkey string
	parent, size, received        int64
	sealed                        int
	contentState, storedState     []byte
}

func (e *Engine) uploadOf(ctx context.Context, rd Reader, id string, now time.Time) (openUpload, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return openUpload{}, err
	}
	if err := e.accounts.ensureUploadTable(); err != nil {
		return openUpload{}, err
	}
	u := openUpload{mbox: mbox}
	err = e.accounts.db.QueryRowContext(ctx, `SELECT parent,name,ctype,size,received,blob,fkey,sealed,content_state,stored_state FROM vayumail_drive_uploads
		WHERE id=? AND mailbox=? AND touched>?`, id, mbox, now.Add(-uploadKeptFor).Unix()).
		Scan(&u.parent, &u.name, &u.ctype, &u.size, &u.received, &u.blob, &u.fkey, &u.sealed, &u.contentState, &u.storedState)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrUploadGone
	}
	return u, err
}

// DriveUploadState is where an open upload stands, to go on from.
func (e *Engine) DriveUploadState(rd Reader, id string, now time.Time) (DriveUpload, error) {
	u, err := e.uploadOf(context.Background(), rd, id, now)
	if err != nil {
		return DriveUpload{}, err
	}
	return DriveUpload{ID: id, Name: u.name, Size: u.size, Received: u.received}, nil
}

// DriveUploadPiece keeps the piece of the upload that starts at offset,
// which must be where the upload stands. The last piece makes it a file.
func (e *Engine) DriveUploadPiece(rd Reader, id string, offset int64, r io.Reader, now time.Time) (DriveUpload, error) {
	lock, _ := uploadLocks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := lock.(*sync.Mutex)
	if !mu.TryLock() {
		return DriveUpload{}, ErrUploadBusy
	}
	defer mu.Unlock()
	ctx := context.Background()
	u, err := e.uploadOf(ctx, rd, id, now)
	if err != nil {
		return DriveUpload{}, err
	}
	state := DriveUpload{ID: id, Name: u.name, Size: u.size, Received: u.received}
	if offset != u.received {
		return state, ErrUploadOffset
	}
	want := min(int64(uploadPiece), u.size-u.received)
	piece, err := io.ReadAll(io.LimitReader(r, want+1))
	if err != nil {
		return state, err
	}
	last := u.received+int64(len(piece)) == u.size
	if int64(len(piece)) > want || (!last && len(piece)%driveChunk != 0) {
		return state, ErrUploadPiece
	}
	key, err := e.openFileKey(u.mbox, u.fkey)
	if err != nil {
		return state, err
	}
	path, err := e.blobPath(u.mbox, u.blob)
	if err != nil {
		return state, err
	}
	part := path + ".part"
	// What a piece that failed part way left after the last whole one is
	// cut off before this one is written.
	written := u.received / driveChunk
	if err := os.Truncate(part, blobHeader+written*(driveChunk+blobTagSize)); err != nil {
		return state, ErrUploadGone
	}
	contentSum, err := hashFrom(u.contentState)
	if err != nil {
		return state, err
	}
	storedSum, err := hashFrom(u.storedState)
	if err != nil {
		return state, err
	}
	if err := writePiece(part, key, uint64(written), piece, last, storedSum); err != nil {
		return state, err
	}
	contentSum.Write(piece)
	return e.uploadKept(ctx, id, u, int64(len(piece)), last, contentSum, storedSum, now)
}

// writePiece seals a piece onto the part-written file after its first n
// chunks, closing the file's last chunk when it is the last piece.
func writePiece(part string, key []byte, n uint64, piece []byte, last bool, storedSum hash.Hash) error {
	f, err := os.OpenFile(part, os.O_RDWR, 0o600) // #nosec G304 -- the caller's path is blobPath's
	if err != nil {
		return ErrUploadGone
	}
	header := make([]byte, blobHeader)
	if _, err := f.ReadAt(header, 0); err != nil {
		_ = f.Close()
		return ErrUploadGone
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return err
	}
	bw, err := resumeBlobWriter(io.MultiWriter(f, storedSum), key, header, n)
	if err == nil {
		_, err = bw.Write(piece)
	}
	if err == nil {
		if last {
			err = bw.Close()
		} else {
			err = bw.Flush()
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// uploadKept records a piece kept, and makes the last one a file.
func (e *Engine) uploadKept(ctx context.Context, id string, u openUpload, n int64, last bool, contentSum, storedSum hash.Hash, now time.Time) (DriveUpload, error) {
	received := u.received + n
	state := DriveUpload{ID: id, Name: u.name, Size: u.size, Received: received}
	if !last {
		cs, err := hashState(contentSum)
		if err != nil {
			return state, err
		}
		ss, err := hashState(storedSum)
		if err != nil {
			return state, err
		}
		_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_drive_uploads SET received=?, content_state=?, stored_state=?, touched=? WHERE id=?`, received, cs, ss, now.Unix(), id)
		return state, err
	}
	path, err := e.blobPath(u.mbox, u.blob)
	if err != nil {
		return state, err
	}
	fileID, _, err := e.keepStored(ctx, u.mbox, u.parent, u.name, u.ctype, path+".part", path, storedFile{
		blob: u.blob, fkey: u.fkey, size: u.size, sealed: u.sealed,
		sum: hex.EncodeToString(contentSum.Sum(nil)), blobSum: hex.EncodeToString(storedSum.Sum(nil)),
	})
	_, _ = e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_drive_uploads WHERE id=?`, id)
	uploadLocks.Delete(id)
	if err != nil {
		return state, err
	}
	state.File = fileID
	return state, nil
}

// DriveUploadStop ends an open upload and removes what came of it.
func (e *Engine) DriveUploadStop(rd Reader, id string, now time.Time) error {
	u, err := e.uploadOf(context.Background(), rd, id, now)
	if err != nil {
		return err
	}
	e.dropUpload(u.mbox, id, u.blob)
	return nil
}

func (e *Engine) dropUpload(mbox, id, blob string) {
	if path, err := e.blobPath(mbox, blob); err == nil {
		_ = os.Remove(path + ".part")
	}
	_, _ = e.accounts.db.ExecContext(context.Background(), `DELETE FROM vayumail_drive_uploads WHERE id=?`, id)
	uploadLocks.Delete(id)
}

// sweepUploads removes uploads left untouched for uploadKeptFor.
func (e *Engine) sweepUploads(now time.Time) {
	if e.accounts == nil || e.accounts.ensureUploadTable() != nil {
		return
	}
	rows, err := e.accounts.db.QueryContext(context.Background(), `SELECT id,mailbox,blob FROM vayumail_drive_uploads WHERE touched<=? LIMIT 100`, now.Add(-uploadKeptFor).Unix())
	if err != nil {
		return
	}
	type left struct{ id, mbox, blob string }
	var all []left
	for rows.Next() {
		var l left
		if rows.Scan(&l.id, &l.mbox, &l.blob) == nil {
			all = append(all, l)
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return
	}
	for _, l := range all {
		e.dropUpload(l.mbox, l.id, l.blob)
	}
}
