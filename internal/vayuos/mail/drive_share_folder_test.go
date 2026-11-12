// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func folderFetch(e *Engine, token, password string, id int64) (string, error) {
	_, f, err := e.DriveSharedFolderOpen(token, password, id)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b), nil
}

func mkdir(t *testing.T, e *Engine, parent int64, name string) int64 {
	t.Helper()
	id, err := e.DriveMkdir(ankush, parent, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestAFolderLinkOpensItsFolderAndNothingAbove — the folder and what is
// under it, to look through and download; nothing beside or above it, by
// any id; nothing in Trash; and the link is gone with its folder.
func TestAFolderLinkOpensItsFolderAndNothingAbove(t *testing.T) {
	e, _ := driveEngine(t)
	project := mkdir(t, e, 0, "Project")
	a := put(t, e, project, "a.txt", "in the project")
	drafts := mkdir(t, e, project, "Drafts")
	b := put(t, e, drafts, "b.txt", "a draft")
	other := mkdir(t, e, 0, "Other")
	c := put(t, e, other, "c.txt", "beside it")
	top := put(t, e, 0, "top.txt", "above it")

	token, err := e.DriveShareFolder(ankush, project, false, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := e.DriveShared(token); err != nil || f.Kind != ShareFolder || f.Name != "Project" {
		t.Fatalf("the link's page: %+v %v", f, err)
	}
	got, err := e.DriveSharedList(token, "", 0)
	if err != nil || entryNames(got.Entries) != "Drafts,a.txt" || len(got.Path) != 1 {
		t.Fatalf("the shared folder: %+v %v", got, err)
	}
	if got, err = e.DriveSharedList(token, "", drafts); err != nil || entryNames(got.Entries) != "b.txt" || entryNames(got.Path) != "Project,Drafts" {
		t.Fatalf("a folder under it: %+v %v", got, err)
	}
	if body, err := folderFetch(e, token, "", b); err != nil || body != "a draft" {
		t.Fatalf("a file under it: %q %v", body, err)
	}
	if links, _ := e.DriveShares(ankush, project); len(links) != 1 || links[0].Downloads != 1 || links[0].Kind != ShareFolder {
		t.Errorf("the holder's list: %+v", links)
	}
	// Beside and above: not there, as a folder or as a file.
	if _, err := e.DriveSharedList(token, "", other); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a folder beside it was listed: %v", err)
	}
	for name, id := range map[string]int64{"c.txt": c, "top.txt": top} {
		if _, err := folderFetch(e, token, "", id); !errors.Is(err, ErrDriveNotFound) {
			t.Errorf("%s was handed out: %v", name, err)
		}
	}
	if _, err := e.DriveSharedList(token, "", a); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a file was listed as a folder: %v", err)
	}
	if _, err := folderFetch(e, token, "", drafts); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a folder was handed out as a file: %v", err)
	}
	if links, _ := e.DriveShares(ankush, project); links[0].Downloads != 1 {
		t.Errorf("refusals were counted as downloads: %d", links[0].Downloads)
	}
	// A folder link is not a file link, nor the other way.
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("a folder link handed itself out as a file: %v", err)
	}
	fileToken := share(t, e, a, "", 0, 0)
	if _, err := e.DriveSharedList(fileToken, "", 0); !errors.Is(err, ErrShareGone) {
		t.Errorf("a file link was listed: %v", err)
	}
	// Trash: a folder in it is not reached, and the shared folder in it
	// takes the link with it.
	if err := e.DriveTrash(ankush, drafts, false); err != nil {
		t.Fatal(err)
	}
	if _, err := folderFetch(e, token, "", b); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a file in a trashed folder was handed out: %v", err)
	}
	if err := e.DriveTrash(ankush, project, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveShared(token); !errors.Is(err, ErrShareGone) {
		t.Errorf("the link outlived its folder going to Trash: %v", err)
	}
}

