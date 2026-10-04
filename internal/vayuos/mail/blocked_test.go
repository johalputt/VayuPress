// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strings"
	"testing"
)

func newsletter(from string) []byte {
	return []byte("From: Shop <" + from + ">\r\nTo: alice@example.com\r\nSubject: Sale\r\n\r\nbuy\r\n")
}

func count(t *testing.T, e *Engine, user, folder string) int {
	t.Helper()
	msgs, _ := e.maildir.ListFolder("example.com", user, folder)
	return len(msgs)
}

// Blocked by the From a person reads, mail sent from another envelope
// address goes straight to Trash; unblocked, it is back in the inbox.
func TestABlockedFromGoesToTrash(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	if err := e.BlockSender(rd, "Shop <news@shop.test>"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeliverInbound("bounce-77@mailer.shop.test", "alice@example.com", newsletter("news@shop.test")); err != nil {
		t.Fatal(err)
	}
	if count(t, e, "alice", "Inbox") != 0 || count(t, e, "alice", "Trash") != 1 {
		t.Fatalf("blocked by From: inbox %d, trash %d", count(t, e, "alice", "Inbox"), count(t, e, "alice", "Trash"))
	}
	if err := e.UnblockSender(rd, "news@shop.test"); err != nil {
		t.Fatal(err)
	}
	e.DeliverInbound("bounce-78@mailer.shop.test", "alice@example.com", newsletter("news@shop.test"))
	if count(t, e, "alice", "Inbox") != 1 {
		t.Fatal("after Unblock the next message did not reach the inbox")
	}
}

// Blocked by envelope sender, mail goes to Trash too (a local send, or the
// SMTP server's DATA), and a block is one mailbox's alone.
func TestABlockIsTheMailboxsOwn(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.BlockSender(ReadAsOwner("alice"), "spam@x.test"); err != nil {
		t.Fatal(err)
	}
	e.DeliverInbound("spam@x.test", "alice@example.com", []byte("From: Other <other@x.test>\r\nSubject: s\r\n\r\nb\r\n"))
	if count(t, e, "alice", "Trash") != 1 {
		t.Fatal("blocked by envelope sender, the message was not filed in Trash")
	}
	e.DeliverInbound("spam@x.test", "bob@example.com", []byte("From: spam@x.test\r\nSubject: s\r\n\r\nb\r\n"))
	if count(t, e, "bob", "Inbox") != 1 {
		t.Fatal("alice's block kept mail from bob")
	}
	if !e.senderRefused("spam@x.test", "alice@example.com") || e.senderRefused("spam@x.test", "bob@example.com") {
		t.Fatal("the RCPT check does not follow alice's block alone")
	}
}

// At RCPT the blocked recipient is refused and the others of the same
// message are not.
func TestSMTPRefusesTheBlockingRecipientAlone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.SMTPListen = "127.0.0.1:0"
	srv := NewSMTPServer(cfg, func(string, []string, []byte) error { return nil }).
		WithBlockCheck(func(from, rcpt string) bool { return from == "spam@x.test" && rcpt == "alice@example.com" })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())
	conn, br := dialLines(t, srv.Addr())
	defer conn.Close()
	send := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	expectPrefix(t, br, "220")
	send("HELO client.test")
	expectPrefix(t, br, "250")
	send("MAIL FROM:<spam@x.test>")
	expectPrefix(t, br, "250")
	send("RCPT TO:<alice@example.com>")
	if l := expectPrefix(t, br, "550"); !strings.Contains(l, "does not accept mail from this sender") {
		t.Fatalf("alice's RCPT: %q", l)
	}
	send("RCPT TO:<bob@example.com>")
	expectPrefix(t, br, "250")
}

// Some addresses cannot be blocked, and a read-only mailbox may not block.
func TestWhatCannotBeBlocked(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.BlockSender(ReadAsOwner("alice"), "alice@example.com"); err == nil {
		t.Fatal("a mailbox blocked itself")
	}
	if err := e.BlockSender(ReadAsOwner("alice"), "not an address"); err == nil {
		t.Fatal("a non-address was blocked")
	}
	if err := e.accounts.Create(context.Background(), "rev@example.com", "x", "R", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if err := e.BlockSender(ReadAsOwner("rev"), "x@y.test"); err == nil {
		t.Fatal("a read-only mailbox blocked a sender")
	}
}

// Removing a mailbox removes its blocked list: a new holder of the address
// did not choose it.
func TestDeletingAMailboxForgetsItsBlocks(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.accounts.Create(context.Background(), "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if err := e.BlockSender(ReadAsOwner("alice"), "x@y.test"); err != nil {
		t.Fatal(err)
	}
	if err := e.accounts.Delete(context.Background(), "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if e.senderRefused("x@y.test", "alice@example.com") {
		t.Fatal("a deleted mailbox's block outlived it")
	}
}

// Trash and Junk empty for good; nothing else can be emptied.
func TestEmptyingTrashAndJunkOnly(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for _, f := range []string{"Trash", "Trash", "Junk", "Inbox"} {
		e.maildir.DeliverTo("example.com", "alice", f, []byte("Subject: x\r\n\r\ny"))
	}
	if n, err := e.EmptyFolder(rd, "Trash"); err != nil || n != 2 || count(t, e, "alice", "Trash") != 0 {
		t.Fatalf("Empty Trash: %d, %v; %d left", n, err, count(t, e, "alice", "Trash"))
	}
	if n, err := e.EmptyFolder(rd, "junk"); err != nil || n != 1 {
		t.Fatalf("Empty Junk: %d, %v", n, err)
	}
	if _, err := e.EmptyFolder(rd, "Inbox"); err == nil || count(t, e, "alice", "Inbox") != 1 {
		t.Fatal("Inbox was emptied")
	}
}

// The wiring, held apart from the guard: the SMTP test above builds its own
// server with the check, so it passes with the engine never attaching it.
func TestTheEngineWiresTheBlockCheckIntoSMTP(t *testing.T) {
	e := readOnlyEngine(t)
	if e.smtpd == nil {
		t.Fatal("the engine started no SMTP listener; this test would prove nothing")
	}
	if err := e.BlockSender(ReadAsOwner("alice"), "spam@x.test"); err != nil {
		t.Fatal(err)
	}
	if f := e.smtpd.senderBlocked; f == nil || !f("spam@x.test", "alice@example.com") || f("spam@x.test", "bob@example.com") {
		t.Fatal("the engine's SMTP listener does not carry the blocked-sender check")
	}
}
