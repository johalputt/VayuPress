// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"time"
)

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

// ViewsOverTimeScoped counts the page views of the last hours by hour
// ("2006-01-02 15:00", UTC) or, past a day, by day ("2006-01-02"): the reader
// side of a site's security beside its traffic.
func (s *Store) ViewsOverTimeScoped(ctx context.Context, scope string, hours int) (map[string]int, error) {
	if hours <= 0 {
		hours = 24
	}
	from := s.now().UTC().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02 15:04:05")
	q, args := viewsOverTimeQuery(scope, from, hours > 24)
	rows, err := s.readDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// viewsOverTimeQuery is the read, apart so its plan can be tested.
func viewsOverTimeQuery(scope, from string, byDay bool) (string, []any) {
	bucket := `strftime('%Y-%m-%d %H:00', p.created_at)`
	if byDay {
		bucket = `strftime('%Y-%m-%d', p.created_at)`
	}
	clause, carg := domainClauseOn("p", scope)
	return `SELECT ` + bucket + ` AS b, COUNT(1) FROM analytics_pageviews p
		 WHERE p.event_type=1 AND p.created_at>=?` + clause + ` GROUP BY b`,
		append([]any{from}, carg...)
}

// PurgeSite deletes one hosted site's analytics older than retainDays: its
// views and visits, each view's properties and each visit's revenue, and the
// site's daily visitor sketches. The install-wide totals (analytics_daily,
// analytics_referrers) hold no visitor and stay under the install's own
// retention. site must name a hosted site: the primary's retention is the
// install's.
func (s *Store) PurgeSite(ctx context.Context, site string, retainDays int) (int64, error) {
	if site == "" || site == ScopeAllDomains || retainDays <= 0 {
		return 0, nil
	}
	cutoff := s.now().UTC().AddDate(0, 0, -retainDays)
	at, day := cutoff.Format("2006-01-02 15:04:05"), cutoff.Format("2006-01-02")
	var total int64
	for _, q := range []struct {
		sql  string
		args []any
	}{
		// Children first, while their parents still say which site they were.
		{`DELETE FROM analytics_event_data WHERE event_id IN (SELECT id FROM analytics_pageviews WHERE domain_id=? AND created_at<?)`, []any{site, at}},
		{`DELETE FROM analytics_revenue WHERE session_id IN (SELECT id FROM analytics_sessions WHERE domain_id=? AND created_at<?)`, []any{site, at}},
		{`DELETE FROM analytics_pageviews WHERE domain_id=? AND created_at<?`, []any{site, at}},
		{`DELETE FROM analytics_sessions WHERE domain_id=? AND created_at<?`, []any{site, at}},
		{`DELETE FROM analytics_rollup_daily WHERE domain_id=? AND day<?`, []any{site, day}},
	} {
		res, err := s.db.ExecContext(ctx, q.sql, q.args...)
		if err != nil {
			return total, err
		}
		if n, e := res.RowsAffected(); e == nil {
			total += n
		}
	}
	return total, nil
}

// SiteFigures is one site's page views and visitors over a range.
type SiteFigures struct {
	Site     string `json:"site"`
	Views    int    `json:"views"`
	Visitors int    `json:"visitors"`
}

// BySite is every site's page views and visitors over the trailing days,
// most viewed first: the install's aggregate, broken down into the sites it
// adds up. The site is grouped through a unary +, so the time index's range is
// read (console_plans_test.go).
func (s *Store) BySite(ctx context.Context, days int) ([]SiteFigures, error) {
	if days <= 0 {
		days = 30
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.readDB().QueryContext(ctx, `SELECT domain_id, SUM(event_type=1), COUNT(DISTINCT visitor_id)
		FROM analytics_pageviews WHERE created_at>=? GROUP BY +domain_id ORDER BY 2 DESC, 1`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SiteFigures
	for rows.Next() {
		var f SiteFigures
		if err := rows.Scan(&f.Site, &f.Views, &f.Visitors); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
