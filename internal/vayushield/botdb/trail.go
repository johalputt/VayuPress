// SPDX-License-Identifier: Apache-2.0

package botdb

// trail.go — aggregate reads over the shield's audit trail.
//
// vayushield_blocked and vayushield_challenges have been INSERTed on every event
// since they were introduced, and until now the only other SQL touching either
// table was a DELETE purge. Every event the shield has ever recorded was written
// and never read.
//
// ADR-0137 was right to remove the old scrolling per-IP list: a page of hashed
// addresses was stale the moment it rendered (the live jail is in memory) and it
// was routinely misread as "these people are currently blocked". But it was
// removed rather than replaced, which left the panel with no time dimension at
// all — every counter on it is cumulative-since-boot, so an operator can see that
// 4,000 requests were blocked and not whether that happened last night or over
// six weeks.
//
// These are aggregates only. No row here can identify a visitor: the source
// column is a salted hash the shield never reverses, and it is not selected by
// any query below.

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// TrailWindow is a period the aggregates are computed over.
type TrailWindow struct {
	Since time.Time
	Hours int
}

// Count is one bucket of an aggregate.
type Count struct {
	Key   string
	Count int64
}

// HourBucket is one hour of activity. Blocks and challenges are counted
// separately because they are different events: a block is a refusal, a
// challenge is an invitation to prove otherwise.
type HourBucket struct {
	Hour       string // "YYYY-MM-DD HH:00" in UTC
	Blocks     int64
	Challenges int64
	Solved     int64
}

// PassRate returns the fraction of this hour's challenges that were solved, and
// whether there were enough to mean anything. Two challenges, one solved, is not
// a 50% pass rate in any useful sense, and rendering it as one invites an
// operator to act on noise.
func (h HourBucket) PassRate() (rate float64, meaningful bool) {
	if h.Challenges <= 0 {
		return 0, false
	}
	return float64(h.Solved) / float64(h.Challenges), h.Challenges >= 10
}

// Trail is the whole report.
type Trail struct {
	Window TrailWindow

	TotalBlocks     int64
	TotalChallenges int64
	TotalSolved     int64

	Reasons   []Count // why the shield refused
	Paths     []Count // what was being hit
	Countries []Count

	Hours []HourBucket

	// RetentionDays is how long rows survive the purge. Stated with the report
	// because a "last 90 days" view over a table pruned at 30 is not a quiet
	// approximation, it is a wrong answer presented confidently.
	RetentionDays int
}

