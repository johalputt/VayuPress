package mail

import (
	"strings"
	"testing"
)

func TestInboundDeliverListRead(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := NewEngine(&cfg, nil, nil)
	e.maildir = NewMaildir(t.TempDir())

	raw := []byte("From: Alice <alice@partner.test>\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Welcome aboard\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
		"\r\n" +
		"Hello Bob, this is the body.\r\n")

	id, err := e.DeliverInbound("bob@example.com", raw)
	if err != nil || id == "" {
		t.Fatalf("deliver inbound: id=%q err=%v", id, err)
	}

	msgs, err := e.Inbox("", "bob")
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if !strings.Contains(m.From, "alice@partner.test") {
		t.Errorf("from not parsed: %q", m.From)
	}
	if m.Subject != "Welcome aboard" {
		t.Errorf("subject not parsed: %q", m.Subject)
	}
	if m.Seen {
		t.Errorf("freshly delivered message should be unseen (in new/)")
	}

	got, err := e.maildir.ReadRaw("example.com", "bob", m.ID)
	if err != nil {
		t.Fatalf("readraw: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("read content mismatch")
	}
}

func TestReadRawRejectsTraversal(t *testing.T) {
	t.Parallel()
	md := NewMaildir(t.TempDir())
	for _, bad := range []string{"../secret", "new/../../etc/passwd", "cur/..", "tmp/x", "new/", "nope/x"} {
		if _, err := md.ReadRaw("example.com", "bob", bad); err == nil {
			t.Errorf("expected rejection for id %q", bad)
		}
	}
}

func TestSplitAddress(t *testing.T) {
	t.Parallel()
	cases := map[string][2]string{
		"bob@example.com":       {"bob", "example.com"},
		"Bob <bob@Example.COM>": {"bob", "example.com"},
		"noatsign":              {"noatsign", ""},
	}
	for in, want := range cases {
		l, d := splitAddress(in)
		if l != want[0] || d != want[1] {
			t.Errorf("splitAddress(%q) = (%q,%q), want (%q,%q)", in, l, d, want[0], want[1])
		}
	}
}
