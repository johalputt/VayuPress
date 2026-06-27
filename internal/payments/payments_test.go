package payments

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore spins up an in-memory SQLite store with the payment_orders
// schema mirroring migration 043.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE payment_orders(id TEXT PRIMARY KEY,reference TEXT NOT NULL UNIQUE,email TEXT NOT NULL,name TEXT NOT NULL DEFAULT '',tier_slug TEXT NOT NULL,cadence TEXT NOT NULL DEFAULT 'monthly',amount_cents INTEGER NOT NULL DEFAULT 0,currency TEXT NOT NULL DEFAULT 'USD',gateway TEXT NOT NULL DEFAULT 'direct',status TEXT NOT NULL DEFAULT 'pending',gateway_ref TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,paid_at DATETIME,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func TestCreateAndFetchOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	o, err := s.Create(ctx, OrderInput{Email: "Reader@Example.com", Name: "Reader", TierSlug: "paid", Cadence: CadenceMonthly, AmountCents: 900, Currency: "usd"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if o.Email != "reader@example.com" {
		t.Errorf("email not normalised: %q", o.Email)
	}
	if o.Currency != "USD" {
		t.Errorf("currency not upper-cased: %q", o.Currency)
	}
	if o.Reference == "" || o.Status != StatusPending {
		t.Errorf("bad new order: %+v", o)
	}
	byRef, err := s.GetByReference(ctx, o.Reference)
	if err != nil || byRef.ID != o.ID {
		t.Fatalf("lookup by reference failed: %v", err)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, OrderInput{Email: "not-an-email", TierSlug: "paid"}); err == nil {
		t.Error("expected invalid-email error")
	}
	if _, err := s.Create(ctx, OrderInput{Email: "a@b.com", TierSlug: ""}); err == nil {
		t.Error("expected missing-tier error")
	}
}

func TestMarkPaidIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	o, _ := s.Create(ctx, OrderInput{Email: "a@b.com", TierSlug: "paid", AmountCents: 500})
	paid, err := s.MarkPaid(ctx, o.Reference, "txn_123")
	if err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if paid.Status != StatusPaid || paid.PaidAt == nil || paid.GatewayRef != "txn_123" {
		t.Errorf("paid order not finalised: %+v", paid)
	}
	// Second call must report ErrAlreadyPaid so the caller skips double fulfilment.
	if _, err := s.MarkPaid(ctx, o.ID, "txn_456"); !errors.Is(err, ErrAlreadyPaid) {
		t.Errorf("expected ErrAlreadyPaid, got %v", err)
	}
}

func TestStatsAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, _ := s.Create(ctx, OrderInput{Email: "a@b.com", TierSlug: "paid", AmountCents: 900, Currency: "USD"})
	b, _ := s.Create(ctx, OrderInput{Email: "c@d.com", TierSlug: "paid", AmountCents: 1900, Currency: "USD"})
	_, _ = s.MarkPaid(ctx, a.ID, "")
	_, _ = s.MarkPaid(ctx, b.ID, "")
	_, _ = s.Create(ctx, OrderInput{Email: "e@f.com", TierSlug: "paid", AmountCents: 100})
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Paid != 2 || st.Pending != 1 || st.RevenueCents != 2800 {
		t.Errorf("unexpected stats: %+v", st)
	}
	if err := s.SetStatus(ctx, a.ID, StatusRefunded); err != nil {
		t.Fatalf("refund: %v", err)
	}
	ref, _ := s.GetByID(ctx, a.ID)
	if ref.Status != StatusRefunded || ref.PaidAt != nil {
		t.Errorf("refund did not clear paid_at: %+v", ref)
	}
}

func TestNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetByID(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
