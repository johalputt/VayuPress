// SPDX-License-Identifier: Apache-2.0

package analytics

// explore.go — the Explorer: one figure, split by one dimension, over the
// events that pass the filters, for a range of days and, beside it, the range
// before.
//
// Every figure is the one docs/ANALYTICS.md defines, counted the way the
// overview counts it: views and events from the event log, visitors as the
// distinct visitors with any event, and visits, bounce rate and average visit
// from the visit rows, counted in the range a visit began. A visit has one
// landing page, place and browser, but no one path or referrer, so the visit
// figures split and filter by the visit's own columns only, and Validate says
// so rather than counting something else under the name.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
)

// ExploreMetric is the figure the Explorer counts.
type ExploreMetric string

// The Explorer's figures.
const (
	ExploreViews      ExploreMetric = "views"
	ExploreVisitors   ExploreMetric = "visitors"
	ExploreVisits     ExploreMetric = "visits"
	ExploreBounceRate ExploreMetric = "bounce_rate"
	ExploreAvgVisit   ExploreMetric = "avg_visit"
	ExploreEvents     ExploreMetric = "events"
)

// exploreMetric is how one figure is counted.
type exploreMetric struct {
	id    ExploreMetric
	label string
	visit bool   // counted from the visit rows
	agg   string // the aggregate, over p (an event) or s (a visit)
	where string // what an event or a visit must be to count
	// additive figures add up over a split: a split's rows are shares of
	// the total.
	additive bool
}

var exploreMetrics = []exploreMetric{
	{ExploreViews, "Page views", false, "COUNT(1)", "p.event_type=1", true},
	{ExploreVisitors, "Visitors", false, "COUNT(DISTINCT p.visitor_id)", "", false},
	{ExploreVisits, "Visits", true, "COUNT(1)", "s.events>0", true},
	{ExploreBounceRate, "Bounce rate", true, "100.0*SUM(s.events=1 AND s.views=1)/COUNT(1)", "s.events>0", false},
	{ExploreAvgVisit, "Average visit", true, "AVG(CASE WHEN s.events>1 THEN (julianday(s.last_at)-julianday(s.created_at))*86400.0 END)", "s.events>0", false},
	{ExploreEvents, "Custom events", false, "COUNT(1)", "p.event_type=2", true},
}

// exploreDim is one way to split or filter: the column of an event (p) or of
// its visit (s) it reads.
type exploreDim struct {
	id, label string
	event     string // over an event row, its visit joined as s; "" when an event has none
	visit     string // over a visit row; "" when a visit has none
	text      bool   // offers "starts with"
	kind      dimKind
}

// dimKind is what a dimension's values are, which decides how it is
// filtered and in what order its split is listed.
type dimKind uint8

const (
	dimWords  dimKind = iota // matched whole; split largest first
	dimNumber                // also "at least" and "at most"; split in order
	dimFlag                  // yes or no
)

// The visit's own facts, over its row s, so they split and filter every
// figure: an event's visit is joined. A visit's referrer is its first page
// view's, the one it arrived by; "returning" is the same visitor's earlier
// visit to the same site, which can only be the same day, as a visitor is
// recognised for a day (docs/ANALYTICS.md); "converted" is a custom event in
// the visit.
const (
	visitFirstView  = `FROM analytics_pageviews f WHERE f.session_id=s.id AND f.event_type=1 ORDER BY f.created_at, f.id LIMIT 1`
	visitReturning  = `CASE WHEN EXISTS(SELECT 1 FROM analytics_sessions r WHERE r.visitor_id=s.visitor_id AND r.domain_id=s.domain_id AND (r.created_at<s.created_at OR (r.created_at=s.created_at AND r.id<s.id))) THEN 'yes' ELSE 'no' END`
	visitConverted  = `CASE WHEN EXISTS(SELECT 1 FROM analytics_pageviews c WHERE c.session_id=s.id AND c.event_type=2) THEN 'yes' ELSE 'no' END`
	visitLengthSecs = `CAST(COALESCE(round((julianday(s.last_at)-julianday(s.created_at))*86400.0),0) AS INTEGER)`
)

