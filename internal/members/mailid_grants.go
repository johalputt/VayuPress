package members

// mailid_grants.go — the premium (vanity) mail-ID marketplace grant ledger.
//
// Buying a premium address is a two-step flow so payment (which settles
// asynchronously via Stripe redirect/webhook or an operator-confirmed offline
// order) is decoupled from provisioning (which needs a password the member sets
// interactively):
//
//	pending  — order opened, awaiting payment
//	paid     — payment confirmed; the member may now CLAIM the address
//	claimed  — the member set a password and the mailbox was provisioned
//
// A grant is keyed to the VayuPress order reference so any of the sovereign
// payment paths (Stripe success, generic webhook, operator "Mark paid") can flip
// it to paid via the shared fulfilment branch, without knowing anything about
// mail.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Premium mail-ID grant statuses.
const (
	GrantPending = "pending"
	GrantPaid    = "paid"
	GrantClaimed = "claimed"
)

// PremiumGrant is one premium-address purchase and its lifecycle.
type PremiumGrant struct {
	ID        string
	Email     string
	Localpart string
	Domain    string
	OrderRef  string
	Status    string
	CreatedAt time.Time
	PaidAt    *time.Time
	ClaimedAt *time.Time
}

// Address returns the full localpart@domain the grant is for.
func (g *PremiumGrant) Address() string {
	if g == nil {
		return ""
	}
	return g.Localpart + "@" + g.Domain
}

const premiumGrantCols = `id,email,localpart,domain,order_ref,status,created_at,paid_at,claimed_at`

func scanPremiumGrant(row interface{ Scan(...any) error }) (*PremiumGrant, error) {
	var g PremiumGrant
	if err := row.Scan(&g.ID, &g.Email, &g.Localpart, &g.Domain, &g.OrderRef, &g.Status, &g.CreatedAt, &g.PaidAt, &g.ClaimedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// CreatePremiumGrant opens a pending grant for a premium address purchase, tied
// to the order reference. It refuses a duplicate when the member already has a
// pending or paid (unclaimed) grant for the same address.
func (s *Store) CreatePremiumGrant(ctx context.Context, email, localpart, domain, orderRef string) (*PremiumGrant, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	localpart = strings.ToLower(strings.TrimSpace(localpart))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if email == "" || localpart == "" || domain == "" {
		return nil, fmt.Errorf("email, localpart and domain required")
	}
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM premium_mailid_grants WHERE email=? AND localpart=? AND domain=? AND status IN ('pending','paid')`,
		email, localpart, domain).Scan(&n)
	if n > 0 {
		return nil, fmt.Errorf("you already have a pending purchase for that address")
	}
	id := "pmg_" + randHex(12)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO premium_mailid_grants(id,email,localpart,domain,order_ref,status) VALUES(?,?,?,?,?,?)`,
		id, email, localpart, domain, strings.TrimSpace(orderRef), GrantPending); err != nil {
		return nil, err
	}
	return s.premiumGrantByID(ctx, id)
}

func (s *Store) premiumGrantByID(ctx context.Context, id string) (*PremiumGrant, error) {
	return scanPremiumGrant(s.db.QueryRowContext(ctx, `SELECT `+premiumGrantCols+` FROM premium_mailid_grants WHERE id=?`, id))
}

// MarkPremiumGrantPaidByOrder flips the pending grant for an order reference to
// paid (claimable). Idempotent: a grant already paid/claimed is left untouched.
func (s *Store) MarkPremiumGrantPaidByOrder(ctx context.Context, orderRef string) error {
	orderRef = strings.TrimSpace(orderRef)
	if orderRef == "" {
		return fmt.Errorf("order reference required")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE premium_mailid_grants SET status='paid', paid_at=CURRENT_TIMESTAMP WHERE order_ref=? AND status='pending'`,
		orderRef)
	return err
}

// ClaimablePremiumGrants returns a member's paid-but-unclaimed premium grants —
// the addresses they have bought and may now activate by setting a password.
func (s *Store) ClaimablePremiumGrants(ctx context.Context, email string) ([]PremiumGrant, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+premiumGrantCols+` FROM premium_mailid_grants WHERE email=? AND status='paid' ORDER BY paid_at`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PremiumGrant
	for rows.Next() {
		g, serr := scanPremiumGrant(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// ClaimablePremiumGrant returns a member's paid, unclaimed grant for a specific
// localpart, or nil when there is none (so a claim can be authorised).
func (s *Store) ClaimablePremiumGrant(ctx context.Context, email, localpart string) (*PremiumGrant, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	localpart = strings.ToLower(strings.TrimSpace(localpart))
	g, err := scanPremiumGrant(s.db.QueryRowContext(ctx,
		`SELECT `+premiumGrantCols+` FROM premium_mailid_grants WHERE email=? AND localpart=? AND status='paid' ORDER BY paid_at LIMIT 1`,
		email, localpart))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return g, err
}

// MarkPremiumGrantClaimed records that a paid grant's address has been
// provisioned. Idempotent on the paid→claimed transition.
func (s *Store) MarkPremiumGrantClaimed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE premium_mailid_grants SET status='claimed', claimed_at=CURRENT_TIMESTAMP WHERE id=? AND status='paid'`, id)
	return err
}
