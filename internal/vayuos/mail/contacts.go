// SPDX-License-Identifier: Apache-2.0

package mail

// contacts.go — the per-mailbox address book. Every contact is owned by exactly
// one mailbox (the `owner` column), so one mailbox's saved contacts are never
// visible to another. Saving is an upsert keyed on (owner, email): re-saving a
// known address just refreshes its display name.

import (
	"context"
	"errors"
	"strings"
	"time"
)

// errBadContact is returned when a save is missing an owner or a usable address.
var errBadContact = errors.New("vayumail: invalid contact (owner and a valid email are required)")

// Contact is one saved address in a mailbox's private address book.
type Contact struct {
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// AddContact saves (or refreshes) a contact in the owner mailbox's address book.
// Both addresses are normalised; a blank owner or a non-address email is rejected.
func (s *AccountStore) AddContact(ctx context.Context, owner, email, name string) error {
	if s.db == nil {
		return nil
	}
	owner = normEmail(owner)
	email = normEmail(email)
	name = strings.TrimSpace(name)
	if len(name) > 200 {
		name = name[:200]
	}
	if owner == "" || email == "" || !strings.Contains(email, "@") {
		return errBadContact
	}
	// A mailbox saving itself as a contact is pointless noise — skip it silently.
	if owner == email {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_contacts(owner,email,name) VALUES(?,?,?)
		 ON CONFLICT(owner,email) DO UPDATE SET name=excluded.name`,
		owner, email, name)
	return err
}

// ListContacts returns all contacts owned by the given mailbox, ordered by a
// case-insensitive display key (name when set, else the address).
func (s *AccountStore) ListContacts(ctx context.Context, owner string) ([]Contact, error) {
	out := []Contact{}
	if s.db == nil {
		return out, nil
	}
	owner = normEmail(owner)
	if owner == "" {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT email,name,created_at FROM vayumail_contacts WHERE owner=?
		 ORDER BY LOWER(CASE WHEN name<>'' THEN name ELSE email END), email`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.Email, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteContact removes a contact from the owner mailbox's address book.
func (s *AccountStore) DeleteContact(ctx context.Context, owner, email string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM vayumail_contacts WHERE owner=? AND email=?`, normEmail(owner), normEmail(email))
	return err
}

// SearchContacts returns the owner mailbox's contacts matching q (a case-
// insensitive substring of either the address or the display name), capped at
// limit. An empty q returns the head of the list. Used to power compose
// autocomplete scoped to the sending mailbox only.
func (s *AccountStore) SearchContacts(ctx context.Context, owner, q string, limit int) ([]Contact, error) {
	out := []Contact{}
	if s.db == nil {
		return out, nil
	}
	owner = normEmail(owner)
	if owner == "" {
		return out, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	like := "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT email,name,created_at FROM vayumail_contacts
		 WHERE owner=? AND (LOWER(email) LIKE ? OR LOWER(name) LIKE ?)
		 ORDER BY LOWER(CASE WHEN name<>'' THEN name ELSE email END), email LIMIT ?`,
		owner, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.Email, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountContacts returns how many contacts a mailbox has saved (for the panel
// header / empty-state).
func (s *AccountStore) CountContacts(ctx context.Context, owner string) int {
	if s.db == nil {
		return 0
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vayumail_contacts WHERE owner=?`, normEmail(owner)).Scan(&n)
	return n
}
