// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// SECTION 2 AUDIT FINDING — the vacation responder is an open reflector.
//
// In the attacker's voice:
//
//	I do not need an account here. I need one mailbox on this install to be on
//	holiday, and I can find that out by mailing it.
//
//	I connect to port 25 and send:
//
//	    MAIL FROM:<me@mine.test>
//	    RCPT TO:<onholiday@example.com>
//	    From: Accounts <victim@example.org>
//	    Reply-To: Accounts <victim@example.org>
//
//	Your server picks the correspondent out of Reply-To, falling back to From.
//	Both are headers. Headers are the part of a message I type myself; they have
//	nothing to do with the envelope I actually sent from. So your server composes
//	a reply, signs it with YOUR DKIM key, and delivers it to a person who never
//	wrote to you.
//
//	The dedupe log is keyed on (mailbox, that same header address), so I vary the
//	header and it never fires twice on the same key. Your IP sends the mail, your
//	domain signs it, and your reputation pays for it.
//
// RFC 3834 §4 is unambiguous about the fix and predates this by twenty years: a
// personal response goes to the ENVELOPE return address, and is never sent at
// all when that address is null. The envelope is the address bounces return to —
// it is the only one that means "this is where this message actually came from".
//
// The envelope sender was available the entire time. inboundDeliver received it
// from smtpd and discarded it in its parameter list:
//
//	func (e *Engine) inboundDeliver(_ string, rcpts []string, raw []byte) error
//
// That underscore is the whole defect.

// autoreplyHarness returns an engine whose queue records what was sent where.
func autoreplyHarness(t *testing.T, mailbox string) (*Engine, *Queue, *[][]string) {
	t.Helper()
	s := aliasTestStore(t)
	if err := s.Create(context.Background(), mailbox, "hash", "Holiday", "mailbox"); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if err := s.SetAutoreply(context.Background(), mailbox,
		Autoreply{Enabled: true, Subject: "OOO", Body: "Away."}); err != nil {
		t.Fatalf("set autoreply: %v", err)
	}
	sentTo := &[][]string{}
	qdb, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("queue db: %v", err)
	}
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = qdb.Close() })
	q, err := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, to []string, _ []byte) error {
		*sentTo = append(*sentTo, to)
		return nil
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	return &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q}, q, sentTo
}

// drainQueue runs the queue and returns every recipient list it delivered.
func drainQueue(t *testing.T, q *Queue, sentTo *[][]string) [][]string {
	t.Helper()
	if _, _, err := q.ProcessDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("process queue: %v", err)
	}
	return *sentTo
}

