package mail

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newContactStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	return s
}

// TestContactsPerMailboxIsolation is the central guarantee: a contact saved by
// one mailbox is never visible to another. Add the same-and-different contacts
// to two owners and assert each only sees its own.
func TestContactsPerMailboxIsolation(t *testing.T) {
	s := newContactStore(t)
	ctx := context.Background()

	if err := s.AddContact(ctx, "ankush@johal.in", "friend@example.com", "Friend"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddContact(ctx, "ankush@johal.in", "vendor@acme.com", "Acme"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddContact(ctx, "support@johal.in", "ticket@zen.com", "Zen"); err != nil {
		t.Fatalf("add: %v", err)
	}

	ankush, _ := s.ListContacts(ctx, "ankush@johal.in")
	if len(ankush) != 2 {
		t.Fatalf("ankush should have 2 contacts, got %d", len(ankush))
	}
	support, _ := s.ListContacts(ctx, "support@johal.in")
	if len(support) != 1 || support[0].Email != "ticket@zen.com" {
		t.Fatalf("support should have only its own contact, got %+v", support)
	}
	// support must NOT see ankush's contacts.
	for _, c := range support {
		if c.Email == "friend@example.com" || c.Email == "vendor@acme.com" {
			t.Fatalf("mailbox isolation breached: support sees %s", c.Email)
		}
	}
}

// TestContactUpsertAndNormalize verifies re-saving an address updates the name
// (not a duplicate row) and that addresses are matched case-insensitively.
func TestContactUpsertAndNormalize(t *testing.T) {
	s := newContactStore(t)
	ctx := context.Background()

	_ = s.AddContact(ctx, "me@johal.in", "Boss@Example.com", "Old Name")
	_ = s.AddContact(ctx, "me@johal.in", "boss@example.com", "New Name")

	list, _ := s.ListContacts(ctx, "me@johal.in")
	if len(list) != 1 {
		t.Fatalf("re-saving must upsert, not duplicate; got %d rows", len(list))
	}
	if list[0].Name != "New Name" {
		t.Errorf("name = %q, want New Name (upsert refreshes the name)", list[0].Name)
	}
	if list[0].Email != "boss@example.com" {
		t.Errorf("email = %q, want lowercased boss@example.com", list[0].Email)
	}
}

// TestContactSaveSelfAndBadInput verifies a mailbox saving itself is skipped and
// invalid input is rejected.
func TestContactSaveSelfAndBadInput(t *testing.T) {
	s := newContactStore(t)
	ctx := context.Background()

	if err := s.AddContact(ctx, "me@johal.in", "me@johal.in", "Me"); err != nil {
		t.Errorf("saving self should be a silent no-op, got err: %v", err)
	}
	if n := s.CountContacts(ctx, "me@johal.in"); n != 0 {
		t.Errorf("saving self must not create a contact, count = %d", n)
	}
	if err := s.AddContact(ctx, "me@johal.in", "not-an-email", ""); err == nil {
		t.Error("a non-address email should be rejected")
	}
	if err := s.AddContact(ctx, "", "x@y.com", ""); err == nil {
		t.Error("a blank owner should be rejected")
	}
}

// TestContactSearchScoped verifies substring search stays within the owner's book.
func TestContactSearchScoped(t *testing.T) {
	s := newContactStore(t)
	ctx := context.Background()
	_ = s.AddContact(ctx, "a@johal.in", "alice@corp.com", "Alice")
	_ = s.AddContact(ctx, "a@johal.in", "bob@corp.com", "Bob")
	_ = s.AddContact(ctx, "b@johal.in", "alice@corp.com", "Alice")

	// Search within a@ for "alice" returns a's alice only.
	res, _ := s.SearchContacts(ctx, "a@johal.in", "alice", 10)
	if len(res) != 1 || res[0].Email != "alice@corp.com" {
		t.Fatalf("scoped search = %+v, want a's alice only", res)
	}
	// A different owner's search never returns another owner's rows beyond its own.
	resB, _ := s.SearchContacts(ctx, "b@johal.in", "corp", 10)
	if len(resB) != 1 {
		t.Fatalf("owner b should match only its own 1 contact, got %d", len(resB))
	}

	// Delete removes only the owner's copy, not the other owner's same address.
	_ = s.DeleteContact(ctx, "a@johal.in", "alice@corp.com")
	if n := s.CountContacts(ctx, "a@johal.in"); n != 1 {
		t.Errorf("after delete a should have 1 left, got %d", n)
	}
	if n := s.CountContacts(ctx, "b@johal.in"); n != 1 {
		t.Errorf("deleting a's contact must not touch b's, got %d", n)
	}
}
