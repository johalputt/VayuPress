package mail

import (
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
