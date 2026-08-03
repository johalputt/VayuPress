// SPDX-License-Identifier: Apache-2.0

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

	"github.com/johalputt/vayupress/internal/config"
)

// Store aggregates page views in SQLite.
type Store struct {
	db     *sql.DB
	reader *sql.DB // dashboard/report read pool; falls back to db. Set via UseReader.
}

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db, reader: db} }

// UseReader routes the report/dashboard read queries at a dedicated read pool
// instead of the single writer connection. The admin Analytics panel runs many
// heavy aggregate scans (Since, OverviewSince/Between, Devices, Browsers, OS,
// CustomEvents, PageviewSeries, TopPages, realtime) over the growing
// analytics_daily/analytics_pageviews tables; on the writer they serialise
// behind the pageview write stream and each other, so the panel could exceed the
// 30s server timeout and 502. Writes (Record, Purge) stay on the writer; WAL
// gives the reader read-your-writes. A nil reader is ignored.
func (s *Store) UseReader(reader *sql.DB) {
	if reader != nil {
		s.reader = reader
	}
}

// readDB returns the handle for read-only report queries: the dedicated read
// pool when set, otherwise the writer.
func (s *Store) readDB() *sql.DB {
	if s.reader != nil {
		return s.reader
	}
	return s.db
}

// Record increments the view counter for path on today's date and, when a
// same-site-external referrer is supplied, the counter for its host. Both writes
// are UPSERT increments, so the table grows only with distinct
// (day, domain, path) and (day, domain, host) pairs — never per visit.
//
// scope is the VayuDomains domain that served the view: "" for the primary, or a
// secondary domain's id, matching the convention in articles.domain_id. It must
// be resolved by the CALLER from the request it is recording, on the request
// goroutine — see RecordFor. Nothing a visitor sends may choose it: attribution
// derived from a client-controlled value is attribution a client can forge into
// a competitor's report.
func (s *Store) Record(ctx context.Context, scope, path, referrer string) error {
	day := time.Now().UTC().Format("2006-01-02")
	path = normalizePath(path)
	if path == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_daily(day,domain_id,path,views) VALUES(?,?,?,1)
		 ON CONFLICT(day,domain_id,path) DO UPDATE SET views=views+1`, day, scope, path); err != nil {
		return err
	}
	if host := referrerHost(referrer); host != "" {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO analytics_referrers(day,domain_id,host,hits) VALUES(?,?,?,1)
			 ON CONFLICT(day,domain_id,host) DO UPDATE SET hits=hits+1`, day, scope, host); err != nil {
			return err
		}
	}
	return nil
}

// ViewsForScope returns the total views and the busiest paths for ONE domain
// over the last `days` days.
//
// Separate from SummarySince, which aggregates across every domain for the
// operator. A caller that wants one client's numbers must ask for that client's
// numbers; there is no "and also filter it afterwards" path, because that is the
// shape a forgotten WHERE clause hides in.
func (s *Store) ViewsForScope(ctx context.Context, scope string, days, limit int) (total int64, top []PathCount, err error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 10
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	if err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(views),0) FROM analytics_daily WHERE day>=? AND domain_id=?`,
		from, scope).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, qerr := s.db.QueryContext(ctx,
		`SELECT path,SUM(views) v FROM analytics_daily WHERE day>=? AND domain_id=? GROUP BY path ORDER BY v DESC LIMIT ?`,
		from, scope, limit)
	if qerr != nil {
		return total, nil, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var p PathCount
		if err := rows.Scan(&p.Path, &p.Views); err != nil {
			return total, nil, err
		}
		top = append(top, p)
	}
	_ = rows.Err()
	return total, top, nil
}

// selfHostPatterns returns the site's own host and the LIKE pattern matching its
// subdomains, for excluding internal navigation from referrer lists.
//
// It exists because the same exclusion was written once, in one of the two
// referrer queries, and the panel called the other one. A predicate duplicated
// across two files is a predicate that will be fixed in one of them.
// The port forms exist because a referrer host is not always bare. A CDN in
// front of the site can serve on one of its alternate HTTP ports, and the
// browser then sends "example.test:2052" as the referrer — which is the site itself
// but matches neither the exact host nor the subdomain pattern, so it climbed
// into the operator's referrer list looking like an external site. Ingest now
// strips the port (see referrerHost), but rows recorded before that still carry
// it, so the read path excludes both spellings and needs no data migration.
func selfHostPatterns() (host, subdomainLike, hostPortLike, subdomainPortLike string) {
	host = strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	return host, "%." + host, host + ":%", "%." + host + ":%"
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
	// RTRIM(…, '/') strips a trailing slash before the join. Slugs never carry
	// one, but recorded paths do: "/post" and "/post/" are the same article and
	// were stored as two different rows, so the trailing-slash spelling matched
	// nothing and its views vanished from trending. Grouping on the trimmed slug
	// merges the two spellings into one total instead of scoring only one of them.
	rows, err := s.readDB().QueryContext(ctx, `
		SELECT a.slug, a.title, COALESCE(a.feature_image,''), SUM(d.views) AS v
		FROM analytics_daily d
		JOIN articles a ON a.slug = RTRIM(SUBSTR(d.path, 2), '/')
		WHERE d.day >= ? AND d.path LIKE '/%'
		  AND RTRIM(SUBSTR(d.path, 2), '/') <> ''
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

