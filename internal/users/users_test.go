package users

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'author',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_login DATETIME)`)
	if err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestCreateAndAuthenticate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Alice@Example.com", "Alice", "supersecret", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	// Email is normalised to lowercase.
	u, err := s.Authenticate(ctx, "alice@example.com", "supersecret")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", u.Role)
	}
	if _, err := s.Authenticate(ctx, "alice@example.com", "wrong"); err == nil {
		t.Error("expected auth failure for wrong password")
	}
	if _, err := s.Authenticate(ctx, "nobody@example.com", "x"); err == nil {
		t.Error("expected auth failure for unknown email")
	}
}

func TestValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "bad-email", "x", "supersecret", ""); err == nil {
		t.Error("expected invalid email error")
	}
	if _, err := s.Create(ctx, "a@b.com", "x", "short", ""); err == nil {
		t.Error("expected short-password error")
	}
	if _, err := s.Create(ctx, "a@b.com", "x", "supersecret", "superuser"); err == nil {
		t.Error("expected invalid-role error")
	}
}

func TestDuplicateEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "a@b.com", "", "supersecret", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "a@b.com", "", "supersecret", ""); err == nil {
		t.Error("expected duplicate-email error")
	}
}

func TestSetPasswordAndCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Create(ctx, "a@b.com", "", "supersecret", "")
	if err := s.SetPassword(ctx, "a@b.com", "newsupersecret"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "a@b.com", "newsupersecret"); err != nil {
		t.Errorf("auth with new password failed: %v", err)
	}
	n, _ := s.Count(ctx)
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}
