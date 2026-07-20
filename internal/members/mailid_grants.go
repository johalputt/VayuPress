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
	GrantRevoked = "revoked" // operator-disapproved / cancelled
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

// AllPremiumGrants returns the most recent premium-address grants for the
// operator's marketplace control panel, newest first.
func (s *Store) AllPremiumGrants(ctx context.Context, limit int) ([]PremiumGrant, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+premiumGrantCols+` FROM premium_mailid_grants ORDER BY created_at DESC LIMIT ?`, limit)
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

// PremiumGrantByID returns a single grant (admin moderation).
func (s *Store) PremiumGrantByID(ctx context.Context, id string) (*PremiumGrant, error) {
	return s.premiumGrantByID(ctx, id)
}

// ApprovePremiumGrant manually confirms a pending grant (operator-confirmed
// offline payment), making the address claimable. Idempotent on pending→paid.
func (s *Store) ApprovePremiumGrant(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE premium_mailid_grants SET status='paid', paid_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'`, id)
	return err
}

// RevokePremiumGrant disapproves/cancels a grant, moving it to revoked from any
// state. A claimed address's mailbox is suspended separately by the caller.
func (s *Store) RevokePremiumGrant(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE premium_mailid_grants SET status='revoked' WHERE id=?`, id)
	return err
}

// ── Operator-managed premium (sellable) localpart list ────────────────────────
//
// The static classifier (mail.IsPremiumLocalpart) marks ultra-short + curated
// vanity names premium; this list lets the operator designate ADDITIONAL exact
// localparts as premium (held back from the free claim, sold at the premium
// price). Callers treat a name as premium when EITHER source says so.

// AddPremiumLocalpart adds an exact localpart to the operator's premium list.
func (s *Store) AddPremiumLocalpart(ctx context.Context, local string) error {
	local = strings.ToLower(strings.TrimSpace(local))
	if local == "" {
		return fmt.Errorf("localpart required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO premium_localparts(localpart) VALUES(?) ON CONFLICT(localpart) DO NOTHING`, local)
	return err
}

// RemovePremiumLocalpart drops a localpart from the operator's premium list.
func (s *Store) RemovePremiumLocalpart(ctx context.Context, local string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM premium_localparts WHERE localpart=?`, strings.ToLower(strings.TrimSpace(local)))
	return err
}

// ListPremiumLocalparts returns the operator's premium localparts, alphabetical.
func (s *Store) ListPremiumLocalparts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT localpart FROM premium_localparts ORDER BY localpart`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var lp string
		if rows.Scan(&lp) == nil {
			out = append(out, lp)
		}
	}
	return out, rows.Err()
}

// IsCustomPremiumLocalpart reports whether the operator has flagged this exact
// localpart as premium.
func (s *Store) IsCustomPremiumLocalpart(ctx context.Context, local string) bool {
	local = strings.ToLower(strings.TrimSpace(local))
	if local == "" {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM premium_localparts WHERE localpart=?`, local).Scan(&n)
	return n > 0
}

// PremiumGrantCounts returns how many premium grants sit in each lifecycle state,
// for the marketplace KPI cards.
func (s *Store) PremiumGrantCounts(ctx context.Context) (pending, paid, claimed int) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(1) FROM premium_mailid_grants GROUP BY status`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			switch st {
			case GrantPending:
				pending = n
			case GrantPaid:
				paid = n
			case GrantClaimed:
				claimed = n
			}
		}
	}
	_ = rows.Err()
	return
}
