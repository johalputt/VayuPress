// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"
)

// bounceEngine is a started engine whose one local mailbox, alice, sends
// through a queue that answers with refusal.
func bounceEngine(t *testing.T, refusal error) *Engine {
	t.Helper()
	e := newLoopbackEngine(t, loopbackBridge{localSet: map[string]bool{"alice@example.com": true, "bob@example.com": true}})
	e.queue.deliver = func(context.Context, string, []string, []byte) error { return refusal }
	return e
}

func sentID(t *testing.T, e *Engine, user string) string {
	t.Helper()
	sent, err := e.ListFolder(ReadAsOwner(user), "Sent")
	if err != nil || len(sent) == 0 {
		t.Fatalf("no Sent copy: %v", err)
	}
	return sent[0].MessageID
}

func bouncesOf(t *testing.T, e *Engine, user, id string) []Bounce {
	t.Helper()
	b, err := e.Bounces(ReadAsOwner(user), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b {
		b[i].At = time.Time{}
	}
	return b
}

// A refusal the queue does not retry is kept against the Sent copy, with
// the server's status code, and its sender finds a delivery report in the
// Inbox that names the recipient and the message.
func TestARefusedMessageIsReadBack(t *testing.T) {
	refusal := &RecipientRefusedError{Rcpt: "ghost@far.test", Err: &textproto.Error{Code: 550, Msg: "5.1.1 <ghost@far.test>: no such user"}}
	e := bounceEngine(t, refusal)
	ctx := context.Background()
	if _, err := e.ComposeRich(ctx, ComposeMessage{From: "alice@example.com", To: []string{"ghost@far.test"}, Subject: "Quarterly report", Body: "numbers"}); err != nil {
		t.Fatal(err)
	}
	if _, failed, _ := e.queue.ProcessDue(ctx, time.Now().Add(time.Hour)); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	id := sentID(t, e, "alice")
	want := []Bounce{{Recipient: "ghost@far.test", Status: "5.1.1", Reason: refusal.Error()}}
	if got := bouncesOf(t, e, "alice", id); !reflect.DeepEqual(got, want) {
		t.Fatalf("bounces %+v", got)
	}
	inbox, _ := e.ListFolder(ReadAsOwner("alice"), "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("the sender's Inbox holds %d messages", len(inbox))
	}
	raw, _ := e.ReadFolderMessage(ReadAsOwner("alice"), "Inbox", inbox[0].ID)
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	media, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if subject != "Not delivered: Quarterly report" || media != "multipart/report" || params["report-type"] != "delivery-status" ||
		cleanMessageID(msg.Header.Get("In-Reply-To")) != id || msg.Header.Get("Auto-Submitted") != "auto-replied" {
		t.Fatalf("the report's headers: %v", msg.Header)
	}
	parts := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		parts[p.Header.Get("Content-Type")] = string(b)
	}
	if !strings.Contains(parts["text/plain; charset=utf-8"], "  ghost@far.test\r\n") || !strings.Contains(parts["text/plain; charset=utf-8"], "The receiving server refused it") {
		t.Fatalf("the report's words:\n%s", parts["text/plain; charset=utf-8"])
	}
	if got := failedRecipients([]byte(parts["message/delivery-status"])); len(got) != 1 || got[0].Recipient != "ghost@far.test" || got[0].Status != "5.1.1" {
		t.Fatalf("the report's status part reads as %+v", got)
	}
	if !strings.Contains(parts["text/rfc822-headers"], "Subject: Quarterly report") {
		t.Fatalf("the report does not carry the message's headers:\n%s", parts["text/rfc822-headers"])
	}
}

