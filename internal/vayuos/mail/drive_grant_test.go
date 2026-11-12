// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func withDana(t *testing.T, s *AccountStore) Reader {
	t.Helper()
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	return ReadAsOwner("dana")
}

func grantRead(t *testing.T, e *Engine, rd Reader, grant, id int64) (string, error) {
	t.Helper()
	_, f, err := e.DriveGrantOpen(rd, grant, id)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b), nil
}

func onlyGrant(t *testing.T, e *Engine, rd Reader) DriveGrant {
	t.Helper()
	gs, err := e.DriveSharedWithMe(rd)
	if err != nil || len(gs) != 1 {
		t.Fatalf("shared with me: %+v %v", gs, err)
	}
	return gs[0]
}

// TestAFolderSharedWithAMailboxIsReadThereAndNothingAbove — listed under
// Shared with me, opened with the folders under it, its files read;
// nothing beside or above it, by any id; gone when stopped.
func TestAFolderSharedWithAMailboxIsReadThereAndNothingAbove(t *testing.T) {
	e, s := driveEngine(t)
	dana := withDana(t, s)
	project := mkdir(t, e, 0, "Project")
	drafts := mkdir(t, e, project, "Drafts")
	draft := put(t, e, drafts, "draft.txt", "a draft")
	top := put(t, e, 0, "top.txt", "above it")
	other := mkdir(t, e, 0, "Other")

	if err := e.DriveGrantTo(ankush, project, "dana", false); err != nil {
		t.Fatal(err)
	}
	g := onlyGrant(t, e, dana)
	if g.Owner != "ankush@example.com" || g.Entry.Name != "Project" || g.Write {
		t.Fatalf("the share: %+v", g)
	}
	_, path, list, err := e.DriveGrantList(dana, g.ID, drafts)
	if err != nil || entryNames(list) != "draft.txt" || entryNames(path) != "Project,Drafts" {
		t.Fatalf("a folder in it: %v %v %v", path, list, err)
	}
	if got, err := grantRead(t, e, dana, g.ID, draft); err != nil || got != "a draft" {
		t.Fatalf("a file in it: %q %v", got, err)
	}
	if _, err := grantRead(t, e, dana, g.ID, top); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a file above it: %v", err)
	}
	if _, _, _, err := e.DriveGrantList(dana, g.ID, other); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a folder beside it: %v", err)
	}
	if _, err := grantRead(t, e, dana, g.ID, drafts); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a folder read as a file: %v", err)
	}
	// To read only: nothing is added.
	if _, err := e.DriveGrantMkdir(dana, g.ID, 0, "Mine"); !errors.Is(err, ErrGrantWrite) {
		t.Errorf("a folder made in a share to read: %v", err)
	}
	if _, err := e.DriveGrantUploadStart(dana, g.ID, 0, "x.txt", "", 1, time.Now()); !errors.Is(err, ErrGrantWrite) {
		t.Errorf("an upload into a share to read: %v", err)
	}
	// The share is dana's, not anyone's who has its number.
	if err := s.Create(context.Background(), "eli@example.com", "h", "Eli", "mailbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := grantRead(t, e, ReadAsOwner("eli"), g.ID, draft); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another mailbox read through dana's share: %v", err)
	}
	if _, err := grantRead(t, e, ReadAsOperator("dana", "boss@example.com"), g.ID, draft); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator read through dana's share: %v", err)
	}
	gs, _ := e.DriveGrants(ankush, project)
	if len(gs) != 1 || gs[0].Grantee != "dana@example.com" {
		t.Fatalf("the holder's list: %+v", gs)
	}
	// Stopped by its holder, or left by dana; by no one else.
	_ = e.DriveUngrant(dana, gs[0].ID)
	_ = e.DriveUngrant(ReadAsOwner("eli"), gs[0].ID)
	_ = e.DriveLeave(ReadAsOwner("eli"), gs[0].ID)
	_ = e.DriveLeave(ankush, gs[0].ID)
	if left, _ := e.DriveGrants(ankush, project); len(left) != 1 {
		t.Fatalf("stopped by someone else: %d left", len(left))
	}
	if err := e.DriveUngrant(ankush, gs[0].ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := e.DriveSharedWithMe(dana); len(left) != 0 {
		t.Errorf("%d left after it was stopped", len(left))
	}
	if _, err := grantRead(t, e, dana, g.ID, draft); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("read after it was stopped: %v", err)
	}
}

