// SPDX-License-Identifier: Apache-2.0

package mail

// bounces.go — mail that did not arrive, read back to its sender. A message
// is known not to have arrived in two ways: this server's queue gives up on
// it (a permanent refusal, or every retry spent), or another server sends a
// delivery report for it (RFC 3464). Either way the failure is kept against
// the sent message's Message-ID in the sender's mailbox, where the reader
// shows it on the Sent copy. A queue failure also files a report in the
// sender's Inbox, as other mail services do, because the sender is not
// looking at Sent; a report from another server already arrives there.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"
)

// Bounce is one recipient a sent message did not reach.
type Bounce struct {
	Recipient string    `json:"recipient"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// maxBounceReason bounds what is kept of a server's reply.
const maxBounceReason = 300

func (s *AccountStore) ensureBouncesTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_bounces(
		mailbox TEXT NOT NULL,
		message_id TEXT NOT NULL,
		recipient TEXT NOT NULL,
		status TEXT NOT NULL,
		reason TEXT NOT NULL,
		at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(mailbox, message_id, recipient));`)
	return err
}

// recordBounce keeps that the message msgID from mailbox did not reach rcpt.
// A second report for the same recipient replaces the first.
func (s *AccountStore) recordBounce(mailbox, msgID, rcpt, status, reason string) error {
	if err := s.ensureBouncesTable(); err != nil {
		return err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if r := []rune(reason); len(r) > maxBounceReason {
		reason = string(r[:maxBounceReason]) + "…"
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO vayumail_bounces(mailbox,message_id,recipient,status,reason) VALUES(?,?,?,?,?)`,
		normEmail(mailbox), cleanMessageID(msgID), normEmail(rcpt), status, reason)
	return err
}

// Bounces lists the recipients the message messageID from rd's mailbox did
// not reach.
func (e *Engine) Bounces(rd Reader, messageID string) ([]Bounce, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || cleanMessageID(messageID) == "" {
		return nil, nil
	}
	if err := e.accounts.ensureBouncesTable(); err != nil {
		return nil, err
	}
	rows, err := e.db.Query(`SELECT recipient,status,reason,at FROM vayumail_bounces WHERE mailbox=? AND message_id=? ORDER BY recipient`,
		e.mailboxAddr(rd), cleanMessageID(messageID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bounce
	for rows.Next() {
		var b Bounce
		if err := rows.Scan(&b.Recipient, &b.Status, &b.Reason, &b.At); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

var enhancedStatus = regexp.MustCompile(`\b[245]\.\d{1,3}\.\d{1,3}\b`)

// failureStatus is the RFC 3463 status of a delivery the queue gave up on:
// the code the receiving server gave when it gave one, else 5.0.0 for a
// refusal and 4.4.7 (delivery time expired) for retries spent.
func failureStatus(err error) string {
	if m := enhancedStatus.FindString(err.Error()); m != "" && m[0] != '2' {
		return m
	}
	if PermanentFailure(err) {
		return "5.0.0"
	}
	return "4.4.7"
}

// deliveryFailed is the queue's word that a message will not be delivered.
// Only a message from a mailbox here is reported, and only once per
// failure: the queue calls this when the message is marked failed.
func (e *Engine) deliveryFailed(from string, to []string, raw []byte, cause error) {
	if e.accounts == nil || e.maildir == nil || !e.isMailboxOrAlias(from) {
		return
	}
	mbox := e.mailboxFor(envelopeAddress(from))
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return
	}
	msgID, status := msg.Header.Get("Message-ID"), failureStatus(cause)
	for _, rcpt := range to {
		_ = e.accounts.recordBounce(mbox, msgID, rcpt, status, cause.Error())
	}
	local, domain := splitAddress(mbox)
	if local == "" {
		return
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	_, _ = e.maildir.Deliver(domain, local, e.failureReport(mbox, to, status, cause.Error(), msg.Header, raw))
}

// failureReport is a delivery report (RFC 3464) for the sender: words first,
// then the machine-readable status per recipient, then the message's headers
// so the sender can tell which message it was.
func (e *Engine) failureReport(sender string, to []string, status, reason string, h mail.Header, raw []byte) []byte {
	reason = strings.Join(strings.Fields(reason), " ")
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	boundary := "report-" + hex.EncodeToString(b)
	subject := h.Get("Subject")
	if d, err := new(mime.WordDecoder).DecodeHeader(subject); err == nil {
		subject = d
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "From: Mail Delivery <mailer-daemon@%s>\r\n", e.cfg.Domain)
	fmt.Fprintf(&w, "To: <%s>\r\n", sender)
	fmt.Fprintf(&w, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", "Not delivered: "+subject))
	fmt.Fprintf(&w, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&w, "Message-ID: <%s>\r\n", e.messageID(e.cfg.Domain))
	if id := h.Get("Message-ID"); id != "" {
		fmt.Fprintf(&w, "In-Reply-To: %s\r\nReferences: %s\r\n", id, id)
	}
	w.WriteString("Auto-Submitted: auto-replied\r\nMIME-Version: 1.0\r\n")
	fmt.Fprintf(&w, "Content-Type: multipart/report; report-type=delivery-status; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&w, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n", boundary)
	w.WriteString("Your message could not be delivered to:\r\n\r\n")
	for _, r := range to {
		w.WriteString("  " + r + "\r\n")
	}
	if status[0] == '4' {
		w.WriteString("\r\nThis server kept trying and has now given up. The last answer was:\r\n\r\n")
	} else {
		w.WriteString("\r\nThe receiving server refused it, and said:\r\n\r\n")
	}
	w.WriteString("  " + reason + "\r\n")
	fmt.Fprintf(&w, "\r\n--%s\r\nContent-Type: message/delivery-status\r\n\r\nReporting-MTA: dns; %s\r\n", boundary, e.cfg.Hostname)
	for _, r := range to {
		fmt.Fprintf(&w, "\r\nFinal-Recipient: rfc822; %s\r\nAction: failed\r\nStatus: %s\r\nDiagnostic-Code: smtp; %s\r\n", r, status, reason)
	}
	fmt.Fprintf(&w, "\r\n--%s\r\nContent-Type: text/rfc822-headers\r\n\r\n", boundary)
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		w.Write(raw[:i+2])
	} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		w.Write(raw[:i+1])
	}
	fmt.Fprintf(&w, "\r\n--%s--\r\n", boundary)
	return w.Bytes()
}

// readDeliveryReport keeps what a delivery report delivered to mailbox says
// failed. Only a report about a message from a domain this install serves is
// believed: a report is easy to forge, and this one can only ever annotate
// mail this server sent.
func (e *Engine) readDeliveryReport(mailbox string, raw []byte) {
	if e.accounts == nil {
		return
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return
	}
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/report" || !strings.HasSuffix(strings.ToLower(params["report-type"]), "delivery-status") || params["boundary"] == "" {
		return
	}
	var failed []Bounce
	var msgID string
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		ptype, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		body := partBody(part)
		switch ptype {
		case "message/delivery-status", "message/global-delivery-status":
			failed = append(failed, failedRecipients(body)...)
		case "text/rfc822-headers", "message/rfc822", "message/rfc822-headers", "message/global", "message/global-headers":
			if orig, err := mail.ReadMessage(io.MultiReader(bytes.NewReader(body), strings.NewReader("\r\n\r\n"))); err == nil {
				msgID = orig.Header.Get("Message-ID")
			}
		}
	}
	id := cleanMessageID(msgID)
	if at := strings.LastIndex(id, "@"); at < 0 || !e.cfg.AcceptsMailDomain(id[at+1:]) {
		return
	}
	for _, b := range failed {
		_ = e.accounts.recordBounce(mailbox, id, b.Recipient, b.Status, b.Reason)
	}
}

// partBody reads a report part, undoing a base64 transfer encoding; the
// multipart reader already undoes quoted-printable itself.
func partBody(p *multipart.Part) []byte {
	var r io.Reader = p
	if strings.EqualFold(strings.TrimSpace(p.Header.Get("Content-Transfer-Encoding")), "base64") {
		r = base64.NewDecoder(base64.StdEncoding, p)
	}
	b, _ := io.ReadAll(io.LimitReader(r, 256<<10))
	return b
}

// failedRecipients reads the per-recipient fields of a delivery-status body
// (RFC 3464 §2.3) and returns those whose Action is failed.
func failedRecipients(body []byte) []Bounce {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(body)))
	var out []Bounce
	for {
		h, err := tp.ReadMIMEHeader()
		if len(h) > 0 && strings.EqualFold(strings.TrimSpace(h.Get("Action")), "failed") {
			rcpt := h.Get("Final-Recipient")
			if _, addr, ok := strings.Cut(rcpt, ";"); ok {
				rcpt = addr
			}
			reason := h.Get("Diagnostic-Code")
			if _, text, ok := strings.Cut(reason, ";"); ok {
				reason = text
			}
			out = append(out, Bounce{Recipient: strings.TrimSpace(rcpt), Status: strings.TrimSpace(h.Get("Status")), Reason: strings.TrimSpace(reason)})
		}
		if err != nil {
			return out
		}
	}
}
