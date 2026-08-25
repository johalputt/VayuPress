// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// fromDay returns the inclusive lower-bound day string for a trailing window.
func fromTime(days int) time.Time {
	if days <= 0 {
		days = 30
	}
	return time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
}

// Overview is the headline human-traffic summary for the dashboard.
type Overview struct {
	Days              int     `json:"days"`               // window length
	Views             int64   `json:"views"`              // human page views
	UniqueVisitors    int64   `json:"unique_visitors"`    // distinct day-stable visitor hashes
	UniqueSessions    int64   `json:"unique_sessions"`    // distinct reading sessions (sliding 30-min)
	NewSessions       int64   `json:"new_sessions"`       // visitors first seen in the window
	ReturningSessions int64   `json:"returning_sessions"` // visitors with history before the window
	AvgTimeSeconds    float64 `json:"avg_time_seconds"`
	AvgScrollPct      float64 `json:"avg_scroll_percent"`
	EngagementRate    float64 `json:"engagement_rate"` // engaged / views
	BounceRate        float64 `json:"bounce_rate"`
	BotViews          int64   `json:"bot_views"` // shown separately, never mixed in
}

// human is the SQL predicate for real (non-bot) traffic.
const human = `client_type NOT IN ('BadBot','GoodBot','AIAgent','Headless')`

// Overview computes the headline metrics over the trailing window.
//
// New-vs-returning is answered per VISITOR against global history: a window
// visitor whose first-ever row predates the window is returning. The probe is
// a covered lookup on idx_va_visitor_time, so it costs one index seek per
// distinct window visitor — never a table scan (audit: the old daily-reset
// flag made this headline read ~zero forever).
//
// Bounce is derived on READ: rows whose beacon never arrived keep the storage
// defaults engaged=0/bounce=0 forever, so SUM(bounce=1) undercounted every
// one-page visit. A row IS a bounce when it recorded one OR it shows no
// engagement signal at all (audit).
func (s *Store) Overview(ctx context.Context, days int) (*Overview, error) {
	from := fromTime(days)
	o := &Overview{Days: days}
	var engaged, bounced int64
	if err := s.readDB().QueryRowContext(ctx, `SELECT
COALESCE(SUM(CASE WHEN `+human+` THEN 1 ELSE 0 END),0),
COALESCE(COUNT(DISTINCT CASE WHEN `+human+` THEN visitor_hash END),0),
COALESCE(COUNT(DISTINCT CASE WHEN `+human+` THEN session_hash END),0),
COALESCE(SUM(CASE WHEN NOT (`+human+`) THEN 1 ELSE 0 END),0),
COALESCE(AVG(CASE WHEN `+human+` THEN time_on_page_seconds END),0),
COALESCE(AVG(CASE WHEN `+human+` THEN scroll_depth_percent END),0),
COALESCE(SUM(CASE WHEN `+human+` AND engaged=1 THEN 1 ELSE 0 END),0),
COALESCE(SUM(CASE WHEN `+human+` AND (bounce=1 OR (engaged=0 AND time_on_page_seconds<=0)) THEN 1 ELSE 0 END),0)
FROM vayuanalytics_sessions WHERE entry_time>=?`, from).Scan(
		&o.Views, &o.UniqueVisitors, &o.UniqueSessions, &o.BotViews,
		&o.AvgTimeSeconds, &o.AvgScrollPct, &engaged, &bounced); err != nil {
		return nil, err
	}
	// True new vs returning over window VISITORS by global first-seen: the
	// EXISTS probe is a covered seek on idx_va_visitor_time, one per distinct
	// window visitor — never a scan.
	if err := s.readDB().QueryRowContext(ctx, `SELECT
COALESCE(COUNT(DISTINCT CASE WHEN NOT EXISTS(
    SELECT 1 FROM vayuanalytics_sessions p
    WHERE p.visitor_hash=v.vh AND p.entry_time<?) THEN v.vh END),0),
COALESCE(COUNT(DISTINCT CASE WHEN EXISTS(
    SELECT 1 FROM vayuanalytics_sessions p
    WHERE p.visitor_hash=v.vh AND p.entry_time<?) THEN v.vh END),0)
FROM (SELECT DISTINCT visitor_hash vh FROM vayuanalytics_sessions
      WHERE entry_time>=? AND visitor_hash<>'' AND `+human+`) v`,
		from, from, from).Scan(&o.NewSessions, &o.ReturningSessions); err != nil {
		return nil, err
	}
	if o.Views > 0 {
		o.EngagementRate = round2(float64(engaged) / float64(o.Views))
		o.BounceRate = round2(float64(bounced) / float64(o.Views))
	}
	o.AvgTimeSeconds = round2(o.AvgTimeSeconds)
	o.AvgScrollPct = round2(o.AvgScrollPct)
	return o, nil
}

