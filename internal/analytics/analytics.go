// Package analytics provides privacy-first, cookieless page-view counting for
// VayuPress.
//
// Privacy by design: NO IP addresses, NO user agents, NO cookies, NO device
// fingerprints, and NO per-visitor rows are ever stored. The only persisted
// data is a daily aggregate count per path plus a daily aggregate count per
// referrer host. There is nothing in the schema that can identify or track an
// individual reader, so the feature needs no consent banner and leaks nothing on
// a database compromise.
package analytics

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"
)

// Store aggregates page views in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Record increments the view counter for path on today's date and, when a
// same-site-external referrer is supplied, the counter for its host. Both writes
// are UPSERT increments, so the table grows only with distinct (day, path) and
// (day, host) pairs — never per visit.
func (s *Store) Record(ctx context.Context, path, referrer string) error {
	day := time.Now().UTC().Format("2006-01-02")
	path = normalizePath(path)
	if path == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_daily(day,path,views) VALUES(?,?,1)
		 ON CONFLICT(day,path) DO UPDATE SET views=views+1`, day, path); err != nil {
		return err
	}
	if host := referrerHost(referrer); host != "" {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO analytics_referrers(day,host,hits) VALUES(?,?,1)
			 ON CONFLICT(day,host) DO UPDATE SET hits=hits+1`, day, host); err != nil {
			return err
		}
	}
	return nil
}

// PathCount is a path with its view total over the queried window.
type PathCount struct {
	Path  string `json:"path"`
	Views int64  `json:"views"`
}

// TrendingArticle is a published article ranked by its view total over a window,
// joined back from the analytics path ("/<slug>") to the article record so the
// caller gets a ready-to-render title and cover image.
type TrendingArticle struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Image string `json:"image"`
	Views int64  `json:"views"`
}

// TrendingArticles returns the most-viewed published, non-page articles over the
// trailing `days` days (inclusive of today), highest first. Views come from the
// cookieless daily aggregate (analytics_daily, path "/<slug>"); the join to
// articles filters to live posts and supplies the title + feature image. Ties
// break by recency so a fresh post outranks an equally-viewed older one.
func (s *Store) TrendingArticles(ctx context.Context, days, limit int) ([]TrendingArticle, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 10
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.slug, a.title, COALESCE(a.feature_image,''), SUM(d.views) AS v
		FROM analytics_daily d
		JOIN articles a ON a.slug = SUBSTR(d.path, 2)
		WHERE d.day >= ? AND d.path LIKE '/%'
		  AND a.status = 'published' AND a.is_page = 0
		GROUP BY a.slug, a.title, a.feature_image
		ORDER BY v DESC, a.created_at DESC
		LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrendingArticle, 0, limit)
	for rows.Next() {
		var t TrendingArticle
		if err := rows.Scan(&t.Slug, &t.Title, &t.Image, &t.Views); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HostCount is a referrer host with its hit total over the queried window.
type HostCount struct {
	Host string `json:"host"`
	Hits int64  `json:"hits"`
}

// DayCount is a single day's total view count.
type DayCount struct {
	Day   string `json:"day"`
	Views int64  `json:"views"`
}

// Summary is the rolled-up analytics view returned to the admin dashboard.
type Summary struct {
	Days       int         `json:"days"`
	TotalViews int64       `json:"total_views"`
	TopPages   []PathCount `json:"top_pages"`
	Referrers  []HostCount `json:"referrers"`
	Daily      []DayCount  `json:"daily"`
}

// Since returns an aggregate summary over the trailing `days` days (inclusive of
// today). limit caps the top-pages and referrers lists.
func (s *Store) Since(ctx context.Context, days, limit int) (*Summary, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	sum := &Summary{Days: days}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(views),0) FROM analytics_daily WHERE day>=?`, from).Scan(&sum.TotalViews); err != nil {
		return nil, err
	}

	if rows, err := s.db.QueryContext(ctx,
		`SELECT path,SUM(views) v FROM analytics_daily WHERE day>=? GROUP BY path ORDER BY v DESC LIMIT ?`, from, limit); err == nil {
		defer rows.Close()
		for rows.Next() {
			var p PathCount
			if err := rows.Scan(&p.Path, &p.Views); err != nil {
				return nil, err
			}
			sum.TopPages = append(sum.TopPages, p)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}

	if rows, err := s.db.QueryContext(ctx,
		`SELECT host,SUM(hits) h FROM analytics_referrers WHERE day>=? GROUP BY host ORDER BY h DESC LIMIT ?`, from, limit); err == nil {
		defer rows.Close()
		for rows.Next() {
			var h HostCount
			if err := rows.Scan(&h.Host, &h.Hits); err != nil {
				return nil, err
			}
			sum.Referrers = append(sum.Referrers, h)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}

	if rows, err := s.db.QueryContext(ctx,
		`SELECT day,SUM(views) v FROM analytics_daily WHERE day>=? GROUP BY day ORDER BY day`, from); err == nil {
		defer rows.Close()
		for rows.Next() {
			var d DayCount
			if err := rows.Scan(&d.Day, &d.Views); err != nil {
				return nil, err
			}
			sum.Daily = append(sum.Daily, d)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}

	return sum, nil
}

// Purge deletes aggregates older than the retention window (days). Returns the
// number of rows removed across both tables.
func (s *Store) Purge(ctx context.Context, retainDays int) (int64, error) {
	if retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays).Format("2006-01-02")
	var total int64
	for _, tbl := range []string{"analytics_daily", "analytics_referrers"} {
		res, err := s.db.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE day<?", cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// normalizePath trims query/fragment and caps length so the table cannot be
// inflated by attacker-chosen query strings.
func normalizePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSpace(p)
	if p == "" {
		p = "/"
	}
	if len(p) > 512 {
		p = p[:512]
	}
	return p
}

// referrerHost extracts the host from a referrer URL, returning "" for empty or
// unparseable referrers. Only the host is kept — never the full URL — so no
// query parameters or paths from the referring site are retained.
func referrerHost(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}
