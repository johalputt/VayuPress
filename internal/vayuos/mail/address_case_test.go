// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"strings"
	"testing"
)

// SECTION 2 AUDIT FINDING — mail accepted, then silently unreachable forever.
//
// Every identity lookup in this system folds case. Every filesystem path does
// not. That mismatch has two halves, and neither needs an attacker:
//
//	DELIVERY. A correspondent's address book holds the display-cased form,
//	so their server sends RCPT TO:<ALICE@example.com>. recipientExists resolves
//	it through RoleFor, which normalises, so the server answers 250 — the
//	sending MTA records the message as delivered and will never retry or bounce
//	it. splitAddress then lowercases only the DOMAIN, so the message lands in
//	<base>/example.com/ALICE/. Alice reads <base>/example.com/alice/. The mail
//	is gone, and nothing anywhere reports a failure.
//
//	LOGIN. POP3 and IMAP derive the mailbox directory from the login string the
//	person typed (pop3d.go: localUser = user[:i]). So Alice signing in as
//	"Alice@example.com" reads a THIRD directory. The same person gets different
//	mail depending on how they capitalise their own address.
//
// A bank statement, an invoice, a password-reset link: accepted with a 250 and
// invisible forever. This is the worst outcome a mail server has, and it fires
// by accident.
//
// The fix is one place — safeSegment, the single function every Maildir path
// component passes through — because the alternative is normalising at each of
// delivery, POP3 login, IMAP login and the retention sweep, and the next caller
// to be added would forget.

func TestMailDeliveredToADisplayCasedAddressIsReadableByItsOwner(t *testing.T) {
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Exactly what an MTA sends when the sender's address book holds the
	// display-cased form. The server has already answered 250 by this point.
	if _, err := md.Deliver("example.com", "ALICE", []byte("Subject: your statement\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	msgs, _ := md.ListFolder("example.com", "alice", "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("alice sees %d messages after delivery to ALICE@example.com, want 1.\n\n"+
			"The sending server was told 250 and will never retry or bounce. The message is "+
			"on this disk and its owner cannot reach it — accepted, then lost, with nothing "+
			"reporting a failure.", len(msgs))
	}
}

// The login half. However the holder capitalises their own address, they must
// land on one mailbox.
func TestTheSameMailboxIsReachedHoweverTheHolderCapitalisesTheirLogin(t *testing.T) {
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := md.Deliver("example.com", "alice", []byte("Subject: hello\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// pop3d.go and imapd.go both derive the directory from the login string, so
	// these are the real values those sessions pass in.
	for _, login := range []string{"alice", "Alice", "ALICE", "aLiCe"} {
		msgs, _ := md.ListFolder("example.com", login, "Inbox")
		if len(msgs) != 1 {
			t.Errorf("signing in as %q shows %d messages, want 1.\n\n"+
				"The same person reaches a different mailbox depending on how they type "+
				"their own address.", login, len(msgs))
		}
	}
}

// And the domain, which DNS treats case-insensitively too.
func TestTheDomainComponentIsCaseInsensitiveToo(t *testing.T) {
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := md.Deliver("EXAMPLE.COM", "alice", []byte("Subject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if msgs, _ := md.ListFolder("example.com", "alice", "Inbox"); len(msgs) != 1 {
		t.Errorf("delivery to EXAMPLE.COM is invisible from example.com (%d messages)", len(msgs))
	}
}

// THE CONTROL, and it is the one that matters most: folding case must not fold
// two different people together. If ALICE and alice are one mailbox, alice and
// bob had better not be.
func TestFoldingCaseDoesNotMergeDifferentMailboxes(t *testing.T) {
	md := NewMaildir(t.TempDir())
	for _, u := range []string{"alice", "bob"} {
		if err := md.CreateAll("example.com", u); err != nil {
			t.Fatalf("create %s: %v", u, err)
		}
	}
	if _, err := md.Deliver("example.com", "bob", []byte("Subject: for bob only\r\n\r\nprivate")); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if msgs, _ := md.ListFolder("example.com", "alice", "Inbox"); len(msgs) != 0 {
		t.Fatalf("alice sees %d of bob's messages — case folding merged two mailboxes", len(msgs))
	}
	if msgs, _ := md.ListFolder("example.com", "bob", "Inbox"); len(msgs) != 1 {
		t.Fatalf("bob has %d messages, want 1 — the fixture did not deliver", len(msgs))
	}
	// Two domains that differ only past the case fold must stay separate too.
	if md.accountDir("example.com", "alice") == md.accountDir("example.net", "alice") {
		t.Error("alice@example.com and alice@example.net resolve to one directory")
	}
}

// The Maildir++ folder directories keep their case, and this is pinned because
// getting it wrong would hide every user's Sent and Junk folder on an existing
// install.
//
// safeSegment folds case, but it has exactly two call sites and both are in
// accountDir (domain, username). folderDir builds "."+canonicalFolder(folder)
// directly, so ".Sent" on disk is never reached by the fold. That separation is
// load-bearing rather than incidental: route a folder name through safeSegment
// and every existing .Sent becomes an unreachable .sent.
func TestFolderDirectoriesKeepTheirCase(t *testing.T) {
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, folder := range StandardFolders {
		if folder == "Inbox" {
			continue // the account root, not a ".Name" subdirectory
		}
		dir := md.folderDir("example.com", "alice", folder)
		if !strings.HasSuffix(dir, "."+folder) {
			t.Errorf("folderDir(%q) = %q, which does not end in %q.\n\n"+
				"Existing installs hold these directories with exactly this casing. "+
				"Folding them renames every user's folder out from under their mail.",
				folder, dir, "."+folder)
		}
	}
}
