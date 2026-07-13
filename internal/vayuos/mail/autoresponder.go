package mail

import (
	"context"
	"errors"
	netmail "net/mail"
	"strings"
	"time"
)

// Vacation autoresponder (RFC 3834 "Recommendations for Automatic Responses
// to Electronic Mail").
//
// When a mailbox has an active autoreply, inbound mail that passes the safety
// checks gets ONE automatic reply per correspondent per dedupe window. The
// safety checks are what make this enterprise-grade rather than a mail-loop
// generator:
//
//   - never respond to auto-generated/auto-replied mail (Auto-Submitted),
//     suppressed senders (X-Auto-Response-Suppress), bulk/list/junk
//     Precedence, or list mail (List-Id / List-Unsubscribe / List-Post);
//   - never respond to bounces or machine senders (mailer-daemon, postmaster,
//     no-reply, bounce addresses) or to the mailbox itself;
//   - never respond to copies our own forwarder relayed (forwardLoopHeader);
//   - our replies are themselves tagged Auto-Submitted: auto-replied and
//     X-Auto-Response-Suppress: All, so two autoresponders can never converse.
//
// Junk-filtered mail never reaches the autoresponder (DeliverInbound returns
// on the Junk branch first).

// Autoreply is a mailbox's vacation-responder configuration. From/Until bound
// the active window; either may be zero for an open side.
type Autoreply struct {
	Enabled bool      `json:"enabled"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	From    time.Time `json:"from"`
	Until   time.Time `json:"until"`
}

// autoreplyDedupeWindow is how long a correspondent stays "already answered".
const autoreplyDedupeWindow = 7 * 24 * time.Hour

// maxAutoreplyBytes bounds the stored subject+body.
const maxAutoreplyBytes = 8192

// Active reports whether the autoreply should fire at instant now.
func (ar Autoreply) Active(now time.Time) bool {
	if !ar.Enabled || strings.TrimSpace(ar.Body) == "" {
		return false
	}
	if !ar.From.IsZero() && now.Before(ar.From) {
		return false
	}
	if !ar.Until.IsZero() && now.After(ar.Until) {
		return false
	}
	return true
}

// SetAutoreply stores a mailbox's autoresponder configuration.
func (s *AccountStore) SetAutoreply(ctx context.Context, email string, ar Autoreply) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if len(ar.Body) > maxAutoreplyBytes {
		ar.Body = ar.Body[:maxAutoreplyBytes]
	}
	if len(ar.Subject) > 998 { // RFC 5322 line limit
		ar.Subject = ar.Subject[:998]
	}
	enabled := 0
	if ar.Enabled {
		enabled = 1
	}
	fromS, untilS := "", ""
	if !ar.From.IsZero() {
		fromS = ar.From.UTC().Format(time.RFC3339)
	}
	if !ar.Until.IsZero() {
		untilS = ar.Until.UTC().Format(time.RFC3339)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET autoreply_enabled=?, autoreply_subject=?, autoreply_body=?, autoreply_from=?, autoreply_until=? WHERE email=?`,
		enabled, ar.Subject, ar.Body, fromS, untilS, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// AutoreplyFor returns a mailbox's autoresponder configuration.
func (s *AccountStore) AutoreplyFor(ctx context.Context, email string) Autoreply {
	var ar Autoreply
	if s.db == nil {
		return ar
	}
	var enabled int
	var fromS, untilS string
	_ = s.db.QueryRowContext(ctx,
		`SELECT autoreply_enabled, autoreply_subject, autoreply_body, autoreply_from, autoreply_until FROM vayumail_accounts WHERE email=?`,
		normEmail(email)).Scan(&enabled, &ar.Subject, &ar.Body, &fromS, &untilS)
	ar.Enabled = enabled == 1
	if t, err := time.Parse(time.RFC3339, fromS); err == nil {
		ar.From = t
	}
	if t, err := time.Parse(time.RFC3339, untilS); err == nil {
		ar.Until = t
	}
	return ar
}

// autoreplyRecently reports whether mailbox already auto-replied to sender
// within the dedupe window, recording the new reply when it did not.
func (s *AccountStore) autoreplyRecently(ctx context.Context, mailbox, sender string, now time.Time) bool {
	if s.db == nil {
		return true // no storage → no dedupe possible → do not reply
	}
	mailbox, sender = normEmail(mailbox), normEmail(sender)
	var last time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT sent_at FROM vayumail_autoreply_log WHERE mailbox=? AND sender=?`, mailbox, sender).Scan(&last)
	if err == nil && now.Sub(last) < autoreplyDedupeWindow {
		return true
	}
	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO vayumail_autoreply_log(mailbox,sender,sent_at) VALUES(?,?,?)
		 ON CONFLICT(mailbox,sender) DO UPDATE SET sent_at=excluded.sent_at`, mailbox, sender, now.UTC())
	return false
}

