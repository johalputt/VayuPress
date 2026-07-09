package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StoredMessage is a summary of a message held in a Maildir.
type StoredMessage struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	Size    int64     `json:"size"`
	Seen    bool      `json:"seen"`
	Flagged bool      `json:"flagged"` // Maildir 'F' flag — surfaced as "pinned" in the panel
}

// List returns the messages in an account's mailbox (new + cur), newest first.
// Header parsing is best-effort; malformed messages still appear with their id.
func (m *Maildir) List(domain, username string) ([]StoredMessage, error) {
	var out []StoredMessage
	for _, sub := range []string{"new", "cur"} {
		dir := filepath.Join(m.accountDir(domain, username), sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			sm := StoredMessage{ID: sub + "/" + e.Name(), Size: info.Size(), Seen: sub == "cur", Date: info.ModTime()}
			if raw, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				if msg, perr := mail.ReadMessage(bytes.NewReader(raw)); perr == nil {
					sm.From = msg.Header.Get("From")
					sm.Subject = msg.Header.Get("Subject")
					if d, derr := msg.Header.Date(); derr == nil {
						sm.Date = d
					}
				}
			}
			out = append(out, sm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}

// ReadRaw returns the raw bytes of a stored message by its List() id
// ("new/<name>" or "cur/<name>"). The id is validated to stay within the
// account directory (no path traversal).
func (m *Maildir) ReadRaw(domain, username, id string) ([]byte, error) {
	sub, name, ok := strings.Cut(id, "/")
	if !ok || (sub != "new" && sub != "cur") {
		return nil, errors.New("vayumail: invalid message id")
	}
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return nil, errors.New("vayumail: invalid message id")
	}
	return os.ReadFile(filepath.Join(m.accountDir(domain, username), sub, name))
}

// forwardLoopHeader tags a message that VayuMail has already auto-forwarded
// once. Its presence stops a second hop, so two servers forwarding at each
// other (A→B, B→A) can never bounce a message forever.
const forwardLoopHeader = "X-VayuMail-Forwarded"

// DeliverInbound performs local delivery of a received message into the
// recipient's Maildir. Alias addresses resolve to their target mailbox first
// (single-level, see CreateAlias), and after successful filing a copy is
// relayed to the mailbox's auto-forward address when one is set (best-effort —
// a forwarding problem never fails local delivery).
func (e *Engine) DeliverInbound(recipientEmail string, raw []byte) (string, error) {
	if e.maildir == nil {
		return "", errors.New("vayumail: not started")
	}
	// Alias resolution: file into the target mailbox instead.
	if e.accounts != nil {
		if target := e.accounts.ResolveAlias(context.Background(), recipientEmail); target != "" {
			recipientEmail = target
		}
	}
	local, domain := splitAddress(recipientEmail)
	if local == "" {
		return "", errors.New("vayumail: invalid recipient")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	// Enforce the mailbox storage quota (0 = unlimited). A delivery that would
	// push the account over its limit is refused, so the sending server gets a
	// clear failure rather than the mailbox silently ballooning. Checked before
	// the junk filter so an over-quota account can't be filled with junk either.
	if e.accounts != nil {
		if quota := e.accounts.QuotaFor(context.Background(), local+"@"+domain); quota > 0 {
			if e.maildir.AccountSize(domain, local)+int64(len(raw)) > quota {
				return "", fmt.Errorf("vayumail: mailbox %s@%s is over its storage quota", local, domain)
			}
		}
	}
	// Built-in heuristic junk filter (fully local — no external services). Mail
	// scoring at or above the threshold is filed straight into the recipient's
	// Junk folder instead of the inbox. Junk is NOT auto-forwarded.
	if e.cfg.JunkFilterEnabled {
		if v := ScoreSpam(raw); v.IsSpam {
			return e.maildir.DeliverTo(domain, local, "Junk", raw)
		}
	}
	id, err := e.maildir.Deliver(domain, local, raw)
	if err == nil {
		e.forwardCopy(local+"@"+domain, raw)
		e.maybeAutoReply(local+"@"+domain, raw)
	}
	return id, err
}

// forwardCopy relays a copy of an inbound message to the mailbox's auto-forward
// address, when one is configured. Loop-safe: a message that already carries
// the forward tag is never forwarded again, and each hop adds the tag. The
// envelope sender is the local mailbox (our own domain), so SPF/DKIM alignment
// for the forwarding hop is ours. Best-effort by design.
func (e *Engine) forwardCopy(mailbox string, raw []byte) {
	if e.accounts == nil || e.queue == nil {
		return
	}
	fwd := e.accounts.ForwardFor(context.Background(), mailbox)
	if fwd == "" || strings.EqualFold(fwd, mailbox) {
		return
	}
	if hasHeader(raw, forwardLoopHeader) {
		return
	}
	tagged := prependHeader(raw, forwardLoopHeader+": "+mailbox)
	_, _ = e.queue.Enqueue(context.Background(), mailbox, []string{fwd}, tagged)
}

// hasHeader reports whether the message's header block contains the named
// header (case-insensitive). Only the block before the first blank line is
// examined, so a quoted header in the body can't spoof the check.
func hasHeader(raw []byte, name string) bool {
	head := raw
	if i := strings.Index(string(raw), "\r\n\r\n"); i >= 0 {
		head = raw[:i]
	} else if i := strings.Index(string(raw), "\n\n"); i >= 0 {
		head = raw[:i]
	}
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(string(head), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), prefix) {
			return true
		}
	}
	return false
}

