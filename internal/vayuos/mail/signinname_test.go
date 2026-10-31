// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func newSigninNameStore(t *testing.T) *AccountStore {
	t.Helper()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A sign-in name is set, found, refused when another mailbox has it or when
// it is a mailbox's local part, refused in a form it may not take, and
// cleared.
func TestASigninNameIsOneMailboxsAndNoAddresss(t *testing.T) {
	s := newSigninNameStore(t)
	ctx := context.Background()
	for _, e := range []string{"ann@example.com", "bob@example.com", "carol.k@example.com"} {
		if err := s.Create(ctx, e, "x", "", "mailbox"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSigninName(ctx, "ann@example.com", "Ann-7k2q"); err != nil {
		t.Fatal(err)
	}
	if got := s.SigninName(ctx, "ANN@example.com"); got != "ann-7k2q" {
		t.Errorf("ann's name = %q", got)
	}
	if got := s.AddressForSigninName(ctx, " ann-7k2q "); got != "ann@example.com" {
		t.Errorf("the name's mailbox = %q", got)
	}
	if err := s.SetSigninName(ctx, "bob@example.com", "ann-7k2q"); !errors.Is(err, ErrSigninNameTaken) {
		t.Errorf("a second mailbox taking ann's name: %v", err)
	}
	if err := s.SetSigninName(ctx, "ann@example.com", "carol.k"); !errors.Is(err, ErrSigninNameTaken) {
		t.Errorf("a name that is another mailbox's local part: %v, want refused as taken", err)
	}
	if err := s.SetSigninName(ctx, "bob@example.com", "carol_k"); err != nil {
		t.Errorf("a name that only looks like a local part with _ for . was refused: %v", err)
	}
	if err := s.SetSigninName(ctx, "ann@example.com", "carol.k2"); err != nil {
		t.Fatal(err)
	}
	_ = s.Create(ctx, "carol.k2@example.com", "x", "", "mailbox")
	if err := s.SetSigninName(ctx, "bob@example.com", "carol.k2"); !errors.Is(err, ErrSigninNameTaken) {
		t.Errorf("a name that is a mailbox's local part: %v", err)
	}
	for _, bad := range []string{"ann@example.com", "-start", "has space", "short"} {
		if err := s.SetSigninName(ctx, "bob@example.com", bad); !errors.Is(err, ErrSigninNameForm) {
			t.Errorf("%q: %v, want refused for its form", bad, err)
		}
	}
	if err := s.SetSigninName(ctx, "ann@example.com", ""); err != nil || s.SigninName(ctx, "ann@example.com") != "" || s.AddressForSigninName(ctx, "carol.k2") != "" {
		t.Errorf("clearing the name: %v, left %q", err, s.SigninName(ctx, "ann@example.com"))
	}
}