// SourceStat is one traffic-source row with engagement quality.
type SourceStat struct {
	Category       string  `json:"category"`
	Detail         string  `json:"detail,omitempty"`
	Views          int64   `json:"views"`
	Sessions       int64   `json:"sessions"`
	AvgTimeSeconds float64 `json:"avg_time_seconds"`
	AvgScrollPct   float64 `json:"avg_scroll_percent"`
	EngagementRate float64 `json:"engagement_rate"`
}

// SourceBreakdown returns human traffic grouped by source category with the
// engagement quality of each — the data behind the source pie + comparison.
func (s *Store) SourceBreakdown(ctx context.Context, days int) ([]SourceStat, error) {
	from := fromTime(days)
	rows, err := s.readDB().QueryContext(ctx, `SELECT source_category,
COUNT(1), COUNT(DISTINCT session_hash),
COALESCE(AVG(time_on_page_seconds),0), COALESCE(AVG(scroll_depth_percent),0),
COALESCE(SUM(engaged),0)
FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+`
GROUP BY source_category ORDER BY COUNT(1) DESC`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceStat
	for rows.Next() {
		var st SourceStat
		var engaged int64
		if err := rows.Scan(&st.Category, &st.Views, &st.Sessions, &st.AvgTimeSeconds, &st.AvgScrollPct, &engaged); err != nil {
			return nil, err
		}
		if st.Views > 0 {
			st.EngagementRate = round2(float64(engaged) / float64(st.Views))
		}
		st.AvgTimeSeconds = round2(st.AvgTimeSeconds)
		st.AvgScrollPct = round2(st.AvgScrollPct)
		out = append(out, st)
	}
	return out, rows.Err()
}

// AITrafficReport contrasts AI-assisted discovery against organic search — the
// signature VayuAnalytics insight publishers cannot get elsewhere.
type AITrafficReport struct {
	BySystem       []SourceStat `json:"by_system"`        // per AI system
	AISummary      SourceStat   `json:"ai_summary"`       // all AI-assisted combined
	OrganicSummary SourceStat   `json:"organic_summary"`  // all organic search combined
	AISharePercent float64      `json:"ai_share_percent"` // AI views / total human views
}

// AITraffic computes the AI-vs-organic comparison over the trailing window.
func (s *Store) AITraffic(ctx context.Context, days int) (*AITrafficReport, error) {
	from := fromTime(days)
	rep := &AITrafficReport{}
	// Per-AI-system breakdown.
	rows, err := s.readDB().QueryContext(ctx, `SELECT source_detail,
COUNT(1), COUNT(DISTINCT session_hash),
COALESCE(AVG(time_on_page_seconds),0), COALESCE(AVG(scroll_depth_percent),0),
COALESCE(SUM(engaged),0)
FROM vayuanalytics_sessions WHERE entry_time>=? AND source_category='ai_assisted' AND `+human+`
GROUP BY source_detail ORDER BY COUNT(1) DESC`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st SourceStat
		var engaged int64
		if err := rows.Scan(&st.Detail, &st.Views, &st.Sessions, &st.AvgTimeSeconds, &st.AvgScrollPct, &engaged); err != nil {
			return nil, err
		}
		st.Category = "ai_assisted"
		if st.Views > 0 {
			st.EngagementRate = round2(float64(engaged) / float64(st.Views))
		}
		st.AvgTimeSeconds = round2(st.AvgTimeSeconds)
		st.AvgScrollPct = round2(st.AvgScrollPct)
		rep.BySystem = append(rep.BySystem, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rep.AISummary = s.categorySummary(ctx, from, "ai_assisted")
	rep.OrganicSummary = s.categorySummary(ctx, from, "organic")
	var totalHuman int64
	_ = s.readDB().QueryRowContext(ctx, `SELECT COUNT(1) FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+``, from).Scan(&totalHuman)
	if totalHuman > 0 {
		rep.AISharePercent = round2(float64(rep.AISummary.Views) / float64(totalHuman) * 100)
	}
	return rep, nil
}