// prependHeader inserts a header line at the top of the message's header block.
func prependHeader(raw []byte, headerLine string) []byte {
	out := make([]byte, 0, len(raw)+len(headerLine)+2)
	out = append(out, []byte(headerLine+"\r\n")...)
	return append(out, raw...)
}

// Inbox returns the messages for a local account, defaulting the domain to the
// engine's configured domain.
func (e *Engine) Inbox(domain, username string) ([]StoredMessage, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	return e.maildir.List(domain, username)
}

// ReadInboxMessage returns a stored message for display, PGP-decrypted for the
// owning account when possible (best-effort).
func (e *Engine) ReadInboxMessage(domain, username, id string) ([]byte, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	raw, err := e.maildir.ReadRaw(domain, username, id)
	if err != nil {
		return nil, err
	}
	if e.decrypt != nil {
		raw = e.decrypt(username+"@"+domain, raw)
	}
	return raw, nil
}

// Sent returns recent outbound messages (the "Sent" view) from the queue.
func (e *Engine) Sent(ctx context.Context, limit int) ([]SentInfo, error) {
	if e.queue == nil {
		return []SentInfo{}, nil
	}
	return e.queue.Recent(ctx, limit)
}

// kickDelivery runs one delivery pass off the request path so a manual
// Resend/Retry is acted on immediately instead of waiting for the 30s worker.
func (e *Engine) kickDelivery() {
	if e.queue == nil {
		return
	}
	go func() { _, _, _ = e.queue.ProcessDue(context.Background(), time.Now()) }()
}

// ResendQueued requeues one outbound message and triggers an immediate delivery
// attempt — the one-click "Resend" for a failed or pending message.
func (e *Engine) ResendQueued(ctx context.Context, id int64) error {
	if e.queue == nil {
		return errors.New("vayumail: not started")
	}
	if err := e.queue.Requeue(ctx, id); err != nil {
		return err
	}
	e.kickDelivery()
	return nil
}

// RetryAllFailed requeues every failed outbound message and triggers an
// immediate delivery pass. Returns how many were requeued.
func (e *Engine) RetryAllFailed(ctx context.Context) (int, error) {
	if e.queue == nil {
		return 0, errors.New("vayumail: not started")
	}
	n, err := e.queue.RequeueAllFailed(ctx)
	if err == nil && n > 0 {
		e.kickDelivery()
	}
	return n, err
}

// DeleteQueued removes an outbound message from the queue.
func (e *Engine) DeleteQueued(ctx context.Context, id int64) error {
	if e.queue == nil {
		return errors.New("vayumail: not started")
	}
	return e.queue.Delete(ctx, id)
}

func splitAddress(addr string) (local, domain string) {
	addr = strings.TrimSpace(addr)
	// Tolerate "Name <user@host>" form.
	if i := strings.LastIndex(addr, "<"); i >= 0 {
		if j := strings.LastIndex(addr, ">"); j > i {
			addr = addr[i+1 : j]
		}
	}
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr, ""
	}
	return addr[:at], strings.ToLower(addr[at+1:])
}

// Accounts lists the provisioned mailbox usernames for a domain.
func (m *Maildir) Accounts(domain string) ([]string, error) {
	dir := filepath.Join(m.base, domain)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// MailboxSummary is a per-account inbox summary for the panel.
type MailboxSummary struct {
	Username string `json:"username"`
	Total    int    `json:"total"`
	Unseen   int    `json:"unseen"`
}

// Mailboxes returns inbox summaries for every provisioned account on the
// engine's configured domain.
func (e *Engine) Mailboxes() ([]MailboxSummary, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	accts, err := e.maildir.Accounts(e.cfg.Domain)
	if err != nil {
		return nil, err
	}
	out := make([]MailboxSummary, 0, len(accts))
	for _, u := range accts {
		msgs, err := e.maildir.List(e.cfg.Domain, u)
		if err != nil {
			continue
		}
		s := MailboxSummary{Username: u, Total: len(msgs)}
		for _, m := range msgs {
			if !m.Seen {
				s.Unseen++
			}
		}
		out = append(out, s)
	}
	return out, nil
}
