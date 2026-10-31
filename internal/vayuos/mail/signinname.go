// SPDX-License-Identifier: Apache-2.0

package mail

// signinname.go — a mailbox's sign-in name. A public address is guessed at
// because it is public: everyone who writes to it knows it. With a sign-in
// name set, the address people write to no longer signs in; only the name
// does, at every door, so a guesser who has the address has half of nothing.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

// Sign-in name errors, for the page to say.
var (
	ErrSigninNameForm  = errors.New("a sign-in name is 6 to 64 letters, digits, dots, dashes or underscores, starting with a letter or digit, and has no @")
	ErrSigninNameTaken = errors.New("that sign-in name cannot be used")
)

var signinNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{5,63}$`)

// SigninName is email's sign-in name, or "" when it has none.
func (s *AccountStore) SigninName(ctx context.Context, email string) string {
	if s.db == nil {
		return ""
	}
	var name sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT signin_name FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&name)
	return name.String
}

// AddressForSigninName is the mailbox whose sign-in name is name, or "".
func (s *AccountStore) AddressForSigninName(ctx context.Context, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if s.db == nil || !signinNameRe.MatchString(name) {
		return ""
	}
	var email string
	_ = s.db.QueryRowContext(ctx, `SELECT email FROM vayumail_accounts WHERE signin_name=?`, name).Scan(&email)
	return email
}

// SetSigninName gives email the sign-in name name, or clears it when name is
// "". A name is refused when another mailbox has it, or when it is the local
// part of a mailbox here: a mail app signing in with a bare local part would
// otherwise reach the named mailbox instead of its own. Both refusals give
// one answer, so the page cannot be used to find which names are taken.
func (s *AccountStore) SetSigninName(ctx context.Context, email, name string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	name = strings.ToLower(strings.TrimSpace(name))
	var arg any
	if name != "" {
		if !signinNameRe.MatchString(name) {
			return ErrSigninNameForm
		}
		var n int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_accounts WHERE (signin_name=? AND email<>?) OR email LIKE ? ESCAPE '\'`,
			name, normEmail(email), strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(name)+"@%").Scan(&n)
		if n > 0 {
			return ErrSigninNameTaken
		}
		arg = name
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET signin_name=? WHERE email=?`, arg, normEmail(email))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrSigninNameTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}
