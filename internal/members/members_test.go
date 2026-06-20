package members

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
	for _, stmt := range []string{
		`CREATE TABLE members(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,tier TEXT NOT NULL DEFAULT 'free',status TEXT NOT NULL DEFAULT 'active',stripe_customer TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE member_login_tokens(token_hash TEXT PRIMARY KEY,email TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE member_sessions(token_hash TEXT PRIMARY KEY,member_id TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE article_access(slug TEXT PRIMARY KEY,level TEXT NOT NULL DEFAULT 'public')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

func TestUpsertAndTier(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, err := s.Upsert(ctx, "Reader@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if m.Tier != TierFree || m.IsPaid() {
		t.Errorf("new member should be free, got %+v", m)
	}
	// Upsert is idempotent.
	m2, _ := s.Upsert(ctx, "reader@example.com")
	if m2.ID != m.ID {
		t.Error("upsert should return the same member")
	}
	if err := s.SetTier(ctx, "reader@example.com", TierPaid); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "reader@example.com")
	if !got.IsPaid() {
		t.Error("member should be paid after SetTier")
	}
}

func TestMagicLinkSingleUse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	token, err := s.CreateLoginToken(ctx, "a@b.com")
	if err != nil {
		t.Fatal(err)
	}
	email, err := s.ConsumeLoginToken(ctx, token)
	if err != nil || email != "a@b.com" {
		t.Fatalf("consume failed: %v email=%s", err, email)
	}
	// Second use must fail.
	if _, err := s.ConsumeLoginToken(ctx, token); err == nil {
		t.Error("magic link should be single-use")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "a@b.com")
	tok, err := s.CreateSession(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ValidateSession(ctx, tok)
	if err != nil || got.ID != m.ID {
		t.Fatalf("validate failed: %v", err)
	}
	if err := s.DestroySession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateSession(ctx, tok); err == nil {
		t.Error("destroyed session should not validate")
	}
}

func TestAccessLevels(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if lvl := s.GetAccess(ctx, "unknown"); lvl != AccessPublic {
		t.Errorf("default access = %q, want public", lvl)
	}
	if err := s.SetAccess(ctx, "premium", AccessMembers); err != nil {
		t.Fatal(err)
	}
	if lvl := s.GetAccess(ctx, "premium"); lvl != AccessMembers {
		t.Errorf("access = %q, want members", lvl)
	}
	if err := s.SetAccess(ctx, "premium", "bogus"); err == nil {
		t.Error("expected invalid level error")
	}
}

func TestUpgradeByEmailCreatesPaid(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpgradeByEmail(ctx, "new@b.com", "cus_123"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "new@b.com")
	if !got.IsPaid() {
		t.Error("UpgradeByEmail should create a paid member")
	}
}