// headerValue returns the (first) value of a header in the message's header
// block, or "". Folded continuation lines are not needed for the headers we
// inspect (address + token headers), so only the first line is returned.
func headerValue(raw []byte, name string) string {
	head := string(raw)
	if i := strings.Index(head, "\r\n\r\n"); i >= 0 {
		head = head[:i]
	} else if i := strings.Index(head, "\n\n"); i >= 0 {
		head = head[:i]
	}
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(head, "\n") {
		l := strings.TrimRight(line, "\r")
		if strings.HasPrefix(strings.ToLower(l), prefix) {
			return strings.TrimSpace(l[len(prefix):])
		}
	}
	return ""
}

// noAutoReplySenderRe matches machine senders we must never answer.
var noAutoReplySenders = []string{"mailer-daemon", "postmaster", "no-reply", "noreply", "bounce", "donotreply"}

// shouldAutoReply applies the RFC 3834 safety checks to an inbound message and
// returns the correspondent address to answer, or "" when no reply must be
// sent.
func shouldAutoReply(mailbox string, raw []byte) string {
	// Machine-generated or suppressed mail: never answer.
	if v := strings.ToLower(headerValue(raw, "Auto-Submitted")); v != "" && v != "no" {
		return ""
	}
	if headerValue(raw, "X-Auto-Response-Suppress") != "" {
		return ""
	}
	switch strings.ToLower(headerValue(raw, "Precedence")) {
	case "bulk", "junk", "list":
		return ""
	}
	// List mail: never answer.
	if headerValue(raw, "List-Id") != "" || headerValue(raw, "List-Unsubscribe") != "" || headerValue(raw, "List-Post") != "" {
		return ""
	}
	// Copies our own forwarder relayed: never answer.
	if hasHeader(raw, forwardLoopHeader) {
		return ""
	}
	// Correspondent: Reply-To wins, else From.
	sender := headerValue(raw, "Reply-To")
	if sender == "" {
		sender = headerValue(raw, "From")
	}
	addr, err := netmail.ParseAddress(sender)
	if err != nil || addr.Address == "" {
		return ""
	}
	lowAddr := strings.ToLower(addr.Address)
	if lowAddr == strings.ToLower(mailbox) {
		return "" // never answer ourselves
	}
	localPart := lowAddr
	if i := strings.IndexByte(localPart, '@'); i > 0 {
		localPart = localPart[:i]
	}
	for _, bad := range noAutoReplySenders {
		if strings.Contains(localPart, bad) {
			return ""
		}
	}
	return addr.Address
}

// maybeAutoReply sends the vacation reply for a just-delivered message when
// the mailbox's autoresponder is active and every safety check passes.
// Best-effort by design: a responder problem never affects delivery.
func (e *Engine) maybeAutoReply(mailbox string, raw []byte) {
	if e.accounts == nil || e.queue == nil {
		return
	}
	ctx := context.Background()
	now := time.Now()
	ar := e.accounts.AutoreplyFor(ctx, mailbox)
	if !ar.Active(now) {
		return
	}
	sender := shouldAutoReply(mailbox, raw)
	if sender == "" {
		return
	}
	if e.accounts.autoreplyRecently(ctx, mailbox, sender, now) {
		return
	}

	subject := strings.TrimSpace(ar.Subject)
	if subject == "" {
		subject = "Automatic reply"
	}
	var b strings.Builder
	b.WriteString("From: <" + mailbox + ">\r\n")
	b.WriteString("To: <" + sender + ">\r\n")
	b.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + e.messageID(e.senderDomain(mailbox)) + "\r\n")
	if mid := headerValue(raw, "Message-ID"); mid != "" {
		b.WriteString("In-Reply-To: " + sanitizeHeader(mid) + "\r\n")
		b.WriteString("References: " + sanitizeHeader(mid) + "\r\n")
	}
	// RFC 3834 §3.1.7 + the de-facto Microsoft header: mark OUR reply as
	// automatic so no compliant responder (including ours) ever answers it.
	b.WriteString("Auto-Submitted: auto-replied\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(ar.Body, "\n", "\r\n"))
	b.WriteString("\r\n")

	out := []byte(b.String())
	if e.dkim != nil {
		if signed, err := e.dkimFor(e.senderDomain(mailbox)).SignMessage(out); err == nil {
			out = signed
		}
	}
	_, _ = e.queue.Enqueue(ctx, mailbox, []string{sender}, out)
}

// sanitizeHeader strips CR/LF so user-controlled text can never inject headers.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}
