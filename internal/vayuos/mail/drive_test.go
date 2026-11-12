// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func driveEngine(t *testing.T) (*Engine, *AccountStore) {
	t.Helper()
	s := aliasTestStore(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.StorageDir = t.TempDir()
	e := &Engine{cfg: cfg, maildir: NewMaildir(filepath.Join(cfg.StorageDir, "maildir")), accounts: s, db: s.db}
	// As the app gives it one at boot, from its secrets store.
	e.UseDriveKey(bytes.Repeat([]byte{7}, 32))
	return e, s
}

var ankush = ReadAsOwner("ankush")

func put(t *testing.T, e *Engine, parent int64, name, body string) int64 {
	t.Helper()
	id, err := e.DrivePut(ankush, parent, name, "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatalf("put %s: %v", name, err)
	}
	return id
}

func read(t *testing.T, e *Engine, rd Reader, id int64) string {
	t.Helper()
	_, f, err := e.DriveOpen(rd, id)
	if err != nil {
		t.Fatalf("open %d: %v", id, err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func entryNames(ds []DriveEntry) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return strings.Join(out, ",")
}

// diskFiles is every file under the engine's drive directory.
func diskFiles(t *testing.T, e *Engine) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(filepath.Join(e.cfg.StorageDir, "drive"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestFilesAreKeptInFoldersAndReadBack — folders first, then by name, and a
// file's bytes come back as written.
func TestFilesAreKeptInFoldersAndReadBack(t *testing.T) {
	e, _ := driveEngine(t)
	dir, err := e.DriveMkdir(ankush, 0, "Clients")
	if err != nil {
		t.Fatal(err)
	}
	put(t, e, 0, "notes.md", "top")
	id := put(t, e, dir, "contract.pdf", "the contract")
	top, _ := e.DriveList(ankush, 0)
	if entryNames(top) != "Clients,notes.md" || !top[0].IsDir {
		t.Fatalf("the top lists %q", entryNames(top))
	}
	if got := read(t, e, ankush, id); got != "the contract" {
		t.Fatalf("read back %q", got)
	}
	path, _ := e.DrivePath(ankush, id)
	if entryNames(path) != "Clients,contract.pdf" {
		t.Fatalf("the path is %q", entryNames(path))
	}
	if _, err := e.DriveMkdir(ankush, 0, "Clients"); !errors.Is(err, ErrDriveExists) {
		t.Fatalf("a second folder of one name: %v", err)
	}
}

// TestAFilesNameIsNeverAPath — what a name cannot be, and that no name
// reaches the disk.
func TestAFilesNameIsNeverAPath(t *testing.T) {
	e, _ := driveEngine(t)
	for _, bad := range []string{"", " ", ".", "..", "a/b", `a\b`, "line\nbreak", strings.Repeat("x", 256), "nul\x00"} {
		if _, err := e.DrivePut(ankush, 0, bad, "", strings.NewReader("x")); !errors.Is(err, ErrDriveName) {
			t.Errorf("the name %q: %v", bad, err)
		}
		if _, err := e.DriveMkdir(ankush, 0, bad); !errors.Is(err, ErrDriveName) {
			t.Errorf("the folder name %q: %v", bad, err)
		}
	}
	put(t, e, 0, "..hidden ..passwd", "x")
	for _, p := range diskFiles(t, e) {
		if strings.Contains(p, "passwd") || strings.Contains(p, "hidden") {
			t.Fatalf("a name reached the disk: %s", p)
		}
	}
	if _, err := e.blobPath("ankush@example.com", "../../../../etc/passwd"); err == nil {
		t.Fatal("a blob name that is a path was taken")
	}
}

// TestFilesCountAgainstTheSameAllowanceAsMail — a write past it is refused
// and leaves nothing behind, and mail arriving counts the files.
func TestFilesCountAgainstTheSameAllowanceAsMail(t *testing.T) {
	e, s := driveEngine(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE vayumail_accounts SET quota_bytes=1000 WHERE email='ankush@example.com'`); err != nil {
		t.Fatal(err)
	}
	put(t, e, 0, "a.bin", strings.Repeat("a", 600))
	if _, err := e.DrivePut(ankush, 0, "b.bin", "", strings.NewReader(strings.Repeat("b", 600))); !errors.Is(err, ErrDriveFull) {
		t.Fatalf("a write past the allowance: %v", err)
	}
	if n := len(diskFiles(t, e)); n != 1 {
		t.Fatalf("%d files on disk after a refused write, want 1", n)
	}
	if got := s.DriveUsed(ctx, "ankush@example.com"); got != 600 {
		t.Fatalf("used %d", got)
	}
	msg := []byte("From: a@b.example\r\nSubject: big\r\n\r\n" + strings.Repeat("m", 500))
	if _, err := e.DeliverInbound("a@b.example", "ankush@example.com", msg); err == nil {
		t.Fatal("mail was delivered past an allowance the files had used")
	}
}

// TestAFileTooLargeIsRefused — and its part-written bytes are removed.
func TestAFileTooLargeIsRefused(t *testing.T) {
	e, _ := driveEngine(t)
	was := maxDriveFile
	maxDriveFile = 3 * driveChunk
	t.Cleanup(func() { maxDriveFile = was })
	big := io.LimitReader(neverEnding('x'), maxDriveFile+1)
	if _, err := e.DrivePut(ankush, 0, "big.bin", "", big); !errors.Is(err, ErrDriveTooLarge) {
		t.Fatalf("a file past the most: %v", err)
	}
	if n := len(diskFiles(t, e)); n != 0 {
		t.Fatalf("%d files left by a refused upload", n)
	}
}

type neverEnding byte

func (b neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

// TestWritingOverAFileKeepsItsLastFiveVersions — and no more.
func TestWritingOverAFileKeepsItsLastFiveVersions(t *testing.T) {
	e, s := driveEngine(t)
	var id int64
	for i := 0; i < 8; i++ {
		id = put(t, e, 0, "plan.txt", "version "+string(rune('0'+i)))
	}
	if got := read(t, e, ankush, id); got != "version 7" {
		t.Fatalf("the file reads %q", got)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_versions WHERE file_id=?`, id).Scan(&n)
	if n != driveVersions {
		t.Fatalf("%d versions kept, want %d", n, driveVersions)
	}
	if files := len(diskFiles(t, e)); files != driveVersions+1 {
		t.Fatalf("%d files on disk, want the file and its %d versions", files, driveVersions)
	}
	if top, _ := e.DriveList(ankush, 0); len(top) != 1 {
		t.Fatalf("writing over a file made another: %q", entryNames(top))
	}
}

// TestTrashKeepsAFileThirtyDays — out of the folder, back on restore, and
// gone with its bytes when its time is up or it is deleted.
func TestTrashKeepsAFileThirtyDays(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "old.txt", "x")
	if err := e.DriveTrash(ankush, id, false); err != nil {
		t.Fatal(err)
	}
	if top, _ := e.DriveList(ankush, 0); len(top) != 0 {
		t.Fatal("a trashed file is still listed")
	}
	if tr, _ := e.DriveTrashed(ankush); entryNames(tr) != "old.txt" {
		t.Fatalf("Trash holds %q", entryNames(tr))
	}
	put(t, e, 0, "old.txt", "a newer one of the same name")
	if err := e.DriveTrash(ankush, id, true); err != nil {
		t.Fatal(err)
	}
	if top, _ := e.DriveList(ankush, 0); entryNames(top) != "old.txt,old.txt (2)" {
		t.Fatalf("after restoring beside its namesake: %q", entryNames(top))
	}
	_ = e.DriveTrash(ankush, id, false)
	e.emptyOldTrash(time.Now().Add(29 * 24 * time.Hour))
	if tr, _ := e.DriveTrashed(ankush); len(tr) != 1 {
		t.Fatal("Trash was emptied before thirty days")
	}
	e.emptyOldTrash(time.Now().Add(31 * 24 * time.Hour))
	if tr, _ := e.DriveTrashed(ankush); len(tr) != 0 {
		t.Fatal("Trash kept a file past thirty days")
	}
	if n := len(diskFiles(t, e)); n != 1 {
		t.Fatalf("%d files on disk, want only the one still kept", n)
	}
	var rows int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("%d rows left", rows)
	}
}

// TestDeletingAFolderRemovesEverythingInIt — rows and bytes.
func TestDeletingAFolderRemovesEverythingInIt(t *testing.T) {
	e, s := driveEngine(t)
	a, _ := e.DriveMkdir(ankush, 0, "A")
	b, _ := e.DriveMkdir(ankush, a, "B")
	put(t, e, a, "one.txt", "1")
	put(t, e, b, "two.txt", "2")
	put(t, e, b, "two.txt", "2 again")
	keep := put(t, e, 0, "keep.txt", "k")
	if err := e.DriveDelete(ankush, a); err != nil {
		t.Fatal(err)
	}
	var rows, versions int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive`).Scan(&rows)
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_versions`).Scan(&versions)
	if rows != 1 || versions != 0 || len(diskFiles(t, e)) != 1 || read(t, e, ankush, keep) != "k" {
		t.Fatalf("after deleting the folder: %d rows, %d versions, %d files", rows, versions, len(diskFiles(t, e)))
	}
}

// TestAFolderIsNotMovedIntoItself — nor into what is inside it; a move to
// where its name is taken is refused.
func TestAFolderIsNotMovedIntoItself(t *testing.T) {
	e, _ := driveEngine(t)
	a, _ := e.DriveMkdir(ankush, 0, "A")
	b, _ := e.DriveMkdir(ankush, a, "B")
	if err := e.DriveMove(ankush, a, a); !errors.Is(err, ErrDriveMove) {
		t.Fatalf("into itself: %v", err)
	}
	if err := e.DriveMove(ankush, a, b); !errors.Is(err, ErrDriveMove) {
		t.Fatalf("into its own folder: %v", err)
	}
	f := put(t, e, 0, "f.txt", "x")
	put(t, e, b, "f.txt", "y")
	if err := e.DriveMove(ankush, f, b); !errors.Is(err, ErrDriveExists) {
		t.Fatalf("onto a taken name: %v", err)
	}
	if err := e.DriveMove(ankush, f, a); err != nil {
		t.Fatal(err)
	}
	if in, _ := e.DriveList(ankush, a); entryNames(in) != "B,f.txt" {
		t.Fatalf("after the move: %q", entryNames(in))
	}
	if err := e.DriveRename(ankush, f, "B"); !errors.Is(err, ErrDriveExists) {
		t.Fatalf("renamed onto a taken name: %v", err)
	}
}

// TestFilesAreTheirHoldersAlone — an administrator's reader is refused
// every way in, and one mailbox cannot name another's file.
func TestFilesAreTheirHoldersAlone(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "private.txt", "mine")
	op := ReadAsOperator("ankush", "boss@example.com")
	if _, err := e.DriveList(op, 0); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator listed: %v", err)
	}
	if _, _, err := e.DriveOpen(op, id); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator opened: %v", err)
	}
	if _, err := e.DrivePut(op, 0, "x", "", strings.NewReader("x")); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator wrote: %v", err)
	}
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := ReadAsOwner("dana")
	if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another mailbox opened the file: %v", err)
	}
	if err := e.DriveDelete(dana, id); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another mailbox deleted the file: %v", err)
	}
	if err := e.DriveMove(dana, id, 0); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another mailbox moved the file: %v", err)
	}
	if read(t, e, ankush, id) != "mine" {
		t.Error("the file did not survive")
	}
}

