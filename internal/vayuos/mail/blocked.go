// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"net/mail"
	"strings"
)

// Blocked senders: addresses a mailbox will not take mail from.
//
// A block is matched against both addresses a message carries. The envelope
// sender is known at RCPT, before anything is accepted, so a message naming a
// blocked sender there is refused to that recipient alone (smtpd,
// WithBlockCheck). The From a person reads, and so blocks, is often not the
// envelope's (a newsletter sends from a bounce address), and is known only
// with the message: such mail is filed straight into Trash, where retention
// clears it, rather than refused, because refusing at DATA would refuse it to
// every other recipient of the same message.

// maxBlockedPerMailbox bounds the list.
const maxBlockedPerMailbox = 1000

func (s *AccountStore) ensureBlockedTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_blocked(
		mailbox TEXT NOT NULL,
		sender TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(mailbox, sender));`)
	return err
}

// blockAddress is the form a sender is kept and compared in.
func blockAddress(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		s = a.Address
	}
	return normEmail(s)
}

// isBlocked reports whether mailbox blocks sender.
func (s *AccountStore) isBlocked(ctx context.Context, mailbox, sender string) bool {
	sender = blockAddress(sender)
	if sender == "" || s.ensureBlockedTable() != nil {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_blocked WHERE mailbox=? AND sender=?`, normEmail(mailbox), sender).Scan(&n)
	return n > 0
}

// BlockSender adds sender to the blocked list of rd's mailbox.
func (e *Engine) BlockSender(rd Reader, sender string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil {
		return errors.New("vayumail: not started")
	}
	addr := blockAddress(sender)
	if !strings.Contains(addr, "@") {
		return errors.New("that is not an address that can be blocked")
	}
	if err := e.accounts.ensureBlockedTable(); err != nil {
		return err
	}
	mbox := e.mailboxAddr(rd)
	if mbox == addr {
		return errors.New("a mailbox cannot block itself")
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_blocked WHERE mailbox=?`, mbox).Scan(&n)
	if n >= maxBlockedPerMailbox {
		return errors.New("the blocked list is full")
	}
	_, err := e.db.Exec(`INSERT OR IGNORE INTO vayumail_blocked(mailbox, sender) VALUES(?,?)`, mbox, addr)
	return err
}

// UnblockSender takes sender off the blocked list of rd's mailbox.
func (e *Engine) UnblockSender(rd Reader, sender string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureBlockedTable() != nil {
		return errors.New("vayumail: not started")
	}
	_, err := e.db.Exec(`DELETE FROM vayumail_blocked WHERE mailbox=? AND sender=?`, e.mailboxAddr(rd), blockAddress(sender))
	return err
}

// BlockedSenders lists the blocked senders of rd's mailbox, in order.
func (e *Engine) BlockedSenders(rd Reader) ([]string, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureBlockedTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT sender FROM vayumail_blocked WHERE mailbox=? ORDER BY sender`, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

// senderRefused is the RCPT-time check: rcpt's mailbox blocks the envelope
// sender.
func (e *Engine) senderRefused(from, rcpt string) bool {
	if e.accounts == nil {
		return false
	}
	if target := e.accounts.ResolveAlias(context.Background(), rcpt); target != "" {
		rcpt = target
	}
	return e.accounts.isBlocked(context.Background(), rcpt, from)
}

// blockedAtDelivery is the delivery-time check: the mailbox blocks the
// envelope sender or the message's From.
func (e *Engine) blockedAtDelivery(mailbox, envelopeFrom string, raw []byte) bool {
	if e.accounts == nil {
		return false
	}
	if e.accounts.isBlocked(context.Background(), mailbox, envelopeFrom) {
		return true
	}
	if m, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		return e.accounts.isBlocked(context.Background(), mailbox, m.Header.Get("From"))
	}
	return false
}

// mailboxAddr is rd's mailbox as a full address.
func (e *Engine) mailboxAddr(rd Reader) string {
	dom, local := e.mailboxKey(rd.Key())
	return normEmail(local + "@" + dom)
}

// EmptyFolder deletes every message in Trash or Junk for good: the two
// folders whose mail is already on its way out. Any other is refused, so
// no mistaken request can empty Inbox.
func (e *Engine) EmptyFolder(rd Reader, folder string) (int, error) {
	if err := e.writeAuthorised(rd); err != nil {
		return 0, err
	}
	if !strings.EqualFold(folder, "Trash") && !strings.EqualFold(folder, "Junk") {
		return 0, errors.New("only Trash and Junk can be emptied")
	}
	if e.maildir == nil {
		return 0, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(rd.Key())
	msgs, err := e.maildir.ListFolder(dom, local, folder)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range msgs {
		if e.maildir.deleteMessage(dom, local, folder, m.ID) == nil {
			n++
		}
	}
	return n, nil
}
