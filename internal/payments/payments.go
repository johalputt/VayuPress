// Package payments implements VayuPress's sovereign monetization ledger: a
// gateway-agnostic order store plus a small registry describing the payment
// gateways the platform can collect through.
//
// Design (ADR-0090): VayuPress never embeds a payment SDK. Money is collected
// in one of two ways, and both converge on the same Order ledger:
//
//   - Direct / offline gateway ("direct") — the reliable, dependency-free
//     option. The operator publishes payment instructions (bank transfer, UPI,
//     a PayPal.me link, …); the reader checks out, an order is recorded as
//     pending with a short human-quotable reference, the reader pays out of
//     band quoting that reference, and the operator confirms receipt in the
//     Monetization console. Confirmation upgrades the member and emails a
//     receipt.
//
//   - Connected third-party gateway ("webhook") — any external processor can
//     confirm an order by POSTing a signed (HMAC-SHA256) event to the payment
//     webhook. This mirrors the existing Stripe webhook pattern, generalised so
//     no provider is special-cased and no SDK is linked.
//
// This package is storage-only and imports nothing from the rest of the app, so
// it can never form an import cycle with members/email. The orchestration that
// reacts to a paid order (upgrade member, send confirmation, fire events) lives
// in the cmd/vayupress handler layer, which already owns those dependencies.
package payments

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Order lifecycle statuses.
const (
	StatusPending  = "pending"  // awaiting payment / operator confirmation
	StatusPaid     = "paid"     // funds received; member fulfilled
	StatusCanceled = "canceled" // abandoned or rejected
	StatusRefunded = "refunded" // reversed after being paid
	StatusFailed   = "failed"   // gateway reported a failure
)

// Billing cadences (mirrors members.Cadence* values).
const (
	CadenceMonthly = "monthly"
	CadenceYearly  = "yearly"
)

// Gateway kinds.
const (
	// GatewayDirect is the built-in offline/manual gateway (operator-confirmed).
	GatewayDirect = "direct"
	// GatewayWebhook is the generic signed-webhook gateway for any connected
	// third-party processor.
	GatewayWebhook = "webhook"
	// GatewayStripe is recognised for orders reconciled by the Stripe webhook.
	GatewayStripe = "stripe"
)

// ErrAlreadyPaid is returned by MarkPaid when the order was already paid, so the
// caller can make fulfilment idempotent (never charge/upgrade/email twice).
var ErrAlreadyPaid = errors.New("payments: order already paid")

// ErrNotFound is returned when no order matches the supplied id/reference.
var ErrNotFound = errors.New("payments: order not found")

// GatewaySpec describes a payment gateway surfaced in the admin UI.
type GatewaySpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// Gateways returns the built-in gateway catalogue. Both ship in the single
// binary; "direct" needs only operator instructions, "webhook" needs a signing
// secret stored in the encrypted credential store.
func Gateways() []GatewaySpec {
	return []GatewaySpec{
		{
			ID: GatewayDirect, Name: "Direct / offline payment", Kind: GatewayDirect,
			Description: "Collect by bank transfer, UPI, or any link you publish. Readers pay out of band quoting an order reference; you confirm receipt to unlock access. No third-party gateway required.",
		},
		{
			ID: GatewayWebhook, Name: "Connected gateway (webhook)", Kind: GatewayWebhook,
			Description: "Connect any external payment processor. It confirms an order by posting a signature-verified webhook — no embedded SDK, no card data ever touches your server.",
		},
	}
}

