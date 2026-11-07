// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuos/pgp"
)

const sealPW = "dana's password"

func privateMessage(subject, body string) []byte {
	return []byte("From: sam@example.org\r\nTo: dana@example.com\r\nSubject: " + subject +
		"\r\nMessage-ID: <" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org>\r\nDate: Mon, 2 Nov 2026 10:00:00 +0000\r\n\r\n" + body + "\r\n")
}

// sealedMaildir is a Maildir whose dana@example.com mailbox is private, with
// its VayuPGP engine; the key is held open, as just after it was made private.
func sealedMaildir(t *testing.T) (*Maildir, *pgp.Engine) {
	t.Helper()
	cfg := pgp.DefaultConfig()
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret")
	keys := pgp.NewEngine(&cfg)
	if err := keys.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.GenerateKeypair(&pgp.PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	md := NewMaildir(t.TempDir())
	md.keys = keys
	if err := md.CreateAll("example.com", "dana"); err != nil {
		t.Fatal(err)
	}
	return md, keys
}

func makePrivate(t *testing.T, keys *pgp.Engine) {
	t.Helper()
	if _, err := keys.LockToHolder("dana@example.com", sealPW); err != nil {
		t.Fatal(err)
	}
}

// noClearText fails when any file in the mailbox holds one of words.
func noClearText(t *testing.T, md *Maildir, words ...string) {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(md.accountDir("example.com", "dana"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, _ := os.ReadFile(p)
		for _, w := range words {
			if bytes.Contains(b, []byte(w)) {
				t.Errorf("%s holds %q in the clear", p, w)
			}
		}
		return nil
	})
	if n == 0 {
		t.Fatal("no message files to look at")
	}
}

// TestAPrivateMailboxsMailIsSealedOnDisk — subject and body never reach the
// disk; the holder reads the original while the key is held open, and the
// sealed message once it is closed.
func TestAPrivateMailboxsMailIsSealedOnDisk(t *testing.T) {
	md, keys := sealedMaildir(t)
	makePrivate(t, keys)
	orig := privateMessage("Board pack", "quarterly figures")
	id, err := md.Deliver("example.com", "dana", orig)
	if err != nil {
		t.Fatal(err)
	}
	noClearText(t, md, "Board pack", "quarterly figures")

	got, err := md.ReadRaw("example.com", "dana", "new/"+id)
	if err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("held open, ReadRaw gave %q (%v), want the original", got, err)
	}
	list, _ := md.ListFolder("example.com", "dana", "Inbox")
	if len(list) != 1 || list[0].Subject != "Board pack" || list[0].Size != int64(len(orig)) {
		t.Fatalf("held open, the listing gave %+v, want the subject at the original's size", list)
	}
	if inbox, _ := md.List("example.com", "dana"); len(inbox) != 1 || inbox[0].Subject != "Board pack" {
		t.Fatalf("held open, List gave %+v", inbox)
	}

	keys.CloseHeld("dana@example.com")
	got, _ = md.ReadRaw("example.com", "dana", "new/"+id)
	if bytes.Contains(got, []byte("quarterly figures")) || !bytes.Contains(got, []byte("multipart/encrypted")) {
		t.Fatalf("closed, ReadRaw gave %q, want the sealed message", got)
	}
	list, _ = md.ListFolder("example.com", "dana", "Inbox")
	if len(list) != 1 || list[0].Subject == "Board pack" || list[0].From == "" || list[0].MessageID == "" {
		t.Fatalf("closed, the listing gave %+v, want the sender and id but not the subject", list)
	}
	if inbox, _ := md.List("example.com", "dana"); len(inbox) != 1 || inbox[0].Subject == "Board pack" {
		t.Fatalf("closed, List gave %+v", inbox)
	}
}

// TestEveryWriteToAPrivateMailboxIsSealed — Sent, Drafts and a message that
// arrives already wearing the sealed mark are sealed all the same.
func TestEveryWriteToAPrivateMailboxIsSealed(t *testing.T) {
	md, keys := sealedMaildir(t)
	makePrivate(t, keys)
	forged := append([]byte(sealedMark+": 1\r\n"), privateMessage("Forged", "marked but plain")...)
	for folder, raw := range map[string][]byte{
		"Sent":   privateMessage("Sent one", "sent words"),
		"Drafts": privateMessage("Draft one", "draft words"),
		"Inbox":  forged,
	} {
		if _, err := md.DeliverTo("example.com", "dana", folder, raw); err != nil {
			t.Fatalf("%s: %v", folder, err)
		}
	}
	noClearText(t, md, "sent words", "draft words", "marked but plain")
	list, _ := md.ListFolder("example.com", "dana", "Inbox")
	if len(list) != 1 || list[0].Subject != "Forged" {
		t.Fatalf("the forged-mark message did not open to itself: %+v", list)
	}
}

type failingSeal struct{ MailboxKeys }

func (failingSeal) HolderKeeps(string) bool { return true }
func (failingSeal) SealTo(string, []byte) ([]byte, error) {
	return nil, errors.New("no key")
}

// TestASealThatFailsStoresNothing — never the message in the clear instead.
func TestASealThatFailsStoresNothing(t *testing.T) {
	md := NewMaildir(t.TempDir())
	md.keys = failingSeal{}
	if _, err := md.Deliver("example.com", "dana", privateMessage("x", "y")); err == nil {
		t.Fatal("a failed seal reported a delivery")
	}
	if _, err := md.DeliverTo("example.com", "dana", "Sent", privateMessage("x", "y")); err == nil {
		t.Fatal("a failed seal reported a filed copy")
	}
	_ = filepath.WalkDir(md.accountDir("example.com", "dana"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("a failed seal left %s", p)
		}
		return nil
	})
}

