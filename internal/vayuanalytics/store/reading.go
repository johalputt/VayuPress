// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"strconv"
	"strings"
)

// AllDomains asks a report for every site's rows together.
const AllDomains = "\x00*all*"

// PageDepth is how far a page's readers scrolled: the readings measured, and
// how many of them reached half the page and nine tenths of it.
//
// A reading is measured when the reader leaves the page (its exit beacon
// carries the furthest point seen); one with no exit beacon, a tab killed
// before it could send one, has no depth to report and is not counted as 0.
// Nine tenths stands for the end, since a footer is rarely scrolled to.
type PageDepth struct {
	Path        string `json:"path"`
	Readings    int64  `json:"readings"`
	ReachedHalf int64  `json:"reached_half"`
	ReachedEnd  int64  `json:"reached_90"`
}

// ReadingDepth is the most read pages' depth over the trailing days, for one
// site, or for every site when domainID is AllDomains.
func (s *Store) ReadingDepth(ctx context.Context, domainID string, days, limit int) ([]PageDepth, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	from, until := spanTimes(ctx, days)
	clause, args := "", []any{from, until}
	if domainID != AllDomains {
		clause, args = " AND domain_id=?", append(args, domainID)
	}
	f, fargs := focusReaders(ctx, from, until)
	rows, err := s.readDB().QueryContext(ctx, readingDepthSQL(clause+f), append(append(args, fargs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageDepth
	for rows.Next() {
		var d PageDepth
		if err := rows.Scan(&d.Path, &d.Readings, &d.ReachedHalf, &d.ReachedEnd); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// readingDepthSQL is ReadingDepth's query with its site clause. The page is
// grouped through a unary +, so the time index's range is read and grouped,
// never idx_va_sessions_page walked end to end (reading_test.go).
func readingDepthSQL(clause string) string {
	return `SELECT page_path, COUNT(1), SUM(scroll_depth_percent>=50), SUM(scroll_depth_percent>=90)
		FROM vayuanalytics_sessions WHERE entry_time>=? AND entry_time<?` + clause + ` AND exit_time IS NOT NULL AND ` + human + `
		GROUP BY +page_path ORDER BY 2 DESC, 1 LIMIT ?`
}

// PageEngaged is a page's readings over a range (page loads measured as the
// reader left, as PageDepth's) and how many of them were engaged: the reader
// stayed engagedThresholdSec and scrolled engagedThresholdScroll per cent.
type PageEngaged struct {
	Path     string
	Readings int64
	Engaged  int64
}

// PageEngagement is every page's readings over the trailing days, for one
// site, or for every site when domainID is AllDomains. Each path is as it was
// recorded: rows from before paths lost their trailing slash are their own,
// and the caller merges them.
func (s *Store) PageEngagement(ctx context.Context, domainID string, days int) ([]PageEngaged, error) {
	from, until := spanTimes(ctx, days)
	clause, args := "", []any{from, until}
	if domainID != AllDomains {
		clause, args = " AND domain_id=?", append(args, domainID)
	}
	f, fargs := focusReaders(ctx, from, until)
	rows, err := s.readDB().QueryContext(ctx, pageEngagementSQL(clause+f), append(args, fargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageEngaged
	for rows.Next() {
		var p PageEngaged
		if err := rows.Scan(&p.Path, &p.Readings, &p.Engaged); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pageEngagementSQL is PageEngagement's query with its site clause, grouped
// through a unary + as readingDepthSQL is.
func pageEngagementSQL(clause string) string {
	return `SELECT page_path, COUNT(1), SUM(engaged) FROM vayuanalytics_sessions WHERE entry_time>=? AND entry_time<?` + clause +
		` AND exit_time IS NOT NULL AND ` + human + ` GROUP BY +page_path`
}

// PageRead is how one page was read over a range: its readings (page loads
// measured as the reader left, as PageDepth's), how many were engaged, the
// average time on the page of the readings that stayed any time, and how
// many reached each tenth of the page, Reach[0] a tenth down and Reach[9]
// the end.
type PageRead struct {
	Readings, Engaged int64
	AvgSeconds        float64
	Reach             [10]int64
}

// PageReading is how path was read on one site (AllDomains for every site)
// over the period, the readers ctx is narrowed to alone. Both spellings of
// the path, with and without a trailing slash, are the page.
func (s *Store) PageReading(ctx context.Context, domainID, path string, days int) (PageRead, error) {
	from, until := spanTimes(ctx, days)
	trimmed := strings.TrimSuffix(path, "/")
	if trimmed == "" {
		trimmed = "/"
	}
	clause, args := " AND page_path IN (?,?)", []any{from, until, trimmed, trimmed + "/"}
	if domainID != AllDomains {
		clause, args = clause+" AND domain_id=?", append(args, domainID)
	}
	f, fargs := focusReaders(ctx, from, until)
	var reach strings.Builder
	for i := 1; i <= 10; i++ {
		reach.WriteString(", COALESCE(SUM(scroll_depth_percent>=" + strconv.Itoa(i*10) + "),0)")
	}
	var r PageRead
	dst := []any{&r.Readings, &r.Engaged, &r.AvgSeconds}
	for i := range r.Reach {
		dst = append(dst, &r.Reach[i])
	}
	err := s.readDB().QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(engaged),0), COALESCE(AVG(CASE WHEN time_on_page_seconds>0 THEN time_on_page_seconds END),0)`+reach.String()+`
		FROM vayuanalytics_sessions WHERE entry_time>=? AND entry_time<?`+clause+f+` AND exit_time IS NOT NULL AND `+human, append(args, fargs...)...).Scan(dst...)
	return r, err
}