// TestAFolderLinksPasswordAndMost — a wrong password is refused for a
// listing and a download alike; the most is the files handed out.
func TestAFolderLinksPasswordAndMost(t *testing.T) {
	e, _ := driveEngine(t)
	dir := mkdir(t, e, 0, "Shared")
	one := put(t, e, dir, "one.txt", "1")
	token, err := e.DriveShareFolder(ankush, dir, false, "a long password", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveSharedList(token, "wrong one", 0); !errors.Is(err, ErrSharePassword) {
		t.Errorf("a listing with the wrong password: %v", err)
	}
	if _, err := folderFetch(e, token, "wrong one", one); !errors.Is(err, ErrSharePassword) {
		t.Errorf("a download with the wrong password: %v", err)
	}
	if body, err := folderFetch(e, token, "a long password", one); err != nil || body != "1" {
		t.Fatalf("the right password: %q %v", body, err)
	}
	if _, err := folderFetch(e, token, "a long password", one); !errors.Is(err, ErrShareGone) {
		t.Errorf("a second download of a link with a most of one: %v", err)
	}
	if _, err := e.DriveSharedList(token, "a long password", 0); !errors.Is(err, ErrShareGone) {
		t.Errorf("a used-up link still lists: %v", err)
	}
}

// TestAnUploadLinkTakesFilesWithoutShowingOrWritingOver — an end is
// required; the folder is not shown; a name taken is numbered, never
// written over; a file not kept gives its use back; the most is files.
func TestAnUploadLinkTakesFilesWithoutShowingOrWritingOver(t *testing.T) {
	e, s := driveEngine(t)
	inbox := mkdir(t, e, 0, "Inbox")
	mine := put(t, e, inbox, "plan.txt", "the holder's plan")
	if _, err := e.DriveShareFolder(ankush, inbox, true, "", 0, 0); !errors.Is(err, ErrShareUploadEnds) {
		t.Fatalf("an upload link with no end: %v", err)
	}
	token, err := e.DriveShareFolder(ankush, inbox, true, "", 24*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := e.DriveShared(token); err != nil || f.Kind != ShareUpload || f.Name != "Inbox" {
		t.Fatalf("the link's page: %+v %v", f, err)
	}
	if _, err := e.DriveSharedList(token, "", 0); !errors.Is(err, ErrShareGone) {
		t.Errorf("an upload link listed its folder: %v", err)
	}
	kept, err := e.DriveSharedUpload(token, "", "plan.txt", "text/plain", strings.NewReader("a stranger's plan"))
	if err != nil || kept != "plan (2).txt" {
		t.Fatalf("kept as %q: %v", kept, err)
	}
	if read(t, e, ankush, mine) != "the holder's plan" {
		t.Error("the holder's file was written over")
	}
	if vs, _ := e.DriveVersions(ankush, mine); len(vs) != 0 {
		t.Errorf("the holder's file has %d versions after an upload", len(vs))
	}
	// Not kept: the use is given back.
	used := s.DriveUsed(context.Background(), "ankush@example.com")
	if err := s.SetQuota(context.Background(), "ankush@example.com", used+2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveSharedUpload(token, "", "big.bin", "", strings.NewReader("past the allowance")); !errors.Is(err, ErrDriveFull) {
		t.Fatalf("past the allowance: %v", err)
	}
	if err := s.SetQuota(context.Background(), "ankush@example.com", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveSharedUpload(token, "", "second.txt", "", strings.NewReader("2")); err != nil {
		t.Fatalf("the second use, after one given back: %v", err)
	}
	if _, err := e.DriveSharedUpload(token, "", "third.txt", "", strings.NewReader("3")); !errors.Is(err, ErrShareGone) {
		t.Errorf("a third file on a link with a most of two: %v", err)
	}
	list, _ := e.DriveList(ankush, inbox)
	if entryNames(list) != "plan (2).txt,plan.txt,second.txt" {
		t.Errorf("the folder holds %s", entryNames(list))
	}
}

// TestAKeepNewNeverWritesOverANameTakenWhileArriving — the name is chosen
// again when the file is kept, not only when its bytes began.
func TestAKeepNewNeverWritesOverANameTakenWhileArriving(t *testing.T) {
	e, _ := driveEngine(t)
	var theirs int64
	arriving := readFunc(func(p []byte) (int, error) {
		if theirs == 0 {
			theirs = put(t, e, 0, "notes.txt", "kept meanwhile")
			return copy(p, "arrived"), nil
		}
		return 0, io.EOF
	})
	_, kept, err := e.DriveKeepNew(ankush, 0, "notes.txt", "", arriving)
	if err != nil || kept != "notes (2).txt" {
		t.Fatalf("kept as %q: %v", kept, err)
	}
	if read(t, e, ankush, theirs) != "kept meanwhile" {
		t.Error("the file kept meanwhile was written over")
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

// TestAPrivateMailboxsFolderIsNotLinkedButTakesUploadsSealed — a folder of
// sealed files would need a copy of each in the clear; an upload is
// sealed to the holder as it is kept.
func TestAPrivateMailboxsFolderIsNotLinkedButTakesUploadsSealed(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	dana := ReadAsOwner("dana")
	dir, err := e.DriveMkdir(dana, 0, "Papers")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveShareFolder(dana, dir, false, "", 0, 0); !errors.Is(err, ErrShareSealedFolder) {
		t.Fatalf("a private mailbox's folder link: %v", err)
	}
	token, err := e.DriveShareFolder(dana, dir, true, "", time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveSharedUpload(token, "", "scan.pdf", "", strings.NewReader("a scan")); err != nil {
		t.Fatal(err)
	}
	list, _ := e.DriveList(dana, dir)
	if len(list) != 1 || !list[0].Sealed {
		t.Errorf("the upload was kept as %+v", list)
	}
}

// TestAFolderLinksFileSealedSinceIsNotHandedOut — a mailbox made private
// after its folder was linked: its sealed files stay sealed.
func TestAFolderLinksFileSealedSinceIsNotHandedOut(t *testing.T) {
	e, s := driveEngine(t)
	dir := mkdir(t, e, 0, "Open")
	id := put(t, e, dir, "f.txt", "x")
	token, err := e.DriveShareFolder(ankush, dir, false, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_drive SET sealed=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := folderFetch(e, token, "", id); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a sealed file was handed out: %v", err)
	}
}
