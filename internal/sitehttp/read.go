// SPDX-License-Identifier: Apache-2.0

package sitehttp

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// Summary is what the server measured of a site over a range of days.
type Summary struct {
	Requests int64 `json:"requests"`
	S2xx     int64 `json:"status_2xx"`
	S3xx     int64 `json:"status_3xx"`
	S4xx     int64 `json:"status_4xx"`
	S404     int64 `json:"status_404"`
	S5xx     int64 `json:"status_5xx"`
	// Pages are the HTML pages among the requests, with their server time:
	// the mean, and each percentile as the bound of the bucket it falls in
	// ("at most", within a tenth).
	Pages  int64   `json:"pages"`
	MeanMS float64 `json:"mean_ms"`
	P50MS  int64   `json:"p50_ms_at_most"`
	P75MS  int64   `json:"p75_ms_at_most"`
	P95MS  int64   `json:"p95_ms_at_most"`
}

// Day is one day of a Summary's range.
type Day struct {
	Day string `json:"day"`
	Summary
}

// from is the first instant of a range of days ending today, as the rows
// name their minutes and hours.
func from(days int) string {
	if days <= 0 {
		days = 30
	}
	return time.Now().UTC().AddDate(0, 0, -(days-1)).Format("2006-01-02") + " 00:00:00"
}

func scopeClause(site string) (string, []any) {
	if site == AllSites {
		return "", nil
	}
	return " AND domain_id=?", []any{site}
}

// Read is site's summary over the trailing days (every site's, for
// AllSites), and the same for each day that had any request.
func Read(ctx context.Context, db *sql.DB, site string, days int) (Summary, []Day, error) {
	clause, carg := scopeClause(site)
	args := append([]any{from(days)}, carg...)
	byDay := map[string]*Day{}
	var order []string
	rows, err := db.QueryContext(ctx, minutesSQL(clause), args...)
	if err != nil {
		return Summary{}, nil, err
	}
	var total Summary
	var totalMS int64
	for rows.Next() {
		d := &Day{}
		var ms int64
		if err := rows.Scan(&d.Day, &d.Requests, &d.S2xx, &d.S3xx, &d.S4xx, &d.S404, &d.S5xx, &d.Pages, &ms); err != nil {
			_ = rows.Close()
			return Summary{}, nil, err
		}
		if d.Pages > 0 {
			d.MeanMS = float64(ms) / float64(d.Pages)
		}
		byDay[d.Day] = d
		order = append(order, d.Day)
		total.Requests += d.Requests
		total.S2xx += d.S2xx
		total.S3xx += d.S3xx
		total.S4xx += d.S4xx
		total.S404 += d.S404
		total.S5xx += d.S5xx
		total.Pages += d.Pages
		totalMS += ms
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Summary{}, nil, err
	}
	_ = rows.Close()
	if total.Pages > 0 {
		total.MeanMS = float64(totalMS) / float64(total.Pages)
	}

	rows, err = db.QueryContext(ctx, latencySQL(clause), args...)
	if err != nil {
		return Summary{}, nil, err
	}
	defer rows.Close()
	all := map[int]int64{}
	perDay := map[string]map[int]int64{}
	for rows.Next() {
		var day string
		var b int
		var n int64
		if err := rows.Scan(&day, &b, &n); err != nil {
			return Summary{}, nil, err
		}
		if b < 0 || b >= len(bounds) {
			continue // a bucket this binary does not have
		}
		all[b] += n
		if perDay[day] == nil {
			perDay[day] = map[int]int64{}
		}
		perDay[day][b] += n
	}
	if err := rows.Err(); err != nil {
		return Summary{}, nil, err
	}
	total.P50MS, total.P75MS, total.P95MS = percentile(all, 0.50), percentile(all, 0.75), percentile(all, 0.95)
	out := make([]Day, 0, len(order))
	for _, day := range order {
		d := byDay[day]
		h := perDay[day]
		d.P50MS, d.P75MS, d.P95MS = percentile(h, 0.50), percentile(h, 0.75), percentile(h, 0.95)
		out = append(out, *d)
	}
	return total, out, nil
}

// minutesSQL and latencySQL are Read's queries with the site clause, each a
// range of its table's key or its time index (sitehttp_test.go holds them to
// it).
func minutesSQL(clause string) string {
	return `SELECT substr(minute,1,10), SUM(requests), SUM(s2xx), SUM(s3xx), SUM(s4xx), SUM(s404), SUM(s5xx), SUM(pages), SUM(page_ms)
		FROM site_http_minutes WHERE minute>=?` + clause + ` GROUP BY 1 ORDER BY 1`
}

func latencySQL(clause string) string {
	return `SELECT substr(hour,1,10), bucket, SUM(n) FROM site_http_latency WHERE hour>=?` + clause + ` GROUP BY 1, 2`
}

// percentile is the q-th of the times in h, by nearest rank (the
// ceil(q·n)-th smallest), as the bound of its bucket; 0 when h is empty.
func percentile(h map[int]int64, q float64) int64 {
	var n int64
	for _, c := range h {
		n += c
	}
	if n == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(n)))
	if rank < 1 {
		rank = 1
	}
	var seen int64
	for b := 0; b < len(bounds); b++ {
		seen += h[b]
		if seen >= rank {
			return bounds[b]
		}
	}
	return bounds[len(bounds)-1]
}
