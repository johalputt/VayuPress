// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scheduledEngine starts an engine over a database FILE and a storage folder,
// so a second engine over the same two is the same install after a restart.
func scheduledEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = filepath.Join(dir, "store")
	cfg.InboundEnabled = false
	e := NewEngine(&cfg, loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()); _ = db.Close() })
	return e
}

func held(subject string) ComposeMessage {
	return ComposeMessage{From: "Alice <alice@example.com>", To: []string{"bob@example.com"}, Subject: subject,
		Body: "Hello Bob\r\n\r\n-- \r\nAlice"}
}

func folderCount(t *testing.T, e *Engine, user, folder string) int {
	t.Helper()
	msgs, err := e.maildir.ListFolder("example.com", user, folder)
	if err != nil {
		return 0
	}
	return len(msgs)
}

func scheduleOne(t *testing.T, e *Engine, subject string, due time.Time) int64 {
	t.Helper()
	id, err := e.Schedule(context.Background(), "alice@example.com", held(subject), "Hello Bob", due)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Nothing goes, and no Sent copy is filed, before the due time; at it, the
// message goes once and leaves the schedule; later passes send nothing more.
func TestScheduledWaitsForItsTimeThenSendsOnce(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	due := time.Now().Add(time.Hour).Truncate(time.Second)
	scheduleOne(t, e, "Later", due)

	if n := e.SendDueScheduled(ctx, due.Add(-time.Second)); n != 0 || folderCount(t, e, "bob", "Inbox") != 0 || folderCount(t, e, "alice", "Sent") != 0 {
		t.Fatalf("a second early: sent %d, inbox %d, Sent %d; want nothing", n, folderCount(t, e, "bob", "Inbox"), folderCount(t, e, "alice", "Sent"))
	}
	if n := e.SendDueScheduled(ctx, due); n != 1 {
		t.Fatalf("at the due time sent %d, want 1", n)
	}
	if got := folderCount(t, e, "bob", "Inbox"); got != 1 {
		t.Fatalf("bob's inbox has %d, want 1", got)
	}
	if left, _ := e.ScheduledFor(ctx, "alice@example.com"); len(left) != 0 {
		t.Fatalf("a sent message is still listed as scheduled: %+v", left)
	}
	e.SendDueScheduled(ctx, due.Add(time.Hour))
	if got := folderCount(t, e, "bob", "Inbox"); got != 1 {
		t.Fatalf("a later pass sent it again: bob's inbox has %d", got)
	}
}

// The due time is a wall-clock instant: a clock set back holds the message
// however many passes run, and a clock set forward past it sends it.
func TestScheduledFollowsTheWallClock(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	due := time.Now().Add(time.Hour)
	scheduleOne(t, e, "Clock", due)
	for i := 0; i < 3; i++ {
		e.SendDueScheduled(ctx, due.Add(-48*time.Hour))
	}
	if got := folderCount(t, e, "bob", "Inbox"); got != 0 {
		t.Fatalf("a clock set back sent it: inbox %d", got)
	}
	if n := e.SendDueScheduled(ctx, due.Add(72*time.Hour)); n != 1 {
		t.Fatalf("a clock set forward past the time sent %d, want 1", n)
	}
}

// A restart keeps what is waiting, and sends it when due.
func TestScheduledSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := scheduledEngine(t, dir)
	due := time.Now().Add(time.Hour)
	scheduleOne(t, first, "Restart", due)
	_ = first.Stop(context.Background())

	again := scheduledEngine(t, dir)
	if n := again.SendDueScheduled(context.Background(), due); n != 1 {
		t.Fatalf("after a restart sent %d, want 1", n)
	}
}

// A message the server stopped in the middle of sending (or of cancelling)
// may already have gone: after the restart it is not sent again by itself,
// and says why. One seed per state.
func TestAnInterruptedSendIsHeldNotRepeated(t *testing.T) {
	for _, state := range []string{ScheduledSending, scheduledCancelling} {
		dir := t.TempDir()
		first := scheduledEngine(t, dir)
		due := time.Now().Add(time.Hour)
		id := scheduleOne(t, first, "Interrupted", due)
		if _, err := first.db.Exec(`UPDATE vayumail_scheduled SET state=? WHERE id=?`, state, id); err != nil {
			t.Fatal(err)
		}
		_ = first.Stop(context.Background())

		again := scheduledEngine(t, dir)
		if n := again.SendDueScheduled(context.Background(), due.Add(time.Hour)); n != 0 {
			t.Fatalf("interrupted while %s: sent again (%d)", state, n)
		}
		list, _ := again.ScheduledFor(context.Background(), "alice@example.com")
		if len(list) != 1 || list[0].State != ScheduledFailed || list[0].LastError != interruptedNote {
			t.Fatalf("interrupted while %s: listed as %+v, want failed with the note", state, list)
		}
	}
}

