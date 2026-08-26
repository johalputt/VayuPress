// SPDX-License-Identifier: Apache-2.0

package analytics

// rollup.go — the daily rollup ladder (2025 plan Wave 4, item 6).
//
// BuildDailyRollup folds one day's raw pageviews into analytics_rollup_daily:
// view/session counters plus an HLL sketch of distinct visitors. The ladder
// rule: dashboards asking for ranges beyond RollupRawCutoffDays read rollups
// (O(days) sketch unions); younger ranges keep reading raw rows for
// hour-fresh accuracy. BuildDailyRollup is an idempotent upsert, so the ladder
// can re-run any day after late beacons without dedup logic.

import (
	"context"
	"time"
)

// RollupRawCutoffDays: ranges longer than this read from rollups.
const RollupRawCutoffDays = 60

// BuildDailyRollup recomputes the rollup rows for the given UTC day (one per
// domain) and upserts them. Safe to call repeatedly.
func (s *Store) BuildDailyRollup(ctx context.Context, day time.Time) error {
	dayKey := day.UTC().Format("2006-01-02")

	// Pass 1 — exact counters per domain.
	type totals struct {
		views    int64
		sessions int64
	}
	tot := map[string]totals{}
	rows, err := s.readDB().QueryContext(ctx, `
		SELECT COALESCE(domain_id,''), COUNT(1), COUNT(DISTINCT session_id)
		FROM analytics_pageviews
		WHERE created_at>=? AND created_at<?
		GROUP BY domain_id`, dayKey, dayKey+"~")
	if err != nil {
		return err
	}
	for rows.Next() {
		var dom string
		var v, ss int64
		if err := rows.Scan(&dom, &v, &ss); err != nil {
			rows.Close()
			return err
		}
		tot[dom] = totals{views: v, sessions: ss}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Pass 2 — fold every distinct visitor per domain into its sketch.
	hlls := map[string]*HLL{}
	vrows, err := s.readDB().QueryContext(ctx, `
		SELECT COALESCE(domain_id,''), visitor_id
		FROM analytics_pageviews
		WHERE created_at>=? AND created_at<?
		GROUP BY domain_id, visitor_id`, dayKey, dayKey+"~")
	if err != nil {
		return err
	}
	for vrows.Next() {
		var dom, vid string
		if err := vrows.Scan(&dom, &vid); err != nil {
			vrows.Close()
			return err
		}
		h := hlls[dom]
		if h == nil {
			h = &HLL{}
			hlls[dom] = h
		}
		h.Add(vid)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return err
	}

	for dom, t := range tot {
		blob := (&HLL{}).Marshal()
		if h := hlls[dom]; h != nil {
			blob = h.Marshal()
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO analytics_rollup_daily(day,domain_id,views,sessions,uniques_hll,updated_at)
			VALUES(?,?,?,?,?,CURRENT_TIMESTAMP)
			ON CONFLICT(day,domain_id) DO UPDATE SET
			  views=excluded.views, sessions=excluded.sessions,
			  uniques_hll=excluded.uniques_hll, updated_at=CURRENT_TIMESTAMP`,
			dayKey, dom, t.views, t.sessions, blob); err != nil {
			return err
		}
	}
	return nil
}

// RollupUniques estimates distinct visitors across [fromDay,toDay] (inclusive
// UTC day keys) by merging the stored sketches.
func (s *Store) RollupUniques(ctx context.Context, fromDay, toDay string) (uint64, error) {
	rows, err := s.readDB().QueryContext(ctx, `
		SELECT uniques_hll FROM analytics_rollup_daily
		WHERE day>=? AND day<=?`, fromDay, toDay)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	merged := &HLL{}
	n := 0
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return 0, err
		}
		part, err := UnmarshalHLL(blob)
		if err != nil {
			continue // foreign/corrupt sketch: skip rather than fail the panel
		}
		merged.Merge(part)
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return uint64(merged.Estimate() + 0.5), nil
}
