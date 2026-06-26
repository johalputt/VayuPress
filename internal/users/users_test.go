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
	_, err = db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'author',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_login DATETIME,totp_secret TEXT NOT NULL DEFAULT '',totp_enabled INTEGER NOT NULL DEFAULT 0,avatar_url TEXT NOT NULL DEFAULT '',bio TEXT NOT NULL DEFAULT '',socials TEXT NOT NULL DEFAULT '{}')`)
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

func TestTOTPLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.Create(ctx, "bob@example.com", "Bob", "supersecret", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	// Fresh account: no secret, not enabled.
	secret, enabled, err := s.TOTPStatus(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secret != "" || enabled {
		t.Fatalf("new account should have no TOTP, got secret=%q enabled=%v", secret, enabled)
	}

	// Store a secret — still disabled until verified.
	if err := s.SetTOTPSecret(ctx, u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	secret, enabled, _ = s.TOTPStatus(ctx, u.ID)
	if secret != "JBSWY3DPEHPK3PXP" || enabled {
		t.Fatalf("after SetTOTPSecret: secret=%q enabled=%v", secret, enabled)
	}

	// Enable, then confirm via the email-keyed lookup used at login.
	if err := s.EnableTOTP(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	es, een, err := s.TOTPSecretByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if es != "JBSWY3DPEHPK3PXP" || !een {
		t.Fatalf("TOTPSecretByEmail: secret=%q enabled=%v", es, een)
	}

	// Disable clears everything.
	if err := s.DisableTOTP(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	secret, enabled, _ = s.TOTPStatus(ctx, u.ID)
	if secret != "" || enabled {
		t.Fatalf("after disable: secret=%q enabled=%v", secret, enabled)
	}
}
