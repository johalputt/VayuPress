// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Your own folders. Each rule is held by its own seed and asserts its own
// reason, so a check that refused everything, or one that let everything
// through, fails here.

func TestFolderNamesAreSafeEverywhereTheyGo(t *testing.T) {
	for name, want := range map[string]bool{
		"Work":                             true,
		"Café 2026":                        true,
		"Q3-reports_x":                     true,
		"":                                 false, // nothing
		strings.Repeat("a", 41):            false, // too long
		" Lead":                            false, // a leading space
		"Trail ":                           false, // a trailing space
		"Sent":                             false, // a standard folder
		"archive":                          false, // a standard folder in another case
		"INBOX":                            false, // Inbox as IMAP spells it
		"Scheduled":                        false, // the list Send later holds
		"a.b":                              false, // a Maildir++ level
		"a/b":                              false, // a path
		"..":                               false, // the parent
		`say "hi"`:                         false, // a quote, which IMAP's strings carry
		"tab\there":                        false, // a control character
		strings.Repeat("é", maxFolderName): true,  // forty characters, not forty bytes
	} {
		if got := ValidFolderName(name); got != want {
			t.Errorf("ValidFolderName(%q) = %v, want %v", name, got, want)
		}
	}
}

func ownFolders(t *testing.T) *Maildir {
	t.Helper()
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatal(err)
	}
	return md
}

