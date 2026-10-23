// SPDX-License-Identifier: Apache-2.0

package analytics

import "context"

// PathCountryViews is how many page views one path had on one site from one
// country (the visit's, empty when unknown) in a window.
type PathCountryViews struct {
	Path, Site, Country string
	Views               int
}

// ViewsByPathCountryScoped groups the page views of the last days by path,
// site and country, most viewed first, at most limit groups. It is what a
// draft of the shield's rules is replayed against: who read what, from
// where, with no address, because none is kept.
func (s *Store) ViewsByPathCountryScoped(ctx context.Context, scope string, days, limit int) ([]PathCountryViews, error) {
	if days <= 0 {
		days = 7
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	q, args := viewsByPathCountryQuery(scope, from, limit)
	rows, err := s.readDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PathCountryViews{}
	for rows.Next() {
		var v PathCountryViews
		if err := rows.Scan(&v.Path, &v.Site, &v.Country, &v.Views); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// viewsByPathCountryQuery is the read, apart so its plan can be tested: the
// window must bound the index search, not only filter what it returns.
func viewsByPathCountryQuery(scope, from string, limit int) (string, []any) {
	clause, carg := domainClauseOn("p", scope)
	return `SELECT p.url_path, p.domain_id, COALESCE(v.country,''), COUNT(1)
		 FROM analytics_pageviews p
		 LEFT JOIN analytics_sessions v ON v.id=p.session_id
		 WHERE p.event_type=1 AND p.created_at>=?` + clause + `
		 GROUP BY 1, 2, 3 ORDER BY 4 DESC LIMIT ?`,
		append(append([]any{from}, carg...), limit)
}