var exploreDims = []exploreDim{
	{"day", "Day", "date(p.created_at)", "date(s.created_at)", false, dimWords},
	{"path", "Page", "p.url_path", "", true, dimWords},
	{"landing", "Landing page", "s.landing", "s.landing", true, dimWords},
	{"referrer", "Referrer", "p.referrer", "", true, dimWords},
	{"channel", "Channel", "p.referrer", "", false, dimWords}, // through Store.channelSQL
	{"utm_source", "Campaign source", "p.utm_source", "", false, dimWords},
	{"utm_medium", "Campaign medium", "p.utm_medium", "", false, dimWords},
	{"utm_campaign", "Campaign", "p.utm_campaign", "", false, dimWords},
	{"event", "Event", "p.event_name", "", false, dimWords},
	{"country", "Country", "s.country", "s.country", false, dimWords},
	{"region", "Region", "s.region", "s.region", false, dimWords},
	{"city", "City", "s.city", "s.city", false, dimWords},
	{"device", "Device", "s.device", "s.device", false, dimWords},
	{"browser", "Browser", "s.browser", "s.browser", false, dimWords},
	{"os", "Operating system", "s.os", "s.os", false, dimWords},
	{"language", "Language", "s.language", "s.language", false, dimWords},
	{"visit_channel", "Visit's channel", "s.id", "s.id", false, dimWords}, // s.id marks it usable; Store.dimSQL writes it
	{"visit_referrer", "Visit's referrer", "COALESCE((SELECT f.referrer " + visitFirstView + "),'')", "COALESCE((SELECT f.referrer " + visitFirstView + "),'')", true, dimWords},
	{"returning", "Returning the same day", visitReturning, visitReturning, false, dimFlag},
	{"converted", "Visit with a custom event", visitConverted, visitConverted, false, dimFlag},
	{"visit_views", "Page views in the visit", "s.views", "s.views", false, dimNumber},
	{"visit_events", "Events in the visit", "s.events", "s.events", false, dimNumber},
	{"visit_length", "Visit length, seconds", visitLengthSecs, visitLengthSecs, false, dimNumber},
}

// dimSQL is d's column for a figure counted from visits or from events, as
// an expression with its bind arguments.
func (s *Store) dimSQL(d exploreDim, visit bool) (string, []any) {
	switch {
	case d.id == "visit_channel":
		// Classified inside the lookup, so the visit's first view is found
		// once, not once for each host the rule names.
		ch, args := s.channelSQL("f.referrer")
		return "COALESCE((SELECT " + ch + " " + visitFirstView + "),'')", args
	case visit:
		return d.visit, nil
	case d.id == "channel":
		return s.channelSQL(d.event)
	}
	return d.event, nil
}

func findMetric(id ExploreMetric) (exploreMetric, bool) {
	for _, m := range exploreMetrics {
		if m.id == id {
			return m, true
		}
	}
	return exploreMetric{}, false
}

func findDim(id string) (exploreDim, bool) {
	for _, d := range exploreDims {
		if d.id == id {
			return d, true
		}
	}
	return exploreDim{}, false
}

// ExploreOption is one choice the Explorer's form offers.
type ExploreOption struct {
	ID, Label string
	Visit     bool // usable with the visit figures
	Event     bool // usable with the event figures
	Text      bool // offers "starts with"
	Number    bool // offers "at least" and "at most"
	Flag      bool // is yes or no
	Additive  bool // a figure whose split adds up to its total
}

// ExploreMetrics are the figures, as the form offers them.
func ExploreMetrics() []ExploreOption {
	out := make([]ExploreOption, 0, len(exploreMetrics))
	for _, m := range exploreMetrics {
		out = append(out, ExploreOption{ID: string(m.id), Label: m.label, Visit: m.visit, Event: !m.visit, Additive: m.additive})
	}
	return out
}

// ExploreDims are the splits and filters, as the form offers them.
func ExploreDims() []ExploreOption {
	out := make([]ExploreOption, 0, len(exploreDims))
	for _, d := range exploreDims {
		out = append(out, ExploreOption{ID: d.id, Label: d.label, Visit: d.visit != "", Event: d.event != "", Text: d.text,
			Number: d.kind == dimNumber, Flag: d.kind == dimFlag})
	}
	return out
}

// ChannelLabel is how a channel the Explorer splits by reads.
func ChannelLabel(c string) string { return channelLabel(classifier.Category(c)) }

