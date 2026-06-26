package members

// analytics.go — membership business metrics.
//
// These read-only aggregates power the Members console: Monthly Recurring
// Revenue (MRR), member growth over time, and tier distribution. All amounts
// are integer cents to avoid floating-point drift.

import (
	"context"
	"database/sql"
	"sort"
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

	// Subscription-engine v2 metrics — the depth Ghost/Substack dashboards lack.
	Trialing            int     `json:"trialing"`               // active subscriptions still in a free trial
	ARPUCents           int     `json:"arpu_cents"`             // average revenue per paying member (MRR/paid)
	LTVCents            int     `json:"ltv_cents"`              // estimated lifetime value per paid member (ARPU/churn)
	ConversionRate      float64 `json:"conversion_rate"`        // paid / total, 0..1
	ChurnRate30         float64 `json:"churn_rate_30"`          // cancellations(30d) / (paid + cancellations(30d)), 0..1
	NewMRRCents         int     `json:"new_mrr_cents"`          // MRR added in the last 30 days
	ChurnedMRRCents     int     `json:"churned_mrr_cents"`      // MRR lost in the last 30 days
	NetMRRMovementCents int     `json:"net_mrr_movement_cents"` // new - churned over 30 days
	NewPaidLast30       int     `json:"new_paid_last_30"`       // new paying subscriptions in the last 30 days
	CanceledLast30      int     `json:"canceled_last_30"`       // cancellations in the last 30 days
}

// TierRevenue is the revenue contribution of a single tier.
type TierRevenue struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Members  int    `json:"members"`
	MRRCents int    `json:"mrr_cents"`
	Currency string `json:"currency"`
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

	// MRR from active, non-zero subscriptions, normalising yearly to monthly and
	// excluding subscriptions still inside a free trial (counted separately).
	mrr := 0
	now := time.Now().UTC()
	subRows, err := s.db.QueryContext(ctx,
		`SELECT cadence,amount_cents,currency,trial_end FROM member_subscriptions WHERE status=? AND amount_cents>0`, SubActive)
	if err == nil {
		currencyVotes := map[string]int{}
		for subRows.Next() {
			var cadence, currency string
			var amount int
			var trialEnd sql.NullTime
			if err := subRows.Scan(&cadence, &amount, &currency, &trialEnd); err != nil {
				continue
			}
			st.ActiveSubs++
			sub := Subscription{Cadence: cadence, AmountCents: amount}
			if trialEnd.Valid && now.Before(trialEnd.Time.UTC()) {
				sub.TrialEnd = &trialEnd.Time
				st.Trialing++
			}
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

	cutoff := now.AddDate(0, 0, -30).Format("2006-01-02 15:04:05")
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members WHERE created_at>=?`, cutoff).Scan(&st.NewLast30)

	// MRR movement + churn over the last 30 days, derived from the activity log.
	cut := now.AddDate(0, 0, -30)
	newSubs, newMRR := s.CountEventsSince(ctx, EventSubscribe, cut)
	upN, upMRR := s.CountEventsSince(ctx, EventUpgrade, cut)
	st.NewPaidLast30 = newSubs + upN
	st.NewMRRCents = newMRR + upMRR
	canceled, churnedMRR := s.CountEventsSince(ctx, EventCancel, cut)
	st.CanceledLast30 = canceled
	st.ChurnedMRRCents = churnedMRR
	st.NetMRRMovementCents = st.NewMRRCents - st.ChurnedMRRCents

	// Derived ratios. ARPU is MRR spread over paying members; conversion is the
	// share of all members who pay; churn is cancellations over the at-risk base
	// (members who were paying at the start of the window ≈ paid + canceled).
	if st.Paid > 0 {
		st.ARPUCents = mrr / st.Paid
	}
	if st.Total > 0 {
		st.ConversionRate = float64(st.Paid) / float64(st.Total)
	}
	atRisk := st.Paid + st.CanceledLast30
	if atRisk > 0 {
		st.ChurnRate30 = float64(st.CanceledLast30) / float64(atRisk)
	}
	// LTV ≈ ARPU / monthly churn rate. With no observed churn we fall back to a
	// conservative 24-month horizon so the figure stays finite and useful.
	if st.ARPUCents > 0 {
		if st.ChurnRate30 > 0 {
			st.LTVCents = int(float64(st.ARPUCents) / st.ChurnRate30)
		} else {
			st.LTVCents = st.ARPUCents * 24
		}
	}

	return st, nil
}

// RevenueByTier returns the MRR contribution and member count of each tier that
// has at least one member, sorted by MRR descending.
func (s *Store) RevenueByTier(ctx context.Context) ([]TierRevenue, error) {
	tiers, err := s.ListTiers(ctx, true)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, t := range tiers {
		names[t.Slug] = t.Name
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT tier_slug,cadence,amount_cents,currency,trial_end FROM member_subscriptions WHERE status=?`, SubActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agg := map[string]*TierRevenue{}
	now := time.Now().UTC()
	for rows.Next() {
		var slug, cadence, currency string
		var amount int
		var trialEnd sql.NullTime
		if err := rows.Scan(&slug, &cadence, &amount, &currency, &trialEnd); err != nil {
			return nil, err
		}
		tr := agg[slug]
		if tr == nil {
			name := names[slug]
			if name == "" {
				name = slug
			}
			tr = &TierRevenue{Slug: slug, Name: name, Currency: currency}
			agg[slug] = tr
		}
		tr.Members++
		sub := Subscription{Cadence: cadence, AmountCents: amount}
		if trialEnd.Valid && now.Before(trialEnd.Time.UTC()) {
			sub.TrialEnd = &trialEnd.Time
		}
		tr.MRRCents += sub.MonthlyValueCents()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TierRevenue, 0, len(agg))
	for _, tr := range agg {
		out = append(out, *tr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MRRCents != out[j].MRRCents {
			return out[i].MRRCents > out[j].MRRCents
		}
		return out[i].Members > out[j].Members
	})
	return out, nil
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
