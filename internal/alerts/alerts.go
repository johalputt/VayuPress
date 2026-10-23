// SPDX-License-Identifier: Apache-2.0

// Package alerts watches one figure of a site's traffic, or of what its shield
// refused, and records each time it crosses the operator's threshold.
//
// An alert counts its figure over the last window of minutes, once a minute,
// and fires when the count is above (or below) its threshold and its cooldown
// has passed since it last fired. Each figure is one count over an index range
// of the table that holds it, so a check costs what the window holds, never
// what the table does. The figures are counted exactly as the reports count
// them; docs/ANALYTICS.md defines each.
package alerts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Install is the scope of an alert on every site of the install together.
const Install = "*"

// Metric names the figure an alert counts.
type Metric string

// The figures an alert can count.
const (
	Views         Metric = "views"
	Visitors      Metric = "visitors"
	EmptySearches Metric = "empty_searches"
	Refused       Metric = "refused"
	Challenged    Metric = "challenged"
)

// metric is how one figure is counted: its query over [since, now) for every
// site and for one, and how it reads in a sentence.
type metric struct {
	label, noun string
	all, site   string
}

// metrics are the figures, in the order the form offers them. Each query's
// plan is held to an index by alerts_plan_test.go.
var metrics = []struct {
	id Metric
	metric
}{
	{Views, metric{"Page views", "page views",
		`SELECT COUNT(1) FROM analytics_pageviews WHERE event_type=1 AND created_at>=?`,
		`SELECT COUNT(1) FROM analytics_pageviews WHERE domain_id=? AND event_type=1 AND created_at>=?`}},
	// A visitor is counted as Realtime counts its readers: anyone with any
	// event in the window, once however many tabs or visits.
	{Visitors, metric{"Visitors", "visitors",
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE created_at>=?`,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE domain_id=? AND created_at>=?`}},
	{EmptySearches, metric{"Searches that found nothing", "searches that found nothing",
		`SELECT COUNT(1) FROM analytics_pageviews p JOIN analytics_event_data d ON d.event_id=p.id AND d.property_key='results' AND d.property_value='0' WHERE p.event_type=3 AND p.created_at>=?`,
		`SELECT COUNT(1) FROM analytics_pageviews p JOIN analytics_event_data d ON d.event_id=p.id AND d.property_key='results' AND d.property_value='0' WHERE p.domain_id=? AND p.event_type=3 AND p.created_at>=?`}},
	// A refusal is what the Security page counts as one: a block or an
	// operator limit's 429.
	{Refused, metric{"Requests the shield refused", "requests refused",
		`SELECT COUNT(1) FROM shield_events WHERE created_at>=? AND action IN ('block','rate_limit')`,
		`SELECT COUNT(1) FROM shield_events WHERE domain_id=? AND created_at>=? AND action IN ('block','rate_limit')`}},
	{Challenged, metric{"Challenges issued", "challenges issued",
		`SELECT COUNT(1) FROM shield_events WHERE created_at>=? AND outcome='issued'`,
		`SELECT COUNT(1) FROM shield_events WHERE domain_id=? AND created_at>=? AND outcome='issued'`}},
}

func lookup(m Metric) (metric, bool) {
	for _, d := range metrics {
		if d.id == m {
			return d.metric, true
		}
	}
	return metric{}, false
}

// Choice is one option of a field on the form.
type Choice struct {
	Value, Label string
}

// MetricChoices are the figures, as the form offers them.
func MetricChoices() []Choice {
	out := make([]Choice, 0, len(metrics))
	for _, d := range metrics {
		out = append(out, Choice{string(d.id), d.label})
	}
	return out
}

// Windows and Cooldowns are the minutes an alert may count over and wait
// between firings. A fixed list keeps every check's cost known: the longest
// window is a day, the range the reports' own hourly figures cover.
var (
	Windows   = []int{5, 15, 30, 60, 180, 360, 720, 1440}
	Cooldowns = []int{5, 15, 30, 60, 180, 360, 720, 1440}
)