// A message the queue gave up retrying is 4.4.7, and its report says the
// server kept trying.
func TestAMessageThatRanOutOfRetries(t *testing.T) {
	e := bounceEngine(t, errors.New("dial tcp: connection refused"))
	ctx := context.Background()
	if _, err := e.ComposeRich(ctx, ComposeMessage{From: "alice@example.com", To: []string{"far@far.test"}, Subject: "Hi", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE vayumail_queue SET max_attempts=1`); err != nil {
		t.Fatal(err)
	}
	e.queue.ProcessDue(ctx, time.Now().Add(time.Hour))
	if got := bouncesOf(t, e, "alice", sentID(t, e, "alice")); len(got) != 1 || got[0].Status != "4.4.7" {
		t.Fatalf("bounces %+v", got)
	}
	inbox, _ := e.ListFolder(ReadAsOwner("alice"), "Inbox")
	raw, _ := e.ReadFolderMessage(ReadAsOwner("alice"), "Inbox", inbox[0].ID)
	if !strings.Contains(string(raw), "kept trying and has now given up") {
		t.Fatalf("the report:\n%s", raw)
	}
}

// Only a failure of mail from a mailbox here is reported, and a delivery
// that succeeds reports nothing.
func TestOnlyALocalSendersFailureIsReported(t *testing.T) {
	e := bounceEngine(t, nil)
	ctx := context.Background()
	if _, err := e.ComposeRich(ctx, ComposeMessage{From: "alice@example.com", To: []string{"far@far.test"}, Subject: "Hi", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	e.queue.ProcessDue(ctx, time.Now().Add(time.Hour))
	e.deliveryFailed("someone@else.test", []string{"far@far.test"}, []byte("Message-ID: <x@else.test>\r\n\r\nb"), errors.New("refused"))
	var n int
	_ = e.accounts.ensureBouncesTable()
	_ = e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_bounces`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bounces kept for mail that was delivered or not sent from here", n)
	}
	if inbox, _ := e.ListFolder(ReadAsOwner("alice"), "Inbox"); len(inbox) != 0 {
		t.Fatalf("a report was filed for a delivered message")
	}
}

// report is a delivery report from another server about the message id,
// its status part in the given transfer encoding.
func report(id, encoding, statusPart string) []byte {
	return []byte("From: MAILER-DAEMON@far.test\r\nTo: alice@example.com\r\nSubject: Undelivered\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nSorry.\r\n" +
		"--b\r\nContent-Type: message/delivery-status\r\nContent-Transfer-Encoding: " + encoding + "\r\n\r\n" + statusPart + "\r\n" +
		"--b\r\nContent-Type: text/rfc822-headers\r\n\r\nMessage-ID: <" + id + ">\r\nSubject: Hi\r\n\r\n" +
		"--b--\r\n")
}

const reportStatus = "Reporting-MTA: dns; mx.far.test\r\n\r\nFinal-Recipient: rfc822; bob@far.test\r\nAction: failed\r\nStatus: 5.2.2\r\nDiagnostic-Code: smtp; 552 5.2.2 mailbox full\r\n\r\nFinal-Recipient: rfc822; carol@far.test\r\nAction: delayed\r\nStatus: 4.4.1\r\n"

// A report from another server is read for the recipients that failed, in
// whichever encoding it comes, and only for a message sent from here.
func TestAReportFromAnotherServerIsReadBack(t *testing.T) {
	e := bounceEngine(t, nil)
	want := []Bounce{{Recipient: "bob@far.test", Status: "5.2.2", Reason: "552 5.2.2 mailbox full"}}
	for name, c := range map[string]struct{ id, enc, part string }{
		"7bit":             {"a1@example.com", "7bit", reportStatus},
		"base64":           {"a2@example.com", "base64", "UmVwb3J0aW5nLU1UQTogZG5zOyBteC5mYXIudGVzdA0KDQpGaW5hbC1SZWNpcGllbnQ6IHJmYzgyMjsgYm9iQGZhci50ZXN0DQpBY3Rpb246IGZhaWxlZA0KU3RhdHVzOiA1LjIuMg0KRGlhZ25vc3RpYy1Db2RlOiBzbXRwOyA1NTIgNS4yLjIgbWFpbGJveCBmdWxsDQo="},
		"quoted-printable": {"a3@example.com", "quoted-printable", strings.Replace(reportStatus, "mailbox full", "mailbox =\r\nfull", 1)},
	} {
		if _, err := e.DeliverInbound("MAILER-DAEMON@far.test", "alice@example.com", report(c.id, c.enc, c.part)); err != nil {
			t.Fatal(err)
		}
		if got := bouncesOf(t, e, "alice", c.id); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if _, err := e.DeliverInbound("MAILER-DAEMON@far.test", "alice@example.com", report("x@elsewhere.test", "7bit", reportStatus)); err != nil {
		t.Fatal(err)
	}
	if got := bouncesOf(t, e, "alice", "x@elsewhere.test"); len(got) != 0 {
		t.Fatalf("a report about mail not sent from here was believed: %+v", got)
	}
	notReport := strings.Replace(string(report("a4@example.com", "7bit", reportStatus)), "multipart/report; report-type=delivery-status;", "multipart/mixed; report-type=delivery-status;", 1)
	if _, err := e.DeliverInbound("someone@far.test", "alice@example.com", []byte(notReport)); err != nil {
		t.Fatal(err)
	}
	if got := bouncesOf(t, e, "alice", "a4@example.com"); len(got) != 0 {
		t.Fatalf("a message that is not a delivery report was read as one: %+v", got)
	}
	if got := bouncesOf(t, e, "bob", "a1@example.com"); len(got) != 0 {
		t.Fatalf("bob sees alice's bounces: %+v", got)
	}
}

// What a server said is kept to 300 characters, on one line.
func TestABouncesReasonIsKeptShort(t *testing.T) {
	e := bounceEngine(t, nil)
	long := strings.Repeat("é", 299) + "\r\n\tmore words"
	if err := e.accounts.recordBounce("alice@example.com", "<m@example.com>", "Bob@Far.test", "5.0.0", long); err != nil {
		t.Fatal(err)
	}
	got := bouncesOf(t, e, "alice", "M@example.com")
	if len(got) != 1 || got[0].Recipient != "bob@far.test" || got[0].Reason != strings.Repeat("é", 299)+" …" {
		t.Fatalf("%+v", got)
	}
}

// Deleting the mailbox removes what it kept of its bounces.
func TestBouncesGoWithTheirMailbox(t *testing.T) {
	e := bounceEngine(t, nil)
	ctx := context.Background()
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	_ = e.accounts.recordBounce("alice@example.com", "m@example.com", "b@far.test", "5.0.0", "no")
	if err := e.accounts.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := bouncesOf(t, e, "alice", "m@example.com"); len(got) != 0 {
		t.Fatalf("outlived its mailbox: %+v", got)
	}
}
