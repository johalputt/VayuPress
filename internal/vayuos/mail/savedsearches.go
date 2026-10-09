// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"errors"
	"unicode/utf8"
)

// Saved searches: a mailbox's searches, kept to run again from Mail's
// sidebar. Each is stored as its terms in SearchQuery.String's one form, so
// it runs the same however the search field's scope is set, and the same
// search typed two ways is kept once.

const (
	maxSavedSearchesPerMailbox = 30
	maxSavedSearchRunes        = 200
)

func (s *AccountStore) ensureSavedSearchesTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_saved_searches(
		mailbox TEXT NOT NULL,
		query TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(mailbox, query));`)
	return err
}

// SaveSearch keeps query in rd's mailbox. A query already kept is kept once.
func (e *Engine) SaveSearch(rd Reader, query string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureSavedSearchesTable() != nil {
		return errors.New("vayumail: not started")
	}
	parsed := ParseSearchQuery(query)
	query = parsed.String()
	switch {
	case parsed.Empty():
		return errors.New("there is nothing to save: the search is empty")
	case utf8.RuneCountInString(query) > maxSavedSearchRunes:
		return errors.New("a saved search is at most 200 characters")
	}
	mbox := e.mailboxAddr(rd)
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(1) FROM vayumail_saved_searches WHERE mailbox=? AND query<>?`, mbox, query).Scan(&n)
	if n >= maxSavedSearchesPerMailbox {
		return errors.New("this mailbox keeps 30 searches; forget one to save another")
	}
	_, err := e.db.Exec(`INSERT OR IGNORE INTO vayumail_saved_searches(mailbox, query) VALUES(?,?)`, mbox, query)
	return err
}

// ForgetSearch removes query from rd's saved searches.
func (e *Engine) ForgetSearch(rd Reader, query string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureSavedSearchesTable() != nil {
		return errors.New("vayumail: not started")
	}
	_, err := e.db.Exec(`DELETE FROM vayumail_saved_searches WHERE mailbox=? AND query=?`, e.mailboxAddr(rd), ParseSearchQuery(query).String())
	return err
}

// SavedSearches lists rd's saved searches in the order they were saved.
func (e *Engine) SavedSearches(rd Reader) ([]string, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureSavedSearchesTable() != nil {
		return nil, errors.New("vayumail: not started")
	}
	rows, err := e.db.Query(`SELECT query FROM vayumail_saved_searches WHERE mailbox=? ORDER BY created_at, rowid`, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
