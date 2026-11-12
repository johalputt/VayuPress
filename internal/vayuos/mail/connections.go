// SPDX-License-Identifier: Apache-2.0

package mail

// connections.go — an AI tool connected to a holder's own mailbox over
// VayuMCP, and the record of what each one did.
//
// A connection is the holder's: made when the holder signs in and grants
// it (the OAuth consent in cmd/vayupress), listed in their Settings with
// its scopes and last use, and ended there. It is bound to the access key
// minted for it, by that key's id, and to nothing an administrator holds:
// a key with no connection row reaches no mailbox, whatever the key's own
// grants, so the operator's keys (a superuser's included) open no mail.
//
// The tools read, draft and file; none sends. A draft a tool makes says so
// in its own header and waits in Drafts for the holder. Every call is
// recorded for the holder: the tool, a line of what it read or wrote, and
// when; the latest maxToolCalls are kept.

import (
	"bytes"
	"context"
	"errors"
	"mime"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// What a connection may do.
const (
	ScopeRead     = "read"     // search, read and list mail, events and free time
	ScopeDraft    = "draft"    // make drafts and file messages
	ScopeCalendar = "calendar" // make, change and cancel events without guests
)

// maxToolCalls is the record kept for one mailbox.
const maxToolCalls = 500

// ErrConnectionHolderOnly refuses anyone but the mailbox's own holder.
var ErrConnectionHolderOnly = errors.New("vayumail: a mailbox's connections are its holder's")

// Connection is an AI tool connected to a mailbox.
type Connection struct {
	KeyID    string
	Mailbox  string
	Client   string // the name the tool gave itself
	Scopes   []string
	Created  time.Time
	LastUsed time.Time // zero: not used yet
}

// Can reports whether the connection was granted scope.
func (c Connection) Can(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// ToolCall is one call in a mailbox's record.
type ToolCall struct {
	Client string
	Tool   string
	What   string
	At     time.Time
}

func (s *AccountStore) ensureConnectionTables() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_connections(
		key_id TEXT PRIMARY KEY,
		mailbox TEXT NOT NULL,
		client TEXT NOT NULL,
		scopes TEXT NOT NULL,
		created DATETIME NOT NULL,
		last_used DATETIME)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_tool_calls(
		mailbox TEXT NOT NULL,
		client TEXT NOT NULL,
		tool TEXT NOT NULL,
		what TEXT NOT NULL,
		at INTEGER NOT NULL)`)
	return err
}

// Connect records keyID as a connection to the mailbox at address, which
// must be an active mailbox here. Only the scopes this package knows are
// kept.
func (e *Engine) Connect(address, keyID, client string, scopes []string) error {
	if e.accounts == nil || keyID == "" {
		return errors.New("vayumail: no account store")
	}
	if err := e.accounts.ensureConnectionTables(); err != nil {
		return err
	}
	mbox := normEmail(address)
	ctx := context.Background()
	var active int
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT active FROM vayumail_accounts WHERE email=?`, mbox).Scan(&active); err != nil || active != 1 {
		return errors.New("vayumail: no such mailbox here")
	}
	var kept []string
	for _, s := range scopes {
		if (s == ScopeRead || s == ScopeDraft || s == ScopeCalendar) && !strings.Contains(strings.Join(kept, ","), s) {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return errors.New("vayumail: a connection is granted something")
	}
	_, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_connections(key_id,mailbox,client,scopes,created) VALUES(?,?,?,?,?)`,
		keyID, mbox, oneLine(client), strings.Join(kept, ","), time.Now().UTC())
	return err
}

// ConnectionOf is the connection a key was minted for, while its mailbox
// is active.
func (e *Engine) ConnectionOf(keyID string) (Connection, bool) {
	if e.accounts == nil || keyID == "" || e.accounts.ensureConnectionTables() != nil {
		return Connection{}, false
	}
	cs, err := e.connections(context.Background(), `SELECT c.key_id,c.mailbox,c.client,c.scopes,c.created,c.last_used FROM vayumail_connections c
		JOIN vayumail_accounts a ON a.email=c.mailbox AND a.active=1 WHERE c.key_id=?`, keyID)
	if err != nil || len(cs) != 1 {
		return Connection{}, false
	}
	return cs[0], true
}

// ownerOf is the holder's own mailbox, or ErrConnectionHolderOnly.
func (e *Engine) ownerOf(rd Reader) (string, error) {
	if rd.kind != readerOwner || e.accounts == nil {
		return "", ErrConnectionHolderOnly
	}
	if err := e.accounts.ensureConnectionTables(); err != nil {
		return "", err
	}
	dom, local := e.mailboxKey(rd.Key())
	return normEmail(local + "@" + dom), nil
}

// Connections is the holder's connections, newest first.
func (e *Engine) Connections(rd Reader) ([]Connection, error) {
	mbox, err := e.ownerOf(rd)
	if err != nil {
		return nil, err
	}
	return e.connections(context.Background(), `SELECT key_id,mailbox,client,scopes,created,last_used FROM vayumail_connections WHERE mailbox=? ORDER BY created DESC`, mbox)
}

// Disconnect ends one of the holder's connections. The caller revokes the
// key too; without its row the key reaches no mailbox in any case.
func (e *Engine) Disconnect(rd Reader, keyID string) error {
	mbox, err := e.ownerOf(rd)
	if err != nil {
		return err
	}
	res, err := e.accounts.db.Exec(`DELETE FROM vayumail_connections WHERE key_id=? AND mailbox=?`, keyID, mbox)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("vayumail: no such connection")
	}
	return nil
}

func (e *Engine) connections(ctx context.Context, query string, args ...any) ([]Connection, error) {
	rows, err := e.accounts.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		var scopes string
		var used *time.Time
		if err := rows.Scan(&c.KeyID, &c.Mailbox, &c.Client, &scopes, &c.Created, &used); err != nil {
			return nil, err
		}
		c.Scopes = strings.Split(scopes, ",")
		if used != nil {
			c.LastUsed = *used
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordToolCall adds a call to its mailbox's record, keeping the latest
// maxToolCalls, and notes the connection as used.
func (e *Engine) RecordToolCall(c Connection, tool, what string) {
	if e.accounts == nil || e.accounts.ensureConnectionTables() != nil {
		return
	}
	if len(what) > 300 {
		what = what[:300]
	}
	_, _ = e.accounts.db.Exec(`UPDATE vayumail_connections SET last_used=? WHERE key_id=?`, time.Now().UTC(), c.KeyID)
	_, _ = e.accounts.db.Exec(`INSERT INTO vayumail_tool_calls(mailbox,client,tool,what,at) VALUES(?,?,?,?,?)`, c.Mailbox, c.Client, tool, oneLine(what), time.Now().UTC().UnixNano())
	_, _ = e.accounts.db.Exec(`DELETE FROM vayumail_tool_calls WHERE mailbox=? AND rowid NOT IN
		(SELECT rowid FROM vayumail_tool_calls WHERE mailbox=? ORDER BY at DESC LIMIT ?)`, c.Mailbox, c.Mailbox, maxToolCalls)
}

// ToolCalls is the holder's record, newest first, at most limit.
func (e *Engine) ToolCalls(rd Reader, limit int) ([]ToolCall, error) {
	mbox, err := e.ownerOf(rd)
	if err != nil {
		return nil, err
	}
	rows, err := e.accounts.db.Query(`SELECT client,tool,what,at FROM vayumail_tool_calls WHERE mailbox=? ORDER BY at DESC LIMIT ?`, mbox, max(1, min(limit, maxToolCalls)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		var t ToolCall
		var at int64
		if err := rows.Scan(&t.Client, &t.Tool, &t.What, &at); err != nil {
			return nil, err
		}
		t.At = time.Unix(0, at).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// ToolDraft is a draft a connected tool makes.
type ToolDraft struct {
	To, Cc  []string
	Subject string
	Body    string
	// InReplyTo and References thread a reply to the message it answers.
	InReplyTo  string
	References []string
}

// SaveToolDraft files a draft a connected tool made into the holder's
// Drafts, marked with the tool's name, and gives its id. Nothing is sent:
// the holder opens it, changes what they like, and sends it.
func (e *Engine) SaveToolDraft(rd Reader, client string, d ToolDraft) (string, error) {
	mbox, err := e.ownerOf(rd)
	if err != nil {
		return "", err
	}
	if e.maildir == nil {
		return "", errors.New("vayumail: not started")
	}
	if e.MailboxOverQuota(mbox) {
		return "", ErrDriveFull
	}
	to, err := draftAddresses(d.To)
	if err != nil {
		return "", err
	}
	cc, err := draftAddresses(d.Cc)
	if err != nil {
		return "", err
	}
	var h strings.Builder
	h.WriteString("From: " + mbox + "\r\n")
	h.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	if len(cc) > 0 {
		h.WriteString("Cc: " + strings.Join(cc, ", ") + "\r\n")
	}
	h.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", oneLine(d.Subject)) + "\r\n")
	h.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	if id := strings.Trim(oneLine(d.InReplyTo), "<> "); id != "" {
		h.WriteString("In-Reply-To: <" + id + ">\r\n")
		refs := make([]string, 0, len(d.References)+1)
		for _, r := range d.References {
			if r = strings.Trim(oneLine(r), "<> "); r != "" {
				refs = append(refs, "<"+r+">")
			}
		}
		h.WriteString("References: " + strings.Join(append(refs, "<"+id+">"), " ") + "\r\n")
	}
	h.WriteString("X-Drafted-By: " + mime.QEncoding.Encode("utf-8", oneLine(client)) + "\r\n")
	h.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(d.Body, "\r\n", "\n"), "\n", "\r\n")
	dom, local := e.mailboxKey(rd.Key())
	return e.maildir.DeliverTo(dom, local, "Drafts", []byte(h.String()+body))
}

// ErrDraftAddress refuses a recipient that is not an address.
var ErrDraftAddress = errors.New("vayumail: a recipient is an address such as name@example.com")

// draftAddresses is each recipient as a header writes it, or
// ErrDraftAddress: a tool's text never becomes a header of its own.
func draftAddresses(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, a := range in {
		if strings.TrimSpace(a) == "" {
			continue
		}
		p, err := netmail.ParseAddress(a)
		if err != nil {
			return nil, ErrDraftAddress
		}
		out = append(out, p.String())
	}
	return out, nil
}

// EventOf is one of the holder's events as written (a repeating one as its
// series), with how many guests it has in any occurrence: a change to an
// event with guests mails them, which a tool does not do.
func (e *Engine) EventOf(rd Reader, uid string) (CalEvent, int, error) {
	mbox, err := e.calendarOf(rd)
	if err != nil {
		return CalEvent{}, 0, err
	}
	o, err := e.accounts.eventByUID(context.Background(), mbox, uid)
	if err != nil {
		return CalEvent{}, 0, err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return CalEvent{}, 0, err
	}
	master, overrides := parts(cal)
	if master == nil {
		return CalEvent{}, 0, errNoSuchEvent
	}
	ev, _, _, err := newCalReader(cal, time.UTC).event(master)
	if err != nil {
		return CalEvent{}, 0, err
	}
	guests := len(guestsOf(master))
	for _, ov := range overrides {
		guests += len(guestsOf(ov))
	}
	return ev, guests, nil
}