// MaxPerScope bounds the alerts one site, or the install, may hold: each is
// a count once a minute.
const MaxPerScope = 50

// Alert is one rule, as stored.
type Alert struct {
	ID, Site, Name string
	Metric         Metric
	Below          bool // fires when the count is below the threshold, else above
	Threshold      int
	WindowMin      int
	CooldownMin    int
	Severity       string // info, warn or danger
	MailTo         string
	CheckedAt      time.Time
	LastValue      int
	Checked        bool // LastValue was counted at CheckedAt
	LastFiredAt    time.Time
}

// Condition is the rule as a sentence: "Page views above 500 in 15 minutes".
func (a Alert) Condition() string {
	m, _ := lookup(a.Metric)
	op := "above"
	if a.Below {
		op = "below"
	}
	return m.label + " " + op + " " + strconv.Itoa(a.Threshold) + " in " + Minutes(a.WindowMin)
}

// Noun is how the alert's figure reads after a number.
func (a Alert) Noun() string {
	m, _ := lookup(a.Metric)
	return m.noun
}

// Minutes is n minutes as the page says it: "15 minutes", "1 hour", "1 day".
func Minutes(n int) string {
	switch {
	case n%1440 == 0:
		return plural(n/1440, "day")
	case n%60 == 0:
		return plural(n/60, "hour")
	}
	return plural(n, "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// crossed reports whether value is on the firing side of the threshold.
func (a Alert) crossed(value int) bool {
	if a.Below {
		return value < a.Threshold
	}
	return value > a.Threshold
}

// Draft is an alert as the form posts it.
type Draft struct {
	Name      string `json:"name"`
	Metric    string `json:"metric"`
	Op        string `json:"op"`
	Threshold string `json:"threshold"`
	Window    string `json:"window"`
	Cooldown  string `json:"cooldown"`
	Severity  string `json:"severity"`
	MailTo    string `json:"mail_to"`
}

// Validate is the alert the draft describes for site, or why it cannot be
// saved, in words for the operator.
func (d Draft) Validate(site string) (Alert, error) {
	a := Alert{Site: site, Name: strings.TrimSpace(d.Name), Metric: Metric(d.Metric), Severity: d.Severity}
	if a.Name == "" || len([]rune(a.Name)) > 80 || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 {
		// A name is the subject of the alert's mail: a line break in it
		// would be a header of its own.
		return a, errors.New("give the alert a name of up to 80 characters on one line")
	}
	if _, ok := lookup(a.Metric); !ok {
		return a, errors.New("choose what the alert counts")
	}
	switch d.Op {
	case "above":
	case "below":
		a.Below = true
	default:
		return a, errors.New("choose whether it fires above or below the threshold")
	}
	n, err := strconv.Atoi(strings.TrimSpace(d.Threshold))
	if err != nil || n < 0 || n > 1_000_000_000 {
		return a, errors.New("the threshold is a whole number, 0 or more")
	}
	if a.Below && n == 0 {
		return a, errors.New("a count is never below 0, so this alert could never fire")
	}
	a.Threshold = n
	if a.WindowMin, err = oneOf(d.Window, Windows); err != nil {
		return a, errors.New("choose a window from the list")
	}
	if a.CooldownMin, err = oneOf(d.Cooldown, Cooldowns); err != nil {
		return a, errors.New("choose a cooldown from the list")
	}
	switch a.Severity {
	case "info", "warn", "danger":
	default:
		return a, errors.New("choose a severity")
	}
	if to := strings.TrimSpace(d.MailTo); to != "" {
		addr, err := mail.ParseAddress(to)
		if err != nil || addr.Address != to || len(to) > 254 {
			return a, errors.New("mail goes to one address, written as name@example.com")
		}
		a.MailTo = to
	}
	return a, nil
}

func oneOf(s string, allowed []int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	for _, v := range allowed {
		if v == n {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%d is not offered", n)
}

// Store keeps the alerts and their firings.
type Store struct {
	db, rdb *sql.DB
	now     func() time.Time
}

// New is a store writing to db and counting from rdb (the read pool), or
// from db when rdb is nil.
func New(db, rdb *sql.DB) *Store {
	if rdb == nil {
		rdb = db
	}
	return &Store{db: db, rdb: rdb, now: time.Now}
}

// ErrTooMany is the refusal of an alert past MaxPerScope.
var ErrTooMany = fmt.Errorf("a site holds at most %d alerts; delete one first", MaxPerScope)

// Create stores a, already validated, and returns its id.
func (s *Store) Create(ctx context.Context, a Alert) (string, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM analytics_alerts WHERE domain_id=?`, a.Site).Scan(&n); err != nil {
		return "", err
	}
	if n >= MaxPerScope {
		return "", ErrTooMany
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	op := "above"
	if a.Below {
		op = "below"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO analytics_alerts(id,domain_id,name,metric,op,threshold,window_min,cooldown_min,severity,mail_to) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, a.Site, a.Name, string(a.Metric), op, a.Threshold, a.WindowMin, a.CooldownMin, a.Severity, a.MailTo)
	return id, err
}

// Delete removes site's alert id, and reports whether there was one. Its
// firings stay in the history, which names what it was.
func (s *Store) Delete(ctx context.Context, site, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM analytics_alerts WHERE id=? AND domain_id=?`, id, site)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

const alertCols = `id,domain_id,name,metric,op,threshold,window_min,cooldown_min,severity,mail_to,checked_at,last_value,last_fired_at`

func scanAlert(sc interface{ Scan(...any) error }) (Alert, error) {
	var a Alert
	var op string
	var checked, fired sql.NullTime
	var last sql.NullInt64
	err := sc.Scan(&a.ID, &a.Site, &a.Name, &a.Metric, &op, &a.Threshold, &a.WindowMin, &a.CooldownMin, &a.Severity, &a.MailTo, &checked, &last, &fired)
	a.Below = op == "below"
	a.CheckedAt, a.LastFiredAt = checked.Time, fired.Time
	a.LastValue, a.Checked = int(last.Int64), checked.Valid && last.Valid
	return a, err
}

// List is site's alerts, oldest first.
func (s *Store) List(ctx context.Context, site string) ([]Alert, error) {
	return s.query(ctx, `SELECT `+alertCols+` FROM analytics_alerts WHERE domain_id=? ORDER BY created_at, id`, site)
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Count is the alert's figure over the window ending at now.
func (s *Store) Count(ctx context.Context, a Alert, now time.Time) (int, error) {
	m, ok := lookup(a.Metric)
	if !ok {
		return 0, fmt.Errorf("alerts: unknown figure %q", a.Metric)
	}
	since := sqliteTime(now.Add(-time.Duration(a.WindowMin) * time.Minute))
	var n int
	var err error
	if a.Site == Install {
		err = s.rdb.QueryRowContext(ctx, m.all, since).Scan(&n)
	} else {
		err = s.rdb.QueryRowContext(ctx, m.site, a.Site, since).Scan(&n)
	}
	return n, err
}

// sqliteTime is t as SQLite's CURRENT_TIMESTAMP writes it, the form every
// created_at the figures are counted from is in: a bound time.Time would be
// stored with its zone and compare as a different string.
func sqliteTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// Firing is one time an alert fired.
type Firing struct {
	ID        int64
	AlertID   string
	Site      string
	At        time.Time
	Name      string
	Condition string // the rule as it read when it fired
	Value     int
	Noun      string
	Severity  string
	Mailed    string // what became of its mail: "" when none was asked
	Seen      bool
}

// Mailer delivers a firing's mail and says what became of it, in words.
type Mailer func(ctx context.Context, a Alert, value int) string

// Check counts every enabled alert whose site live reports existing, records
// what it read, and records a firing for each that crossed its threshold
// outside its cooldown, mailing it through send. It returns the firings.
func (s *Store) Check(ctx context.Context, live func(site string) bool, send Mailer) ([]Firing, error) {
	all, err := s.query(ctx, `SELECT `+alertCols+` FROM analytics_alerts WHERE enabled=1 ORDER BY domain_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Truncate(time.Second)
	at := sqliteTime(now)
	var fired []Firing
	for _, a := range all {
		if a.Site != Install && !live(a.Site) {
			continue // the site was removed; its alerts count nothing
		}
		value, err := s.Count(ctx, a, now)
		if err != nil {
			return fired, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE analytics_alerts SET checked_at=?, last_value=? WHERE id=?`, at, value, a.ID); err != nil {
			return fired, err
		}
		if !a.crossed(value) || (!a.LastFiredAt.IsZero() && now.Sub(a.LastFiredAt) < time.Duration(a.CooldownMin)*time.Minute) {
			continue
		}
		// The firing is recorded before its mail goes, so a slow or failing
		// mail server can never let the alert fire twice in one cooldown.
		if _, err := s.db.ExecContext(ctx, `UPDATE analytics_alerts SET last_fired_at=? WHERE id=?`, at, a.ID); err != nil {
			return fired, err
		}
		f := Firing{AlertID: a.ID, Site: a.Site, At: now, Name: a.Name, Condition: a.Condition(), Value: value, Noun: a.Noun(), Severity: a.Severity}
		op := "above"
		if a.Below {
			op = "below"
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO alert_events(alert_id,domain_id,fired_at,name,metric,op,threshold,window_min,value,severity) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.Site, at, a.Name, string(a.Metric), op, a.Threshold, a.WindowMin, value, a.Severity)
		if err != nil {
			return fired, err
		}
		f.ID, _ = res.LastInsertId()
		if a.MailTo != "" && send != nil {
			f.Mailed = send(ctx, a, value)
			if _, err := s.db.ExecContext(ctx, `UPDATE alert_events SET mailed=? WHERE id=?`, f.Mailed, f.ID); err != nil {
				return fired, err
			}
		}
		fired = append(fired, f)
	}
	return fired, nil
}

// History is site's most recent firings, newest first.
func (s *Store) History(ctx context.Context, site string, limit int) ([]Firing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,alert_id,domain_id,fired_at,name,metric,op,threshold,window_min,value,severity,mailed,seen
		FROM alert_events WHERE domain_id=? ORDER BY fired_at DESC, id DESC LIMIT ?`, site, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Firing
	for rows.Next() {
		var f Firing
		var a Alert
		var op string
		if err := rows.Scan(&f.ID, &f.AlertID, &f.Site, &f.At, &f.Name, &a.Metric, &op, &a.Threshold, &a.WindowMin, &f.Value, &f.Severity, &f.Mailed, &f.Seen); err != nil {
			return nil, err
		}
		a.Below = op == "below"
		f.Condition, f.Noun = a.Condition(), a.Noun()
		out = append(out, f)
	}
	return out, rows.Err()
}

// Unseen is one site's firings not yet marked seen: how many, and the most
// severe of them.
type Unseen struct {
	Site     string
	Count    int
	Severity string
}

// UnseenBySite is the firings not yet marked seen, by site.
func (s *Store) UnseenBySite(ctx context.Context) ([]Unseen, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT domain_id, COUNT(1),
		MAX(CASE severity WHEN 'danger' THEN 2 WHEN 'warn' THEN 1 ELSE 0 END)
		FROM alert_events WHERE seen=0 GROUP BY domain_id ORDER BY domain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unseen
	for rows.Next() {
		var u Unseen
		var sev int
		if err := rows.Scan(&u.Site, &u.Count, &sev); err != nil {
			return nil, err
		}
		u.Severity = []string{"info", "warn", "danger"}[sev]
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkSeen marks site's firings seen, and returns how many were not.
func (s *Store) MarkSeen(ctx context.Context, site string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE alert_events SET seen=1 WHERE seen=0 AND domain_id=?`, site)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
