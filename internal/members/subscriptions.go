package members

// subscriptions.go — per-member subscription state.
//
// A subscription links a member to a tier with a cadence (monthly / yearly /
// complimentary) and an amount. The newest active row is the member's current
// plan; canceled and expired rows are retained for history and churn reporting.
// Starting a new subscription supersedes any existing active one.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Subscription cadences and statuses.
const (
	CadenceMonthly       = "monthly"
	CadenceYearly        = "yearly"
	CadenceComplimentary = "complimentary" // operator-granted, no recurring charge

	SubActive   = "active"
	SubCanceled = "canceled"
	SubExpired  = "expired"
)

// Subscription is one membership subscription record.
type Subscription struct {
	ID                 string     `json:"id"`
	MemberID           string     `json:"member_id"`
	TierSlug           string     `json:"tier_slug"`
	Status             string     `json:"status"`
	Cadence            string     `json:"cadence"`
	AmountCents        int        `json:"amount_cents"`
	Currency           string     `json:"currency"`
	StripeSubscription string     `json:"-"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
	StartedAt          time.Time  `json:"started_at"`
	CanceledAt         *time.Time `json:"canceled_at,omitempty"`
}

// MonthlyValueCents normalises the subscription to a monthly recurring value:
// yearly plans divide by 12, complimentary / zero-amount plans contribute 0.
func (sub *Subscription) MonthlyValueCents() int {
	if sub == nil || sub.AmountCents <= 0 {
		return 0
	}
	if sub.Cadence == CadenceYearly {
		return sub.AmountCents / 12
	}
	if sub.Cadence == CadenceComplimentary {
		return 0
	}
	return sub.AmountCents
}

// SubscriptionInput carries the fields needed to start a subscription.
type SubscriptionInput struct {
	MemberID           string
	TierSlug           string
	Cadence            string
	AmountCents        int
	Currency           string
	StripeSubscription string
	CurrentPeriodEnd   *time.Time
}

// StartSubscription supersedes any active subscription for the member and
// records a new active one. The member's tier column is updated to match.
func (s *Store) StartSubscription(ctx context.Context, in SubscriptionInput) error {
	if in.MemberID == "" || strings.TrimSpace(in.TierSlug) == "" {
		return fmt.Errorf("member id and tier are required")
	}
	cadence := in.Cadence
	switch cadence {
	case CadenceMonthly, CadenceYearly, CadenceComplimentary:
	default:
		cadence = CadenceMonthly
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := tx.ExecContext(ctx,
		`UPDATE member_subscriptions SET status=?,canceled_at=? WHERE member_id=? AND status=?`,
		SubCanceled, now, in.MemberID, SubActive); err != nil {
		return err
	}
	var periodEnd interface{}
	if in.CurrentPeriodEnd != nil {
		periodEnd = in.CurrentPeriodEnd.UTC().Format("2006-01-02 15:04:05")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_subscriptions(id,member_id,tier_slug,status,cadence,amount_cents,currency,stripe_subscription,current_period_end)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		"sub_"+randHex(10), in.MemberID, in.TierSlug, SubActive, cadence,
		maxInt(0, in.AmountCents), currency, in.StripeSubscription, periodEnd); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET tier=? WHERE id=?`, in.TierSlug, in.MemberID); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelSubscription marks a member's active subscription canceled and drops the
// member back to the free tier.
func (s *Store) CancelSubscription(ctx context.Context, memberID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := tx.ExecContext(ctx,
		`UPDATE member_subscriptions SET status=?,canceled_at=? WHERE member_id=? AND status=?`,
		SubCanceled, now, memberID, SubActive); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET tier=? WHERE id=?`, TierFree, memberID); err != nil {
		return err
	}
	return tx.Commit()
}

// ActiveSubscription returns a member's current active subscription, or
// (nil, nil) when they have none (i.e. a free member).
func (s *Store) ActiveSubscription(ctx context.Context, memberID string) (*Subscription, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,member_id,tier_slug,status,cadence,amount_cents,currency,stripe_subscription,current_period_end,started_at,canceled_at
		   FROM member_subscriptions WHERE member_id=? AND status=? ORDER BY started_at DESC LIMIT 1`,
		memberID, SubActive)
	sub, err := scanSubscription(row)
	if err != nil {
		return nil, nil //nolint:nilerr // no active subscription is not an error
	}
	return sub, nil
}

// syncSubscriptionForTier keeps subscription state consistent when an operator
// assigns a tier manually. Assigning free cancels any active subscription;
// assigning a paid tier records a complimentary subscription if none is active.
func (s *Store) syncSubscriptionForTier(ctx context.Context, memberID, tier string) error {
	if tier == TierFree {
		return s.CancelSubscription(ctx, memberID)
	}
	if sub, _ := s.ActiveSubscription(ctx, memberID); sub != nil && sub.TierSlug == tier {
		return nil // already on this tier
	}
	return s.StartSubscription(ctx, SubscriptionInput{
		MemberID: memberID, TierSlug: tier, Cadence: CadenceComplimentary,
	})
}

func scanSubscription(sc scanner) (*Subscription, error) {
	var sub Subscription
	var periodEnd, canceledAt sql.NullTime
	var stripe string
	if err := sc.Scan(&sub.ID, &sub.MemberID, &sub.TierSlug, &sub.Status, &sub.Cadence,
		&sub.AmountCents, &sub.Currency, &stripe, &periodEnd, &sub.StartedAt, &canceledAt); err != nil {
		return nil, err
	}
	sub.StripeSubscription = stripe
	if periodEnd.Valid {
		t := periodEnd.Time.UTC()
		sub.CurrentPeriodEnd = &t
	}
	if canceledAt.Valid {
		t := canceledAt.Time.UTC()
		sub.CanceledAt = &t
	}
	return &sub, nil
}
