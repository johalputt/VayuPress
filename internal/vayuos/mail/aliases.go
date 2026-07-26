// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Aliases and auto-forwarding.
//
// An ALIAS is an extra receive-only address (sales@domain) that delivers into
// an existing mailbox. Resolution is single-level by construction: an alias's
// target must be a real account at creation time and can never be another
// alias, so delivery never chases chains or loops.
//
// AUTO-FORWARD is a per-mailbox setting: inbound mail is filed locally as
// normal AND a copy is relayed to the forward address through the outbound
// queue. A tagging header (see forwardLoopHeader in inbound.go) stops two
// VayuMail servers forwarding at each other forever.

// Alias is one extra address delivering into Target's mailbox.
type Alias struct {
	Alias     string    `json:"alias"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateAlias registers alias → target. The caller validates shape/domain; the
// store enforces referential rules: the target must be an existing account, and
// the alias must not collide with an account or an existing alias.
func (s *AccountStore) CreateAlias(ctx context.Context, alias, target string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	alias, target = normEmail(alias), normEmail(target)
	if alias == "" || target == "" {
		return errors.New("alias and target are required")
	}
	if alias == target {
		return errors.New("an alias cannot point at itself")
	}
	// Target must be a real, existing account (never another alias).
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_accounts WHERE email=?`, target).Scan(&n)
	if n == 0 {
		return errors.New("target mailbox does not exist")
	}
	// The alias must not shadow a real account's address.
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_accounts WHERE email=?`, alias).Scan(&n)
	if n > 0 {
		return errors.New("that address is already a mailbox")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_aliases(alias,target,created_at) VALUES(?,?,?)`, alias, target, time.Now().UTC())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return errors.New("that alias already exists")
	}
	return err
}

// DeleteAlias removes an alias.
func (s *AccountStore) DeleteAlias(ctx context.Context, alias string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_aliases WHERE alias=?`, normEmail(alias))
	return err
}

// ListAliases returns every alias, ordered for display.
func (s *AccountStore) ListAliases(ctx context.Context) ([]Alias, error) {
	out := []Alias{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT alias,target,created_at FROM vayumail_aliases ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.Alias, &a.Target, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveAlias returns the target mailbox for addr when addr is an alias, or
// "" when it is not. Single-level by construction (see CreateAlias).
func (s *AccountStore) ResolveAlias(ctx context.Context, addr string) string {
	if s.db == nil {
		return ""
	}
	var target string
	_ = s.db.QueryRowContext(ctx, `SELECT target FROM vayumail_aliases WHERE alias=?`, normEmail(addr)).Scan(&target)
	return target
}

// SetForward sets (or clears, with "") an account's auto-forward address. The
// caller validates the address shape; the store rejects self-forwarding.
func (s *AccountStore) SetForward(ctx context.Context, email, forwardTo string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	email, forwardTo = normEmail(email), normEmail(forwardTo)
	if forwardTo != "" && forwardTo == email {
		return errors.New("a mailbox cannot forward to itself")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET forward_to=? WHERE email=?`, forwardTo, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// ForwardFor returns an account's auto-forward address ("" = off).
func (s *AccountStore) ForwardFor(ctx context.Context, email string) string {
	if s.db == nil {
		return ""
	}
	var fwd string
	_ = s.db.QueryRowContext(ctx, `SELECT forward_to FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&fwd)
	return fwd
}
