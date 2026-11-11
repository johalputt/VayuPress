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

func share(t *testing.T, e *Engine, id int64, password string, ttl time.Duration, most int) string {
	t.Helper()
	token, err := e.DriveShareFile(ankush, id, password, ttl, most)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	return token
}

// fetch is what a press of the link's button gets.
func fetch(e *Engine, token, password string) (string, error) {
	_, f, err := e.DriveSharedOpen(token, password)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b), nil
}

// TestALinkHandsOutTheFileAsItIsNow — to anyone with the token, counted,
// and the newest bytes when the file is saved over.
func TestALinkHandsOutTheFileAsItIsNow(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "terms.txt", "the terms")
	token := share(t, e, id, "", 0, 0)
	if len(token) != 64 {
		t.Fatalf("a token of %d characters", len(token))
	}
	f, err := e.DriveShared(token)
	if err != nil || f.Name != "terms.txt" || f.Size != 9 || f.From != "ankush@example.com" || f.Password {
		t.Fatalf("the link's page: %+v %v", f, err)
	}
	if got, err := fetch(e, token, ""); err != nil || got != "the terms" {
		t.Fatalf("the link hands out %q %v", got, err)
	}
	put(t, e, 0, "terms.txt", "the terms, amended")
	if got, _ := fetch(e, token, ""); got != "the terms, amended" {
		t.Errorf("after the file is saved over the link hands out %q", got)
	}
	links, err := e.DriveShares(ankush, id)
	if err != nil || len(links) != 1 || links[0].Downloads != 2 || links[0].Copy || !links[0].Expires.IsZero() {
		t.Fatalf("the holder's list: %+v %v", links, err)
	}
	// The token is kept nowhere: only its hash, which opens nothing.
	var kept string
	_ = s.db.QueryRow(`SELECT token_hash FROM vayumail_drive_shares`).Scan(&kept)
	if kept == token || kept != hashRecoveryToken(token) {
		t.Errorf("the store keeps %q for the token", kept)
	}
	if _, err := fetch(e, kept, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("the stored hash used as a token: %v", err)
	}
	for _, bad := range []string{"", "x", strings.Repeat("0", 64)} {
		if _, err := e.DriveShared(bad); !errors.Is(err, ErrShareGone) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
}

// TestALinksPasswordIsAskedAndWrongOnesCloseIt — the right one works after
// nine wrong, and the tenth wrong one ends the link for good.
func TestALinksPasswordIsAskedAndWrongOnesCloseIt(t *testing.T) {
	e, _ := driveEngine(t)
	id := put(t, e, 0, "plan.txt", "the plan")
	token := share(t, e, id, "correct horse", 0, 0)
	if f, _ := e.DriveShared(token); !f.Password {
		t.Fatal("the page does not ask for the password")
	}
	for i := 0; i < shareMaxFailures-1; i++ {
		if _, err := fetch(e, token, "wrong"); !errors.Is(err, ErrSharePassword) {
			t.Fatalf("wrong password %d: %v", i+1, err)
		}
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrSharePassword) {
		t.Fatalf("no password, the tenth wrong: %v", err)
	}
	if _, err := fetch(e, token, "correct horse"); !errors.Is(err, ErrShareGone) {
		t.Fatalf("the right password after ten wrong: %v", err)
	}
	if links, _ := e.DriveShares(ankush, id); len(links) != 0 {
		t.Errorf("a closed link is still listed: %+v", links)
	}

	again := share(t, e, id, "correct horse", 0, 0)
	for i := 0; i < shareMaxFailures-1; i++ {
		_, _ = fetch(e, again, "wrong")
	}
	if got, err := fetch(e, again, "correct horse"); err != nil || got != "the plan" {
		t.Fatalf("the right password after nine wrong: %q %v", got, err)
	}
	if links, _ := e.DriveShares(ankush, id); len(links) != 1 || links[0].Downloads != 1 || !links[0].Password {
		t.Errorf("wrong passwords were counted as downloads: %+v", links)
	}
}

// TestALinkEndsAtItsMostDownloads — the page says so, and a press past the
// most gets nothing.
func TestALinkEndsAtItsMostDownloads(t *testing.T) {
	e, _ := driveEngine(t)
	id := put(t, e, 0, "once.txt", "twice only")
	token := share(t, e, id, "", 0, 2)
	for i := 0; i < 2; i++ {
		if _, err := fetch(e, token, ""); err != nil {
			t.Fatalf("download %d: %v", i+1, err)
		}
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Fatalf("a third download of two: %v", err)
	}
	if _, err := e.DriveShared(token); !errors.Is(err, ErrShareGone) {
		t.Fatalf("the page of a used-up link: %v", err)
	}
	if links, _ := e.DriveShares(ankush, id); len(links) != 1 || links[0].Downloads != 2 || links[0].Most != 2 {
		t.Errorf("the holder's list: %+v", links)
	}
}

