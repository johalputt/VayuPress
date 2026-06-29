package mail

import (
	"bytes"
	"context"
	"errors"
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

// DeliverInbound performs local delivery of a received message into the
// recipient's Maildir. This is the storage half of inbound mail; a listening
// MX/IMAP server is a separate, governed milestone.
func (e *Engine) DeliverInbound(recipientEmail string, raw []byte) (string, error) {
	if e.maildir == nil {
		return "", errors.New("vayumail: not started")
	}
	local, domain := splitAddress(recipientEmail)
	if local == "" {
		return "", errors.New("vayumail: invalid recipient")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	// Built-in heuristic junk filter (fully local — no external services). Mail
	// scoring at or above the threshold is filed straight into the recipient's
	// Junk folder instead of the inbox.
	if e.cfg.JunkFilterEnabled {
		if v := ScoreSpam(raw); v.IsSpam {
			return e.maildir.DeliverTo(domain, local, "Junk", raw)
		}
	}
	return e.maildir.Deliver(domain, local, raw)
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
