// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestAutoreplyStoreAndWindow(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()

	// Defaults: off.
	if ar := s.AutoreplyFor(ctx, "ankush@example.com"); ar.Enabled || ar.Active(time.Now()) {
		t.Fatalf("autoreply defaults on: %+v", ar)
	}

	until := time.Now().Add(48 * time.Hour).Truncate(time.Second).UTC()
	in := Autoreply{Enabled: true, Subject: "Out of office", Body: "Back Monday.", Until: until}
	if err := s.SetAutoreply(ctx, "ankush@example.com", in); err != nil {
		t.Fatalf("set: %v", err)
	}
	ar := s.AutoreplyFor(ctx, "ankush@example.com")
	if !ar.Enabled || ar.Subject != "Out of office" || ar.Body != "Back Monday." || !ar.Until.Equal(until) {
		t.Fatalf("round-trip mismatch: %+v", ar)
	}
	if !ar.Active(time.Now()) {
		t.Fatal("should be active now")
	}
	if ar.Active(until.Add(time.Hour)) {
		t.Fatal("must deactivate after the until date")
	}
	// A future start date keeps it inactive until then.
	in.From = time.Now().Add(24 * time.Hour)
	_ = s.SetAutoreply(ctx, "ankush@example.com", in)
	if s.AutoreplyFor(ctx, "ankush@example.com").Active(time.Now()) {
		t.Fatal("must stay inactive before the from date")
	}
	// Empty body never fires, and unknown accounts error.
	if (Autoreply{Enabled: true}).Active(time.Now()) {
		t.Fatal("empty body must never fire")
	}
	if err := s.SetAutoreply(ctx, "ghost@example.com", in); err == nil {
		t.Fatal("unknown account must error")
	}
}

func TestShouldAutoReplySafetyMatrix(t *testing.T) {
	t.Parallel()
	mbox := "ankush@example.com"
	plain := "From: Alice <alice@other.com>\r\nSubject: hi\r\n\r\nhello"

	if got := shouldAutoReply("alice@other.com", mbox, []byte(plain)); got != "alice@other.com" {
		t.Fatalf("plain mail should be answered, got %q", got)
	}
	// The envelope decides, and the headers do not get a vote.
	//
	// This assertion used to read "Reply-To should win", which is the Section 2
	// finding written down as a requirement: it pinned the behaviour that let a
	// sender aim this server's replies at anyone they named. Both header fields
	// are ignored now, so the reply follows the address the message actually
	// arrived from.
	rt := "From: Alice <alice@other.com>\r\nReply-To: Team <team@other.com>\r\n\r\nx"
	if got := shouldAutoReply("alice@other.com", mbox, []byte(rt)); got != "alice@other.com" {
		t.Fatalf("the envelope sender should be answered, not a header; got %q", got)
	}
	// And a null envelope is never answered, whatever the headers claim.
	if got := shouldAutoReply("", mbox, []byte(plain)); got != "" {
		t.Fatalf("a null envelope sender must not be answered, got %q", got)
	}

	// Each case carries its own envelope, because the envelope and the headers
	// now answer different questions. The first six are HEADER suppressions and
	// get a perfectly ordinary envelope, so it is the header being tested rather
	// than an empty envelope short-circuiting the whole function. The last three
	// are sender-identity rules, which read the envelope — the machine-name list
	// used to match the From header, where the sender chose it.
	never := map[string]struct{ env, msg string }{
		"auto-submitted":  {"a@b.c", "Auto-Submitted: auto-replied\r\nFrom: a@b.c\r\n\r\nx"},
		"suppress":        {"a@b.c", "X-Auto-Response-Suppress: All\r\nFrom: a@b.c\r\n\r\nx"},
		"precedence-bulk": {"a@b.c", "Precedence: bulk\r\nFrom: a@b.c\r\n\r\nx"},
		"list-id":         {"a@b.c", "List-Id: <dev.lists.x.org>\r\nFrom: a@b.c\r\n\r\nx"},
		"list-unsub":      {"a@b.c", "List-Unsubscribe: <mailto:u@x.y>\r\nFrom: a@b.c\r\n\r\nx"},
		"forward-tag":     {"a@b.c", forwardLoopHeader + ": m@x.y\r\nFrom: a@b.c\r\n\r\nx"},
		"mailer-daemon":   {"MAILER-DAEMON@other.com", "From: someone@other.com\r\n\r\nx"},
		"noreply":         {"no-reply@other.com", "From: someone@other.com\r\n\r\nx"},
		"self":            {mbox, "From: someone@other.com\r\n\r\nx"},
		"null-envelope":   {"", "From: perfectly@normal.com\r\n\r\nx"},
	}
	for name, c := range never {
		if got := shouldAutoReply(c.env, mbox, []byte(c.msg)); got != "" {
			t.Errorf("%s: must not answer, got %q", name, got)
		}
	}
	// A missing From header is no longer a reason to stay silent: the reply
	// address comes from the envelope, and a message with a real return path
	// deserves an answer whether or not it bothered to name an author.
	if got := shouldAutoReply("real@other.com", mbox, []byte("Subject: only\r\n\r\nx")); got != "real@other.com" {
		t.Errorf("a message with no From header but a real envelope should be answered, got %q", got)
	}
	// Auto-Submitted: no is explicitly answerable (RFC 3834).
	asNo := "Auto-Submitted: no\r\nFrom: a@other.com\r\n\r\nx"
	if got := shouldAutoReply("a@other.com", mbox, []byte(asNo)); got != "a@other.com" {
		t.Fatalf("Auto-Submitted: no should be answered, got %q", got)
	}
}