// Cancel puts the message back in Drafts with the body as typed (the
// composer adds the signature again on send) and takes it off the schedule.
func TestCancelFilesTheTypedBodyAsADraft(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	due := time.Now().Add(time.Hour)
	id := scheduleOne(t, e, "Cancel me", due)
	if _, err := e.CancelScheduled(ctx, "alice@example.com", id); err != nil {
		t.Fatal(err)
	}
	if got := folderCount(t, e, "alice", "Drafts"); got != 1 {
		t.Fatalf("Drafts has %d, want the cancelled message", got)
	}
	raw := readMaildirRaw(t, e.cfg.StorageDir)
	if !strings.Contains(raw, "Hello Bob") || strings.Contains(raw, "-- \r\nAlice") {
		t.Fatalf("draft is not the body as typed:\n%s", raw)
	}
	if n := e.SendDueScheduled(ctx, due); n != 0 || folderCount(t, e, "bob", "Inbox") != 0 {
		t.Fatal("a cancelled message was sent")
	}
}

// Another mailbox can neither cancel nor send someone's held message.
func TestScheduledActionsAreTheOwnersOnly(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	id := scheduleOne(t, e, "Mine", time.Now().Add(time.Hour))
	if _, err := e.CancelScheduled(ctx, "mallory@example.com", id); err == nil {
		t.Fatal("another mailbox cancelled it")
	}
	if err := e.SendScheduledNow(ctx, "mallory@example.com", id); err == nil || folderCount(t, e, "bob", "Inbox") != 0 {
		t.Fatal("another mailbox sent it")
	}
	if list, _ := e.ScheduledFor(ctx, "mallory@example.com"); len(list) != 0 {
		t.Fatalf("another mailbox lists it: %+v", list)
	}
	if err := e.SendScheduledNow(ctx, "alice@example.com", id); err != nil || folderCount(t, e, "bob", "Inbox") != 1 {
		t.Fatalf("its owner's Send now: %v", err)
	}
}

// A message being sent cannot be cancelled: the answer says so, and it is
// still there.
func TestASendingMessageCannotBeCancelled(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	id := scheduleOne(t, e, "Busy", time.Now().Add(time.Hour))
	if _, err := e.db.Exec(`UPDATE vayumail_scheduled SET state=? WHERE id=?`, ScheduledSending, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CancelScheduled(ctx, "alice@example.com", id); !errors.Is(err, ErrScheduledBusy) {
		t.Fatalf("cancel of a sending message: %v, want ErrScheduledBusy", err)
	}
	if folderCount(t, e, "alice", "Drafts") != 0 {
		t.Fatal("a sending message was also filed as a draft")
	}
	if err := e.SendScheduledNow(ctx, "alice@example.com", id); !errors.Is(err, ErrScheduledBusy) || folderCount(t, e, "bob", "Inbox") != 0 {
		t.Fatalf("Send now on a message being sent: %v, and it went a second time", err)
	}
}

// A mailbox full at the due time does not send: its Sent copy would not fit.
func TestAFullMailboxAtTheDueTimeDoesNotSend(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "Alice", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := e.maildir.DeliverTo("example.com", "alice", "Inbox", []byte("Subject: big\r\n\r\n"+strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour)
	scheduleOne(t, e, "Full", due)
	if err := e.accounts.SetQuota(ctx, "alice@example.com", 1024); err != nil {
		t.Fatal(err)
	}
	if n := e.SendDueScheduled(ctx, due); n != 0 || folderCount(t, e, "bob", "Inbox") != 0 {
		t.Fatal("a full mailbox's scheduled message was sent")
	}
	list, _ := e.ScheduledFor(ctx, "alice@example.com")
	if len(list) != 1 || !strings.Contains(list[0].LastError, "full") {
		t.Fatalf("listed as %+v, want failed saying the mailbox is full", list)
	}
}

// A mailbox made read-only after scheduling does not send at the due time.
func TestReadOnlyAtTheDueTimeDoesNotSend(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "Alice", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour)
	scheduleOne(t, e, "Read-only", due)
	if err := e.accounts.SetRole(ctx, "alice@example.com", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if n := e.SendDueScheduled(ctx, due); n != 0 || folderCount(t, e, "bob", "Inbox") != 0 {
		t.Fatal("a read-only mailbox's scheduled message was sent")
	}
	list, _ := e.ScheduledFor(ctx, "alice@example.com")
	if len(list) != 1 || list[0].State != ScheduledFailed || !strings.Contains(list[0].LastError, "read-only") {
		t.Fatalf("listed as %+v, want failed saying read-only", list)
	}
}

// Removing a mailbox removes what it had waiting to send.
func TestDeletingAMailboxDropsItsScheduledMail(t *testing.T) {
	e := scheduledEngine(t, t.TempDir())
	ctx := context.Background()
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "Alice", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour)
	scheduleOne(t, e, "Gone", due)
	if err := e.accounts.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if n := e.SendDueScheduled(ctx, due); n != 0 {
		t.Fatalf("a deleted mailbox's scheduled message was sent (%d)", n)
	}
}
