// SPDX-License-Identifier: Apache-2.0

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
		`CREATE TABLE members(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',tier TEXT NOT NULL DEFAULT 'free',status TEXT NOT NULL DEFAULT 'active',newsletter_opt_in INTEGER NOT NULL DEFAULT 1,reply_notify INTEGER NOT NULL DEFAULT 1,stripe_customer TEXT NOT NULL DEFAULT '',last_seen_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,country TEXT NOT NULL DEFAULT '',region TEXT NOT NULL DEFAULT '',city TEXT NOT NULL DEFAULT '',domain_id TEXT NOT NULL DEFAULT '',gender TEXT NOT NULL DEFAULT '',avatar_choice TEXT NOT NULL DEFAULT '',avatar_mime TEXT NOT NULL DEFAULT '',avatar_blob BLOB,mail_address TEXT NOT NULL DEFAULT '',verified_at DATETIME)`,
		`CREATE TABLE member_login_tokens(token_hash TEXT PRIMARY KEY,email TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE member_sessions(token_hash TEXT PRIMARY KEY,member_id TEXT NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE article_access(slug TEXT PRIMARY KEY,level TEXT NOT NULL DEFAULT 'public',price_cents INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE article_purchases(id TEXT PRIMARY KEY,email TEXT NOT NULL,slug TEXT NOT NULL,order_ref TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,paid_at DATETIME,UNIQUE(email,slug))`,
		`CREATE TABLE member_tiers(id TEXT PRIMARY KEY,slug TEXT NOT NULL UNIQUE,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',monthly_cents INTEGER NOT NULL DEFAULT 0,yearly_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',benefits TEXT NOT NULL DEFAULT '[]',visibility TEXT NOT NULL DEFAULT 'public',active INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,trial_days INTEGER NOT NULL DEFAULT 0,stripe_monthly_price TEXT NOT NULL DEFAULT '',stripe_yearly_price TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,mail_enabled INTEGER NOT NULL DEFAULT 0,mail_quota_mb INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE member_subscriptions(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,tier_slug TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',cadence TEXT NOT NULL DEFAULT 'monthly',amount_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',stripe_subscription TEXT NOT NULL DEFAULT '',current_period_end DATETIME,trial_end DATETIME,cancel_at_period_end INTEGER NOT NULL DEFAULT 0,started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,canceled_at DATETIME)`,
		`CREATE TABLE member_labels(id TEXT PRIMARY KEY,name TEXT NOT NULL UNIQUE,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE member_label_map(member_id TEXT NOT NULL,label_id TEXT NOT NULL,PRIMARY KEY(member_id,label_id))`,
		`CREATE TABLE member_events(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,type TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',amount_cents INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE mailid_agreements(id TEXT PRIMARY KEY,email TEXT NOT NULL,address TEXT NOT NULL,terms_sha256 TEXT NOT NULL,accepted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE premium_mailid_grants(id TEXT PRIMARY KEY,email TEXT NOT NULL,localpart TEXT NOT NULL,domain TEXT NOT NULL,order_ref TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,paid_at DATETIME,claimed_at DATETIME)`,
		`CREATE TABLE premium_localparts(localpart TEXT PRIMARY KEY,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO member_tiers(id,slug,name,monthly_cents,yearly_cents,sort) VALUES('tier_free','free','Free',0,0,0),('tier_paid','paid','Premium',500,5000,1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

func TestMailIDAgreement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	count := func(email string) int {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM mailid_agreements WHERE email=?`, email).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := count("reader@example.com"); n != 0 {
		t.Fatalf("expected 0 agreements initially, got %d", n)
	}
	// Case-insensitive email; two distinct addresses recorded for one member.
	if err := s.RecordMailIDAgreement(ctx, "Reader@Example.com", "Me@Example.com", "abc123"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.RecordMailIDAgreement(ctx, "reader@example.com", "vip@example.com", "def456"); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if n := count("reader@example.com"); n != 2 {
		t.Errorf("expected 2 agreements, got %d", n)
	}
	// Blank email/address is rejected so an acceptance is never stored anonymously.
	if err := s.RecordMailIDAgreement(ctx, "", "x@example.com", "z"); err == nil {
		t.Error("expected error for blank email")
	}
	if err := s.RecordMailIDAgreement(ctx, "reader@example.com", "", "z"); err == nil {
		t.Error("expected error for blank address")
	}
}