// ExploreFilter keeps the events (or visits) whose dimension is, is not, or
// starts with a value.
type ExploreFilter struct {
	Dim   string `json:"dim"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// The filter operators.
const (
	FilterIs      = "is"
	FilterIsNot   = "not"
	FilterPrefix  = "prefix"
	FilterAtLeast = "atleast"
	FilterAtMost  = "atmost"
)

// ExploreMaxFilters bounds a query's filters; ExploreMaxRows its split.
const (
	ExploreMaxFilters = 5
	ExploreMaxRows    = 100
)

// ExploreQuery is one question to the Explorer.
type ExploreQuery struct {
	Scope   string // a site, or ScopeAllDomains
	Metric  ExploreMetric
	By      string // a dimension, or "" for the total alone
	Filters []ExploreFilter
	// Segment is a saved segment's filters, held with Filters: the events
	// counted pass both.
	Segment []ExploreFilter
	Days    int
	Compare bool // also count the range of equal length before
}

// ValidFilter says, in words, why f cannot be applied to any figure.
func ValidFilter(f ExploreFilter) error {
	d, ok := findDim(f.Dim)
	if !ok || f.Dim == "day" {
		return errors.New("a filter is on one of the listed dimensions; the range sets the days")
	}
	name := strings.ToLower(d.label)
	whole := func() error {
		if _, err := strconv.Atoi(f.Value); err != nil {
			return fmt.Errorf("%s is a whole number", name)
		}
		return nil
	}
	switch f.Op {
	case FilterIs, FilterIsNot:
		switch d.kind {
		case dimFlag:
			if f.Value != "yes" && f.Value != "no" {
				return fmt.Errorf("%s is yes or no", name)
			}
		case dimNumber:
			return whole()
		}
	case FilterPrefix:
		if !d.text {
			return fmt.Errorf("%s is matched whole, not by its start", name)
		}
		if f.Value == "" {
			return errors.New("a start is not empty")
		}
	case FilterAtLeast, FilterAtMost:
		if d.kind != dimNumber {
			return fmt.Errorf("%s is not a number", name)
		}
		return whole()
	default:
		return errors.New("a filter is, is not, starts with, at least or at most")
	}
	if len([]rune(f.Value)) > 200 {
		return errors.New("a filter's value is up to 200 characters")
	}
	return nil
}

// Validate says, in words, why the query cannot be answered as asked.
func (q ExploreQuery) Validate() error {
	m, ok := findMetric(q.Metric)
	if !ok {
		return errors.New("choose a figure")
	}
	if q.Days < 1 || q.Days > 1095 {
		return errors.New("a range is 1 to 1095 days")
	}
	usable := func(d exploreDim) bool { return (m.visit && d.visit != "") || (!m.visit && d.event != "") }
	if q.By != "" {
		d, ok := findDim(q.By)
		if !ok {
			return errors.New("choose what to split by")
		}
		if !usable(d) {
			return fmt.Errorf("%s is counted per visit, and a visit has no one %s", m.label, strings.ToLower(d.label))
		}
	}
	if len(q.Filters) > ExploreMaxFilters || len(q.Segment) > ExploreMaxFilters {
		return fmt.Errorf("at most %d filters", ExploreMaxFilters)
	}
	for i, f := range append(append([]ExploreFilter(nil), q.Segment...), q.Filters...) {
		in := ""
		if i < len(q.Segment) {
			in = "the segment: "
		}
		if err := ValidFilter(f); err != nil {
			return errors.New(in + err.Error())
		}
		if d, _ := findDim(f.Dim); !usable(d) {
			return fmt.Errorf("%s%s is counted per visit, and a visit has no one %s to filter by", in, m.label, strings.ToLower(d.label))
		}
	}
	return nil
}

// ExploreRow is one value of the split, its figure, and the figure the range
// before had for it.
type ExploreRow struct {
	Key   string
	Value float64
	Prev  float64
	// HasPrev says the range before counted this value at all; a rate or an
	// average over nothing is not 0.
	HasPrev bool
}

// ExploreResult is the answer: the total over the filtered events, the split
// (largest first, or by day), and whether the split was cut at
// ExploreMaxRows.
type ExploreResult struct {
	Total, PrevTotal float64
	HasTotal         bool // the figure is defined over the range: a rate over no visits is not
	HasPrevTotal     bool
	Rows             []ExploreRow
	Truncated        bool
}

// Explore answers q, which must Validate.
func (s *Store) Explore(ctx context.Context, q ExploreQuery) (*ExploreResult, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	m, _ := findMetric(q.Metric)
	now := s.now().UTC()
	from := now.AddDate(0, 0, -(q.Days - 1)).Format("2006-01-02")
	res := &ExploreResult{}
	var err error
	if res.Total, res.HasTotal, err = s.exploreTotal(ctx, m, q, from, ""); err != nil {
		return nil, err
	}
	if q.Compare {
		prevFrom := now.AddDate(0, 0, -(2*q.Days - 1)).Format("2006-01-02")
		if res.PrevTotal, res.HasPrevTotal, err = s.exploreTotal(ctx, m, q, prevFrom, from); err != nil {
			return nil, err
		}
	}
	if q.By == "" {
		return res, nil
	}
	cur, err := s.exploreSplit(ctx, m, q, from, "")
	if err != nil {
		return nil, err
	}
	if len(cur) > ExploreMaxRows {
		cur, res.Truncated = cur[:ExploreMaxRows], true
	}
	res.Rows = cur
	if !q.Compare || len(cur) == 0 {
		return res, nil
	}
	// The range before is counted for exactly the values shown, so a value
	// outside its own top rows is still compared, never read as absent.
	keys := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		k := r.Key
		if q.By == "day" {
			k = shiftDay(k, -q.Days)
		}
		keys = append(keys, k)
	}
	prevFrom := now.AddDate(0, 0, -(2*q.Days - 1)).Format("2006-01-02")
	prev, err := s.exploreSplit(ctx, m, q, prevFrom, from, keys...)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]float64, len(prev))
	for _, p := range prev {
		byKey[p.Key] = p.Value
	}
	for i := range res.Rows {
		k := res.Rows[i].Key
		if q.By == "day" {
			// A day is compared with the day as far into the range before.
			k = shiftDay(k, -q.Days)
		}
		res.Rows[i].Prev, res.Rows[i].HasPrev = byKey[k]
	}
	return res, nil
}

// exploreFrom is the query's FROM and WHERE for [from, to), with its
// arguments.
func (s *Store) exploreFrom(m exploreMetric, q ExploreQuery, from, to string) (string, []any) {
	var b strings.Builder
	var args []any
	alias := "p"
	if m.visit {
		alias = "s"
		b.WriteString(" FROM analytics_sessions s WHERE s.created_at>=?")
	} else {
		// Every event has its visit row (migration 113); the join is a
		// lookup by its key.
		b.WriteString(" FROM analytics_pageviews p JOIN analytics_sessions s ON s.id=p.session_id WHERE p.created_at>=?")
	}
	args = append(args, from)
	if to != "" {
		b.WriteString(" AND " + alias + ".created_at<?")
		args = append(args, to)
	}
	clause, carg := domainClauseOn(alias, q.Scope)
	b.WriteString(clause)
	args = append(args, carg...)
	if m.where != "" {
		b.WriteString(" AND " + m.where)
	}
	for _, f := range append(append([]ExploreFilter(nil), q.Segment...), q.Filters...) {
		d, _ := findDim(f.Dim)
		c, cargs := s.dimSQL(d, m.visit)
		// A number's value is bound as written: every count is a column or a
		// CAST, whose integer affinity compares '3' as 3.
		value := f.Value
		switch f.Op {
		case FilterIs:
			b.WriteString(" AND (" + c + ")=?")
			args = append(append(args, cargs...), value)
		case FilterIsNot:
			b.WriteString(" AND (" + c + ")<>?")
			args = append(append(args, cargs...), value)
		case FilterAtLeast:
			b.WriteString(" AND (" + c + ")>=?")
			args = append(append(args, cargs...), value)
		case FilterAtMost:
			b.WriteString(" AND (" + c + ")<=?")
			args = append(append(args, cargs...), value)
		case FilterPrefix:
			// substr, not LIKE: LIKE ignores case and reads % and _ in the
			// value as wildcards.
			b.WriteString(" AND substr(" + c + ",1,?)=?")
			args = append(append(args, cargs...), len([]rune(f.Value)), f.Value)
		}
	}
	return b.String(), args
}

func (s *Store) exploreTotal(ctx context.Context, m exploreMetric, q ExploreQuery, from, to string) (float64, bool, error) {
	where, args := s.exploreFrom(m, q, from, to)
	var v sql.NullFloat64
	if err := s.readDB().QueryRowContext(ctx, `SELECT `+m.agg+where, args...).Scan(&v); err != nil {
		return 0, false, err
	}
	return v.Float64, v.Valid, nil
}

// exploreSplit counts the figure for each value of q.By over [from, to): the
// largest ExploreMaxRows+1, or, given only, those values alone.
func (s *Store) exploreSplit(ctx context.Context, m exploreMetric, q ExploreQuery, from, to string, only ...string) ([]ExploreRow, error) {
	d, _ := findDim(q.By)
	col, cargs := s.dimSQL(d, m.visit)
	where, wargs := s.exploreFrom(m, q, from, to)
	args := append(append([]any{}, cargs...), wargs...)
	order := " ORDER BY 2 DESC, 1 LIMIT " + strconv.Itoa(ExploreMaxRows+1)
	switch {
	case only != nil:
		where += " AND +(" + col + ") IN (?" + strings.Repeat(",?", len(only)-1) + ")"
		args = append(args, cargs...)
		for _, k := range only {
			args = append(args, k)
		}
		order = ""
	case q.By == "day":
		order = " ORDER BY 1" // every day of the range, at most 1095
	case d.kind == dimNumber:
		order = " ORDER BY 1 LIMIT " + strconv.Itoa(ExploreMaxRows+1) // a distribution, in order
	}
	// The key is grouped through a unary +, so SQLite reads the time index's
	// range and groups it, never an index on the key walked end to end
	// (console_plans_test.go).
	rows, err := s.readDB().QueryContext(ctx, `SELECT +(`+col+`) AS k, `+m.agg+where+` GROUP BY k HAVING `+m.agg+` IS NOT NULL`+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExploreRow
	for rows.Next() {
		var r ExploreRow
		if err := rows.Scan(&r.Key, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// shiftDay is the "YYYY-MM-DD" day n days from day.
func shiftDay(day string, n int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}
