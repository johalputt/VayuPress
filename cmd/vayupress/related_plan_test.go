// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Related articles are read by primary key, never by walking every published
// post: the plan that used idx_articles_status made each uncached article on a
// 234k-post install cost 53 ms, and articles queued for render slots behind it.
// Asserted on the plan of a freshly migrated database, which is what the live
// install plans with (it keeps no statistics).
func TestRelatedArticlesAreReadByPrimaryKey(t *testing.T) {
	openMigratedDB(t)
	got := queryPlan(t, relatedByIDSQL(3), "a", "b", "c", "current-slug")
	if !strings.Contains(got, "(id=?)") || strings.Contains(got, "idx_articles_status") {
		t.Errorf("related articles are not read by primary key: %s", got)
	}
}

// A tag page's count never reads an article row: status is stored after
// content, and the row read walked every tagged post's body (49.5 ms for one
// tag on 234k posts). Global and per-domain, since both are served.
func TestATagCountReadsOnlyTheIndex(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []struct {
		name string
		sql  string
		args []any
	}{
		{"global", tagCountSQL, []any{"go"}},
		{"one domain", tagCountSQL + ` AND a.domain_id=?`, []any{"go", "d1"}},
	} {
		got := queryPlan(t, q.sql, q.args...)
		if !strings.Contains(got, "SEARCH a USING COVERING INDEX idx_articles_id_status") {
			t.Errorf("%s: the tag count reads article rows: %s", q.name, got)
		}
	}
}

// queryPlan is EXPLAIN QUERY PLAN's detail lines, joined.
func queryPlan(t *testing.T, sql string, args ...any) string {
	t.Helper()
	rows, err := dbpkg.Reader().Query(`EXPLAIN QUERY PLAN `+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
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
	return strings.Join(plan, " | ")
}
