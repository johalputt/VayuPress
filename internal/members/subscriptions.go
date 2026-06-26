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
	TrialEnd           *time.Time `json:"trial_end,omitempty"`
	CancelAtPeriodEnd  bool       `json:"cancel_at_period_end"`
	StartedAt          time.Time  `json:"started_at"`
	CanceledAt         *time.Time `json:"canceled_at,omitempty"`
}

// MonthlyValueCents normalises the subscription to a monthly recurring value:
// yearly plans divide by 12, complimentary / zero-amount plans contribute 0,
// and subscriptions still inside a free trial contribute 0 until the trial
// converts (matching how Ghost/Substack report trialing members separately).
func (sub *Subscription) MonthlyValueCents() int {
	if sub == nil || sub.AmountCents <= 0 {
		return 0
	}
	if sub.IsTrialing() {
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

// IsTrialing reports whether the subscription is currently inside a free trial.
func (sub *Subscription) IsTrialing() bool {
	return sub != nil && sub.TrialEnd != nil && time.Now().UTC().Before(sub.TrialEnd.UTC())
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
	// TrialDays, when > 0, starts the subscription inside a free trial of that
	// length. The member gets full paid access but contributes no MRR until the
	// trial ends.
	TrialDays int
}

// StartSubscription supersedes any active subscription for the member and
// records a new active one. The member's tier column is updated to match. When
// in.TrialDays > 0 the subscription starts inside a free trial and a trial_start
// event is logged; otherwise a subscribe event records the monthly value.
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
	var trialEnd *time.Time
	if in.TrialDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, in.TrialDays)
		trialEnd = &t
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
	var periodEnd, trialEndVal interface{}
	if in.CurrentPeriodEnd != nil {
		periodEnd = in.CurrentPeriodEnd.UTC().Format("2006-01-02 15:04:05")
	}
	if trialEnd != nil {
		trialEndVal = trialEnd.UTC().Format("2006-01-02 15:04:05")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_subscriptions(id,member_id,tier_slug,status,cadence,amount_cents,currency,stripe_subscription,current_period_end,trial_end)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"sub_"+randHex(10), in.MemberID, in.TierSlug, SubActive, cadence,
		maxInt(0, in.AmountCents), currency, in.StripeSubscription, periodEnd, trialEndVal); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET tier=? WHERE id=?`, in.TierSlug, in.MemberID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Record an activity event (best-effort; never blocks the subscription).
	switch {
	case trialEnd != nil:
		s.recordEventTx(ctx, in.MemberID, EventTrialStart, in.TierSlug, 0)
	case cadence == CadenceComplimentary || maxInt(0, in.AmountCents) == 0:
		s.recordEventTx(ctx, in.MemberID, EventComp, in.TierSlug, 0)
	default:
		sub := Subscription{Cadence: cadence, AmountCents: maxInt(0, in.AmountCents)}
		s.recordEventTx(ctx, in.MemberID, EventSubscribe, in.TierSlug, sub.MonthlyValueCents())
	}
	return nil
}

// CancelSubscription marks a member's active subscription canceled and drops the
// member back to the free tier immediately.
func (s *Store) CancelSubscription(ctx context.Context, memberID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// Capture the monthly value being lost so the event records churned MRR.
	var cadence string
	var amount int
	_ = tx.QueryRowContext(ctx,
		`SELECT cadence,amount_cents FROM member_subscriptions WHERE member_id=? AND status=? ORDER BY started_at DESC LIMIT 1`,
		memberID, SubActive).Scan(&cadence, &amount)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := tx.ExecContext(ctx,
		`UPDATE member_subscriptions SET status=?,canceled_at=? WHERE member_id=? AND status=?`,
		SubCanceled, now, memberID, SubActive); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET tier=? WHERE id=?`, TierFree, memberID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	lost := (&Subscription{Cadence: cadence, AmountCents: amount}).MonthlyValueCents()
	s.recordEventTx(ctx, memberID, EventCancel, "", lost)
	return nil
}

// ScheduleCancellation flags a member's active subscription to cancel at the end
// of the current paid period instead of immediately. The member keeps full
// access until then. This is the default "cancel" behaviour readers expect from
// Ghost/Substack — access is not yanked the moment they cancel.
func (s *Store) ScheduleCancellation(ctx context.Context, memberID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE member_subscriptions SET cancel_at_period_end=1 WHERE member_id=? AND status=?`,
		memberID, SubActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no active subscription to cancel")
	}
	s.recordEventTx(ctx, memberID, EventCancelScheduled, "", 0)
	return nil
}

// ActiveSubscription returns a member's current active subscription, or
// (nil, nil) when they have none (i.e. a free member).
func (s *Store) ActiveSubscription(ctx context.Context, memberID string) (*Subscription, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,member_id,tier_slug,status,cadence,amount_cents,currency,stripe_subscription,current_period_end,trial_end,cancel_at_period_end,started_at,canceled_at
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
	var periodEnd, trialEnd, canceledAt sql.NullTime
	var stripe string
	var cancelAtEnd int
	if err := sc.Scan(&sub.ID, &sub.MemberID, &sub.TierSlug, &sub.Status, &sub.Cadence,
		&sub.AmountCents, &sub.Currency, &stripe, &periodEnd, &trialEnd, &cancelAtEnd, &sub.StartedAt, &canceledAt); err != nil {
		return nil, err
	}
	sub.StripeSubscription = stripe
	sub.CancelAtPeriodEnd = cancelAtEnd != 0
	if periodEnd.Valid {
		t := periodEnd.Time.UTC()
		sub.CurrentPeriodEnd = &t
	}
	if trialEnd.Valid {
		t := trialEnd.Time.UTC()
		sub.TrialEnd = &t
	}
	if canceledAt.Valid {
		t := canceledAt.Time.UTC()
		sub.CanceledAt = &t
	}
	return &sub, nil
}
