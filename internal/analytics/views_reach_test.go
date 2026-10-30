// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The read is bounded by its window in the index search itself, for the
// install and for one site. An index on event_type alone would also read as
// a search, and would read every view ever recorded.
func TestViewsByPathCountryIsBoundedByItsWindow(t *testing.T) {
	openTrendingDB(t)
	type read struct {
		q    string
		args []any
	}
	var reads []read
	for _, scope := range []string{ScopeAllDomains, "d1"} {
		q, args := viewsByPathCountryQuery(scope, "2026-10-01", 10)
		reads = append(reads, read{q, args})
		for _, byDay := range []bool{false, true} {
			q, args := viewsOverTimeQuery(scope, "2026-10-01 00:00:00", byDay)
			reads = append(reads, read{q, args})
		}
	}
	for _, rd := range reads {
		q, args, scope := rd.q, rd.args, rd.args
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
			t.Errorf("%v: the window does not bound the search: %s\n%s", scope, strings.Join(plan, " | "), q)
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

// Views are counted per hour within a day's window and per day beyond it,
// for the site asked about alone.
func TestViewsOverTimeBucketsBySite(t *testing.T) {
	openTrendingDB(t)
	now := time.Now().UTC()
	for i, v := range []struct {
		site string
		ago  time.Duration
		et   int
	}{{"d1", time.Hour, 1}, {"d1", time.Hour, 1}, {"d1", 3 * time.Hour, 1}, {"d1", time.Hour, 3}, {"d2", time.Hour, 1}, {"d1", 50 * time.Hour, 1}} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at) VALUES(?,?,?,?,?,?)`,
			"o"+strconv.Itoa(i), "s", "/a", v.et, v.site, now.Add(-v.ago).Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dbpkg.DB)
	hourly, err := s.ViewsOverTimeScoped(context.Background(), "d1", 24)
	if err != nil {
		t.Fatal(err)
	}
	if got := hourly[now.Add(-time.Hour).Format("2006-01-02 15:00")]; got != 2 {
		t.Errorf("an hour ago: %d views, want 2 (a search and another site's view are not views of d1): %v", got, hourly)
	}
	total := 0
	for _, n := range hourly {
		total += n
	}
	if total != 3 {
		t.Errorf("the day holds %d views, want 3 (50 hours ago is outside it): %v", total, hourly)
	}
	daily, err := s.ViewsOverTimeScoped(context.Background(), "d1", 24*7)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for k, n := range daily {
		if len(k) != len("2006-01-02") {
			t.Errorf("a week is bucketed by %q, want days", k)
		}
		sum += n
	}
	if sum != 4 {
		t.Errorf("the week holds %d views, want 4", sum)
	}
}

// A site's purge removes that site's rows older than its window from every
// table that holds a visitor, and nothing of another site's or newer.
func TestPurgeSiteRemovesOnlyThatSitesOldRows(t *testing.T) {
	openTrendingDB(t)
	old := time.Now().UTC().AddDate(0, 0, -40)
	recent := time.Now().UTC().AddDate(0, 0, -5)
	f := func(t time.Time) string { return t.Format("2006-01-02 15:04:05") }
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at) VALUES('so','v','s1',?),('sn','v','s1',?),('to','v','s2',?),('po0','v','',?)`, []any{f(old), f(recent), f(old), f(old)}},
		{`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at) VALUES('po','so','/a',1,'s1',?),('pn','sn','/a',1,'s1',?),('qo','to','/a',1,'s2',?)`, []any{f(old), f(recent), f(old)}},
		{`INSERT INTO analytics_event_data(event_id,property_key,property_value,created_at) VALUES('po','k','v',?),('pn','k','v',?),('qo','k','v',?)`, []any{f(old), f(recent), f(old)}},
		{`INSERT INTO analytics_revenue(id,session_id,amount,currency,created_at) VALUES('ro','so',100,'INR',?),('rn','sn',100,'INR',?),('xo','to',100,'INR',?)`, []any{f(old), f(recent), f(old)}},
		{`INSERT INTO analytics_rollup_daily(day,domain_id,uniques_hll) VALUES(?,'s1',x''),(?,'s1',x''),(?,'s2',x'')`, []any{old.Format("2006-01-02"), recent.Format("2006-01-02"), old.Format("2006-01-02")}},
		{`INSERT INTO analytics_heat(domain_id,path,day,width,element,x,y,clicks) VALUES('s1','/a',?,'phone','a',0,0,1),('s1','/a',?,'phone','a',0,0,1),('s2','/a',?,'phone','a',0,0,1)`, []any{old.Format("2006-01-02"), recent.Format("2006-01-02"), old.Format("2006-01-02")}},
	} {
		if _, err := dbpkg.DB.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	n, err := New(dbpkg.DB).PurgeSite(context.Background(), "s1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("removed %d rows, want 6: site one's old view, its property, its visit, its revenue, its sketch and its clicks", n)
	}
	for table, want := range map[string]int{"analytics_sessions": 3, "analytics_pageviews": 2, "analytics_event_data": 2, "analytics_revenue": 2, "analytics_rollup_daily": 2, "analytics_heat": 2} {
		var got int
		_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&got)
		if got != want {
			t.Errorf("%s keeps %d rows, want %d (site one's recent and site two's old)", table, got, want)
		}
	}
	if n, _ := New(dbpkg.DB).PurgeSite(context.Background(), "", 30); n != 0 {
		t.Errorf("a purge naming no site removed %d rows of the primary's: its retention is the install's", n)
	}
}