func TestTheAutoresponderRepliesToTheEnvelopeNotAHeader(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	// The attacker sends from their own address and names a stranger in both
	// header fields the responder used to trust.
	raw := []byte("From: Accounts <victim@example.org>\r\n" +
		"Reply-To: Accounts <victim@example.org>\r\n" +
		"Message-ID: <m1@mine.test>\r\nSubject: invoice\r\n\r\npay up")
	if _, err := e.DeliverInbound("attacker@mine.test", mailbox, raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	for _, to := range drainQueue(t, q, sentTo) {
		for _, addr := range to {
			if strings.EqualFold(addr, "victim@example.org") {
				t.Fatalf("the autoresponder sent mail to %q — an address that appears only in "+
					"headers the sender wrote.\n\n"+
					"This install is now a reflector: attacker-chosen destination, DKIM-signed "+
					"by this domain, sent from this IP.", addr)
			}
		}
	}
	// And it does answer the person who actually sent it.
	got := drainQueue(t, q, sentTo)
	if len(got) != 1 || len(got[0]) != 1 || !strings.EqualFold(got[0][0], "attacker@mine.test") {
		t.Errorf("reply went to %+v, want the envelope sender attacker@mine.test", got)
	}
}

// A bounce carries a NULL envelope sender. Answering one is how a mail loop
// starts, and RFC 3834 forbids it outright.
//
// The package comment on autoresponder.go has always claimed "never respond to
// bounces". What enforced that was a substring search for "mailer-daemon" and
// friends in the header address — a naming convention, not a control, and one
// the sender chooses. The null envelope is the actual signal.
func TestTheAutoresponderNeverAnswersANullEnvelope(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	raw := []byte("From: Some Relay <relay@example.net>\r\nSubject: Undelivered Mail\r\n\r\nfailed")
	if _, err := e.DeliverInbound("", mailbox, raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if got := drainQueue(t, q, sentTo); len(got) != 0 {
		t.Errorf("a message with a null envelope sender was answered, to %+v.\n\n"+
			"That is a bounce. Replying to it is how two servers start talking to each "+
			"other forever.", got)
	}
}

// Varying the header address must not buy a fresh dedupe slot. The log is keyed
// on the correspondent, so if the correspondent is attacker-chosen then so is
// the key, and one sender can be answered without limit.
func TestOneSenderCannotFarmRepliesByVaryingHeaders(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	for i, victim := range []string{"a@example.org", "b@example.org", "c@example.org"} {
		raw := []byte("From: <" + victim + ">\r\nReply-To: <" + victim + ">\r\n" +
			"Message-ID: <m" + string(rune('0'+i)) + "@mine.test>\r\nSubject: hi\r\n\r\nx")
		if _, err := e.DeliverInbound("attacker@mine.test", mailbox, raw); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	got := drainQueue(t, q, sentTo)
	if len(got) > 1 {
		t.Errorf("one sender got %d replies out of three messages by changing a header "+
			"each time: %+v.\n\n"+
			"The dedupe window is per correspondent. If the sender picks the "+
			"correspondent, the sender picks the rate limit too.", len(got), got)
	}
}

// The seam the whole finding lived in.
//
// inboundDeliver is what smtpd hands every received message to, and its first
// parameter was `_`. Testing DeliverInbound alone proves the responder reads an
// envelope; it does not prove anything connects the network to it. Re-dropping
// the envelope here — restoring the exact defect — passed every other test in
// this file, so this one exists to make that impossible.
func TestTheInboundSeamCarriesTheEnvelopeToTheResponder(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	raw := []byte("From: Accounts <victim@example.org>\r\n" +
		"Reply-To: Accounts <victim@example.org>\r\nSubject: invoice\r\n\r\nx")
	// Exactly the call smtpd makes, through the InboundHandler signature.
	if err := e.inboundDeliver("attacker@mine.test", []string{mailbox}, raw); err != nil {
		t.Fatalf("inbound deliver: %v", err)
	}

	got := drainQueue(t, q, sentTo)
	if len(got) != 1 || !strings.EqualFold(got[0][0], "attacker@mine.test") {
		t.Fatalf("mail received over SMTP produced %+v, want one reply to the envelope "+
			"sender attacker@mine.test.\n\n"+
			"If this seam drops the envelope, the responder falls back to no sender at "+
			"all or to the headers, and the reflector is back.", got)
	}
}

// THE CONTROL. The responder must still do its job. An ordinary correspondent —
// envelope and From agreeing, as they do for essentially all real mail — gets
// exactly one reply, at the address they wrote from.
func TestAnOrdinaryCorrespondentStillGetsTheirReply(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	raw := []byte("From: Bob <bob@other.com>\r\nMessage-ID: <m1@other.com>\r\nSubject: hi\r\n\r\nping")
	if _, err := e.DeliverInbound("bob@other.com", mailbox, raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	got := drainQueue(t, q, sentTo)
	if len(got) != 1 || len(got[0]) != 1 || !strings.EqualFold(got[0][0], "bob@other.com") {
		t.Fatalf("Bob was sent %+v, want exactly one reply to bob@other.com.\n\n"+
			"A responder that stops responding is not a security fix, it is a broken "+
			"feature with a good excuse.", got)
	}
}

// THE OTHER CONTROL. A forwarding hop legitimately rewrites the envelope while
// leaving From alone — a person whose old provider forwards to this mailbox. The
// reply belongs at the envelope address, which is the forwarder, because that is
// the path the mail actually travelled and the path a bounce would take back.
// This is RFC 3834's intent rather than an accident of the fix.
func TestAForwardedMessageIsAnsweredAtItsReturnPath(t *testing.T) {
	t.Parallel()
	const mailbox = "onholiday@example.com"
	e, q, sentTo := autoreplyHarness(t, mailbox)

	raw := []byte("From: Carol <carol@origin.example>\r\nSubject: hello\r\n\r\nhi")
	if _, err := e.DeliverInbound("forwarder@relay.example", mailbox, raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	got := drainQueue(t, q, sentTo)
	if len(got) != 1 || !strings.EqualFold(got[0][0], "forwarder@relay.example") {
		t.Errorf("a forwarded message was answered at %+v, want the return path "+
			"forwarder@relay.example", got)
	}
}
