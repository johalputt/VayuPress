// SPDX-License-Identifier: Apache-2.0

package mail

// contactgroups.go — a mailbox's contact groups: a name on some of its
// contacts (contacts.go), so a message can go to everyone in one at once.
// A member is always a contact, which is what lets a vCard's CATEGORIES
// carry the groups out and back in unchanged (vcard.go). Owned per mailbox
// as contacts are, under the console's contactOwner authority.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxContactGroups       = 50
	maxContactGroupMembers = 500
	maxContactGroupName    = 40
)

// ContactGroup is one group and the addresses in it, in order.
type ContactGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// ValidGroupName reports whether name can name a group: letters, digits,
// spaces, '-' and '_', at most 40, with no space at either end. Compose
// reads a typed group name as the group, so a name can never be an address.
func ValidGroupName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > maxContactGroupName || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// SaveContactGroup makes the group name in owner's contacts exactly members:
// an address not yet a contact is saved as one, by address alone, and a
// member left out leaves the group. Saving under a name in use, in any case,
// replaces that group, which is how one is edited or renamed in case.
func (s *AccountStore) SaveContactGroup(ctx context.Context, owner, name string, members []string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	owner = normEmail(owner)
	if !ValidGroupName(name) {
		return errors.New("a group's name is letters, digits, spaces, '-' and '_', at most 40")
	}
	seen := map[string]bool{owner: true}
	var list []string
	for _, m := range members {
		m = normEmail(m)
		if m == "" || seen[m] {
			continue
		}
		if !strings.Contains(m, "@") {
			return fmt.Errorf("%q is not an email address", m)
		}
		seen[m] = true
		list = append(list, m)
	}
	switch {
	case len(list) == 0:
		return errors.New("a group needs at least one address other than this mailbox's own")
	case len(list) > maxContactGroupMembers:
		return fmt.Errorf("a group has at most %d people", maxContactGroupMembers)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var others int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT name) FROM vayumail_contact_groups WHERE owner=? AND name<>?`, owner, name).Scan(&others); err != nil {
		return err
	}
	if others >= maxContactGroups {
		return fmt.Errorf("this mailbox keeps %d groups; delete one to make another", maxContactGroups)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_contact_groups WHERE owner=? AND name=?`, owner, name); err != nil {
		return err
	}
	for _, m := range list {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO vayumail_contacts(owner,email,name) VALUES(?,?,'')`, owner, m); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_contact_groups(owner,name,email) VALUES(?,?,?)`, owner, name, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteContactGroup removes the group; its members stay contacts.
func (s *AccountStore) DeleteContactGroup(ctx context.Context, owner, name string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_contact_groups WHERE owner=? AND name=?`, normEmail(owner), name)
	return err
}

// ContactGroups lists owner's groups by name, each with its members.
func (s *AccountStore) ContactGroups(ctx context.Context, owner string) ([]ContactGroup, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, email FROM vayumail_contact_groups WHERE owner=? ORDER BY name, email`, normEmail(owner))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactGroup
	for rows.Next() {
		var name, email string
		if err := rows.Scan(&name, &email); err != nil {
			return nil, err
		}
		if len(out) == 0 || !strings.EqualFold(out[len(out)-1].Name, name) {
			out = append(out, ContactGroup{Name: name})
		}
		g := &out[len(out)-1]
		g.Members = append(g.Members, email)
	}
	return out, rows.Err()
}

// groupsByContact is owner's groups turned about: each address and the
// groups it is in, in the groups' own order.
func groupsByContact(groups []ContactGroup) map[string][]string {
	out := map[string][]string{}
	for _, g := range groups {
		for _, m := range g.Members {
			out[m] = append(out[m], g.Name)
		}
	}
	return out
}
