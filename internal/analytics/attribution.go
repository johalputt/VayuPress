// SPDX-License-Identifier: Apache-2.0

package analytics

// attribution.go — multi-touch attribution models over goal conversions
// (2025 plan Wave 4). For one goal and a trailing window it credits the UTM
// triple each converted session touched under three standard models:
//
//	first-touch — the whole conversion belongs to the first source seen;
//	last-touch  — the whole conversion belongs to the last source seen;
//	linear      — the conversion splits evenly across distinct sources.
//
// The computation is one ordered scan of the converted sessions' pageviews;
// k-anonymity is not applied here because credits are aggregate model outputs,
// not visitor lists.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AttributionRow aggregates one UTM triple's credit under each model.
type AttributionRow struct {
	Source     string  `json:"source"`
	Medium     string  `json:"medium"`
	Campaign   string  `json:"campaign"`
	FirstTouch float64 `json:"first_touch"`
	LastTouch  float64 `json:"last_touch"`
	Linear     float64 `json:"linear"`
}

type attributionKey struct{ s, m, c string }

// goalPredicate mirrors goals.go's matching rules so attribution and the goals
// panel can never disagree about what counts as a completion.
func goalPredicate(g Goal) (string, interface{}) {
	switch g.Kind {
	case GoalKindEvent:
		return `p.event_type=2 AND p.event_name=?`, g.Target
	default:
		if strings.HasSuffix(g.Target, "*") {
			return `p.event_type=1 AND p.url_path LIKE ?`, strings.TrimSuffix(g.Target, "*") + "%"
		}
		return `p.event_type=1 AND p.url_path=?`, normalizePathExtended(g.Target)
	}
}

// Attribution computes the three models' credit distribution for goal goalID
// over the trailing days. Unknown goal ids yield an error, not silence.
func (s *Store) Attribution(ctx context.Context, days int, goalID string) ([]AttributionRow, error) {
	goals, err := s.ListGoals(ctx)
	if err != nil {
		return nil, err
	}
	var goal *Goal
	for i := range goals {
		if goals[i].ID == goalID {
			goal = &goals[i]
			break
		}
	}
	if goal == nil {
		return nil, fmt.Errorf("no such goal: %s", goalID)
	}
	if days <= 0 {
		days = 30
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	where, arg := goalPredicate(*goal)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT p.session_id, p.utm_source, p.utm_medium, p.utm_campaign
		FROM analytics_pageviews p
		WHERE p.created_at>=?
		  AND p.session_id IN (
		    SELECT DISTINCT p2.session_id FROM analytics_pageviews p2
		    WHERE p2.created_at>=? AND `+where+`)
		ORDER BY p.session_id, p.created_at`, from, from, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	first := map[attributionKey]float64{}
	lastOfSession := map[string]attributionKey{}
	distinctPerSession := map[string]map[attributionKey]bool{}

	for rows.Next() {
		var sess, src, med, camp string
		if err := rows.Scan(&sess, &src, &med, &camp); err != nil {
			return nil, err
		}
		k := attributionKey{src, med, camp}
		if _, seen := lastOfSession[sess]; !seen {
			first[k]++ // this session's first touch
		} else if lastOfSession[sess] != k {
			// repeat touches of the same source don't change anything
			_ = k
		}
		lastOfSession[sess] = k
		if distinctPerSession[sess] == nil {
			distinctPerSession[sess] = map[attributionKey]bool{}
		}
		distinctPerSession[sess][k] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	linear := map[attributionKey]float64{}
	for _, ks := range distinctPerSession {
		share := 1 / float64(len(ks))
		for k := range ks {
			linear[k] += share
		}
	}

	agg := map[attributionKey]*AttributionRow{}
	add := func(k attributionKey, apply func(*AttributionRow)) {
		r := agg[k]
		if r == nil {
			r = &AttributionRow{Source: k.s, Medium: k.m, Campaign: k.c}
			agg[k] = r
		}
		apply(r)
	}
	for k, v := range first {
		kk := k
		add(kk, func(r *AttributionRow) { r.FirstTouch += v })
	}
	for _, k := range lastOfSession {
		kk := k
		add(kk, func(r *AttributionRow) { r.LastTouch++ })
	}
	for k, v := range linear {
		kk := k
		add(kk, func(r *AttributionRow) { r.Linear += v })
	}

	out := make([]AttributionRow, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].FirstTouch+out[i].LastTouch+out[i].Linear, out[j].FirstTouch+out[j].LastTouch+out[j].Linear
		return si > sj
	})
	return out, nil
}
