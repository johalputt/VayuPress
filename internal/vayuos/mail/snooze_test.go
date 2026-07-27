// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"testing"
	"time"
)

func TestSnoozeAndWake(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}

	raw := []byte("From: bob@other.com\r\nSubject: later\r\n\r\nx")
	id, err := e.maildir.Deliver("example.com", "ankush", raw)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	// Mark it read first, so the wake can prove it resurfaces unread.
	if nid, merr := e.MarkRead("ankush", "Inbox", id); merr == nil {
		id = nid
	}

	until := time.Now().Add(30 * time.Minute)
	if err := e.Snooze("ankush", "Inbox", id, until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	inbox, _ := e.ListFolder("ankush", "Inbox")
	snoozed, _ := e.ListFolder("ankush", "Snoozed")
	if len(inbox) != 0 || len(snoozed) != 1 {
		t.Fatalf("after snooze: inbox=%d snoozed=%d, want 0/1", len(inbox), len(snoozed))
	}

	// Sweeping BEFORE the wake time must not move it.
	e.sweepSnoozes(time.Now())
	if snoozed, _ = e.ListFolder("ankush", "Snoozed"); len(snoozed) != 1 {
		t.Fatal("swept early")
	}

	// Sweeping AFTER the wake time returns it to the Inbox, unread.
	e.sweepSnoozes(until.Add(time.Minute))
	inbox, _ = e.ListFolder("ankush", "Inbox")
	snoozed, _ = e.ListFolder("ankush", "Snoozed")
	if len(inbox) != 1 || len(snoozed) != 0 {
		t.Fatalf("after wake: inbox=%d snoozed=%d, want 1/0", len(inbox), len(snoozed))
	}
	if inbox[0].Seen {
		t.Fatal("woken message should resurface unread")
	}
	// The wake row is gone: a second sweep is a no-op.
	e.sweepSnoozes(until.Add(time.Hour))
	if inbox, _ = e.ListFolder("ankush", "Inbox"); len(inbox) != 1 {
		t.Fatal("second sweep duplicated the message")
	}

	// Guard rails: past wake time and un-snoozable folders are rejected.
	if err := e.Snooze("ankush", "Inbox", "whatever", time.Now().Add(-time.Hour)); err == nil {
		t.Fatal("past wake time accepted")
	}
	if err := e.Snooze("ankush", "Sent", "x", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("Sent snooze accepted")
	}
	if err := e.Snooze("ankush", "Snoozed", "x", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("Snoozed re-snooze accepted")
	}
}

func TestSnoozeStaleRowDiscarded(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}

	raw := []byte("From: bob@other.com\r\nSubject: gone\r\n\r\nx")
	id, _ := e.maildir.Deliver("example.com", "ankush", raw)
	until := time.Now().Add(10 * time.Minute)
	if err := e.Snooze("ankush", "Inbox", id, until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	// Operator empties the Snoozed folder by hand.
	snoozed, _ := e.ListFolder("ankush", "Snoozed")
	if err := e.DeleteMessage("ankush", "Snoozed", snoozed[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The sweep discards the stale row without error or resurrection.
	e.sweepSnoozes(until.Add(time.Minute))
	if inbox, _ := e.ListFolder("ankush", "Inbox"); len(inbox) != 0 {
		t.Fatal("stale row resurrected a deleted message")
	}
	if rows := s.dueSnoozes(context.Background(), until.Add(time.Hour)); len(rows) != 0 {
		t.Fatalf("stale row not cleared: %+v", rows)
	}
}

// TestSnoozeWakesASecondaryDomainMailbox is the regression test for snooze
// silently doing nothing outside the primary domain.
//
// Snooze rows store a full address. The sweeper split it, kept only the
// localpart, and passed e.cfg.Domain — the PRIMARY domain — whatever domain the
// mailbox was actually on. For a secondary mailbox that looked for the message
// in the primary's Maildir, where it does not exist, so the move failed. The
// error is discarded and the row is cleared regardless (deliberately, to drop
// rows whose message was moved out of Snoozed by hand), so the message stayed in
// Snoozed for ever, never woke, and was never retried.
//
// Nothing surfaced: the user snoozed a message and it simply never came back.
func TestSnoozeWakesASecondaryDomainMailbox(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}

	const mailbox = "bob@shop.example"
	raw := []byte("From: a@partner.test\r\nSubject: later\r\n\r\nx")
	id, err := e.maildir.Deliver("shop.example", "bob", raw)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}

	until := time.Now().Add(30 * time.Minute)
	if err := e.Snooze(mailbox, "Inbox", id, until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	if snoozed, _ := e.ListFolder(mailbox, "Snoozed"); len(snoozed) != 1 {
		t.Fatalf("after snooze: snoozed=%d, want 1", len(snoozed))
	}

	e.sweepSnoozes(until.Add(time.Minute))

	inbox, _ := e.ListFolder(mailbox, "Inbox")
	snoozed, _ := e.ListFolder(mailbox, "Snoozed")
	if len(inbox) != 1 || len(snoozed) != 0 {
		t.Fatalf("a secondary-domain mailbox's snoozed message did not wake: inbox=%d snoozed=%d, want 1/0 "+
			"(it is stranded in Snoozed, and its wake row has been cleared so it will never be retried)",
			len(inbox), len(snoozed))
	}

	// The primary mailbox with the same localpart must be untouched — waking one
	// domain's message into another's Maildir would be worse than not waking it.
	if pi, _ := e.ListFolder("bob@example.com", "Inbox"); len(pi) != 0 {
		t.Errorf("the wake landed in the PRIMARY bob's inbox (%d messages)", len(pi))
	}
}
