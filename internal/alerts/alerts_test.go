// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func openDB(t *testing.T) *Store {
	t.Helper()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "alerts.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
		config.Cfg.DBPath = prev
	})
	return New(dbpkg.DB, nil)
}

func exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// ago is a created_at the given minutes before now, as the tables store it.
func ago(min int) string { return sqliteTime(time.Now().Add(-time.Duration(min) * time.Minute)) }

func draft() Draft {
	return Draft{Name: "Traffic spike", Metric: "views", Op: "above", Threshold: "2", Window: "15", Cooldown: "60", Severity: "warn"}
}

// Each refusal is the one its field causes: one seed per rule, each from a
// draft that is otherwise valid.
func TestADraftIsRefusedForTheFieldThatIsWrong(t *testing.T) {
	if _, err := draft().Validate("s1"); err != nil {
		t.Fatalf("the valid draft is refused: %v", err)
	}
	for _, c := range []struct {
		edit func(*Draft)
		want string
	}{
		{func(d *Draft) { d.Name = "  " }, "name"},
		{func(d *Draft) { d.Name = strings.Repeat("x", 81) }, "name"},
		{func(d *Draft) { d.Name = "spike\r\nBcc: x@y.z" }, "one line"},
		{func(d *Draft) { d.Metric = "bounce" }, "what the alert counts"},
		{func(d *Draft) { d.Op = "equals" }, "above or below"},
		{func(d *Draft) { d.Threshold = "-1" }, "whole number"},
		{func(d *Draft) { d.Threshold = "1.5" }, "whole number"},
		{func(d *Draft) { d.Op, d.Threshold = "below", "0" }, "never below 0"},
		{func(d *Draft) { d.Window = "7" }, "window"},
		{func(d *Draft) { d.Cooldown = "2" }, "cooldown"},
		{func(d *Draft) { d.Severity = "critical" }, "severity"},
		{func(d *Draft) { d.MailTo = "ops@example.com, cc@example.com" }, "one address"},
		{func(d *Draft) { d.MailTo = "Ops <ops@example.com>" }, "one address"},
		{func(d *Draft) { d.MailTo = "ops@example.com\r\nBcc: x@y.z" }, "one address"},
	} {
		d := draft()
		c.edit(&d)
		_, err := d.Validate("s1")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: refused with %v, want the reason to name %q", d, err, c.want)
		}
	}
	d := draft()
	d.MailTo = " ops@example.com "
	if a, err := d.Validate("s1"); err != nil || a.MailTo != "ops@example.com" {
		t.Errorf("a plain address is refused or kept unclean: %q, %v", a.MailTo, err)
	}
}