func TestAFolderIsMadeOnceAndFoundInAnyCase(t *testing.T) {
	md := ownFolders(t)
	if err := md.CreateFolder("example.com", "bob", "Work"); err != nil {
		t.Fatal(err)
	}
	if got := md.Folders("example.com", "bob"); got[len(got)-1] != "Work" || len(got) != len(StandardFolders)+1 {
		t.Fatalf("Folders = %v, want the standard ones then Work", got)
	}
	if err := md.CreateFolder("example.com", "bob", "work"); !errors.Is(err, ErrFolderExists) {
		t.Fatalf("a second Work in another case: %v, want ErrFolderExists", err)
	}
	if err := md.CreateFolder("example.com", "bob", "Trash"); !errors.Is(err, ErrFolderName) {
		t.Fatalf("a folder named Trash: %v, want ErrFolderName", err)
	}
	// Delivered by any spelling, it lands in Work, not Inbox.
	if _, err := md.DeliverTo("example.com", "bob", "WORK", []byte("Subject: q3\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := md.ListFolder("example.com", "bob", "Work"); len(msgs) != 1 {
		t.Fatalf("Work holds %d, want the message delivered to WORK", len(msgs))
	}
	// A folder that does not exist is still Inbox, as before folders of your own.
	if got := md.resolveFolder("example.com", "bob", "Nowhere"); got != "Inbox" {
		t.Fatalf("resolveFolder(Nowhere) = %q, want Inbox", got)
	}
}

// Only a valid name on disk is a folder: a directory put there by hand with a
// name the console would not make is not listed, so it is never a path the
// console builds.
func TestADirectoryWithAnInvalidNameIsNotAFolder(t *testing.T) {
	md := ownFolders(t)
	if err := os.MkdirAll(filepath.Join(md.accountDir("example.com", "bob"), ".x.y", "cur"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range md.Folders("example.com", "bob") {
		if f == "x.y" {
			t.Fatal("a directory named .x.y is listed as a folder")
		}
	}
}

func TestRenamingAFolderKeepsItsMail(t *testing.T) {
	md := ownFolders(t)
	for _, f := range []string{"Work", "Home"} {
		if err := md.CreateFolder("example.com", "bob", f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := md.DeliverTo("example.com", "bob", "Work", []byte("Subject: kept\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	if err := md.RenameFolder("example.com", "bob", "Work", "Home"); !errors.Is(err, ErrFolderExists) {
		t.Fatalf("rename onto Home: %v, want ErrFolderExists", err)
	}
	if err := md.RenameFolder("example.com", "bob", "Sent", "Outgoing"); !errors.Is(err, ErrNoSuchFolder) {
		t.Fatalf("rename of Sent: %v, want ErrNoSuchFolder", err)
	}
	if err := md.RenameFolder("example.com", "bob", "Work", "Office"); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := md.ListFolder("example.com", "bob", "Office"); len(msgs) != 1 {
		t.Fatalf("Office holds %d after the rename, want Work's message", len(msgs))
	}
	// A change of case alone is the same folder, so it is allowed.
	if err := md.RenameFolder("example.com", "bob", "Office", "office"); err != nil {
		t.Fatalf("rename to another case: %v", err)
	}
}

// Deleting a folder deletes no message: its mail goes to Trash.
func TestDeletingAFolderMovesItsMailToTrash(t *testing.T) {
	md := ownFolders(t)
	if err := md.CreateFolder("example.com", "bob", "Old"); err != nil {
		t.Fatal(err)
	}
	if _, err := md.DeliverTo("example.com", "bob", "Old", []byte("Subject: keep me\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	if err := md.DeleteFolder("example.com", "bob", "Junk"); !errors.Is(err, ErrNoSuchFolder) {
		t.Fatalf("delete of Junk: %v, want ErrNoSuchFolder", err)
	}
	if err := md.DeleteFolder("example.com", "bob", "Old"); err != nil {
		t.Fatal(err)
	}
	if trash, _ := md.ListFolder("example.com", "bob", "Trash"); len(trash) != 1 || trash[0].Subject != "keep me" {
		t.Fatalf("Trash after deleting Old: %+v, want its message", trash)
	}
	if got := md.resolveFolder("example.com", "bob", "Old"); got != "Inbox" {
		t.Fatalf("Old still resolves to %q", got)
	}
}

// A folder of your own counts toward the quota, and search looks in it.
func TestOwnFoldersAreCountedAndSearched(t *testing.T) {
	md := ownFolders(t)
	if err := md.CreateFolder("example.com", "bob", "Work"); err != nil {
		t.Fatal(err)
	}
	before := md.AccountSize("example.com", "bob")
	if _, err := md.DeliverTo("example.com", "bob", "Work", []byte("Subject: needle\r\n\r\n"+strings.Repeat("x", 1000))); err != nil {
		t.Fatal(err)
	}
	if grew := md.AccountSize("example.com", "bob") - before; grew < 1000 {
		t.Fatalf("a message in Work added %d bytes to the mailbox's size", grew)
	}
	if hits, _ := md.Search("example.com", "bob", ParseSearchQuery("needle"), 10); len(hits) != 1 || hits[0].Folder != "Work" {
		t.Fatalf("search for a message in Work: %+v", hits)
	}
}

// IMAP lists your own folders and makes, renames and deletes them, and a
// read-only mailbox may do none of that.
func TestIMAPKeepsOwnFoldersInStep(t *testing.T) {
	srv, md := readOnlyIMAP(t, false)
	resp := converse(t, srv.Addr(), "a LOGIN bob pw", `c CREATE "Work/"`, `l LIST "" "*"`, `r RENAME Work Office`, `x CREATE Sent`, "q LOGOUT")
	mustContain(t, resp, "c OK CREATE completed")
	mustContain(t, resp, `"/" "Work"`)
	mustContain(t, resp, "r OK RENAME completed")
	mustContain(t, resp, "x NO [ALREADYEXISTS]")
	if got := md.resolveFolder("example.com", "bob", "Office"); got != "Office" {
		t.Fatalf("after RENAME, Office resolves to %q", got)
	}
	resp = converse(t, srv.Addr(), "a LOGIN bob pw", "d DELETE Office", "q LOGOUT")
	mustContain(t, resp, "d OK DELETE completed")

	ro, rmd := readOnlyIMAP(t, true)
	resp = converse(t, ro.Addr(), "a LOGIN bob pw", "c CREATE Work", "q LOGOUT")
	mustContain(t, resp, "c NO Mailbox is read-only")
	if len(rmd.Folders("example.com", "bob")) != len(StandardFolders) {
		t.Fatal("a read-only mailbox made a folder over IMAP")
	}
}

// IMAP's UIDs for your own folder are its own, not Inbox's.
func TestOwnFolderUIDsAreNotInboxs(t *testing.T) {
	if folderKey("Work") != "Work" || folderKey("inbox") != "Inbox" || folderKey("a/b") != "Inbox" {
		t.Fatalf("folderKey: Work=%q inbox=%q a/b=%q", folderKey("Work"), folderKey("inbox"), folderKey("a/b"))
	}
}

// A message snoozed from your own folder wakes back into it.
func TestSnoozeFromYourOwnFolderWakesThere(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.maildir.CreateFolder("example.com", "bob", "Work"); err != nil {
		t.Fatal(err)
	}
	id, err := e.maildir.DeliverTo("example.com", "bob", "Work", []byte("Subject: later\r\n\r\nx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Snooze(ReadAsOwner("bob"), "Work", id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.sweepSnoozes(time.Now().Add(2 * time.Hour))
	if work, _ := e.maildir.ListFolder("example.com", "bob", "Work"); len(work) != 1 {
		t.Fatalf("the snoozed message woke elsewhere: Work holds %d", len(work))
	}
}

// A folder's path stays inside the account directory whatever its name,
// independently of ValidFolderName.
func TestAnOwnFolderPathStaysInTheAccount(t *testing.T) {
	if dir, err := ownFolderDir("/m/example.com/bob", "Work"); err != nil || dir != "/m/example.com/bob/.Work" {
		t.Fatalf("Work: %q, %v", dir, err)
	}
	if dir, err := ownFolderDir("/m/example.com/bob", "/../../../alice/cur"); err == nil {
		t.Fatalf("a name climbing out of the account was given %q", dir)
	}
}
