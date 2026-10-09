// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Templates: a mailbox's saved wording, a subject and a body to start a
// message from. Saved under a name; saving under a name in use replaces it,
// which is how one is edited.

const (
	maxTemplatesPerMailbox = 50
	maxTemplateNameRunes   = 60
	maxTemplateBodyBytes   = 16 << 10
)

// Template is one saved subject and body.
type Template struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (s *AccountStore) ensureTemplatesTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_templates(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		name TEXT NOT NULL,
		subject TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(mailbox, name));`)
	return err
}

// SaveTemplate keeps subject and body under name in rd's mailbox, replacing
// a template of that name.
func (e *Engine) SaveTemplate(rd Reader, name, subject, body string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureTemplatesTable() != nil {
		return errors.New("vayumail: not started")
	}
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return errors.New("a template needs a name")
	case utf8.RuneCountInString(name) > maxTemplateNameRunes:
		return fmt.Errorf("a template's name is at most %d characters", maxTemplateNameRunes)
	case strings.TrimSpace(subject) == "" && strings.TrimSpace(body) == "":
		return errors.New("there is nothing to save: the subject and the message are empty")
	case len(subject)+len(body) > maxTemplateBodyBytes:
		return fmt.Errorf("a template is at most %d KB of text", maxTemplateBodyBytes>>10)
	}
	mbox := e.mailboxAddr(rd)
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_templates WHERE mailbox=? AND name<>?`, mbox, name).Scan(&n)
	if n >= maxTemplatesPerMailbox {
		return fmt.Errorf("this mailbox has %d templates; delete one to save another", maxTemplatesPerMailbox)
	}
	_, err := e.db.Exec(`INSERT INTO vayumail_templates(mailbox, name, subject, body) VALUES(?,?,?,?)
		ON CONFLICT(mailbox, name) DO UPDATE SET subject=excluded.subject, body=excluded.body, updated_at=CURRENT_TIMESTAMP`,
		mbox, name, subject, body)
	return err
}

// DeleteTemplate removes template id from rd's mailbox; another mailbox's id
// removes nothing.
func (e *Engine) DeleteTemplate(rd Reader, id int64) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureTemplatesTable() != nil {
		return errors.New("vayumail: not started")
	}
	_, err := e.db.Exec(`DELETE FROM vayumail_templates WHERE id=? AND mailbox=?`, id, e.mailboxAddr(rd))
	return err
}

// Templates lists rd's templates by name.
func (e *Engine) Templates(rd Reader) ([]Template, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureTemplatesTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT id, name, subject, body FROM vayumail_templates WHERE mailbox=? ORDER BY name COLLATE NOCASE`, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Subject, &t.Body); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