func TestAutoReplyEndToEnd(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()
	_ = s.SetAutoreply(ctx, "ankush@example.com", Autoreply{Enabled: true, Subject: "OOO", Body: "Away."})

	var sent [][]byte
	var sentTo [][]string
	qdb, _ := sql.Open("sqlite3", ":memory:")
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { qdb.Close() })
	q, err := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, to []string, raw []byte) error {
		sentTo = append(sentTo, to)
		sent = append(sent, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q}

	raw := []byte("From: Bob <bob@other.com>\r\nMessage-ID: <m1@other.com>\r\nSubject: hi\r\n\r\nping")
	if _, err := e.DeliverInbound("bob@other.com", "ankush@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if d, f, _ := q.ProcessDue(ctx, time.Now().Add(time.Hour)); d != 1 || f != 0 {
		t.Fatalf("autoreply not enqueued: delivered=%d failed=%d", d, f)
	}
	if len(sentTo) != 1 || sentTo[0][0] != "bob@other.com" {
		t.Fatalf("reply recipient = %+v", sentTo)
	}
	reply := string(sent[0])
	for _, want := range []string{"Auto-Submitted: auto-replied", "X-Auto-Response-Suppress: All", "Subject: OOO", "In-Reply-To: <m1@other.com>", "Away."} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply missing %q", want)
		}
	}

	// Dedupe: the same sender gets no second reply inside the window.
	if _, err := e.DeliverInbound("bob@other.com", "ankush@example.com", raw); err != nil {
		t.Fatalf("second deliver: %v", err)
	}
	if d, _, _ := q.ProcessDue(ctx, time.Now().Add(2*time.Hour)); d != 0 {
		t.Fatal("dedupe failed: second autoreply sent")
	}

	// Loop: our own style of auto-reply must never be answered.
	loop := []byte("From: other@other.com\r\nAuto-Submitted: auto-replied\r\n\r\naway too")
	if _, err := e.DeliverInbound("other@other.com", "ankush@example.com", loop); err != nil {
		t.Fatalf("loop deliver: %v", err)
	}
	if d, _, _ := q.ProcessDue(ctx, time.Now().Add(3*time.Hour)); d != 0 {
		t.Fatal("answered an auto-replied message (mail loop!)")
	}
}
