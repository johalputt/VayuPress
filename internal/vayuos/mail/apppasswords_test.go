package mail

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestAppPasswordLifecycle covers create → verify-listable → rotate-by-label →
// revoke-by-id, including the mailbox scoping that stops cross-account revokes.
func TestAppPasswordLifecycle(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1)
	defer db.Close()
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()

	id1, err := s.CreateAppPassword(ctx, "User@Example.com", "setup-qr", "hashA")
	if err != nil || id1 == 0 {
		t.Fatalf("create 1: id=%d err=%v", id1, err)
	}
	id2, err := s.CreateAppPassword(ctx, "user@example.com", "phone", "hashB")
	if err != nil || id2 == 0 {
		t.Fatalf("create 2: id=%d err=%v", id2, err)
	}

	if got := s.AppPasswordHashes(ctx, "user@example.com"); len(got) != 2 {
		t.Fatalf("want 2 hashes, got %v", got)
	}
	if got := s.ListAppPasswords(ctx, "user@example.com"); len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	} else if got[0].Label != "phone" { // newest first
		t.Fatalf("order/label mismatch: %+v", got)
	}

	// Rotating the setup QR retires only that label.
	if err := s.DeleteAppPasswordsByLabel(ctx, "user@example.com", "setup-qr"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if got := s.AppPasswordHashes(ctx, "user@example.com"); len(got) != 1 || got[0] != "hashB" {
		t.Fatalf("after rotate want [hashB], got %v", got)
	}

	// Another mailbox cannot revoke this credential.
	if err := s.DeleteAppPassword(ctx, "other@example.com", id2); err == nil {
		t.Fatal("cross-account revoke must fail")
	}
	if err := s.DeleteAppPassword(ctx, "user@example.com", id2); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := s.AppPasswordHashes(ctx, "user@example.com"); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}
