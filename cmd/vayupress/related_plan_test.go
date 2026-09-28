// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

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

// Pinned posts are found through the featured index, never by walking every
// published post: that plan read each post's body to test featured (stored
// after content) on every trending request, 4 s each on johal.in.
func TestPinnedPostsAreFoundByTheFeaturedIndex(t *testing.T) {
	openMigratedDB(t)
	got := queryPlan(t, pinnedSQL, 5)
	if !strings.Contains(got, "USING INDEX idx_articles_featured (featured=?)") {
		t.Errorf("pinned posts are not found through idx_articles_featured: %s", got)
	}
}

// The bell's newest posts stop at the edge of their window: the query runs on
// every console page, and without an index bound it read every post body
// whenever fewer than eight were published in the day.
func TestTheBellsRecentPostsStopAtTheWindow(t *testing.T) {
	openMigratedDB(t)
	got := queryPlan(t, recentPostsSQL, "2026-09-26", "2026-09-27 03:00:00")
	if !strings.Contains(got, "USING INDEX idx_articles_created (created_at>?)") {
		t.Errorf("the bell's recent posts are not bounded by idx_articles_created: %s", got)
	}
}

// The floor sorts at or below every spelling of a time inside the window, so
// it can narrow the scan but never drop a post the window holds.
func TestTheRecentPostsFloorNeverExcludesTheWindow(t *testing.T) {
	since := time.Date(2026, 9, 27, 0, 30, 0, 0, time.UTC)
	floor := recentPostsFloor(since)
	for _, stored := range []string{
		"2026-09-27 00:30:00",       // CURRENT_TIMESTAMP
		"2026-09-27T00:30:00Z",      // Go, UTC
		"2026-09-26T19:00:00-05:30", // the same instant with an offset: its date is the day before
	} {
		if stored < floor {
			t.Errorf("a post stored as %q, inside the window, sorts below the floor %q", stored, floor)
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