// TestASharedMailboxLeavesAShare — off its list and the holder's.
func TestASharedMailboxLeavesAShare(t *testing.T) {
	e, s := driveEngine(t)
	dana := withDana(t, s)
	f := put(t, e, 0, "f.txt", "x")
	if err := e.DriveGrantTo(ankush, f, "dana", false); err != nil {
		t.Fatal(err)
	}
	if err := e.DriveLeave(dana, onlyGrant(t, e, dana).ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := e.DriveGrants(ankush, f); len(left) != 0 {
		t.Errorf("%d left after dana left it", len(left))
	}
}

// TestAShareIsOnlyWithAnotherMailboxHere — not with oneself, nor an
// address that is not here; a file is not shared to write.
func TestAShareIsOnlyWithAnotherMailboxHere(t *testing.T) {
	e, s := driveEngine(t)
	withDana(t, s)
	file := put(t, e, 0, "f.txt", "x")
	for _, who := range []string{"ankush", "ankush@example.com", "nobody@example.com", "dana@elsewhere.org", ""} {
		if err := e.DriveGrantTo(ankush, file, who, false); !errors.Is(err, ErrGrantWho) {
			t.Errorf("%q: %v", who, err)
		}
	}
	if err := e.DriveGrantTo(ankush, file, "dana@example.com", true); !errors.Is(err, ErrGrantWrite) {
		t.Errorf("a file shared to write: %v", err)
	}
	if err := e.DriveGrantTo(ankush, file, "Dana@Example.com", false); err != nil {
		t.Fatalf("by its full address: %v", err)
	}
	if err := e.DriveGrantTo(ReadAsOperator("ankush", "boss@example.com"), file, "dana", false); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator shared it: %v", err)
	}
}

