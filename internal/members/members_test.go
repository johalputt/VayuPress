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
		`CREATE TABLE members(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',tier TEXT NOT NULL DEFAULT 'free',status TEXT NOT NULL DEFAULT 'active',newsletter_opt_in INTEGER NOT NULL DEFAULT 1,reply_notify INTEGER NOT NULL DEFAULT 1,stripe_customer TEXT NOT NULL DEFAULT '',last_seen_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE member_login_tokens(token_hash TEXT PRIMARY KEY,email TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE member_sessions(token_hash TEXT PRIMARY KEY,member_id TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE article_access(slug TEXT PRIMARY KEY,level TEXT NOT NULL DEFAULT 'public')`,
		`CREATE TABLE member_tiers(id TEXT PRIMARY KEY,slug TEXT NOT NULL UNIQUE,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',monthly_cents INTEGER NOT NULL DEFAULT 0,yearly_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',benefits TEXT NOT NULL DEFAULT '[]',visibility TEXT NOT NULL DEFAULT 'public',active INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,trial_days INTEGER NOT NULL DEFAULT 0,stripe_monthly_price TEXT NOT NULL DEFAULT '',stripe_yearly_price TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE member_subscriptions(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,tier_slug TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',cadence TEXT NOT NULL DEFAULT 'monthly',amount_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',stripe_subscription TEXT NOT NULL DEFAULT '',current_period_end DATETIME,trial_end DATETIME,cancel_at_period_end INTEGER NOT NULL DEFAULT 0,started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,canceled_at DATETIME)`,
		`CREATE TABLE member_labels(id TEXT PRIMARY KEY,name TEXT NOT NULL UNIQUE,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE member_label_map(member_id TEXT NOT NULL,label_id TEXT NOT NULL,PRIMARY KEY(member_id,label_id))`,
		`CREATE TABLE member_events(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,type TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',amount_cents INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO member_tiers(id,slug,name,monthly_cents,yearly_cents,sort) VALUES('tier_free','free','Free',0,0,0),('tier_paid','paid','Premium',500,5000,1)`,
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
