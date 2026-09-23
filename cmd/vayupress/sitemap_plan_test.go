// SPDX-License-Identifier: Apache-2.0

package main

// sitemap_plan_test.go — the sitemap must never read a post body.
//
// Every column the sitemap needs is stored after content in each row. When a
// plan leaves the covering index, SQLite walks every post's overflow pages to
// reach them: measured on a 234,615-post copy of the live install, one rebuild
// read 5,255 MB, and a rebuild followed every post write. The whole site stalled
// and the proxy returned 502 while it ran. With the indexes of migration 094 the
// same rebuild reads 32 MB.
//
// Asserted on the query plan of a freshly migrated database, for the global
// sitemap and for a hosted domain's, so re-wrapping a column in COALESCE or
// dropping an index fails here rather than on the live install.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// openMigratedDB opens a real, freshly migrated database file: the read pool
// these queries use exists only for a file database, and the migrations must
// run exactly as they do at boot.
func openMigratedDB(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "vp-migrated")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) // registered first, so it runs last
	t.Setenv("DB_PATH", filepath.Join(dir, "test.db"))
	t.Setenv("API_KEY", "test-key")
	t.Setenv("DOMAIN", "example.com")
	t.Setenv("CACHE_DIR", dir)
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
	})
}

func TestTheSitemapReadsOnlyItsCoveringIndex(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []struct{ name, sql string }{
		{"posts, global", sitemapPostsSQL + ` ORDER BY updated_at DESC`},
		{"posts, one domain", sitemapPostsSQL + ` AND domain_id=?` + ` ORDER BY updated_at DESC`},
		{"tags, global", sitemapTagsSQL},
		{"tags, one domain", sitemapTagsSQL + ` AND domain_id=?`},
	} {
		args := []any{}
		if strings.Contains(q.sql, "domain_id=?") {
			args = append(args, "d1")
		}
		rows, err := dbpkg.Reader().Query(`EXPLAIN QUERY PLAN `+q.sql, args...)
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		p := strings.Join(plan, " | ")
		if !strings.Contains(p, "USING COVERING INDEX idx_articles_sitemap") {
			t.Errorf("%s: plan %q reads the table — every post body is read on every rebuild", q.name, p)
		}
		if strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("%s: plan %q sorts in a temporary tree instead of reading the index in order", q.name, p)
		}
	}
}