// Order is one checkout/payment record — the single source of truth for what a
// payer owes and the state of that obligation.
type Order struct {
	ID          string     `json:"id"`
	Reference   string     `json:"reference"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	TierSlug    string     `json:"tier_slug"`
	Cadence     string     `json:"cadence"`
	AmountCents int        `json:"amount_cents"`
	Currency    string     `json:"currency"`
	Gateway     string     `json:"gateway"`
	Status      string     `json:"status"`
	GatewayRef  string     `json:"gateway_ref,omitempty"`
	Note        string     `json:"note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AmountMajor formats the amount as a major-unit string (e.g. "9.00").
func (o *Order) AmountMajor() string {
	if o == nil {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", float64(o.AmountCents)/100)
}

// Store persists orders in the payment_orders table (migration 043).
type Store struct{ db *sql.DB }

// New creates a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

const orderCols = `id,reference,email,name,tier_slug,cadence,amount_cents,currency,gateway,status,gateway_ref,note,created_at,paid_at,updated_at`

// OrderInput carries the fields needed to open an order.
type OrderInput struct {
	Email       string
	Name        string
	TierSlug    string
	Cadence     string
	AmountCents int
	Currency    string
	Gateway     string
}

// Create opens a new pending order and returns it, including the generated
// reference the payer quotes when paying.
func (s *Store) Create(ctx context.Context, in OrderInput) (*Order, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if strings.TrimSpace(in.TierSlug) == "" {
		return nil, fmt.Errorf("tier is required")
	}
	cadence := in.Cadence
	if cadence != CadenceYearly {
		cadence = CadenceMonthly
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	gateway := strings.TrimSpace(in.Gateway)
	if gateway == "" {
		gateway = GatewayDirect
	}
	id := "ord_" + randHex(12)
	ref := s.uniqueReference(ctx)
	amount := in.AmountCents
	if amount < 0 {
		amount = 0
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO payment_orders(id,reference,email,name,tier_slug,cadence,amount_cents,currency,gateway,status) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, ref, email, strings.TrimSpace(in.Name), in.TierSlug, cadence, amount, currency, gateway, StatusPending); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return s.GetByID(ctx, id)
}

// GetByID returns the order with the given id.
func (s *Store) GetByID(ctx context.Context, id string) (*Order, error) {
	return scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM payment_orders WHERE id=?`, id))
}

// GetByReference returns the order with the given (unique) reference code.
func (s *Store) GetByReference(ctx context.Context, ref string) (*Order, error) {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	return scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM payment_orders WHERE reference=?`, ref))
}

// List returns orders newest first. status "" (or "all") returns every order.
func (s *Store) List(ctx context.Context, status string, limit int) ([]Order, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + orderCols + ` FROM payment_orders`
	var args []interface{}
	if status != "" && status != "all" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// MarkPaid transitions a pending order to paid, stamping paid_at and recording
// the gateway's own reference (e.g. a transaction id) when supplied. It is
// idempotent: an already-paid order returns (order, ErrAlreadyPaid) so the
// caller can skip re-fulfilment. The id may be an order id or a reference.
func (s *Store) MarkPaid(ctx context.Context, idOrRef, gatewayRef string) (*Order, error) {
	o, err := s.resolve(ctx, idOrRef)
	if err != nil {
		return nil, err
	}
	if o.Status == StatusPaid {
		return o, ErrAlreadyPaid
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE payment_orders SET status=?,gateway_ref=COALESCE(NULLIF(?,''),gateway_ref),paid_at=?,updated_at=? WHERE id=?`,
		StatusPaid, strings.TrimSpace(gatewayRef), now, now, o.ID); err != nil {
		return nil, err
	}
	return s.GetByID(ctx, o.ID)
}

// SetStatus updates an order's status (e.g. cancel, refund). It clears paid_at
// when moving away from paid so reporting stays truthful.
func (s *Store) SetStatus(ctx context.Context, idOrRef, status string) error {
	switch status {
	case StatusPending, StatusPaid, StatusCanceled, StatusRefunded, StatusFailed:
	default:
		return fmt.Errorf("invalid status %q", status)
	}
	o, err := s.resolve(ctx, idOrRef)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if status == StatusPaid {
		_, err = s.db.ExecContext(ctx, `UPDATE payment_orders SET status=?,paid_at=?,updated_at=? WHERE id=?`, status, now, now, o.ID)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE payment_orders SET status=?,paid_at=NULL,updated_at=? WHERE id=?`, status, now, o.ID)
	}
	return err
}

// Stats summarises the ledger for the console dashboard.
type Stats struct {
	Pending      int    `json:"pending"`
	Paid         int    `json:"paid"`
	RevenueCents int    `json:"revenue_cents"`
	Currency     string `json:"currency"`
}

// Stats returns pending/paid counts and total collected revenue. Revenue is
// summed across the most common currency for a simple headline figure.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM payment_orders WHERE status=?`, StatusPending).Scan(&st.Pending)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM payment_orders WHERE status=?`, StatusPaid).Scan(&st.Paid)
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_cents),0),COALESCE(currency,'USD') FROM payment_orders WHERE status=? GROUP BY currency ORDER BY SUM(amount_cents) DESC LIMIT 1`, StatusPaid)
	if err := row.Scan(&st.RevenueCents, &st.Currency); err != nil {
		st.Currency = "USD"
	}
	if st.Currency == "" {
		st.Currency = "USD"
	}
	return st, nil
}

// resolve fetches an order by id, falling back to reference lookup.
func (s *Store) resolve(ctx context.Context, idOrRef string) (*Order, error) {
	if o, err := s.GetByID(ctx, idOrRef); err == nil {
		return o, nil
	}
	o, err := s.GetByReference(ctx, idOrRef)
	if err != nil {
		return nil, ErrNotFound
	}
	return o, nil
}

// uniqueReference returns a short, unambiguous, upper-case reference code that
// is not already in use (e.g. "VP-7K3Q8M").
func (s *Store) uniqueReference(ctx context.Context) string {
	for i := 0; i < 12; i++ {
		ref := "VP-" + randCode(6)
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM payment_orders WHERE reference=?`, ref).Scan(&n); err == nil && n == 0 {
			return ref
		}
	}
	// Extremely unlikely fallback: widen the code.
	return "VP-" + randCode(10)
}

func scanOrder(sc interface{ Scan(...interface{}) error }) (*Order, error) {
	var o Order
	var gatewayRef, note string
	var paidAt sql.NullTime
	if err := sc.Scan(&o.ID, &o.Reference, &o.Email, &o.Name, &o.TierSlug, &o.Cadence, &o.AmountCents,
		&o.Currency, &o.Gateway, &o.Status, &gatewayRef, &note, &o.CreatedAt, &paidAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	o.GatewayRef = gatewayRef
	o.Note = note
	if paidAt.Valid {
		t := paidAt.Time.UTC()
		o.PaidAt = &t
	}
	return &o, nil
}

// randHex returns n random bytes as a hex string.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
	}
	return string(out)
}

// randCode returns an n-char code from an unambiguous alphabet (no 0/O/1/I).
func randCode(n int) string {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}
