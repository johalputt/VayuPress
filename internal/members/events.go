package members

// events.go — the member activity log.
//
// Every meaningful lifecycle moment (a signup, a subscription start, a trial, an
// upgrade, a cancellation, a renewal, a failed payment, an operator comp) is
// appended here. The log powers the "Recent activity" feed in the Members
// console and the per-member timeline, and feeds the churn / MRR-movement
// analytics. Recording is best-effort: a failed insert never blocks the
// membership action that triggered it.

import (
	"context"
	"time"
)

// Event types. These are stable string keys persisted in member_events.type.
const (
	EventSignup          = "signup"           // a new member account was created
	EventSubscribe       = "subscribe"        // a paying subscription started
	EventTrialStart      = "trial_start"      // a free trial started
	EventUpgrade         = "upgrade"          // moved to a higher-priced tier
	EventDowngrade       = "downgrade"        // moved to a lower-priced tier
	EventRenew           = "renew"            // a subscription period renewed
	EventCancel          = "cancel"           // a subscription ended immediately
	EventCancelScheduled = "cancel_scheduled" // cancellation queued for period end
	EventComp            = "comp"             // operator granted a complimentary plan
	EventPaymentFailed   = "payment_failed"   // a Stripe invoice payment failed
)

// Event is one row in a member's activity timeline.
type Event struct {
	ID          string    `json:"id"`
	MemberID    string    `json:"member_id"`
	Type        string    `json:"type"`
	Detail      string    `json:"detail,omitempty"`
	AmountCents int       `json:"amount_cents"`
	CreatedAt   time.Time `json:"created_at"`
	// Email is populated only by the global feed (RecentEvents) so the console
	// can show who an event belongs to without a second lookup.
	Email string `json:"email,omitempty"`
}

// RecordEvent appends an activity event for a member. Errors are returned so
// callers that care (tests) can assert, but the lifecycle helpers ignore them.
func (s *Store) RecordEvent(ctx context.Context, memberID, typ, detail string, amountCents int) error {
	if memberID == "" || typ == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO member_events(id,member_id,type,detail,amount_cents) VALUES(?,?,?,?,?)`,
		"evt_"+randHex(10), memberID, typ, detail, amountCents)
	return err
}

// recordEventTx is the fire-and-forget variant used by the lifecycle helpers.
func (s *Store) recordEventTx(ctx context.Context, memberID, typ, detail string, amountCents int) {
	_ = s.RecordEvent(ctx, memberID, typ, detail, amountCents)
}

// EventsForMember returns a member's activity timeline, newest first.
func (s *Store) EventsForMember(ctx context.Context, memberID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,member_id,type,detail,amount_cents,created_at
		   FROM member_events WHERE member_id=? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		memberID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.MemberID, &e.Type, &e.Detail, &e.AmountCents, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecentEvents returns the most recent activity across all members, newest
// first, with each member's email joined in for display.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id,e.member_id,e.type,e.detail,e.amount_cents,e.created_at,COALESCE(m.email,'')
		   FROM member_events e LEFT JOIN members m ON m.id=e.member_id
		  ORDER BY e.created_at DESC, e.rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.MemberID, &e.Type, &e.Detail, &e.AmountCents, &e.CreatedAt, &e.Email); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEventsSince returns how many events of the given type occurred on or
// after the cutoff. Used by the churn / MRR-movement analytics.
func (s *Store) CountEventsSince(ctx context.Context, typ string, cutoff time.Time) (int, int) {
	var n, amount int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),COALESCE(SUM(amount_cents),0) FROM member_events WHERE type=? AND created_at>=?`,
		typ, cutoff.UTC().Format("2006-01-02 15:04:05")).Scan(&n, &amount)
	return n, amount
}
