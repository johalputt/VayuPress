// SPDX-License-Identifier: Apache-2.0

package mail

// labels.go — labels: names on a mailbox's messages, so one message can be in
// several places without being copied. Maildir has folders and nothing else,
// so a label is kept here, against the message's Message-ID: it follows the
// message from folder to folder, and every copy of one message (the Sent copy
// and the one delivered back) carries it. Mail apps over IMAP see folders
// only; labels are the console's.

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxLabelsPerMailbox = 100
	maxLabelName        = 30
)

// ErrNoMessageID refuses a label on a message that has no Message-ID, which
// is the only name a label can keep for it.
var ErrNoMessageID = errors.New("this message has no Message-ID, so it cannot be labelled")

// ValidLabel reports whether name can name a label: letters, digits, spaces,
// '-' and '_', at most 30, with no space at either end. A label is typed as
// a search term (label:name), so it holds nothing a term would misread.
func ValidLabel(name string) bool {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > maxLabelName || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func (s *AccountStore) ensureLabelsTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_labels(
		mailbox TEXT NOT NULL,
		message_id TEXT NOT NULL,
		label TEXT NOT NULL COLLATE NOCASE,
		PRIMARY KEY(mailbox, message_id, label));`)
	return err
}

// SetLabel puts label on the message messageID in rd's mailbox, or takes it
// off. A label is kept in the spelling it was first given.
func (e *Engine) SetLabel(rd Reader, messageID, label string, on bool) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureLabelsTable() != nil {
		return errors.New("vayumail: not started")
	}
	id, mbox := cleanMessageID(messageID), e.mailboxAddr(rd)
	if id == "" {
		return ErrNoMessageID
	}
	if !on {
		_, err := e.db.Exec(`DELETE FROM vayumail_labels WHERE mailbox=? AND message_id=? AND label=?`, mbox, id, label)
		return err
	}
	label = strings.TrimSpace(label)
	if !ValidLabel(label) {
		return errors.New("a label is letters, digits, spaces, '-' and '_', at most 30")
	}
	var spelling string
	if err := e.db.QueryRow(`SELECT label FROM vayumail_labels WHERE mailbox=? AND label=? LIMIT 1`, mbox, label).Scan(&spelling); err == nil {
		label = spelling
	} else {
		var n int
		_ = e.db.QueryRow(`SELECT COUNT(DISTINCT label) FROM vayumail_labels WHERE mailbox=?`, mbox).Scan(&n)
		if n >= maxLabelsPerMailbox {
			return errors.New("this mailbox keeps 100 labels; take one off every message to make another")
		}
	}
	_, err := e.db.Exec(`INSERT OR IGNORE INTO vayumail_labels(mailbox,message_id,label) VALUES(?,?,?)`, mbox, id, label)
	return err
}

// Labels lists the labels in use in rd's mailbox, by name.
func (e *Engine) Labels(rd Reader) ([]string, error) {
	return e.labelQuery(rd, `SELECT DISTINCT label FROM vayumail_labels WHERE mailbox=? ORDER BY label`)
}

// LabelsByMessage is every labelled message of rd's mailbox, by Message-ID,
// with its labels by name: one read for a whole list.
func (e *Engine) LabelsByMessage(rd Reader) (map[string][]string, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureLabelsTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT message_id, label FROM vayumail_labels WHERE mailbox=? ORDER BY label`, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		out[id] = append(out[id], label)
	}
	return out, rows.Err()
}

func (e *Engine) labelQuery(rd Reader, q string) ([]string, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureLabelsTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(q, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// labelled is the set of Message-IDs in rd's mailbox that carry label.
func (e *Engine) labelled(rd Reader, label string) (map[string]bool, error) {
	if e.accounts == nil || e.accounts.ensureLabelsTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT message_id FROM vayumail_labels WHERE mailbox=? AND label=?`, e.mailboxAddr(rd), label)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
