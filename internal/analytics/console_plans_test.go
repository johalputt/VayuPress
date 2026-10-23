// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// console_plans_test.go — no read the console makes walks a whole table.
//
// Every report is bounded by a time window, but SQLite chose the plan by the
// GROUP BY: grouping on url_path, referrer or session_id, each of which has an
// index, it read that index end to end, visiting every row ever recorded to
// test its date. On johal.in (532k pageviews, 280k visits) the live panel's
// poll took 8 s at p95, and Home, which asks for the week's bounce rate and
// visit length, took more than 15 s (3cm). Read by the time index, the same
// queries cost what the window holds.
//
// The queries are not listed here: every one the Store issues is recorded on
// its way to the driver, with its arguments, and its plan is read from a
// freshly migrated database. A new report that walks a table fails without
// anyone remembering to add it.

type recordedQuery struct {
	sql  string
	args []driver.NamedValue
}

type recordingDriver struct {
	inner driver.Driver
	mu    sync.Mutex
	seen  []recordedQuery
}

func (d *recordingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: c, d: d}, nil
}

type recordingConn struct {
	driver.Conn
	d *recordingDriver
}

func (c *recordingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	c.d.seen = append(c.d.seen, recordedQuery{query, append([]driver.NamedValue(nil), args...)})
	c.d.mu.Unlock()
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

var (
	recordingOnce sync.Once
	recorder      = &recordingDriver{}
)

// growing are the tables that gain rows with every visit. A walk of one of
// them costs more each day; the definitions (goals, funnels) stay a handful
// of rows and are read whole on purpose.
var growing = map[string]bool{
	"analytics_pageviews": true, "analytics_sessions": true, "analytics_daily": true,
	"analytics_event_data": true, "analytics_revenue": true, "analytics_rollup_daily": true,
}

// derivedRe names the plan's own subqueries; scanning one of those is reading
// a result already bounded, not a table. fromRe maps an alias to its table.
var (
	derivedRe = regexp.MustCompile(`^(?:CO-ROUTINE|MATERIALIZE) (\S+)`)
	scanRe    = regexp.MustCompile(`^SCAN (\S+)`)
	fromRe    = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+(\w+)(?:\s+(?:AS\s+)?(\w+))?`)
	sqlWords  = map[string]bool{"WHERE": true, "JOIN": true, "LEFT": true, "INNER": true, "ON": true, "GROUP": true, "ORDER": true, "LIMIT": true, "USING": true, "CROSS": true}
)

func TestNoConsoleReadWalksAWholeTable(t *testing.T) {
	openTrendingDB(t)
	recordingOnce.Do(func() {
		recorder.inner = dbpkg.DB.Driver()
		sql.Register("sqlite3-recording", recorder)
	})
	recorder.mu.Lock()
	recorder.seen = nil
	recorder.mu.Unlock()
	rdb, err := sql.Open("sqlite3-recording", config.Cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()

	s := New(rdb)
	ctx := context.Background()
	fid, err := s.CreateFunnel(ctx, "f", []FunnelStep{{Name: "a", URLPath: "/a"}, {Name: "b", URLPath: "/b"}}, 3600)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := s.CreateGoal(ctx, "g", "path", "/a")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	for _, days := range []int{1, 7, 30} {
		_, _, _ = s.ViewsForScope(ctx, "", days, 10)
		_, _ = s.TrendingArticles(ctx, days, 10)
		_, _ = s.TrendingArticlesByViews(ctx, days, 10)
		_, _ = s.ViewsBySlug(ctx, days, []string{"a", "b"})
		_, _ = s.Since(ctx, days, 10)
		_, _ = s.Attribution(ctx, days, gid)
		_, _ = s.OverviewSince(ctx, days)
		_, _ = s.PageviewSeries(ctx, days)
		_, _ = s.TopPages(ctx, days, 10)
		_, _ = s.TopReferrers(ctx, days, 10)
		_, _ = s.Browsers(ctx, days)
		_, _ = s.Devices(ctx, days)
		_, _ = s.OperatingSystems(ctx, days)
		_, _ = s.Countries(ctx, days)
		_, _ = s.Regions(ctx, days)
		_, _ = s.Cities(ctx, days)
		_, _ = s.Channels(ctx, days)
		_, _ = s.UTMStats(ctx, days)
		_, _ = s.CustomEvents(ctx, days)
		_, _ = s.RecentSessions(ctx, days, 10)
		_, _ = s.RevenueStats(ctx, days)
		_, _ = s.OverviewSinceScoped(ctx, "d1", days)
		_, _ = s.TopPagesScoped(ctx, "d1", days, 10)
		_, _ = s.LandingPagesScoped(ctx, "d1", days, 10)
		_, _ = s.TopReferrersScoped(ctx, "d1", days, 10)
		_, _ = s.PageviewSeriesScoped(ctx, "d1", days)
		_, _ = s.ChannelsScoped(ctx, "d1", days)
		_, _ = s.UTMStatsScoped(ctx, "d1", days)
		_, _ = s.CustomEventsScoped(ctx, "d1", days)
		for _, col := range []string{"browser", "device", "os", "country", "region", "city"} {
			_, _ = s.AudienceScoped(ctx, "d1", col, days)
		}
		_, _ = s.GoalResults(ctx, days)
		_, _ = s.GoalResultsScoped(ctx, "d1", days)
		_, _ = s.SearchesScoped(ctx, "d1", days, 20)
		_, _ = s.SearchesScoped(ctx, ScopeAllDomains, days, 20)
		_, _ = s.ViewsByPathCountryScoped(ctx, "d1", days, 100)
		_, _ = s.ViewsByPathCountryScoped(ctx, ScopeAllDomains, days, 100)
		_, _ = s.ViewsOverTimeScoped(ctx, "d1", days*24)
		_, _ = s.PathFlows(ctx, days, 10)
	}
	_, _ = s.OverviewBetween(ctx, weekAgo, today)
	_, _ = s.OverviewBetweenScoped(ctx, "d1", weekAgo, today)
	_, _ = s.Realtime(ctx)
	_, _ = s.RealtimeScoped(ctx, "d1")
	// Not a console read, but it runs daily over the growing tables.
	if err := s.BuildDailyRollup(ctx, time.Now()); err != nil {
		t.Fatalf("the daily rollup: %v", err)
	}
	_, _, _ = s.GetFunnel(ctx, fid)
	_, _ = s.ListFunnels(ctx)
	_, _ = s.ListGoals(ctx)
	_, _, _ = s.GetFunnelScoped(ctx, "d1", fid)
	_, _ = s.ListFunnelsScoped(ctx, "d1")
	_, _ = s.ListGoalsScoped(ctx, "d1")

	recorder.mu.Lock()
	seen := recorder.seen
	recorder.mu.Unlock()
	if len(seen) < 40 {
		t.Fatalf("only %d queries were recorded: the recorder is not seeing the Store's reads", len(seen))
	}
	checked := map[string]bool{}
	for _, q := range seen {
		if checked[q.sql] {
			continue
		}
		checked[q.sql] = true
		args := make([]any, len(q.args))
		for i, a := range q.args {
			args[i] = a.Value
		}
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+q.sql, args...)
		if err != nil {
			// The Store discards most read errors, so a query the schema
			// cannot run reads as an empty report rather than a failure.
			t.Errorf("the schema cannot run %s: %v", oneLine(q.sql), err)
			continue
		}
		var plan []string
		derived := map[string]bool{}
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
			if m := derivedRe.FindStringSubmatch(detail); m != nil {
				derived[m[1]] = true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		tables := map[string]string{}
		for _, m := range fromRe.FindAllStringSubmatch(q.sql, -1) {
			tables[m[1]] = m[1]
			if m[2] != "" && !sqlWords[strings.ToUpper(m[2])] {
				tables[m[2]] = m[1]
			}
		}
		for _, d := range plan {
			if m := scanRe.FindStringSubmatch(d); m != nil && !derived[m[1]] && growing[tables[m[1]]] {
				t.Errorf("walks %s end to end (%s):\n  %s\n  plan: %s", m[1], d, oneLine(q.sql), strings.Join(plan, " | "))
				break
			}
		}
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