// sqliteTime formats a time the way SQLite's CURRENT_TIMESTAMP does, so string
// comparison against created_at is correct. The columns are DATETIME with a
// CURRENT_TIMESTAMP default, which SQLite stores as "YYYY-MM-DD HH:MM:SS" in
// UTC; comparing against anything else compares strings that only look like
// dates.
func sqliteTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// ReadTrail computes the aggregate report over the last `hours`.
//
// Every query is bounded by created_at and driven by idx_blocked_created /
// idx_challenges_created, so the cost scales with the window rather than with
// the table. That matters: this runs on a panel that refreshes itself, on the
// same SQLite the site serves from.
func (s *Store) ReadTrail(ctx context.Context, hours, topN, retentionDays int) (Trail, error) {
	if s == nil || s.reader() == nil {
		return Trail{}, nil
	}
	if hours <= 0 {
		hours = 24
	}
	if topN <= 0 || topN > 50 {
		topN = 8
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	cut := sqliteTime(since)

	t := Trail{
		Window:        TrailWindow{Since: since, Hours: hours},
		RetentionDays: retentionDays,
	}

	db := s.reader()
	row := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayushield_blocked WHERE created_at>=?`, cut)
	if err := row.Scan(&t.TotalBlocks); err != nil && err != sql.ErrNoRows {
		return t, err
	}
	row = db.QueryRowContext(ctx,
		`SELECT COUNT(1), COALESCE(SUM(CASE WHEN outcome='solved' THEN 1 ELSE 0 END),0)
		 FROM vayushield_challenges WHERE created_at>=?`, cut)
	if err := row.Scan(&t.TotalChallenges, &t.TotalSolved); err != nil && err != sql.ErrNoRows {
		return t, err
	}

	var err error
	// COALESCE + NULLIF collapses empty strings into one "(not recorded)" bucket
	// rather than letting them masquerade as a real value at the top of the list.
	// country_code is empty whenever no geo lookup is wired, which is the default.
	if t.Reasons, err = s.topCounts(ctx, `block_reason`, cut, topN); err != nil {
		return t, err
	}
	if t.Paths, err = s.topCounts(ctx, `request_path`, cut, topN); err != nil {
		return t, err
	}
	if t.Countries, err = s.topCounts(ctx, `country_code`, cut, topN); err != nil {
		return t, err
	}
	if t.Hours, err = s.hourly(ctx, cut); err != nil {
		return t, err
	}
	return t, nil
}

// topCounts returns the top N values of a column over the window.
//
// The column name is NOT a parameter in the SQL sense — SQLite cannot bind an
// identifier — so it is chosen from a fixed set here and never taken from a
// caller's input. Every call site passes a literal.
func (s *Store) topCounts(ctx context.Context, column, cut string, topN int) ([]Count, error) {
	var expr string
	switch column {
	case "block_reason":
		expr = `COALESCE(NULLIF(TRIM(block_reason),''),'(not recorded)')`
	case "request_path":
		expr = `COALESCE(NULLIF(TRIM(request_path),''),'(not recorded)')`
	case "country_code":
		expr = `COALESCE(NULLIF(TRIM(country_code),''),'(no geo data)')`
	default:
		// Unreachable from any call site; refusing beats interpolating.
		return nil, nil
	}
	rows, err := s.reader().QueryContext(ctx,
		`SELECT `+expr+` AS k, COUNT(1) AS n FROM vayushield_blocked
		 WHERE created_at>=? GROUP BY k ORDER BY n DESC, k ASC LIMIT ?`, cut, topN)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// hourly builds the per-hour series — the time dimension the panel lacks
// entirely. Blocks and challenges live in different tables, so they are read
// separately and merged in Go rather than joined: a full outer join in SQLite
// means a UNION of two correlated subqueries, which is more SQL to be wrong in
// for a merge of at most a few hundred rows.
func (s *Store) hourly(ctx context.Context, cut string) ([]HourBucket, error) {
	byHour := map[string]*HourBucket{}
	get := func(h string) *HourBucket {
		if b, ok := byHour[h]; ok {
			return b
		}
		b := &HourBucket{Hour: h}
		byHour[h] = b
		return b
	}

	rows, err := s.reader().QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d %H:00', created_at) AS h, COUNT(1)
		 FROM vayushield_blocked WHERE created_at>=? GROUP BY h`, cut)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		get(h).Blocks = n
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	rows, err = s.reader().QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d %H:00', created_at) AS h, COUNT(1),
		        COALESCE(SUM(CASE WHEN outcome='solved' THEN 1 ELSE 0 END),0)
		 FROM vayushield_challenges WHERE created_at>=? GROUP BY h`, cut)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h string
		var n, solved int64
		if err := rows.Scan(&h, &n, &solved); err != nil {
			return nil, err
		}
		b := get(h)
		b.Challenges, b.Solved = n, solved
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sorted ascending so a caller can render a series without re-sorting, and so
	// "the last bucket" is unambiguously the most recent hour.
	keys := make([]string, 0, len(byHour))
	for k := range byHour {
		keys = append(keys, k)
	}
	// Zero-padded "YYYY-MM-DD HH:00" means lexicographic order is chronological.
	sort.Strings(keys)
	out := make([]HourBucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byHour[k])
	}
	return out, nil
}
