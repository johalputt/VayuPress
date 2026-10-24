// SPDX-License-Identifier: Apache-2.0

package store

import "context"

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
	clause, args := "", []any{fromTime(days)}
	if domainID != AllDomains {
		clause, args = " AND domain_id=?", append(args, domainID)
	}
	rows, err := s.readDB().QueryContext(ctx, readingDepthSQL(clause), append(args, limit)...)
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
		FROM vayuanalytics_sessions WHERE entry_time>=?` + clause + ` AND exit_time IS NOT NULL AND ` + human + `
		GROUP BY +page_path ORDER BY 2 DESC, 1 LIMIT ?`
}
