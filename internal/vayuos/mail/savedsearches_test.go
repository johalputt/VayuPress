// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func saved(t *testing.T, e *Engine, rd Reader) string {
	t.Helper()
	list, err := e.SavedSearches(rd)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(list, ",")
}

// A search typed two ways is kept once, in the one written form, in the
// order searches were saved; it is forgotten by either spelling.
func TestASavedSearchIsKeptOnceInOneForm(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for _, q := range []string{"from:priya   invoice", "is:unread", `invoice FROM:"priya"`} {
		if err := e.SaveSearch(rd, q); err != nil {
			t.Fatalf("save %q: %v", q, err)
		}
	}
	if got := saved(t, e, rd); got != "invoice from:priya,is:unread" {
		t.Fatalf("kept %q", got)
	}
	if err := e.ForgetSearch(rd, `  invoice  from:"priya"`); err != nil {
		t.Fatal(err)
	}
	if got := saved(t, e, rd); got != "is:unread" {
		t.Fatalf("after forgetting, kept %q", got)
	}
}

// One seed per refusal, and at the cap a search already kept is still
// accepted.
func TestWhatASavedSearchCannotBe(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	rd := ReadAsOwner("alice")
	for name, c := range map[string]struct{ q, want string }{
		"empty":    {`  "" from: `, "nothing to save"},
		"too long": {strings.Repeat("é", 201), "at most 200 characters"},
	} {
		if err := e.SaveSearch(rd, c.q); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := e.SaveSearch(rd, strings.Repeat("é", 200)); err != nil {
		t.Errorf("a 200-character search: %v", err)
	}
	for i := 1; i < maxSavedSearchesPerMailbox; i++ {
		if err := e.SaveSearch(rd, "s"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.SaveSearch(rd, "one more"); err == nil || !strings.Contains(err.Error(), "forget one") {
		t.Fatalf("past the cap: %v", err)
	}
	if err := e.SaveSearch(rd, "s7"); err != nil {
		t.Fatalf("saving a kept search at the cap: %v", err)
	}
}

// A saved search is its mailbox's: another mailbox neither lists nor forgets
// it, a read-only mailbox saves none, and deleting the mailbox removes them.
func TestSavedSearchesAreTheMailboxsOwn(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	ctx := context.Background()
	alice, bob := ReadAsOwner("alice"), ReadAsOwner("bob")
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveSearch(alice, "mine"); err != nil {
		t.Fatal(err)
	}
	if got := saved(t, e, bob); got != "" {
		t.Fatalf("bob lists %q", got)
	}
	if err := e.ForgetSearch(bob, "mine"); err != nil || saved(t, e, alice) != "mine" {
		t.Fatalf("bob forgetting alice's search: %v; alice has %q", err, saved(t, e, alice))
	}
	if err := e.accounts.Create(ctx, "rev@example.com", "x", "R", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	rev := ReadAsOwner("rev")
	if err := e.SaveSearch(rev, "x"); err == nil {
		t.Fatal("a read-only mailbox saved a search")
	}
	// One kept from before the mailbox became read-only stays kept.
	if _, err := e.db.Exec(`INSERT INTO vayumail_saved_searches(mailbox, query) VALUES(?, 'older')`, e.mailboxAddr(rev)); err != nil {
		t.Fatal(err)
	}
	if err := e.ForgetSearch(rev, "older"); err == nil || saved(t, e, rev) != "older" {
		t.Fatalf("a read-only mailbox forgot a search: %v; kept %q", err, saved(t, e, rev))
	}
	if err := e.accounts.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := saved(t, e, alice); got != "" {
		t.Fatalf("a deleted mailbox's searches outlived it: %q", got)
	}
}