// TestAnOrdinaryMailboxIsStoredAsItCame — the seal is for private mailboxes.
func TestAnOrdinaryMailboxIsStoredAsItCame(t *testing.T) {
	md, _ := sealedMaildir(t)
	orig := privateMessage("Plain", "plain words")
	id, err := md.Deliver("example.com", "dana", orig)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(md.accountDir("example.com", "dana"), "new", id))
	if !bytes.Equal(b, orig) {
		t.Fatal("an ordinary mailbox's mail was changed on the way to disk")
	}
}

// TestStoredMailIsSealedInPlace — the mail from before, in every folder, with
// its name (flags, IMAP identity) and its time (retention) kept.
func TestStoredMailIsSealedInPlace(t *testing.T) {
	md, keys := sealedMaildir(t)
	a, _ := md.Deliver("example.com", "dana", privateMessage("Old one", "old words"))
	if _, err := md.DeliverTo("example.com", "dana", "Archive", privateMessage("Filed", "filed words")); err != nil {
		t.Fatal(err)
	}
	// Stored before the mark was stripped from inbound: marked, never sealed.
	if _, err := md.DeliverTo("example.com", "dana", "Archive", append([]byte(sealedMark+": 1\r\n"), privateMessage("Marked", "marked words")...)); err != nil {
		t.Fatal(err)
	}
	seenID, err := md.setMessageFlags("example.com", "dana", "Inbox", "new/"+a, map[byte]bool{'S': true})
	if err != nil {
		t.Fatal(err)
	}
	seenPath := filepath.Join(md.accountDir("example.com", "dana"), seenID)
	readAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(seenPath, readAt, readAt); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	sealed, left, err := md.SealStored("example.com", "dana")
	if err != nil || sealed != 3 || left != 0 {
		t.Fatalf("sealed %d, left %d (%v), want 3 and 0", sealed, left, err)
	}
	noClearText(t, md, "old words", "filed words", "Old one", "Filed", "marked words")
	info, err := os.Stat(seenPath)
	if err != nil {
		t.Fatalf("the read message lost its name: %v", err)
	}
	if !info.ModTime().Equal(readAt) {
		t.Errorf("its time went from %v to %v; retention counts from it", readAt, info.ModTime())
	}
	if got, _ := md.ReadRawFolder("example.com", "dana", "Inbox", seenID); !bytes.Contains(got, []byte("old words")) {
		t.Errorf("held open, the sealed old message reads %q", got)
	}
	if again, _, _ := md.SealStored("example.com", "dana"); again != 0 {
		t.Errorf("a second run sealed %d again", again)
	}
}

// TestTheSealedMarkCannotBeSentIn — stripped from inbound like the other
// headers the server alone sets.
func TestTheSealedMarkCannotBeSentIn(t *testing.T) {
	out := stripTrustedHeaders([]byte("X-VayuMail-Sealed: 1\r\nSubject: hi\r\n\r\nbody"))
	if isSealed(out) {
		t.Fatalf("inbound kept the sealed mark: %q", out)
	}
}
