// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestFilterCRUDAndValidation(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()
	mbox := "ankush@example.com"

	ok := FilterRule{Mailbox: mbox, Field: "from", Contains: "newsletter@", Action: "move", Target: "Archive"}
	if err := s.CreateFilter(ctx, ok); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Vocabulary enforcement.
	bad := []FilterRule{
		{Mailbox: mbox, Field: "body", Contains: "x", Action: "move", Target: "Archive"}, // bad field
		{Mailbox: mbox, Field: "from", Contains: "x", Action: "explode"},                 // bad action
		{Mailbox: mbox, Field: "from", Contains: "x", Action: "move", Target: "Nope"},    // bad folder
		{Mailbox: mbox, Field: "from", Contains: "", Action: "pin"},                      // empty match
	}
	for i, r := range bad {
		if err := s.CreateFilter(ctx, r); err == nil {
			t.Errorf("bad rule %d accepted", i)
		}
	}
	rules, err := s.FiltersFor(ctx, mbox)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %v (err %v), want 1", rules, err)
	}
	// Deleting with the wrong mailbox must not remove the rule.
	_ = s.DeleteFilter(ctx, "other@example.com", rules[0].ID)
	if rr, _ := s.FiltersFor(ctx, mbox); len(rr) != 1 {
		t.Fatal("cross-mailbox delete removed the rule")
	}
	_ = s.DeleteFilter(ctx, mbox, rules[0].ID)
	if rr, _ := s.FiltersFor(ctx, mbox); len(rr) != 0 {
		t.Fatal("delete failed")
	}
}

func TestFilterMatching(t *testing.T) {
	t.Parallel()
	raw := []byte("From: News <NEWSLETTER@big.co>\r\nTo: me@x.y\r\nCc: team@x.y\r\nSubject: Weekly Update\r\n\r\nbody subject: hidden")
	cases := []struct {
		rule FilterRule
		want bool
	}{
		{FilterRule{Field: "from", Contains: "newsletter@"}, true}, // case-insensitive
		{FilterRule{Field: "to", Contains: "team@"}, true},         // Cc counts as to
		{FilterRule{Field: "subject", Contains: "weekly"}, true},   //
		{FilterRule{Field: "subject", Contains: "hidden"}, false},  // body must not match
		{FilterRule{Field: "from", Contains: "nobody"}, false},     //
	}
	for i, c := range cases {
		if got := matchFilter(c.rule, raw); got != c.want {
			t.Errorf("case %d: match=%v want %v", i, got, c.want)
		}
	}
}

func TestFilterDeliveryEndToEnd(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()
	mbox := "ankush@example.com"
	// Rule 1: newsletters → Archive. Rule 2 (later): anything from spammer → Junk.
	_ = s.CreateFilter(ctx, FilterRule{Mailbox: mbox, Field: "from", Contains: "news@", Action: "move", Target: "Archive"})
	_ = s.CreateFilter(ctx, FilterRule{Mailbox: mbox, Field: "from", Contains: "spam@", Action: "move", Target: "Junk"})
	// Forward + autoreply configured, to prove Junk-filed mail skips both.
	_ = s.SetForward(ctx, mbox, "backup@other.com")
	_ = s.SetAutoreply(ctx, mbox, Autoreply{Enabled: true, Body: "away"})

	var forwarded int
	qdb, _ := sql.Open("sqlite3", ":memory:")
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { qdb.Close() })
	q, _ := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, _ []string, _ []byte) error {
		forwarded++
		return nil
	})
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q}

	// Newsletter → Archive; forward + autoreply still fire (2 enqueues).
	news := []byte("From: news@big.co\r\nSubject: weekly\r\n\r\nx")
	if _, err := e.DeliverInbound("sender@other.test", mbox, news); err != nil {
		t.Fatalf("deliver news: %v", err)
	}
	if msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Archive"); len(msgs) != 1 {
		t.Fatalf("Archive has %d messages, want 1", len(msgs))
	}
	if d, _, _ := q.ProcessDue(ctx, time.Now().Add(time.Hour)); d != 2 {
		t.Fatalf("moved-to-Archive mail should still forward+autoreply, delivered=%d want 2", d)
	}

	// Spam rule → Junk; forward and autoreply must both be suppressed.
	spam := []byte("From: spam@bad.co\r\nSubject: buy\r\n\r\nx")
	if _, err := e.DeliverInbound("sender@other.test", mbox, spam); err != nil {
		t.Fatalf("deliver spam: %v", err)
	}
	if msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Junk"); len(msgs) != 1 {
		t.Fatalf("Junk has %d messages, want 1", len(msgs))
	}
	if d, _, _ := q.ProcessDue(ctx, time.Now().Add(2*time.Hour)); d != 0 {
		t.Fatal("Junk-filed mail must not forward or autoreply")
	}

	// No matching rule → Inbox as before.
	plain := []byte("From: friend@x.y\r\nSubject: hi\r\n\r\nx")
	if _, err := e.DeliverInbound("sender@other.test", mbox, plain); err != nil {
		t.Fatalf("deliver plain: %v", err)
	}
	if msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox"); len(msgs) != 1 {
		t.Fatalf("Inbox has %d messages, want 1", len(msgs))
	}

	// markread action: filed in Inbox already seen.
	_ = s.CreateFilter(ctx, FilterRule{Mailbox: mbox, Field: "subject", Contains: "receipt", Action: "markread"})
	rcpt := []byte("From: shop@x.y\r\nSubject: your receipt\r\n\r\nx")
	if _, err := e.DeliverInbound("sender@other.test", mbox, rcpt); err != nil {
		t.Fatalf("deliver receipt: %v", err)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	seen := 0
	for _, m := range msgs {
		if m.Seen {
			seen++
		}
	}
	if len(msgs) != 2 || seen != 1 {
		t.Fatalf("Inbox=%d seen=%d, want 2 messages with exactly 1 seen", len(msgs), seen)
	}
}
