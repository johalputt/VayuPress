// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The read is bounded by its window in the index search itself, for the
// install and for one site. An index on event_type alone would also read as
// a search, and would read every view ever recorded.
func TestViewsByPathCountryIsBoundedByItsWindow(t *testing.T) {
	openTrendingDB(t)
	for _, scope := range []string{ScopeAllDomains, "d1"} {
		q, args := viewsByPathCountryQuery(scope, "2026-10-01", 10)
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		bounded := false
		for _, d := range plan {
			if strings.HasPrefix(d, "SEARCH p ") && strings.Contains(d, "created_at>?") {
				bounded = true
			}
		}
		if !bounded {
			t.Errorf("scope %q: the window does not bound the search: %s", scope, strings.Join(plan, " | "))
		}
	}
}

// What it counts: views only, grouped by path, site and the visit's country.
func TestViewsByPathCountryCountsViewsByPathSiteAndCountry(t *testing.T) {
	openTrendingDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbpkg.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO analytics_sessions(id, visitor_id, country, domain_id) VALUES('s1','v1','IN','d1'),('s2','v2','','d1')`)
	for _, r := range []struct {
		id, sess, path string
		et             int
	}{{"a", "s1", "/x", 1}, {"b", "s1", "/x", 1}, {"c", "s2", "/x", 1}, {"d", "s1", "/x", 2}, {"e", "s1", "/y", 1}} {
		exec(`INSERT INTO analytics_pageviews(id, session_id, url_path, event_type, domain_id, created_at) VALUES(?,?,?,?,'d1',CURRENT_TIMESTAMP)`, r.id, r.sess, r.path, r.et)
	}
	got, err := New(dbpkg.DB).ViewsByPathCountryScoped(context.Background(), "d1", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[PathCountryViews]bool{{"/x", "d1", "IN", 2}: true, {"/x", "d1", "", 1}: true, {"/y", "d1", "IN", 1}: true}
	if len(got) != len(want) || got[0] != (PathCountryViews{"/x", "d1", "IN", 2}) {
		t.Fatalf("got %+v; want %v, most viewed first", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected group %+v", g)
		}
	}
}