// TestASharedFolderToWriteTakesFilesAndFoldersAndKeepsVersions — an
// upload in pieces goes on as the other mailbox's own, into the holder's
// mailbox; a name already there is saved over with a version kept; a share
// stopped mid-upload takes no more of it.
func TestASharedFolderToWriteTakesFilesAndFoldersAndKeepsVersions(t *testing.T) {
	e, s := driveEngine(t)
	dana := withDana(t, s)
	team := mkdir(t, e, 0, "Team")
	plan := put(t, e, team, "plan.txt", "the holder's plan")
	if err := e.DriveGrantTo(ankush, team, "dana", true); err != nil {
		t.Fatal(err)
	}
	g := onlyGrant(t, e, dana)
	sub, err := e.DriveGrantMkdir(dana, g.ID, 0, "From Dana")
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := e.DriveList(ankush, team); entryNames(list) != "From Dana,plan.txt" || list[0].ID != sub {
		t.Fatalf("the holder's folder: %s", entryNames(list))
	}
	now := time.Now()
	body := []byte("dana's plan")
	up, err := e.DriveGrantUploadStart(dana, g.ID, 0, "plan.txt", "text/plain", int64(len(body)), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadPiece(ankush, up.ID, 0, bytes.NewReader(body), now); !errors.Is(err, ErrUploadGone) {
		t.Errorf("the holder went on with dana's upload: %v", err)
	}
	if st, err := e.DriveUploadPiece(dana, up.ID, 0, bytes.NewReader(body), now); err != nil || st.File != plan {
		t.Fatalf("the upload: %+v %v", st, err)
	}
	if read(t, e, ankush, plan) != "dana's plan" {
		t.Error("the file was not saved over")
	}
	if vs, _ := e.DriveVersions(ankush, plan); len(vs) != 1 || readVersion(t, e, ankush, plan, vs[0].ID) != "the holder's plan" {
		t.Errorf("what it was is not kept: %+v", vs)
	}
	if d, _ := e.DriveList(dana, 0); len(d) != 0 {
		t.Errorf("the upload landed in dana's own files: %s", entryNames(d))
	}
	// Stopped mid-upload.
	up, err = e.DriveGrantUploadStart(dana, g.ID, sub, "late.txt", "", 4, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DriveUngrant(ankush, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadPiece(dana, up.ID, 0, bytes.NewReader([]byte("late")), now); !errors.Is(err, ErrUploadGone) {
		t.Errorf("a piece after the share was stopped: %v", err)
	}
}

// TestASharedFolderToReadAgainStopsAnUploadUnderWay — what it may do is
// read at each piece, not only when the upload began.
func TestASharedFolderToReadAgainStopsAnUploadUnderWay(t *testing.T) {
	e, s := driveEngine(t)
	dana := withDana(t, s)
	team := mkdir(t, e, 0, "Team")
	if err := e.DriveGrantTo(ankush, team, "dana", true); err != nil {
		t.Fatal(err)
	}
	g := onlyGrant(t, e, dana)
	up, err := e.DriveGrantUploadStart(dana, g.ID, 0, "x.txt", "", 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DriveGrantTo(ankush, team, "dana", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveUploadPiece(dana, up.ID, 0, bytes.NewReader([]byte("x")), time.Now()); !errors.Is(err, ErrUploadGone) {
		t.Errorf("a piece after the share became to read: %v", err)
	}
}

// TestAShareGoesWithItsEntryItsHolderAndItsAddress — a suspended holder
// shares nothing; Trash hides it; deleting the entry, or either address,
// removes it.
func TestAShareGoesWithItsEntryItsHolderAndItsAddress(t *testing.T) {
	e, s := driveEngine(t)
	dana := withDana(t, s)
	ctx := context.Background()
	f := put(t, e, 0, "f.txt", "x")
	if err := e.DriveGrantTo(ankush, f, "dana", false); err != nil {
		t.Fatal(err)
	}
	g := onlyGrant(t, e, dana)
	if got, err := grantRead(t, e, dana, g.ID, f); err != nil || got != "x" {
		t.Fatalf("the shared file: %q %v", got, err)
	}
	if err := s.SetActive(ctx, "ankush@example.com", false); err != nil {
		t.Fatal(err)
	}
	if gs, _ := e.DriveSharedWithMe(dana); len(gs) != 0 {
		t.Error("a suspended holder's share is listed")
	}
	if _, err := grantRead(t, e, dana, g.ID, f); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a suspended holder's file was read: %v", err)
	}
	if err := s.SetActive(ctx, "ankush@example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := e.DriveTrash(ankush, f, false); err != nil {
		t.Fatal(err)
	}
	if _, err := grantRead(t, e, dana, g.ID, f); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a file in Trash was read: %v", err)
	}
	if err := e.DriveDelete(ankush, f); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_grants`).Scan(&n)
		return n
	}
	if n := count(); n != 0 {
		t.Errorf("%d shares left after the file was deleted", n)
	}
	// The address it was shared with goes: an address issued again
	// inherits nothing.
	f2 := put(t, e, 0, "g.txt", "y")
	if err := e.DriveGrantTo(ankush, f2, "dana", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "dana@example.com"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("%d shares left after the address was deleted", n)
	}
}

// TestAPrivateMailboxSharesNothingWithAnother — its files are sealed to a
// key the other mailbox does not have.
func TestAPrivateMailboxSharesNothingWithAnother(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	dana := withDana(t, s)
	makePrivate(t, keys)
	f, err := e.DrivePut(dana, 0, "secret.txt", "", bytes.NewReader([]byte("s")))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DriveGrantTo(dana, f, "ankush", false); !errors.Is(err, ErrGrantSealed) {
		t.Errorf("a private mailbox shared a file: %v", err)
	}
}