func TestPremiumLocalpartsAndModeration(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Operator premium-name list: add (case-insensitive), detect, list, remove.
	if s.IsCustomPremiumLocalpart(ctx, "founder") {
		t.Fatal("nothing should be premium initially")
	}
	if err := s.AddPremiumLocalpart(ctx, "Founder"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !s.IsCustomPremiumLocalpart(ctx, "founder") {
		t.Error("added localpart should be premium")
	}
	if list, _ := s.ListPremiumLocalparts(ctx); len(list) != 1 || list[0] != "founder" {
		t.Errorf("list = %v", list)
	}
	if err := s.RemovePremiumLocalpart(ctx, "founder"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if s.IsCustomPremiumLocalpart(ctx, "founder") {
		t.Error("removed localpart must no longer be premium")
	}

	// Grant moderation: approve a pending grant, revoke another.
	g1, _ := s.CreatePremiumGrant(ctx, "a@x.com", "boss", "mail.x.com", "VP-A")
	if err := s.ApprovePremiumGrant(ctx, g1.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if cg, _ := s.ClaimablePremiumGrant(ctx, "a@x.com", "boss"); cg == nil {
		t.Error("approved grant should be claimable")
	}
	g2, _ := s.CreatePremiumGrant(ctx, "b@x.com", "ceo", "mail.x.com", "VP-B")
	if err := s.RevokePremiumGrant(ctx, g2.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, _ := s.PremiumGrantByID(ctx, g2.ID)
	if got == nil || got.Status != GrantRevoked {
		t.Errorf("revoked grant status = %+v", got)
	}
}

func TestArticlePurchaseFlow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// No price by default; setting one is readable back.
	if c := s.GetPostPriceCents(ctx, "my-post"); c != 0 {
		t.Fatalf("default price should be 0, got %d", c)
	}
	if err := s.SetPostPrice(ctx, "my-post", 300); err != nil {
		t.Fatalf("set price: %v", err)
	}
	if c := s.GetPostPriceCents(ctx, "my-post"); c != 300 {
		t.Errorf("price = %d, want 300", c)
	}
	// Setting the price must not clobber an existing access level.
	if err := s.SetAccess(ctx, "my-post", AccessPaid); err != nil {
		t.Fatalf("set access: %v", err)
	}
	if err := s.SetPostPrice(ctx, "my-post", 500); err != nil {
		t.Fatalf("reprice: %v", err)
	}
	if lvl := s.GetAccess(ctx, "my-post"); lvl != AccessPaid {
		t.Errorf("level should stay paid after reprice, got %q", lvl)
	}
	// Purchase lifecycle: pending → not unlocked; paid → unlocked.
	if err := s.CreateArticlePurchase(ctx, "Reader@Example.com", "my-post", "VP-P1"); err != nil {
		t.Fatalf("create purchase: %v", err)
	}
	if s.HasPurchasedArticle(ctx, "reader@example.com", "my-post") {
		t.Error("a pending purchase must not unlock the post")
	}
	if err := s.MarkArticlePurchasePaidByOrder(ctx, "VP-P1"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if !s.HasPurchasedArticle(ctx, "reader@example.com", "my-post") {
		t.Error("a paid purchase must unlock the post")
	}
	// A different member has not purchased it.
	if s.HasPurchasedArticle(ctx, "someone@else.com", "my-post") {
		t.Error("purchase must be per-member")
	}
}

func TestPremiumGrantLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	g, err := s.CreatePremiumGrant(ctx, "Reader@Example.com", "VIP", "mail.example.com", "VP-OT-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.Status != GrantPending || g.Address() != "vip@mail.example.com" {
		t.Fatalf("bad grant: %+v", g)
	}
	// A second pending purchase for the same address is refused.
	if _, err := s.CreatePremiumGrant(ctx, "reader@example.com", "vip", "mail.example.com", "VP-OT-2"); err == nil {
		t.Error("expected duplicate pending grant to be refused")
	}
	// Not claimable while still pending.
	if cg, _ := s.ClaimablePremiumGrant(ctx, "reader@example.com", "vip"); cg != nil {
		t.Error("pending grant must not be claimable")
	}
	// Payment (by order reference) makes it claimable.
	if err := s.MarkPremiumGrantPaidByOrder(ctx, "VP-OT-1"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	cg, err := s.ClaimablePremiumGrant(ctx, "reader@example.com", "vip")
	if err != nil || cg == nil {
		t.Fatalf("expected claimable grant, err=%v cg=%v", err, cg)
	}
	if list, _ := s.ClaimablePremiumGrants(ctx, "reader@example.com"); len(list) != 1 {
		t.Errorf("expected 1 claimable grant, got %d", len(list))
	}
	// Claiming removes it from the claimable set.
	if err := s.MarkPremiumGrantClaimed(ctx, cg.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cg2, _ := s.ClaimablePremiumGrant(ctx, "reader@example.com", "vip"); cg2 != nil {
		t.Error("claimed grant must no longer be claimable")
	}
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

// TestClaimMailAddressAtomic guards audit M13: the "one mailbox per member" rule
// is enforced by an atomic conditional UPDATE, so two concurrent claims with
// different localparts cannot both reserve a slot — exactly one wins, and a
// release re-opens the slot for a retry after a provisioning failure.
func TestClaimMailAddressAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO members(id,email) VALUES('m1','a@x.com')`); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimMailAddress(ctx, "a@x.com", "alice@x.com"); err != nil || !ok {
		t.Fatalf("first claim should win: ok=%v err=%v", ok, err)
	}
	// A second claim with a different localpart must lose (slot already taken).
	if ok, err := s.ClaimMailAddress(ctx, "a@x.com", "alice2@x.com"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("second concurrent claim must lose (one mailbox per member)")
	}
	if got := s.MailAddressFor(ctx, "a@x.com"); got != "alice@x.com" {
		t.Fatalf("stored address = %q, want alice@x.com", got)
	}
	// Releasing the reservation (a provisioning failure) re-opens the slot.
	if err := s.ClearMailAddressIf(ctx, "a@x.com", "alice@x.com"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ClaimMailAddress(ctx, "a@x.com", "bob@x.com"); !ok {
		t.Fatal("claim should succeed after the slot is released")
	}
	// ClearMailAddressIf must NOT clear a different, already-provisioned address.
	if err := s.ClearMailAddressIf(ctx, "a@x.com", "someone-else@x.com"); err != nil {
		t.Fatal(err)
	}
	if got := s.MailAddressFor(ctx, "a@x.com"); got != "bob@x.com" {
		t.Fatalf("address wrongly cleared: %q, want bob@x.com", got)
	}
}
