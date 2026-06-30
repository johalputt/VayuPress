package mail

import (
	"context"
	"strings"
	"testing"
)

func TestMarkReadAndUnread(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	raw := []byte(crlf("From: a@partner.test\nTo: bob@example.com\nSubject: hi\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\n\nbody\n"))
	if _, err := e.DeliverInbound("bob@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	msgs, _ := e.ListFolder("bob", "Inbox")
	if len(msgs) != 1 || msgs[0].Seen {
		t.Fatalf("expected 1 unseen message, got %+v", msgs)
	}
	if _, err := e.MarkRead("bob", "Inbox", msgs[0].ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	msgs, _ = e.ListFolder("bob", "Inbox")
	if len(msgs) != 1 || !msgs[0].Seen {
		t.Fatalf("expected message marked read, got %+v", msgs)
	}
	if _, err := e.MarkUnread("bob", "Inbox", msgs[0].ID); err != nil {
		t.Fatalf("mark unread: %v", err)
	}
	msgs, _ = e.ListFolder("bob", "Inbox")
	if len(msgs) != 1 || msgs[0].Seen {
		t.Fatalf("expected message marked unread, got %+v", msgs)
	}
}

// TestMarkReadStaleID reproduces the 500 the panel hit: after a message is read
// (moved new→cur with an S flag) its old "new/<name>" id is stale, but a second
// action carrying that stale id must still succeed rather than error with ENOENT.
func TestMarkReadStaleID(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	raw := []byte(crlf("From: a@partner.test\nTo: bob@example.com\nSubject: hi\n\nbody\n"))
	if _, err := e.DeliverInbound("bob@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	msgs, _ := e.ListFolder("bob", "Inbox")
	staleID := msgs[0].ID // e.g. new/<name>
	if _, err := e.MarkRead("bob", "Inbox", staleID); err != nil {
		t.Fatalf("first mark read: %v", err)
	}
	// The file has now moved to cur/<name>:2,S, so staleID no longer maps to a
	// real path. A second mark with the stale id must resolve it, not 500.
	if _, err := e.MarkRead("bob", "Inbox", staleID); err != nil {
		t.Fatalf("mark read with stale id must not error: %v", err)
	}
	if _, err := e.MarkUnread("bob", "Inbox", staleID); err != nil {
		t.Fatalf("mark unread with stale id must not error: %v", err)
	}
}

// TestPinPreservesSeen checks that pinning (Maildir 'F') and the seen flag are
// independent: a read message stays read when pinned, and an unread message
// stays unread when pinned.
func TestPinPreservesSeen(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	raw := []byte(crlf("From: a@partner.test\nTo: bob@example.com\nSubject: hi\n\nbody\n"))
	if _, err := e.DeliverInbound("bob@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	id := mustFirstID(t, e, "bob", "Inbox")
	if _, err := e.SetPinned("bob", "Inbox", id, true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	msgs, _ := e.ListFolder("bob", "Inbox")
	if len(msgs) != 1 || !msgs[0].Flagged || msgs[0].Seen {
		t.Fatalf("expected pinned+unseen, got %+v", msgs[0])
	}
	// Reading it keeps the pin.
	if _, err := e.MarkRead("bob", "Inbox", msgs[0].ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	msgs, _ = e.ListFolder("bob", "Inbox")
	if !msgs[0].Flagged || !msgs[0].Seen {
		t.Fatalf("expected pinned+seen after read, got %+v", msgs[0])
	}
	// Unpinning leaves it read.
	if _, err := e.SetPinned("bob", "Inbox", msgs[0].ID, false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	msgs, _ = e.ListFolder("bob", "Inbox")
	if msgs[0].Flagged || !msgs[0].Seen {
		t.Fatalf("expected unpinned+seen, got %+v", msgs[0])
	}
}

func mustFirstID(t *testing.T, e *Engine, user, folder string) string {
	t.Helper()
	msgs, err := e.ListFolder(user, folder)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("list %s: err=%v len=%d", folder, err, len(msgs))
	}
	return msgs[0].ID
}

func TestSaveDraftFilesToDraftsFolder(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	id, err := e.SaveDraft(`"Alice" <alice@example.com>`, []string{"bob@example.com"}, "Draft subject", "draft body")
	if err != nil || id == "" {
		t.Fatalf("save draft: id=%q err=%v", id, err)
	}
	drafts, _ := e.ListFolder("alice", "Drafts")
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}
	if drafts[0].Subject != "Draft subject" || !strings.Contains(drafts[0].To, "bob@example.com") {
		t.Errorf("unexpected draft headers: %+v", drafts[0])
	}
	raw, err := e.ReadFolderMessage("alice", "Drafts", drafts[0].ID)
	if err != nil || !strings.Contains(string(raw), "draft body") {
		t.Errorf("draft body missing: %q (err %v)", raw, err)
	}
}

func TestDKIMPValue(t *testing.T) {
	t.Parallel()
	got := dkimPValue("v=DKIM1; k=rsa; p=MIIBIjANBgkq AB12")
	if got != "MIIBIjANBgkqAB12" {
		t.Errorf("dkimPValue = %q", got)
	}
	if dkimPValue("v=DKIM1; k=rsa") != "" {
		t.Errorf("expected empty p for record without p=")
	}
}

// TestMailboxQuotaEnforced verifies inbound delivery is refused once an account
// is at/over its storage quota, and unlimited (0) never blocks.
func TestMailboxQuotaEnforced(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	ctx := context.Background()
	if err := e.Accounts().Create(ctx, "cap@example.com", "hash", "Cap", RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 2 KB quota.
	if err := e.Accounts().SetQuota(ctx, "cap@example.com", 2048); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	small := []byte(crlf("From: a@p.test\nTo: cap@example.com\nSubject: ok\n\n" + "x" + "\n"))
	if _, err := e.DeliverInbound("cap@example.com", small); err != nil {
		t.Fatalf("small delivery under quota should succeed: %v", err)
	}
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'y'
	}
	raw := append([]byte(crlf("From: a@p.test\nTo: cap@example.com\nSubject: big\n\n")), big...)
	if _, err := e.DeliverInbound("cap@example.com", raw); err == nil {
		t.Fatalf("delivery over quota must be refused")
	}
	// Unlimited (0) never blocks.
	if err := e.Accounts().SetQuota(ctx, "cap@example.com", 0); err != nil {
		t.Fatalf("clear quota: %v", err)
	}
	if _, err := e.DeliverInbound("cap@example.com", raw); err != nil {
		t.Fatalf("unlimited quota should never block: %v", err)
	}
}