// TestALinkEndsAtItsTime — and is then removed, not only refused.
func TestALinkEndsAtItsTime(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "soon.txt", "x")
	token := share(t, e, id, "", time.Hour, 0)
	links, _ := e.DriveShares(ankush, id)
	if len(links) != 1 || time.Until(links[0].Expires) < 59*time.Minute || time.Until(links[0].Expires) > time.Hour {
		t.Fatalf("an hour's link: %+v", links)
	}
	if _, err := fetch(e, token, ""); err != nil {
		t.Fatalf("inside its hour: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_drive_shares SET expires=?`, time.Now().Add(-time.Second).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Fatalf("past its end: %v", err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_shares`).Scan(&n)
	if n != 0 {
		t.Errorf("%d ended links still kept", n)
	}
}

// TestAFileInTrashIsNotSharedAndADeletedOnesLinksGo — nor one in a folder
// that is in Trash; restored, its link works again.
func TestAFileInTrashIsNotSharedAndADeletedOnesLinksGo(t *testing.T) {
	e, s := driveEngine(t)
	dir, err := e.DriveMkdir(ankush, 0, "Contracts")
	if err != nil {
		t.Fatal(err)
	}
	id := put(t, e, dir, "deal.txt", "the deal")
	token := share(t, e, id, "", 0, 0)
	for _, what := range []int64{id, dir} {
		if err := e.DriveTrash(ankush, what, false); err != nil {
			t.Fatal(err)
		}
		if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
			t.Errorf("with entry %d in Trash the link hands out the file: %v", what, err)
		}
		if _, err := e.DriveShared(token); !errors.Is(err, ErrShareGone) {
			t.Errorf("with entry %d in Trash the link's page is shown: %v", what, err)
		}
		if _, err := e.DriveShareFile(ankush, id, "", 0, 0); !errors.Is(err, ErrDriveNotFound) {
			t.Errorf("with entry %d in Trash a new link is made: %v", what, err)
		}
		if err := e.DriveTrash(ankush, what, true); err != nil {
			t.Fatal(err)
		}
		if got, err := fetch(e, token, ""); err != nil || got != "the deal" {
			t.Errorf("with entry %d restored: %q %v", what, got, err)
		}
	}
	if _, err := e.DriveShareFile(ankush, dir, "", 0, 0); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("a folder was shared: %v", err)
	}
	if err := e.DriveDelete(ankush, dir); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_shares`).Scan(&n)
	if n != 0 {
		t.Errorf("%d links kept for a deleted file", n)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("a deleted file's link: %v", err)
	}
}

// TestASuspendedMailboxSharesNothing — while it is suspended.
func TestASuspendedMailboxSharesNothing(t *testing.T) {
	e, s := driveEngine(t)
	token := share(t, e, put(t, e, 0, "a.txt", "a"), "", 0, 0)
	if err := s.SetActive(context.Background(), "ankush@example.com", false); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Fatalf("a suspended mailbox's link: %v", err)
	}
	if err := s.SetActive(context.Background(), "ankush@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(e, token, ""); err != nil {
		t.Fatalf("active again: %v", err)
	}
}

// TestASealedFilesLinkIsAnUnsealedCopyCountedAndRemoved — made only while
// the key is open, readable after it is closed, counted in the allowance,
// and gone from the disk when the link is stopped.
func TestASealedFilesLinkIsAnUnsealedCopyCountedAndRemoved(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	dana := ReadAsOwner("dana")
	id, err := e.DrivePut(dana, 0, "secret.txt", "", strings.NewReader("the launch codes"))
	if err != nil {
		t.Fatal(err)
	}
	before, used := len(diskFiles(t, e)), s.DriveUsed(context.Background(), "dana@example.com")

	if err := s.SetQuota(context.Background(), "dana@example.com", used+8); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DriveShareFile(dana, id, "", 0, 0); !errors.Is(err, ErrDriveFull) {
		t.Fatalf("a copy past the allowance: %v", err)
	}
	if err := s.SetQuota(context.Background(), "dana@example.com", 0); err != nil {
		t.Fatal(err)
	}
	shown := e.MailboxUsage("dana@example.com")
	token, err := e.DriveShareFile(dana, id, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The figure a page shows is remembered for a few seconds; the copy is
	// in it at once all the same.
	if now := e.MailboxUsage("dana@example.com"); now != shown+16 {
		t.Errorf("the mailbox's figure went from %d to %d on a 16-byte copy", shown, now)
	}
	links, _ := e.DriveShares(dana, id)
	if len(links) != 1 || !links[0].Copy {
		t.Fatalf("a sealed file's link is not marked as a copy: %+v", links)
	}
	if got := len(diskFiles(t, e)); got != before+1 {
		t.Fatalf("%d files on disk, want the sealed file and one copy", got)
	}
	if now := s.DriveUsed(context.Background(), "dana@example.com"); now != used+16 {
		t.Errorf("the copy is not counted: %d bytes, want %d", now, used+16)
	}
	keys.CloseHeld("dana@example.com")
	if got, err := fetch(e, token, ""); err != nil || got != "the launch codes" {
		t.Fatalf("with the key closed the link hands out %q %v", got, err)
	}
	if f, _ := e.DriveShared(token); f.Size != 16 {
		t.Errorf("the page gives the size as %d", f.Size)
	}
	// With the key closed no link can be made, and nothing is left behind.
	if _, err := e.DriveShareFile(dana, id, "", 0, 0); !errors.Is(err, ErrDriveLocked) {
		t.Fatalf("sharing a sealed file with the key closed: %v", err)
	}
	if got := len(diskFiles(t, e)); got != before+1 {
		t.Errorf("a refused link left %d files on disk", got)
	}
	if err := e.DriveUnshare(dana, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(diskFiles(t, e)); got != before {
		t.Errorf("a stopped link's copy is still on disk: %d files, want %d", got, before)
	}
	if now := s.DriveUsed(context.Background(), "dana@example.com"); now != used {
		t.Errorf("a stopped link's copy is still counted: %d, want %d", now, used)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("a stopped link: %v", err)
	}
}

// TestALinkNeverHandsOutSealedBytes — a file shared while its mailbox was
// plain and saved over once it is private has no copy made for the link:
// the link ends there, it does not hand out what only the key opens.
func TestALinkNeverHandsOutSealedBytes(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := ReadAsOwner("dana")
	id, err := e.DrivePut(dana, 0, "notes.txt", "", strings.NewReader("written in the clear"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := e.DriveShareFile(dana, id, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fetch(e, token, ""); err != nil || got != "written in the clear" {
		t.Fatalf("a plain file's link: %q %v", got, err)
	}
	makePrivate(t, keys)
	if _, err := e.DrivePut(dana, 0, "notes.txt", "", strings.NewReader("written once private")); err != nil {
		t.Fatal(err)
	}
	if got, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Fatalf("the link to a file since sealed hands out %d bytes: %v", len(got), err)
	}
	if _, err := e.DriveShared(token); !errors.Is(err, ErrShareGone) {
		t.Errorf("its page is shown: %v", err)
	}
}

// TestALinkIsItsHoldersToMakeListAndStop — not another holder's, and not
// an administrator's.
func TestALinkIsItsHoldersToMakeListAndStop(t *testing.T) {
	e, s := driveEngine(t)
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana, boss := ReadAsOwner("dana"), ReadAsOperator("ankush", "boss@example.com")
	id := put(t, e, 0, "mine.txt", "ankush's")
	token := share(t, e, id, "", 0, 0)
	links, _ := e.DriveShares(ankush, id)
	if _, err := e.DriveShareFile(dana, id, "", 0, 0); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another holder shared the file: %v", err)
	}
	if got, _ := e.DriveShares(dana, id); len(got) != 0 {
		t.Errorf("another holder lists the links: %+v", got)
	}
	_ = e.DriveUnshare(dana, links[0].ID)
	if _, err := fetch(e, token, ""); err != nil {
		t.Errorf("another holder stopped the link: %v", err)
	}
	if _, err := e.DriveShareFile(boss, id, "", 0, 0); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator shared the file: %v", err)
	}
	if _, err := e.DriveShares(boss, id); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator listed the links: %v", err)
	}
	if err := e.DriveUnshare(boss, links[0].ID); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator stopped the link: %v", err)
	}
	if err := e.DriveUnshare(ankush, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(e, token, ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("the holder's own stop: %v", err)
	}
}

// TestALinksSettingsAreBounded — one refusal per setting.
func TestALinksSettingsAreBounded(t *testing.T) {
	e, _ := driveEngine(t)
	id := put(t, e, 0, "a.txt", "a")
	for name, mk := range map[string]func() (string, error){
		"a short password":       func() (string, error) { return e.DriveShareFile(ankush, id, "1234567", 0, 0) },
		"an end past a year":     func() (string, error) { return e.DriveShareFile(ankush, id, "", ShareMaxTTL+time.Second, 0) },
		"an end in the past":     func() (string, error) { return e.DriveShareFile(ankush, id, "", -time.Second, 0) },
		"too many downloads":     func() (string, error) { return e.DriveShareFile(ankush, id, "", 0, ShareMaxDownloads+1) },
		"fewer than no download": func() (string, error) { return e.DriveShareFile(ankush, id, "", 0, -1) },
	} {
		if _, err := mk(); !errors.Is(err, ErrShareDetails) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for i := 0; i < maxSharesPerFile; i++ {
		share(t, e, id, "", 0, 0)
	}
	if _, err := e.DriveShareFile(ankush, id, "", 0, 0); !errors.Is(err, ErrShareTooMany) {
		t.Errorf("a link past the most a file has: %v", err)
	}
}