// An alert fires once past its threshold, holds through its cooldown, and
// counts its own site's traffic alone.
func TestAnAlertFiresOnceAndCountsItsSiteAlone(t *testing.T) {
	s := openDB(t)
	ctx := context.Background()
	a, _ := draft().Validate("s1")
	id, err := s.Create(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	// Two views on site one inside the window, one outside it, three on
	// site two: site one's count is 2, not above 2.
	for i, row := range []struct {
		site string
		at   int
	}{{"s1", 1}, {"s1", 10}, {"s1", 20}, {"s2", 1}, {"s2", 1}, {"s2", 1}} {
		exec(t, `INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES(?,?,?,1,?,?,?)`,
			"p"+string(rune('a'+i)), "x", "/", row.site, ago(row.at), "v")
	}
	live := func(string) bool { return true }
	mails := 0
	send := func(context.Context, Alert, int) string { mails++; return "sent" }
	fired, err := s.Check(ctx, live, send)
	if err != nil || len(fired) != 0 {
		t.Fatalf("an alert at its threshold fired: %+v, %v", fired, err)
	}
	list, _ := s.List(ctx, "s1")
	if len(list) != 1 || !list[0].Checked || list[0].LastValue != 2 {
		t.Fatalf("the check was not recorded as 2: %+v", list)
	}
	exec(t, `INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('p9','x','/',1,'s1',?,'v')`, ago(2))
	if fired, _ = s.Check(ctx, live, send); len(fired) != 1 || fired[0].Value != 3 || fired[0].AlertID != id {
		t.Fatalf("an alert past its threshold did not fire once with 3: %+v", fired)
	}
	if fired, _ = s.Check(ctx, live, send); len(fired) != 0 {
		t.Fatalf("the alert fired again inside its cooldown")
	}
	// Past the cooldown, still above: it fires again.
	s.now = func() time.Time { return time.Now().Add(61 * time.Minute) }
	exec(t, `UPDATE analytics_pageviews SET created_at=? WHERE domain_id='s1'`, sqliteTime(time.Now().Add(60*time.Minute)))
	if fired, _ = s.Check(ctx, live, send); len(fired) != 1 {
		t.Fatalf("the alert did not fire again after its cooldown")
	}
	if mails != 0 {
		t.Errorf("an alert with no address was mailed")
	}
	h, _ := s.History(ctx, "s1", 10)
	if len(h) != 2 || h[0].Condition != "Page views above 2 in 15 minutes" || h[0].Noun != "page views" {
		t.Errorf("the history does not read as the rule: %+v", h)
	}
	if other, _ := s.History(ctx, "s2", 10); len(other) != 0 {
		t.Errorf("site two's history has site one's firings")
	}
}

// A below alert fires when the count drops under it; an alert for a site
// that is gone counts nothing; a mail's outcome is kept with the firing.
func TestBelowRemovedSitesAndMail(t *testing.T) {
	s := openDB(t)
	ctx := context.Background()
	d := draft()
	d.Op, d.Threshold, d.MailTo, d.Severity = "below", "1", "ops@example.com", "danger"
	a, _ := d.Validate("s1")
	if _, err := s.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	gone, _ := d.Validate("gone")
	if _, err := s.Create(ctx, gone); err != nil {
		t.Fatal(err)
	}
	// Site three has one view: at its threshold, not below it.
	at, _ := d.Validate("s3")
	if _, err := s.Create(ctx, at); err != nil {
		t.Fatal(err)
	}
	exec(t, `INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('p1','x','/',1,'s3',?,'v')`, ago(1))
	live := func(site string) bool { return site != "gone" }
	fired, err := s.Check(ctx, live, func(_ context.Context, a Alert, v int) string { return "sent to " + a.MailTo })
	if err != nil || len(fired) != 1 || fired[0].Site != "s1" || fired[0].Value != 0 {
		t.Fatalf("a site with no views did not fire its below alert alone: %+v, %v", fired, err)
	}
	h, _ := s.History(ctx, "s1", 10)
	if len(h) != 1 || h[0].Mailed != "sent to ops@example.com" || h[0].Severity != "danger" {
		t.Errorf("the firing does not keep what its mail did: %+v", h)
	}
	if g, _ := s.List(ctx, "gone"); len(g) != 1 || g[0].Checked {
		t.Errorf("a removed site's alert was counted")
	}
	u, _ := s.UnseenBySite(ctx)
	if len(u) != 1 || u[0].Site != "s1" || u[0].Count != 1 || u[0].Severity != "danger" {
		t.Errorf("the unseen firings are %+v", u)
	}
	if n, _ := s.MarkSeen(ctx, "s2"); n != 0 {
		t.Errorf("marking site two seen marked %d of site one's", n)
	}
	if n, _ := s.MarkSeen(ctx, "s1"); n != 1 {
		t.Errorf("marking site one seen marked %d", n)
	}
	if u, _ = s.UnseenBySite(ctx); len(u) != 0 {
		t.Errorf("seen firings are still unseen: %+v", u)
	}
}

// Each figure counts what its report counts: refusals are blocks and limits,
// challenges are those issued, empty searches those that found nothing, and
// visitors are people, not views.
func TestEachFigureCountsWhatItsReportCounts(t *testing.T) {
	s := openDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO shield_events(action,domain_id,created_at) VALUES('block','s1',?),('rate_limit','s1',?),('tarpit','s1',?),('block','s2',?)`,
		`INSERT INTO shield_events(action,outcome,domain_id,created_at) VALUES('challenge','issued','s1',?),('challenge','solved','s1',?),('challenge','issued','s1',?),('challenge','issued','s2',?)`,
	} {
		exec(t, q, ago(1), ago(1), ago(1), ago(1))
	}
	exec(t, `INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
		('a','x','/',1,'','s1',?,'r1'),('b','x','/',1,'','s1',?,'r1'),('c','y','/',2,'click','s1',?,'r2'),
		('d','z','/search',3,'q1','s1',?,'r3'),('e','z','/search',3,'q2','s1',?,'r3')`, ago(1), ago(1), ago(1), ago(1), ago(1))
	exec(t, `INSERT INTO analytics_event_data(event_id,property_key,property_value) VALUES('d','results','0'),('e','results','4')`)
	for m, want := range map[Metric]int{Refused: 2, Challenged: 2, Views: 2, Visitors: 3, EmptySearches: 1} {
		n, _, err := s.Count(ctx, Alert{Site: "s1", Metric: m, WindowMin: 15}, time.Now())
		if err != nil || n != want {
			t.Errorf("%s on site one counts %d (%v), want %d", m, n, err, want)
		}
	}
	if n, _, _ := s.Count(ctx, Alert{Site: Install, Metric: Refused, WindowMin: 15}, time.Now()); n != 3 {
		t.Errorf("the install's refusals count %d, want every site's 3", n)
	}
}

// No alert's count walks a table: each is a range of an index, so a check
// costs what its window holds.
func TestNoAlertCountWalksATable(t *testing.T) {
	openDB(t)
	queries := []string{`SELECT domain_id, COUNT(1), MAX(CASE severity WHEN 'danger' THEN 2 WHEN 'warn' THEN 1 ELSE 0 END) FROM alert_events WHERE seen=0 GROUP BY domain_id ORDER BY domain_id`}
	for _, m := range metrics {
		queries = append(queries, m.all, m.site)
	}
	for _, q := range queries {
		args := []any{"2026-01-01 00:00:00"}
		if strings.Contains(q, "domain_id=?") {
			args = []any{"s1", "2026-01-01 00:00:00"}
		} else if !strings.Contains(q, "?") {
			args = nil
		}
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
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
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN") {
				t.Errorf("%s\nwalks a table: %s", q, strings.Join(plan, " | "))
			}
		}
	}
}

// A site holds at most MaxPerScope alerts; another site's are its own.
func TestASiteHoldsAtMostItsShareOfAlerts(t *testing.T) {
	s := openDB(t)
	ctx := context.Background()
	a, _ := draft().Validate("s1")
	for i := 0; i < MaxPerScope; i++ {
		if _, err := s.Create(ctx, a); err != nil {
			t.Fatalf("alert %d: %v", i+1, err)
		}
	}
	if _, err := s.Create(ctx, a); err != ErrTooMany {
		t.Errorf("one past the limit answered %v, want ErrTooMany", err)
	}
	b, _ := draft().Validate("s2")
	if _, err := s.Create(ctx, b); err != nil {
		t.Errorf("another site's first alert is refused: %v", err)
	}
}

// The server's figures are its minute rows for the window: errors and
// missing pages summed, the average server time over the pages, and an
// average over no pages is not measured, so a "below" alert on it is not
// fired by an empty window.
func TestTheServersFiguresAreItsMinutes(t *testing.T) {
	s := openDB(t)
	ctx := context.Background()
	min := func(ago int) string {
		return time.Now().UTC().Add(-time.Duration(ago) * time.Minute).Truncate(time.Minute).Format("2006-01-02 15:04:05")
	}
	exec(t, `INSERT INTO site_http_minutes(domain_id,minute,requests,s404,s5xx,pages,page_ms) VALUES
		('s1',?,10,2,1,4,400),('s1',?,5,1,2,2,600),('s1',?,9,9,9,9,9000),('s2',?,3,0,5,1,50)`, min(2), min(5), min(40), min(2))
	for m, want := range map[Metric]int{ServerErrors: 3, NotFound: 3, ServerTime: 166} {
		n, measured, err := s.Count(ctx, Alert{Site: "s1", Metric: m, WindowMin: 15}, time.Now())
		if err != nil || !measured || n != want {
			t.Errorf("%s on site one = %d (measured %v, %v), want %d", m, n, measured, err, want)
		}
	}
	if n, _, _ := s.Count(ctx, Alert{Site: Install, Metric: ServerErrors, WindowMin: 15}, time.Now()); n != 8 {
		t.Errorf("the install's server errors = %d, want every site's 8", n)
	}
	if _, measured, _ := s.Count(ctx, Alert{Site: "quiet", Metric: ServerTime, WindowMin: 15}, time.Now()); measured {
		t.Errorf("an average over no pages is measured")
	}
	d := Draft{Name: "Slow", Metric: "server_time", Op: "below", Threshold: "50", Window: "15", Cooldown: "60", Severity: "warn"}
	a, err := d.Validate("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	fired, err := s.Check(ctx, func(string) bool { return true }, nil)
	if err != nil || len(fired) != 0 {
		t.Errorf("an alert on an empty window fired: %+v, %v", fired, err)
	}
	if l, _ := s.List(ctx, "quiet"); len(l) != 1 || l[0].Checked || l[0].CheckedAt.IsZero() {
		t.Errorf("the empty window is not recorded as checked with nothing to count: %+v", l)
	}
}
