// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var uploadNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// brokenReader gives n bytes and then fails, as a connection that drops.
type brokenReader struct {
	r io.Reader
	n int
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.n <= 0 {
		return 0, errors.New("the connection dropped")
	}
	if len(p) > b.n {
		p = p[:b.n]
	}
	k, err := b.r.Read(p)
	b.n -= k
	return k, err
}

// TestAFileIsUploadedInPiecesAndTakenUpAgain — a piece that drops part
// way leaves the upload where it was; sent again it goes on; the file is
// whole, sealed in chunks, with the content's sum, and only a file once its
// last byte has come.
func TestAFileIsUploadedInPiecesAndTakenUpAgain(t *testing.T) {
	e, s := driveEngine(t)
	body := bytes.Repeat([]byte("abcdefgh"), (3*driveChunk+100)/8)
	body = append(body, []byte("tail")...)
	u, err := e.DriveUploadStart(ankush, 0, "report.pdf", "application/pdf", int64(len(body)), uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := e.DriveList(ankush, 0); len(list) != 0 {
		t.Fatal("an upload begun is already a file")
	}
	first := body[:2*driveChunk]
	// The first try drops after a chunk and a half.
	if _, err := e.DriveUploadPiece(ankush, u.ID, 0, &brokenReader{r: bytes.NewReader(first), n: driveChunk + driveChunk/2}, uploadNow); err == nil {
		t.Fatal("a dropped piece was kept")
	}
	if st, _ := e.DriveUploadState(ankush, u.ID, uploadNow); st.Received != 0 {
		t.Fatalf("after a dropped piece the upload stands at %d", st.Received)
	}
	st, err := e.DriveUploadPiece(ankush, u.ID, 0, bytes.NewReader(first), uploadNow)
	if err != nil || st.Received != int64(len(first)) || st.File != 0 {
		t.Fatalf("the first piece: %+v %v", st, err)
	}
	if _, err := e.DriveUploadPiece(ankush, u.ID, 0, bytes.NewReader(first), uploadNow); !errors.Is(err, ErrUploadOffset) {
		t.Errorf("the first piece twice: %v", err)
	}
	// A write cut off by a crash leaves bytes after the last whole piece:
	// they are cut away before the next is written.
	var blobName string
	_ = s.db.QueryRow(`SELECT blob FROM vayumail_drive_uploads WHERE id=?`, u.ID).Scan(&blobName)
	partPath, _ := e.blobPath(calBox, blobName)
	pf, err := os.OpenFile(partPath+".part", os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pf.Write(bytes.Repeat([]byte{0xee}, 777))
	_ = pf.Close()
	// The rest drops too, part way, and is sent again.
	rest := body[len(first):]
	if _, err := e.DriveUploadPiece(ankush, u.ID, int64(len(first)), &brokenReader{r: bytes.NewReader(rest), n: 1000}, uploadNow); err == nil {
		t.Fatal("a dropped last piece was kept")
	}
	st, err = e.DriveUploadPiece(ankush, u.ID, int64(len(first)), bytes.NewReader(rest), uploadNow)
	if err != nil || st.File == 0 {
		t.Fatalf("the last piece: %+v %v", st, err)
	}
	if got := read(t, e, ankush, st.File); got != string(body) {
		t.Fatalf("the file came back as %d bytes, not the %d sent", len(got), len(body))
	}
	_, fkey, sum, blobSum := blobRow(t, s, st.File)
	blob, _, _, _ := blobRow(t, s, st.File)
	path, _ := e.blobPath(calBox, blob)
	stored, _ := os.ReadFile(path)
	if !strings.HasPrefix(fkey, "i1:") || sum != hexSum(body) || blobSum != hexSum(stored) {
		t.Errorf("kept as %.8q with sums %s / %s", fkey, sum, blobSum)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Error("the part-written file is left beside the file")
	}
	if _, err := e.DriveUploadState(ankush, u.ID, uploadNow); !errors.Is(err, ErrUploadGone) {
		t.Errorf("a finished upload is still open: %v", err)
	}
}

// TestAnUploadsPiecesAreBounded — whole chunks but for the last, no more
// than is left, one at a time, the holder's own.
func TestAnUploadsPiecesAreBounded(t *testing.T) {
	e, _ := driveEngine(t)
	u, err := e.DriveUploadStart(ankush, 0, "a.bin", "", 3*driveChunk, uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadPiece(ankush, u.ID, 0, bytes.NewReader(make([]byte, driveChunk+1)), uploadNow); !errors.Is(err, ErrUploadPiece) {
		t.Errorf("a piece of a chunk and a byte: %v", err)
	}
	if _, err := e.DriveUploadPiece(ankush, u.ID, 0, bytes.NewReader(make([]byte, 3*driveChunk+1)), uploadNow); !errors.Is(err, ErrUploadPiece) {
		t.Errorf("a piece past the file's end: %v", err)
	}
	// Whole chunks, but more than is left of a file of two chunks less a
	// byte.
	short, err := e.DriveUploadStart(ankush, 0, "short.bin", "", 2*driveChunk-1, uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadPiece(ankush, short.ID, 0, bytes.NewReader(make([]byte, 2*driveChunk)), uploadNow); !errors.Is(err, ErrUploadPiece) {
		t.Errorf("whole chunks past the file's end: %v", err)
	}
	// A piece of an upload one is already being kept for; that one's lock
	// stays held, as by a request still running.
	held, err := e.DriveUploadStart(ankush, 0, "held.bin", "", driveChunk, uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	lock, _ := uploadLocks.LoadOrStore(held.ID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	if _, err := e.DriveUploadPiece(ankush, held.ID, 0, bytes.NewReader(make([]byte, driveChunk)), uploadNow); !errors.Is(err, ErrUploadBusy) {
		t.Errorf("a piece beside another: %v", err)
	}
	dana := ReadAsOwner("dana")
	if _, err := e.DriveUploadPiece(dana, u.ID, 0, bytes.NewReader(make([]byte, driveChunk)), uploadNow); !errors.Is(err, ErrUploadGone) {
		t.Errorf("another holder's piece: %v", err)
	}
	if err := e.DriveUploadStop(dana, u.ID, uploadNow); !errors.Is(err, ErrUploadGone) {
		t.Errorf("another holder stopped it: %v", err)
	}
	if _, err := e.DriveUploadStart(ReadAsOperator("ankush", "boss@example.com"), 0, "x", "", 1, uploadNow); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator's upload: %v", err)
	}
	if _, err := e.DriveUploadStart(ankush, 0, "x", "", maxDriveFile+1, uploadNow); !errors.Is(err, ErrDriveTooLarge) {
		t.Errorf("a file past the most: %v", err)
	}
}

// TestUploadsAreFewAndEnd — five open at once; one stopped, or left a day,
// is gone with its bytes; one past the allowance is not begun.
func TestUploadsAreFewAndEnd(t *testing.T) {
	e, s := driveEngine(t)
	var ids []string
	for i := 0; i < uploadsPerMailbox; i++ {
		u, err := e.DriveUploadStart(ankush, 0, "f"+string(rune('a'+i)), "", 10, uploadNow)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	if _, err := e.DriveUploadStart(ankush, 0, "g", "", 10, uploadNow); !errors.Is(err, ErrUploadsOpen) {
		t.Errorf("a sixth: %v", err)
	}
	if n := len(diskFiles(t, e)); n != uploadsPerMailbox {
		t.Fatalf("%d part-written files", n)
	}
	if err := e.DriveUploadStop(ankush, ids[0], uploadNow); err != nil {
		t.Fatal(err)
	}
	if n := len(diskFiles(t, e)); n != uploadsPerMailbox-1 {
		t.Errorf("a stopped upload's bytes are left: %d files", n)
	}
	e.sweepUploads(uploadNow.Add(uploadKeptFor - time.Minute))
	if n := len(diskFiles(t, e)); n != uploadsPerMailbox-1 {
		t.Errorf("uploads swept before their day: %d left", n)
	}
	e.sweepUploads(uploadNow.Add(uploadKeptFor + time.Minute))
	if n := len(diskFiles(t, e)); n != 0 {
		t.Errorf("uploads left a day are still on the disk: %d", n)
	}
	if err := s.SetQuota(context.Background(), calBox, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadStart(ankush, 0, "big", "", 1000, uploadNow.Add(48*time.Hour)); !errors.Is(err, ErrDriveFull) {
		t.Errorf("an upload past the allowance: %v", err)
	}
}

// TestAnUploadWritesOverAndSeals — an empty file is a file; an upload of a
// name that is there keeps what was there as a version; a private
// mailbox's upload is sealed to its holder.
func TestAnUploadWritesOverAndSeals(t *testing.T) {
	e, s := driveEngine(t)
	first := put(t, e, 0, "notes.txt", "the first notes")
	u, _ := e.DriveUploadStart(ankush, 0, "notes.txt", "", 0, uploadNow)
	st, err := e.DriveUploadPiece(ankush, u.ID, 0, bytes.NewReader(nil), uploadNow)
	if err != nil || st.File != first {
		t.Fatalf("an empty upload over a file: %+v %v", st, err)
	}
	if got := read(t, e, ankush, first); got != "" {
		t.Errorf("the empty file reads %q", got)
	}
	if vs, _ := e.DriveVersions(ankush, first); len(vs) != 1 {
		t.Errorf("%d versions kept", len(vs))
	}
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	dana := ReadAsOwner("dana")
	u, err = e.DriveUploadStart(dana, 0, "secret.txt", "", 6, uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	st, err = e.DriveUploadPiece(dana, u.ID, 0, strings.NewReader("sealed"), uploadNow)
	if err != nil {
		t.Fatal(err)
	}
	var fkey string
	_ = s.db.QueryRow(`SELECT fkey FROM vayumail_drive WHERE id=?`, st.File).Scan(&fkey)
	if !strings.HasPrefix(fkey, "p1:") || read(t, e, dana, st.File) != "sealed" {
		t.Errorf("a private upload is kept as %.8q", fkey)
	}
}