// TestAPrivateMailboxsFilesAreSealedOnDisk — unreadable there, opened while
// the key is held open, locked when it is not.
func TestAPrivateMailboxsFilesAreSealedOnDisk(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	dana := ReadAsOwner("dana")
	id, err := e.DrivePut(dana, 0, "secret.txt", "text/plain", strings.NewReader("the launch codes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range diskFiles(t, e) {
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("launch codes")) {
			t.Fatalf("a private mailbox's file is in the clear at %s", p)
		}
	}
	if got := read(t, e, dana, id); got != "the launch codes" {
		t.Fatalf("opened as %q", got)
	}
	keys.CloseHeld("dana@example.com")
	if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveLocked) {
		t.Fatalf("with the key locked: %v", err)
	}
	// Its key is sealed to the holder, not wrapped by the install's.
	var fkey string
	_ = s.db.QueryRow(`SELECT fkey FROM vayumail_drive WHERE id=?`, id).Scan(&fkey)
	if !strings.HasPrefix(fkey, "p1:") {
		t.Errorf("a private file's key is kept as %.8q", fkey)
	}
	// Sealed in chunks, a private file is as large as any other: one past
	// the 25 MB that sealing whole allowed is kept and comes back whole.
	makePrivate(t, keys)
	big := io.LimitReader(neverEnding('x'), 26<<20)
	bigID, err := e.DrivePut(dana, 0, "big.bin", "", big)
	if err != nil {
		t.Fatalf("a 26 MB private file: %v", err)
	}
	_, f, err := e.DriveOpen(dana, bigID)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, f)
	_ = f.Close()
	if err != nil || n != 26<<20 {
		t.Errorf("the 26 MB private file read back %d bytes: %v", n, err)
	}
}

