// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestTrendingUsesCompositeIndex guards the performance fix for the public
// "Trending" widget: with the idx_apv_trending(event_type, created_at, url_path)
// index (migration 057), the hot inner aggregation over analytics_pageviews must
// be served by an index-only (COVERING) scan, never a full-table scan with a
// per-row fetch. On a large event log under a cold cache that is the difference
// between a bounded index range scan and reading every pageview row — the class
// of slow query that contributed to the update-time latency spikes.
func TestTrendingUsesCompositeIndex(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Mirror the production schema for the tables + indexes the query touches
	// (articles.slug is UNIQUE with idx_articles_slug; analytics_pageviews carries
	// idx_apv_trending from migration 057).
	for _, q := range []string{
		`CREATE TABLE articles(id INTEGER PRIMARY KEY, slug TEXT UNIQUE, title TEXT, feature_image TEXT, status TEXT NOT NULL DEFAULT 'published', is_page INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE INDEX idx_articles_slug ON articles(slug);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '', url_path TEXT NOT NULL, event_type INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE INDEX idx_apv_trending ON analytics_pageviews(event_type, created_at, url_path);`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN
		SELECT a.slug, a.title, COALESCE(a.feature_image,''), pv.v
		FROM (SELECT url_path, COUNT(1) AS v FROM analytics_pageviews
		      WHERE event_type = 1 AND created_at >= ? AND url_path LIKE '/%'
		      GROUP BY url_path) pv
		JOIN articles a ON a.slug = SUBSTR(pv.url_path, 2)
		WHERE a.status='published' AND a.is_page=0
		ORDER BY pv.v DESC, a.created_at DESC LIMIT ?`, "2020-01-01", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	p := plan.String()
	if !strings.Contains(p, "idx_apv_trending") {
		t.Errorf("trending inner scan must use idx_apv_trending; plan:\n%s", p)
	}
	if !strings.Contains(p, "COVERING INDEX idx_apv_trending") {
		t.Errorf("trending inner scan must be index-only (COVERING); plan:\n%s", p)
	}
}
