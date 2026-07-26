// SPDX-License-Identifier: Apache-2.0

package members

import (
	"context"
	"testing"
)

// A member row is a claim that somebody proved they control an address. These
// tests pin that claim, because the bug being fixed was exactly its absence:
// requesting a sign-in link created a member, so a mistyped or undeliverable
// address became a permanent "member" who never received anything.

// TestCreationAlwaysRecordsVerification pins that there is no way to create a
// member without stamping verified_at. Creation is only reachable from callers
// that have just proven the address (magic link consumed, mailbox credential
// authenticated, payment completed), so the stamp belongs at the single insert
// rather than being left to each caller to remember.
func TestCreationAlwaysRecordsVerification(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.UpsertScoped(ctx, "", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if m.VerifiedAt == nil {
		t.Fatal("a newly created member must carry verified_at")
	}
	// The stamp must be persisted, not merely present on the returned value.
	again, err := s.Get(ctx, "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if again.VerifiedAt == nil {
		t.Error("verified_at must be stored, not only returned")
	}
	if s.CountUnverified(ctx) != 0 {
		t.Error("a member created through the normal path is not unconfirmed")
	}
}

// TestLegacyUnverifiedRowIsConfirmedOnFirstProof covers the person who is real
// but whose row predates the rule: the moment they do come back with a valid
// link, the same upsert must confirm the existing row instead of leaving them
// flagged forever.
func TestLegacyUnverifiedRowIsConfirmedOnFirstProof(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	insertLegacyUnverified(t, s, "legacy@example.com")

	if s.CountUnverified(ctx) != 1 {
		t.Fatal("expected the legacy row to count as unconfirmed")
	}
	m, err := s.UpsertScoped(ctx, "", "legacy@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if m.VerifiedAt == nil {
		t.Error("proving the address must confirm the existing row")
	}
	if n := s.CountUnverified(ctx); n != 0 {
		t.Errorf("CountUnverified = %d, want 0 after confirmation", n)
	}
}

// TestDeleteOnlyRemovesUnconfirmedMembers is the safety property behind the
// console's Remove button: the cleanup path must be incapable of deleting a real
// member's account, however it is called.
func TestDeleteOnlyRemovesUnconfirmedMembers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.UpsertScoped(ctx, "", "real@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "real@example.com"); err == nil {
		t.Fatal("Delete must refuse a verified member")
	}
	if _, err := s.Get(ctx, "real@example.com"); err != nil {
		t.Error("the verified member must still exist after the refusal")
	}

	insertLegacyUnverified(t, s, "ghost@example.com")
	if err := s.Delete(ctx, "ghost@example.com"); err != nil {
		t.Fatalf("Delete of an unconfirmed member: %v", err)
	}
	if _, err := s.Get(ctx, "ghost@example.com"); err == nil {
		t.Error("the unconfirmed member should be gone")
	}
	if err := s.Delete(ctx, "nobody@example.com"); err == nil {
		t.Error("Delete of an unknown address should fail")
	}
}

// TestDeleteClearsPendingSignInLinks makes sure removing a ghost row also drops
// any sign-in link still outstanding for that address — otherwise a link issued
// before the cleanup could recreate the member afterwards.
func TestDeleteClearsPendingSignInLinks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	insertLegacyUnverified(t, s, "ghost@example.com")

	token, err := s.CreateLoginToken(ctx, "ghost@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "ghost@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLoginToken(ctx, token); err == nil {
		t.Error("a sign-in link issued before removal must not survive it")
	}
}

// TestUnverifiedListsOnlyGhosts checks the console's listing shows exactly the
// rows it offers to remove — nothing more.
func TestUnverifiedListsOnlyGhosts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertScoped(ctx, "", "real@example.com"); err != nil {
		t.Fatal(err)
	}
	insertLegacyUnverified(t, s, "ghost1@example.com")
	insertLegacyUnverified(t, s, "ghost2@example.com")

	list, err := s.Unverified(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("Unverified returned %d rows, want 2", len(list))
	}
	for _, m := range list {
		if m.VerifiedAt != nil {
			t.Errorf("%s is verified and must not be listed for removal", m.Email)
		}
	}
}

// TestExistsDoesNotCreate guards the helper the login flow uses to decide whether
// a welcome greeting is due: asking must never be the thing that creates a row.
func TestExistsDoesNotCreate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if s.Exists(ctx, "stranger@example.com") {
		t.Error("Exists reported an address that was never added")
	}
	if n := s.CountUnverified(ctx); n != 0 {
		t.Errorf("asking about an address created %d rows", n)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM members`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("members table holds %d rows, want 0", total)
	}
}

// insertLegacyUnverified writes the row shape the old pre-send signup path left
// behind. It goes in with raw SQL on purpose: no exported API can produce an
// unverified member any more, which is the guarantee being tested.
func insertLegacyUnverified(t *testing.T, s *Store, email string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO members(id,email,domain_id) VALUES(?,?,'')`, randHex(12), email); err != nil {
		t.Fatal(err)
	}
}