// TrendingArticlesByViews returns the most-viewed published, non-page articles
// over the trailing `days` days using the SAME source as the admin Analytics
// "Top pages" panel — the per-pageview event log (analytics_pageviews,
// event_type=1) — so the public "Trending" widget shows exactly the top pages
// the operator sees there, restricted to real posts. Highest pageviews first;
// ties break by recency. Returns an empty (not nil) slice when there is no data
// so the caller can fall back cleanly.
func (s *Store) TrendingArticlesByViews(ctx context.Context, days, limit int) ([]TrendingArticle, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 10
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	// Aggregate pageviews per slug FIRST, then join to articles. This shape lets
	// the hot inner scan be served by idx_apv_trending (event_type, created_at,
	// url_path) and probes the articles slug index once per distinct path instead
	// of once per pageview row. On a large event log under a cold cache this is
	// the difference between an index range scan and a full-table scan with a
	// row fetch per view.
	//
	// The grouping key is the path with its LEADING slash removed and any
	// TRAILING slash stripped, because that is what actually identifies an
	// article. It used to group on the raw url_path and join with SUBSTR(…, 2),
	// which strips only the leading slash — so "/post/" became "post/", matched no
	// slug, and every view recorded with a trailing slash was silently dropped
	// from trending. Normalising inside the GROUP BY (rather than only at the
	// join) matters: the two spellings are one article, and grouping them
	// separately would score the same post twice at half its real traffic each.
	rows, err := s.readDB().QueryContext(ctx, `
		SELECT a.slug, a.title, COALESCE(a.feature_image,''), pv.v
		FROM (
			SELECT RTRIM(SUBSTR(url_path, 2), '/') AS slug, COUNT(1) AS v
			FROM analytics_pageviews
			WHERE event_type = 1 AND created_at >= ? AND url_path LIKE '/%'
			GROUP BY RTRIM(SUBSTR(url_path, 2), '/')
		) pv
		JOIN articles a ON a.slug = pv.slug
		WHERE pv.slug <> '' AND a.status = 'published' AND a.is_page = 0
		ORDER BY pv.v DESC, a.created_at DESC
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

	// TotalReferrals is the hit total across EVERY external referrer in the
	// window, not just the ones in Referrers. It exists so a panel can express a
	// referrer's share of referred traffic honestly: Referrers is a top-N list,
	// and taking each row's share of the rows shown reports the largest entry as
	// a far bigger slice than it is.
	TotalReferrals int64 `json:"total_referrals"`
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

	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(views),0) FROM analytics_daily WHERE day>=?`, from).Scan(&sum.TotalViews); err != nil {
		return nil, err
	}

	if rows, err := s.readDB().QueryContext(ctx,
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

	// The site's own host and its subdomains are excluded here too.
	//
	// They were not, and that is why the operator-facing panel was topped by the
	// operator's own webmail and MCP hosts at 93% of all "referrals". Internal
	// navigation is not a referral, and the classifier already agrees — it counts
	// a same-site referrer as Direct — so the Audience card and the Referrers list
	// were giving opposite answers about the same traffic.
	//
	// This is a SECOND query over a SECOND table. The same defect was fixed in
	// TopReferrers (analytics_pageviews) and left here (analytics_referrers),
	// because fixing the function whose name matched is not the same as fixing the
	// one the panel calls. Both now share selfHostPatterns so they cannot drift
	// apart again.
	site, sub, sitePort, subPort := selfHostPatterns()
	// The population behind the top-N list, under the identical exclusions — a
	// denominator taken from a different filter would be its own quiet lie.
	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(hits),0) FROM analytics_referrers
		 WHERE day>=? AND LOWER(host)<>? AND LOWER(host) NOT LIKE ?
		   AND LOWER(host) NOT LIKE ? AND LOWER(host) NOT LIKE ?`,
		from, site, sub, sitePort, subPort).Scan(&sum.TotalReferrals); err != nil {
		sum.TotalReferrals = 0
	}
	if rows, err := s.readDB().QueryContext(ctx,
		`SELECT host,SUM(hits) h FROM analytics_referrers
		 WHERE day>=? AND LOWER(host)<>? AND LOWER(host) NOT LIKE ?
		   AND LOWER(host) NOT LIKE ? AND LOWER(host) NOT LIKE ?
		 GROUP BY host ORDER BY h DESC LIMIT ?`, from, site, sub, sitePort, subPort, limit); err == nil {
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

	if rows, err := s.readDB().QueryContext(ctx,
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
	// Hostname(), not Host: Host keeps ":port". A CDN serving the site on one of
	// its alternate HTTP ports made the browser send "example.test:2052", which is
	// this site but matched no self-host pattern, so it was reported as an
	// external referrer. A port never distinguishes one site from another here.
	return strings.ToLower(u.Hostname())
}
