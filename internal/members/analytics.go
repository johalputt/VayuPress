package members

// analytics.go — membership business metrics.
//
// These read-only aggregates power the Members console: Monthly Recurring
// Revenue (MRR), member growth over time, and tier distribution. All amounts
// are integer cents to avoid floating-point drift.

import (
	"context"
	"time"
)

// Stats is a snapshot of membership health.
type Stats struct {
	Total      int            `json:"total"`       // all members
	Free       int            `json:"free"`        // members on the free tier
	Paid       int            `json:"paid"`        // members on any paying tier
	ByTier     map[string]int `json:"by_tier"`     // member count per tier slug
	MRRCents   int            `json:"mrr_cents"`   // monthly recurring revenue
	ARRCents   int            `json:"arr_cents"`   // annual run-rate (MRR*12)
	Currency   string         `json:"currency"`    // dominant subscription currency
	ActiveSubs int            `json:"active_subs"` // active paying subscriptions
	NewLast30  int            `json:"new_last_30"` // signups in the last 30 days
}

// DayCount is a single point in a time series.
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// Stats computes the membership snapshot in a handful of cheap aggregate queries.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	st := &Stats{ByTier: map[string]int{}, Currency: "USD"}

	rows, err := s.db.QueryContext(ctx, `SELECT tier,COUNT(*) FROM members GROUP BY tier`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tier string
		var n int
		if err := rows.Scan(&tier, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.ByTier[tier] = n
		st.Total += n
		if tier == TierFree || tier == "" {
			st.Free += n
		} else {
			st.Paid += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// MRR from active, non-zero subscriptions, normalising yearly to monthly.
	mrr := 0
	subRows, err := s.db.QueryContext(ctx,
		`SELECT cadence,amount_cents,currency FROM member_subscriptions WHERE status=? AND amount_cents>0`, SubActive)
	if err == nil {
		currencyVotes := map[string]int{}
		for subRows.Next() {
			var cadence, currency string
			var amount int
			if err := subRows.Scan(&cadence, &amount, &currency); err != nil {
				continue
			}
			st.ActiveSubs++
			sub := Subscription{Cadence: cadence, AmountCents: amount}
			mrr += sub.MonthlyValueCents()
			currencyVotes[currency]++
		}
		subRows.Close()
		best := 0
		for c, v := range currencyVotes {
			if v > best {
				best, st.Currency = v, c
			}
		}
	}
	st.MRRCents = mrr
	st.ARRCents = mrr * 12

	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02 15:04:05")
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members WHERE created_at>=?`, cutoff).Scan(&st.NewLast30)

	return st, nil
}

// SignupsByDay returns member signup counts for the last n days, oldest first.
// Days with no signups are included with a zero count so charts are continuous.
func (s *Store) SignupsByDay(ctx context.Context, days int) ([]DayCount, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT substr(created_at,1,10) AS d,COUNT(*) FROM members WHERE created_at>=? GROUP BY d`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		counts[d] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DayCount, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		out = append(out, DayCount{Day: day, Count: counts[day]})
	}
	return out, nil
}