// legacyFile keeps a file as files were kept before chunks: its bytes as
// they are, or sealed whole to the holder, with no key in its row.
func legacyFile(t *testing.T, e *Engine, mbox, name, body string, sealedWhole bool) int64 {
	t.Helper()
	blob, _ := randomHex(16)
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, sealed := []byte(body), 0
	if sealedWhole {
		if data, err = e.maildir.keys.SealTo(mbox, data); err != nil {
			t.Fatal(err)
		}
		sealed = 1
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.accounts.ensureDriveTables(); err != nil {
		t.Fatal(err)
	}
	res, err := e.accounts.db.Exec(`INSERT INTO vayumail_drive(mailbox,parent,name,is_dir,size,blob,sealed,ctype,modified) VALUES(?,0,?,0,?,?,?,'',?)`,
		mbox, name, len(body), blob, sealed, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestADeletedMailboxsFilesAreSetAside — rows gone, bytes moved out of the
// way of the next holder.
func TestADeletedMailboxsFilesAreSetAside(t *testing.T) {
	e, _ := driveEngine(t)
	put(t, e, 0, "left.txt", "behind")
	if _, err := e.DeleteMailbox(context.Background(), "ankush@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.driveDir("ankush@example.com")); !os.IsNotExist(err) {
		t.Fatal("the deleted mailbox's files are where the next holder's would be")
	}
	if n := len(diskFiles(t, e)); n != 1 {
		t.Fatalf("%d files kept aside, want 1", n)
	}
	if got := e.accounts.DriveUsed(context.Background(), "ankush@example.com"); got != 0 {
		t.Fatalf("the address still counts %d bytes of files", got)
	}
}

// TestAnEarlierVersionIsBroughtBackAndTheFileItReplacedIsKept — going back
// is itself undone by going back, and keeps no more bytes than before.
func TestAnEarlierVersionIsBroughtBackAndTheFileItReplacedIsKept(t *testing.T) {
	e, s := driveEngine(t)
	put(t, e, 0, "plan.txt", "one")
	put(t, e, 0, "plan.txt", "two two")
	id := put(t, e, 0, "plan.txt", "three three three")
	vs, err := e.DriveVersions(ankush, id)
	if err != nil || len(vs) != 2 || vs[0].Size != 7 || vs[1].Size != 3 {
		t.Fatalf("versions, newest first: %+v %v", vs, err)
	}
	used := s.DriveUsed(context.Background(), "ankush@example.com")
	if err := e.DriveRestoreVersion(ankush, id, vs[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e, ankush, id); got != "one" {
		t.Fatalf("after bringing the first version back the file reads %q", got)
	}
	d, _ := e.driveEntry(context.Background(), "ankush@example.com", id)
	if d.Size != 3 {
		t.Errorf("the file's size is %d, want the version's 3", d.Size)
	}
	after, _ := e.DriveVersions(ankush, id)
	if len(after) != 2 || after[1].ID != vs[1].ID || after[1].Size != 17 {
		t.Fatalf("the file it replaced is not kept in the version's place: %+v", after)
	}
	if now := s.DriveUsed(context.Background(), "ankush@example.com"); now != used {
		t.Errorf("going back changed the bytes kept from %d to %d", used, now)
	}
	if files := len(diskFiles(t, e)); files != 3 {
		t.Errorf("%d files on disk, want 3", files)
	}
	if err := e.DriveRestoreVersion(ankush, id, vs[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e, ankush, id); got != "three three three" {
		t.Errorf("going back again reads %q", got)
	}
}

// TestAVersionOfAnotherFileOrAnotherHolderBringsNothingBack — a version is
// reached through its own file, by its own holder.
func TestAVersionOfAnotherFileOrAnotherHolderBringsNothingBack(t *testing.T) {
	e, s := driveEngine(t)
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	put(t, e, 0, "a.txt", "a, before")
	a := put(t, e, 0, "a.txt", "a, now")
	b := put(t, e, 0, "b.txt", "b, now")
	vs, _ := e.DriveVersions(ankush, a)
	if len(vs) != 1 {
		t.Fatalf("%d versions of a.txt", len(vs))
	}
	dana := ReadAsOwner("dana")
	mine, err := e.DrivePut(dana, 0, "mine.txt", "", strings.NewReader("dana's"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DriveRestoreVersion(ankush, b, vs[0].ID); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a.txt's version asked of b.txt: %v", err)
	}
	if err := e.DriveRestoreVersion(dana, a, vs[0].ID); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another holder asking for the file's version: %v", err)
	}
	if err := e.DriveRestoreVersion(dana, mine, vs[0].ID); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another holder's version asked of their own file: %v", err)
	}
	// Even a row that wrongly ties one holder's bytes to another's file is
	// not followed: a version is read by its mailbox as well as its file.
	res, err := s.db.Exec(`INSERT INTO vayumail_drive_versions(mailbox,file_id,size,blob,sealed,saved) SELECT mailbox,?,size,blob,sealed,saved FROM vayumail_drive_versions WHERE id=?`, mine, vs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	stray, _ := res.LastInsertId()
	if err := e.DriveRestoreVersion(dana, mine, stray); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a version row of another mailbox, tied to dana's file: %v", err)
	}
	if got, _ := e.DriveVersions(dana, mine); len(got) != 0 {
		t.Errorf("dana's file lists another mailbox's bytes as its version: %+v", got)
	}
	if got, _ := e.DriveVersions(dana, a); len(got) != 0 {
		t.Errorf("another holder lists the file's versions: %+v", got)
	}
	if err := e.DriveRestoreVersion(ReadAsOperator("ankush", "boss@example.com"), a, vs[0].ID); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator bringing a version back: %v", err)
	}
	for id, want := range map[int64]string{a: "a, now", b: "b, now"} {
		if got := read(t, e, ankush, id); got != want {
			t.Errorf("file %d reads %q after the refusals, want %q", id, got, want)
		}
	}
	if got := read(t, e, dana, mine); got != "dana's" {
		t.Errorf("dana's file reads %q", got)
	}
}

// TestAMailboxHasTwoSealedFilesInMemoryAtOnce — a third is told to wait,
// a closed one gives its place back, and another mailbox is not held up.
func TestAMailboxHasTwoSealedFilesInMemoryAtOnce(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	dana := ReadAsOwner("dana")
	// Only a file sealed whole, as files were before chunks, is held in
	// memory when it is opened.
	id := legacyFile(t, e, "dana@example.com", "secret.txt", "sealed", true)
	_, one, err := e.DriveOpen(dana, id)
	if err != nil {
		t.Fatal(err)
	}
	_, two, err := e.DriveOpen(dana, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveBusy) {
		t.Fatalf("a third sealed file opened at once: %v", err)
	}
	// A file kept in chunks, private or not, is read from the disk as it is
	// sent, and is not counted.
	chunked, err := e.DrivePut(dana, 0, "more.txt", "", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("a private file kept while two old ones are open: %v", err)
	}
	plain := put(t, e, 0, "plain.txt", "p")
	for i := 0; i < 4; i++ {
		for who, fid := range map[Reader]int64{ankush: plain, dana: chunked} {
			_, f, err := e.DriveOpen(who, fid)
			if err != nil {
				t.Fatalf("a file kept in chunks, opened %d times at once: %v", i+1, err)
			}
			defer f.Close()
		}
	}
	_ = one.Close()
	_ = one.Close() // closing twice gives one place back, not two
	_, three, err := e.DriveOpen(dana, id)
	if err != nil {
		t.Fatalf("after one was closed: %v", err)
	}
	if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveBusy) {
		t.Fatalf("closing one file twice gave two places back: %v", err)
	}
	_ = two.Close()
	_ = three.Close()
	// A refusal gives its place back too: with the key locked, every open
	// fails, and the mailbox is not left busy by them.
	keys.CloseHeld("dana@example.com")
	for i := 0; i < 5; i++ {
		if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveLocked) {
			t.Fatalf("locked, open %d: %v", i+1, err)
		}
	}
}

// TestGoingPrivateSealsTheFilesAlreadyKept — a file kept in chunks has
// its key sealed to the holder; one kept in the clear before chunks is
// rewritten in chunks under such a key; each version too, whatever its
// size, nothing twice; a link made while a file was readable ends.
func TestGoingPrivateSealsTheFilesAlreadyKept(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := ReadAsOwner("dana")
	keep := func(name, body string) int64 {
		t.Helper()
		id, err := e.DrivePut(dana, 0, name, "", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	keep("plan.txt", "the first words")
	plan := keep("plan.txt", "the second words")
	other := keep("other.txt", "other words")
	old := legacyFile(t, e, "dana@example.com", "old.txt", "words kept in the clear", false)
	token, err := e.DriveShareFile(dana, other, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	sealed, left, err := e.SealStored("dana@example.com")
	if err != nil || sealed != 4 || left != 0 {
		t.Fatalf("sealed %d, left %d, %v; want the file, its earlier version, the other file and the old one", sealed, left, err)
	}
	for _, p := range diskFiles(t, e) {
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("kept in the clear")) {
			t.Errorf("the old file is still readable on the disk at %s", p)
		}
	}
	rows, _ := s.db.Query(`SELECT fkey FROM vayumail_drive WHERE is_dir=0 UNION ALL SELECT fkey FROM vayumail_drive_versions`)
	for rows.Next() {
		var fkey string
		_ = rows.Scan(&fkey)
		if !strings.HasPrefix(fkey, "p1:") {
			t.Errorf("a file's key is kept as %.8q after going private", fkey)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	list, _ := e.DriveList(dana, 0)
	for _, d := range list {
		if !d.Sealed {
			t.Errorf("%s is not marked sealed", d.Name)
		}
	}
	// A second run finds nothing to do, and seals nothing twice.
	if again, _, _ := e.SealStored("dana@example.com"); again != 0 {
		t.Errorf("a second run sealed %d more", again)
	}
	if got := read(t, e, dana, old); got != "words kept in the clear" {
		t.Errorf("the old file opens as %q", got)
	}
	if got := read(t, e, dana, plan); got != "the second words" {
		t.Fatalf("the file opens as %q", got)
	}
	vs, _ := e.DriveVersions(dana, plan)
	if len(vs) != 1 {
		t.Fatalf("%d versions", len(vs))
	}
	if err := e.DriveRestoreVersion(dana, plan, vs[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e, dana, plan); got != "the first words" {
		t.Errorf("its earlier version opens as %q", got)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("a link made while the file was readable: %v", err)
	}
	keys.CloseHeld("dana@example.com")
	for _, id := range []int64{plan, old} {
		if _, _, err := e.DriveOpen(dana, id); !errors.Is(err, ErrDriveLocked) {
			t.Errorf("with the key closed: %v", err)
		}
	}
}

// TestAFileIsNotSealedTwice — a run that listed a file as unsealed, and
// reaches it after another run sealed it, leaves it alone: a key sealed
// once, a file rewritten once.
func TestAFileIsNotSealedTwice(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := ReadAsOwner("dana")
	id, err := e.DrivePut(dana, 0, "plan.txt", "", strings.NewReader("the plan"))
	if err != nil {
		t.Fatal(err)
	}
	old := legacyFile(t, e, "dana@example.com", "old.txt", "the old plan", false)
	blobOf := func(id int64) string {
		var blob string
		_ = s.db.QueryRow(`SELECT blob FROM vayumail_drive WHERE id=?`, id).Scan(&blob)
		return blob
	}
	blob, oldBlob := blobOf(id), blobOf(old)
	makePrivate(t, keys)
	for i := 0; i < 2; i++ {
		for _, b := range []string{blob, oldBlob} {
			if err := e.sealBlob(context.Background(), "dana@example.com", b); err != nil {
				t.Fatalf("seal %d: %v", i+1, err)
			}
		}
	}
	if got := read(t, e, dana, id); got != "the plan" {
		t.Fatalf("after two runs reached it the file opens as %q", got)
	}
	if got := read(t, e, dana, old); got != "the old plan" {
		t.Fatalf("after two runs reached it the old file opens as %q", got)
	}
}

// TestAFileKeptAsNewNeverWritesOverAnother — a taken name gets a number
// before its extension, and the first file is as it was, with no version.
func TestAFileKeptAsNewNeverWritesOverAnother(t *testing.T) {
	e, _ := driveEngine(t)
	first := put(t, e, 0, "invoice.pdf", "march")
	var names []string
	for _, body := range []string{"april", "may"} {
		_, name, err := e.DriveKeepNew(ankush, 0, "invoice.pdf", "application/pdf", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if strings.Join(names, ", ") != "invoice (2).pdf, invoice (3).pdf" {
		t.Fatalf("kept as %q", names)
	}
	if got := read(t, e, ankush, first); got != "march" {
		t.Errorf("the first file now reads %q", got)
	}
	if vs, _ := e.DriveVersions(ankush, first); len(vs) != 0 {
		t.Errorf("the first file was written over: %d versions", len(vs))
	}
	if _, name, err := e.DriveKeepNew(ankush, 0, "notes", "", strings.NewReader("a")); err != nil || name != "notes" {
		t.Errorf("a free name: %q %v", name, err)
	}
	if _, name, _ := e.DriveKeepNew(ankush, 0, "notes", "", strings.NewReader("b")); name != "notes (2)" {
		t.Errorf("a taken name with no extension: %q", name)
	}
	long := strings.Repeat("é", 125) + ".txt" // 254 bytes
	put(t, e, 0, long, "x")
	_, name, err := e.DriveKeepNew(ankush, 0, long, "", strings.NewReader("y"))
	if err != nil || len(name) > 255 || !utf8.ValidString(name) || !strings.HasSuffix(name, " (2).txt") {
		t.Errorf("a long taken name: %d bytes, %q, %v", len(name), name, err)
	}
	if _, _, err := e.DriveKeepNew(ankush, 0, "a/b", "", strings.NewReader("x")); !errors.Is(err, ErrDriveName) {
		t.Errorf("a path as a name: %v", err)
	}
	if _, _, err := e.DriveKeepNew(ReadAsOperator("ankush", "boss@example.com"), 0, "x.txt", "", strings.NewReader("x")); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator: %v", err)
	}
}