func (s *Store) categorySummary(ctx context.Context, from time.Time, category string) SourceStat {
	st := SourceStat{Category: category}
	var engaged int64
	_ = s.readDB().QueryRowContext(ctx, `SELECT COUNT(1), COUNT(DISTINCT session_hash),
COALESCE(AVG(time_on_page_seconds),0), COALESCE(AVG(scroll_depth_percent),0), COALESCE(SUM(engaged),0)
FROM vayuanalytics_sessions WHERE entry_time>=? AND source_category=? AND `+human+``,
		from, category).Scan(&st.Views, &st.Sessions, &st.AvgTimeSeconds, &st.AvgScrollPct, &engaged)
	if st.Views > 0 {
		st.EngagementRate = round2(float64(engaged) / float64(st.Views))
	}
	st.AvgTimeSeconds = round2(st.AvgTimeSeconds)
	st.AvgScrollPct = round2(st.AvgScrollPct)
	return st
}

// PageStat is one page ranked by engagement.
type PageStat struct {
	Path           string  `json:"path"`
	Views          int64   `json:"views"`
	AvgTimeSeconds float64 `json:"avg_time_seconds"`
	AvgScrollPct   float64 `json:"avg_scroll_percent"`
	EngagementRate float64 `json:"engagement_rate"`
}

// TopPages returns the most-viewed human pages with their engagement quality.
func (s *Store) TopPages(ctx context.Context, days, limit int) ([]PageStat, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := fromTime(days)
	rows, err := s.readDB().QueryContext(ctx, `SELECT page_path,
COUNT(1), COALESCE(AVG(time_on_page_seconds),0), COALESCE(AVG(scroll_depth_percent),0), COALESCE(SUM(engaged),0)
FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+`
GROUP BY page_path ORDER BY COUNT(1) DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageStat
	for rows.Next() {
		var p PageStat
		var engaged int64
		if err := rows.Scan(&p.Path, &p.Views, &p.AvgTimeSeconds, &p.AvgScrollPct, &engaged); err != nil {
			return nil, err
		}
		if p.Views > 0 {
			p.EngagementRate = round2(float64(engaged) / float64(p.Views))
		}
		p.AvgTimeSeconds = round2(p.AvgTimeSeconds)
		p.AvgScrollPct = round2(p.AvgScrollPct)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Realtime is the live-activity snapshot.
type Realtime struct {
	WindowMinutes  int              `json:"window_minutes"`
	ActiveVisitors int64            `json:"active_visitors"`
	ActivePages    []PageStat       `json:"active_pages"`
	ByCountry      map[string]int64 `json:"by_country"`
	BySource       map[string]int64 `json:"by_source"`
	BotsActive     int64            `json:"bots_active"`
}

// Realtime returns activity within the trailing windowMinutes (default 5).
func (s *Store) Realtime(ctx context.Context, windowMinutes int) (*Realtime, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	since := time.Now().UTC().Add(-time.Duration(windowMinutes) * time.Minute)
	rt := &Realtime{WindowMinutes: windowMinutes}
	_ = s.readDB().QueryRowContext(ctx, `SELECT COUNT(DISTINCT session_hash) FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+``, since).Scan(&rt.ActiveVisitors)
	_ = s.readDB().QueryRowContext(ctx, `SELECT COUNT(DISTINCT session_hash) FROM vayuanalytics_sessions WHERE entry_time>=? AND NOT (`+human+`)`, since).Scan(&rt.BotsActive)

	rt.ByCountry = s.groupCount(ctx, `SELECT COALESCE(NULLIF(country_code,''),'??'), COUNT(1) FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+` GROUP BY country_code`, since)
	rt.BySource = s.groupCount(ctx, `SELECT source_category, COUNT(1) FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+` GROUP BY source_category`, since)

	if rows, err := s.readDB().QueryContext(ctx, `SELECT page_path, COUNT(1), COALESCE(AVG(time_on_page_seconds),0), COALESCE(AVG(scroll_depth_percent),0) FROM vayuanalytics_sessions WHERE entry_time>=? AND `+human+` GROUP BY page_path ORDER BY COUNT(1) DESC LIMIT 20`, since); err == nil {
		for rows.Next() {
			var p PageStat
			if err := rows.Scan(&p.Path, &p.Views, &p.AvgTimeSeconds, &p.AvgScrollPct); err == nil {
				rt.ActivePages = append(rt.ActivePages, p)
			}
		}
		// Best-effort panel (authoritative data stays in SQLite); still check Err.
		_ = rows.Err()
		_ = rows.Close()
	}
	return rt, nil
}

// groupCount runs a "SELECT key, COUNT(1) ... GROUP BY key" query bound to a
// single time parameter and returns the key→count map. Errors yield an empty
// map (a realtime panel is best-effort). rows.Err() is always checked.
func (s *Store) groupCount(ctx context.Context, query string, since time.Time) map[string]int64 {
	out := map[string]int64{}
	rows, err := s.readDB().QueryContext(ctx, query, since)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return out
		}
		out[k] = n
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
