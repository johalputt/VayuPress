// Package analytics — extended Umami-grade features.
//
// This file adds session-based pageview tracking, custom events, funnels,
// retention, revenue, and session replay on top of the privacy-first
// daily-aggregate foundation in analytics.go.
package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ── Collect request ──────────────────────────────────────────────────────────

// CollectRequest is the payload sent by the tracking script.
type CollectRequest struct {
	URL         string            `json:"u"`
	Referrer    string            `json:"r"`
	PageTitle   string            `json:"t"`
	Hostname    string            `json:"h"`
	Screen      string            `json:"sc"`
	Language    string            `json:"sl"`
	Browser     string            `json:"br"`
	OS          string            `json:"os"`
	Device      string            `json:"dv"`
	UTMSource   string            `json:"utm_source"`
	UTMMedium   string            `json:"utm_medium"`
	UTMCampaign string            `json:"utm_campaign"`
	UTMContent  string            `json:"utm_content"`
	UTMTerm     string            `json:"utm_term"`
	EventType   int               `json:"event_type"` // 1=pageview 2=customEvent
	EventName   string            `json:"event_name"`
	EventData   map[string]string `json:"event_data"`
	VisitorID   string            `json:"vid"`
	SessionID   string            `json:"sid"`
}

// Collect stores a pageview or custom event. It creates the session if new.
func (s *Store) Collect(ctx context.Context, req CollectRequest) error {
	if req.URL == "" {
		req.URL = "/"
	}
	if req.VisitorID == "" {
		req.VisitorID = "anon"
	}
	if req.SessionID == "" {
		req.SessionID = "s" + fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// Upsert session
	var exists string
	_ = s.db.QueryRowContext(ctx, `SELECT id FROM analytics_sessions WHERE id=?`, req.SessionID).Scan(&exists)
	if exists == "" {
		_, _ = s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO analytics_sessions(id,visitor_id,browser,os,device,screen,language,country,region,city,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			req.SessionID, req.VisitorID, req.Browser, req.OS, req.Device, req.Screen, req.Language, "", "", "", time.Now().UTC())
	}

	// Insert pageview/event
	eventID := fmt.Sprintf("e%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		eventID, req.SessionID, req.URL, "", req.PageTitle, req.Referrer, req.Hostname,
		req.UTMSource, req.UTMMedium, req.UTMCampaign, req.UTMContent, req.UTMTerm,
		req.EventType, req.EventName, time.Now().UTC())
	if err != nil {
		return err
	}

	// Store custom event properties
	if req.EventType == 2 && len(req.EventData) > 0 {
		for k, v := range req.EventData {
			_, _ = s.db.ExecContext(ctx,
				`INSERT INTO analytics_event_data(event_id,property_key,property_value,created_at) VALUES(?,?,?,?)`,
				eventID, k, v, time.Now().UTC())
		}
	}

	return nil
}

// ── Overview ─────────────────────────────────────────────────────────────────

// Overview holds aggregate stats for a date range.
type Overview struct {
	TotalPageviews int     `json:"total_pageviews"`
	UniqueVisitors int     `json:"unique_visitors"`
	TotalVisits    int     `json:"total_visits"`
	BounceRate     float64 `json:"bounce_rate"`
	AvgDuration    float64 `json:"avg_duration"`
}

// OverviewSince returns aggregate analytics for the trailing N days.
func (s *Store) OverviewSince(ctx context.Context, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	o := &Overview{}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=?`, from).
		Scan(&o.TotalPageviews, &o.UniqueVisitors, &o.TotalVisits)
	_ = s.db.QueryRowContext(ctx,
		`SELECT AVG(CASE WHEN v.cnt=1 THEN 1.0 ELSE 0.0 END) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? GROUP BY session_id) v`, from).
		Scan(&o.BounceRate)
	return o, nil
}

// ── Pageview time series ─────────────────────────────────────────────────────

// DayPageviews is a single day's pageview + visitor count.
type DayPageviews struct {
	Date    string `json:"date"`
	Count   int    `json:"pageviews"`
	Visitors int   `json:"visitors"`
}

// PageviewSeries returns daily pageview + visitor counts.
func (s *Store) PageviewSeries(ctx context.Context, days int) ([]DayPageviews, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT DATE(created_at) as d,COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? GROUP BY d ORDER BY d`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DayPageviews
	for rows.Next() {
		var dp DayPageviews
		if err := rows.Scan(&dp.Date, &dp.Count, &dp.Visitors); err != nil {
			return nil, err
		}
		result = append(result, dp)
	}
	if result == nil {
		result = []DayPageviews{}
	}
	return result, nil
}

// ── Top pages ────────────────────────────────────────────────────────────────

// PageStat holds per-page analytics.
type PageStat struct {
	Path           string  `json:"path"`
	Pageviews      int     `json:"pageviews"`
	UniqueVisitors int     `json:"unique_visitors"`
	AvgDuration    float64 `json:"avg_duration"`
	BounceRate     float64 `json:"bounce_rate"`
}

// TopPages returns the most-viewed pages.
func (s *Store) TopPages(ctx context.Context, days, limit int) ([]PageStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT url_path,COUNT(1) as pv,COUNT(DISTINCT session_id) as uv FROM analytics_pageviews WHERE created_at>=? AND event_type=1 GROUP BY url_path ORDER BY pv DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PageStat
	for rows.Next() {
		var ps PageStat
		if err := rows.Scan(&ps.Path, &ps.Pageviews, &ps.UniqueVisitors); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}
	if result == nil {
		result = []PageStat{}
	}
	return result, nil
}

// ── Referrers ────────────────────────────────────────────────────────────────

// ReferrerStat holds per-referrer analytics.
type ReferrerStat struct {
	Referrer string `json:"referrer"`
	Domain   string `json:"domain"`
	Count    int    `json:"count"`
}

// TopReferrers returns the most common referrers.
func (s *Store) TopReferrers(ctx context.Context, days, limit int) ([]ReferrerStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT referrer,COUNT(1) as cnt FROM analytics_pageviews WHERE created_at>=? AND referrer!='' GROUP BY referrer ORDER BY cnt DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ReferrerStat
	for rows.Next() {
		var rs ReferrerStat
		if err := rows.Scan(&rs.Referrer, &rs.Count); err != nil {
			return nil, err
		}
		idx := strings.Index(rs.Referrer, "://")
		if idx > -1 {
			rs.Domain = rs.Referrer[idx+3:]
		} else {
			rs.Domain = rs.Referrer
		}
		if idx2 := strings.Index(rs.Domain, "/"); idx2 > -1 {
			rs.Domain = rs.Domain[:idx2]
		}
		result = append(result, rs)
	}
	if result == nil {
		result = []ReferrerStat{}
	}
	return result, nil
}

// ── Audience (countries, browsers, devices) ──────────────────────────────────

// AudienceStat is a generic audience breakdown row.
type AudienceStat struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Countries returns visitor counts by country.
func (s *Store) Countries(ctx context.Context, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.audienceQuery(ctx,
		`SELECT country,COUNT(DISTINCT session_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=? AND country!='' GROUP BY country ORDER BY 2 DESC LIMIT 50`, from)
}

// Browsers returns visitor counts by browser.
func (s *Store) Browsers(ctx context.Context, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.audienceQuery(ctx,
		`SELECT browser,COUNT(DISTINCT session_id) FROM analytics_sessions WHERE created_at>=? GROUP BY browser ORDER BY 2 DESC`, from)
}

// Devices returns visitor counts by device type.
func (s *Store) Devices(ctx context.Context, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.audienceQuery(ctx,
		`SELECT device,COUNT(DISTINCT session_id) FROM analytics_sessions WHERE created_at>=? GROUP BY device ORDER BY 2 DESC`, from)
}

func (s *Store) audienceQuery(ctx context.Context, query string, args ...interface{}) ([]AudienceStat, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AudienceStat
	for rows.Next() {
		var a AudienceStat
		if err := rows.Scan(&a.Label, &a.Count); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	if result == nil {
		result = []AudienceStat{}
	}
	return result, nil
}

// ── UTM ──────────────────────────────────────────────────────────────────────

// UTMStat holds UTM campaign breakdown.
type UTMStat struct {
	Source   string `json:"source"`
	Medium   string `json:"medium"`
	Campaign string `json:"campaign"`
	Count    int    `json:"count"`
}

// UTMStats returns UTM campaign performance.
func (s *Store) UTMStats(ctx context.Context, days int) ([]UTMStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT utm_source,utm_medium,utm_campaign,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND (utm_source!='' OR utm_medium!='' OR utm_campaign!='') GROUP BY utm_source,utm_medium,utm_campaign ORDER BY 4 DESC LIMIT 50`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UTMStat
	for rows.Next() {
		var u UTMStat
		if err := rows.Scan(&u.Source, &u.Medium, &u.Campaign, &u.Count); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	if result == nil {
		result = []UTMStat{}
	}
	return result, nil
}

// ── Custom events ────────────────────────────────────────────────────────────

// EventStat holds a custom event count.
type EventStat struct {
	Name  string `json:"event"`
	Count int    `json:"count"`
}

// CustomEvents returns the most-triggered custom events.
func (s *Store) CustomEvents(ctx context.Context, days int) ([]EventStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT event_name,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND event_type=2 GROUP BY event_name ORDER BY 2 DESC LIMIT 50`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []EventStat
	for rows.Next() {
		var e EventStat
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	if result == nil {
		result = []EventStat{}
	}
	return result, nil
}

// ── Realtime ─────────────────────────────────────────────────────────────────

// RealtimeStats holds live visitor data.
type RealtimeStats struct {
	ActiveVisitors int              `json:"active_visitors"`
	ActivePages    []RealtimePage   `json:"active_pages"`
}

// RealtimePage is a page with active visitor count.
type RealtimePage struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Realtime returns stats for the last 5 minutes.
func (s *Store) Realtime(ctx context.Context) (*RealtimeStats, error) {
	since := time.Now().UTC().Add(-5 * time.Minute).Format("2006-01-02 15:04:05")
	rs := &RealtimeStats{}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=?`, since).
		Scan(&rs.ActiveVisitors)
	rows, err := s.db.QueryContext(ctx,
		`SELECT url_path,COUNT(1) FROM analytics_pageviews WHERE created_at>=? GROUP BY url_path ORDER BY 2 DESC LIMIT 10`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p RealtimePage
			if err := rows.Scan(&p.Path, &p.Count); err == nil {
				rs.ActivePages = append(rs.ActivePages, p)
			}
		}
	}
	if rs.ActivePages == nil {
		rs.ActivePages = []RealtimePage{}
	}
	return rs, nil
}

// ── Sessions ─────────────────────────────────────────────────────────────────

// SessionInfo holds summary data for a session.
type SessionInfo struct {
	ID        string `json:"id"`
	VisitorID string `json:"visitor_id"`
	Browser   string `json:"browser"`
	OS        string `json:"os"`
	Device    string `json:"device"`
	Country   string `json:"country"`
	CreatedAt string `json:"created_at"`
	Events    int    `json:"events"`
}

// RecentSessions returns the most recent sessions.
func (s *Store) RecentSessions(ctx context.Context, days, limit int) ([]SessionInfo, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 50
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id,s.visitor_id,s.browser,s.os,s.device,s.country,s.created_at,COUNT(p.id) FROM analytics_sessions s LEFT JOIN analytics_pageviews p ON s.id=p.session_id WHERE s.created_at>=? GROUP BY s.id ORDER BY s.created_at DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.ID, &si.VisitorID, &si.Browser, &si.OS, &si.Device, &si.Country, &si.CreatedAt, &si.Events); err != nil {
			return nil, err
		}
		result = append(result, si)
	}
	if result == nil {
		result = []SessionInfo{}
	}
	return result, nil
}

// ── Funnels ──────────────────────────────────────────────────────────────────

// Funnel holds a funnel definition.
type Funnel struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Steps      []FunnelStep `json:"steps"`
	TimeWindow int          `json:"time_window"`
	CreatedAt  time.Time    `json:"created_at"`
}

// FunnelStep is a single step in a funnel.
type FunnelStep struct {
	Name    string `json:"name"`
	URLPath string `json:"url_path"`
}

// FunnelResult holds conversion data for a funnel.
type FunnelResult struct {
	Name     string  `json:"name"`
	URLPath  string  `json:"url_path"`
	Visitors int     `json:"visitors"`
	Rate     float64 `json:"rate"`
}

// CreateFunnel stores a new funnel definition.
func (s *Store) CreateFunnel(ctx context.Context, name string, steps []FunnelStep, timeWindow int) (string, error) {
	if timeWindow <= 0 {
		timeWindow = 30
	}
	id := fmt.Sprintf("f%d", time.Now().UnixNano())
	stepsJSON, _ := json.Marshal(steps)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_funnels(id,name,steps_json,time_window,created_at) VALUES(?,?,?,?,?)`,
		id, name, string(stepsJSON), timeWindow, time.Now().UTC())
	return id, err
}

// GetFunnel returns a funnel and its conversion data.
func (s *Store) GetFunnel(ctx context.Context, id string) (*Funnel, []FunnelResult, error) {
	var f Funnel
	var stepsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,name,steps_json,time_window,created_at FROM analytics_funnels WHERE id=?`, id).
		Scan(&f.ID, &f.Name, &stepsJSON, &f.TimeWindow, &f.CreatedAt)
	if err != nil {
		return nil, nil, err
	}
	_ = json.Unmarshal([]byte(stepsJSON), &f.Steps)

	since := time.Now().UTC().AddDate(0, 0, -f.TimeWindow).Format("2006-01-02")
	var results []FunnelResult
	totalVisitors := 0
	for i, step := range f.Steps {
		var cnt int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND url_path LIKE ? AND event_type=1`,
			since, "%"+step.URLPath+"%").Scan(&cnt)
		if i == 0 {
			totalVisitors = cnt
		}
		rate := 0.0
		if totalVisitors > 0 {
			rate = float64(cnt) / float64(totalVisitors) * 100
		}
		results = append(results, FunnelResult{Name: step.Name, URLPath: step.URLPath, Visitors: cnt, Rate: rate})
	}
	return &f, results, nil
}

// ListFunnels returns all funnel definitions.
func (s *Store) ListFunnels(ctx context.Context) ([]Funnel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,time_window,created_at FROM analytics_funnels ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Funnel
	for rows.Next() {
		var f Funnel
		if err := rows.Scan(&f.ID, &f.Name, &f.TimeWindow, &f.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	if result == nil {
		result = []Funnel{}
	}
	return result, nil
}

// ── Retention ────────────────────────────────────────────────────────────────

// CohortRow holds a single retention cohort.
type CohortRow struct {
	Date  string `json:"date"`
	Size  int    `json:"size"`
	Weeks []int  `json:"weeks"`
}

// Retention returns cohort retention data for the last N weeks.
func (s *Store) Retention(ctx context.Context, weeks int) ([]CohortRow, error) {
	if weeks <= 0 {
		weeks = 12
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT visitor_id,DATE(MIN(created_at)) as first_visit FROM analytics_sessions GROUP BY visitor_id ORDER BY first_visit DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type visitorCohort struct {
		FirstVisit string
		ReturnWeeks map[int]bool
	}
	var visitors []visitorCohort
	now := time.Now().UTC()
	for rows.Next() {
		var vid, fv string
		if err := rows.Scan(&vid, &fv); err != nil {
			continue
		}
		vc := visitorCohort{FirstVisit: fv, ReturnWeeks: make(map[int]bool)}
		fvTime, _ := time.Parse("2006-01-02", fv)
		for w := 1; w < weeks && w <= 11; w++ {
			wStart := fvTime.AddDate(0, 0, w*7)
			wEnd := fvTime.AddDate(0, 0, (w+1)*7)
			if wEnd.After(now) {
				break
			}
			var cnt int
			_ = s.db.QueryRowContext(ctx,
				`SELECT COUNT(DISTINCT session_id) FROM analytics_sessions WHERE visitor_id=? AND created_at>=? AND created_at<?`,
				vid, wStart.Format("2006-01-02"), wEnd.Format("2006-01-02")).Scan(&cnt)
			if cnt > 0 {
				vc.ReturnWeeks[w] = true
			}
		}
		visitors = append(visitors, vc)
	}

	// Aggregate by date
	type agg struct {
		Size   int
		Weeks  []int
	}
	aggregated := make(map[string]*agg)
	for _, v := range visitors {
		a, ok := aggregated[v.FirstVisit]
		if !ok {
			a = &agg{Weeks: make([]int, weeks)}
			aggregated[v.FirstVisit] = a
		}
		a.Size++
		for w := 1; w < weeks && w <= 11; w++ {
			if v.ReturnWeeks[w] {
				a.Weeks[w]++
			}
		}
	}

	var result []CohortRow
	for date, a := range aggregated {
		result = append(result, CohortRow{Date: date, Size: a.Size, Weeks: a.Weeks})
	}
	if result == nil {
		result = []CohortRow{}
	}
	return result, nil
}

// ── Revenue ──────────────────────────────────────────────────────────────────

// RevenueStat holds revenue reporting data.
type RevenueStat struct {
	Date         string  `json:"date"`
	Revenue      float64 `json:"revenue"`
	Transactions int     `json:"transactions"`
	AOV          float64 `json:"aov"`
	Currency     string  `json:"currency"`
}

// RevenueStats returns revenue metrics for a date range.
func (s *Store) RevenueStats(ctx context.Context, days int) (map[string]interface{}, error) {
	if days <= 0 {
		days = 30
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	var totalRevenue float64
	var totalTransactions int
	var avgOrderValue float64
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount),0),COUNT(1),COALESCE(AVG(amount),0) FROM analytics_revenue WHERE created_at>=?`, from).
		Scan(&totalRevenue, &totalTransactions, &avgOrderValue)

	rows, err := s.db.QueryContext(ctx,
		`SELECT DATE(created_at),SUM(amount),COUNT(1),AVG(amount),currency FROM analytics_revenue WHERE created_at>=? GROUP BY DATE(created_at) ORDER BY DATE(created_at)`, from)
	var daily []RevenueStat
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var rs RevenueStat
			if err := rows.Scan(&rs.Date, &rs.Revenue, &rs.Transactions, &rs.AOV, &rs.Currency); err == nil {
				daily = append(daily, rs)
			}
		}
	}
	if daily == nil {
		daily = []RevenueStat{}
	}

	return map[string]interface{}{
		"total_revenue":      totalRevenue,
		"total_transactions": totalTransactions,
		"avg_order_value":    avgOrderValue,
		"daily":              daily,
	}, nil
}

// RecordRevenue stores a revenue event.
func (s *Store) RecordRevenue(ctx context.Context, sessionID, currency, orderID, eventName string, amount float64) (string, error) {
	if currency == "" {
		currency = "USD"
	}
	if sessionID == "" {
		sessionID = "unknown"
	}
	id := fmt.Sprintf("r%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_revenue(id,session_id,amount,currency,order_id,event_name,created_at) VALUES(?,?,?,?,?,?,?)`,
		id, sessionID, amount, currency, orderID, eventName, time.Now().UTC())
	return id, err
}

// ── Replays ──────────────────────────────────────────────────────────────────

// ReplayInfo holds replay metadata.
type ReplayInfo struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	DurationMS int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ListReplays returns recent session replays.
func (s *Store) ListReplays(ctx context.Context, limit int) ([]ReplayInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,session_id,duration_ms,created_at,expires_at FROM analytics_replays ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ReplayInfo
	for rows.Next() {
		var ri ReplayInfo
		if err := rows.Scan(&ri.ID, &ri.SessionID, &ri.DurationMS, &ri.CreatedAt, &ri.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, ri)
	}
	if result == nil {
		result = []ReplayInfo{}
	}
	return result, nil
}

// GetReplay returns replay data by ID.
func (s *Store) GetReplay(ctx context.Context, id string) (*ReplayInfo, json.RawMessage, error) {
	var ri ReplayInfo
	var eventsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,session_id,events_json,duration_ms,created_at,expires_at FROM analytics_replays WHERE id=?`, id).
		Scan(&ri.ID, &ri.SessionID, &eventsJSON, &ri.DurationMS, &ri.CreatedAt, &ri.ExpiresAt)
	if err != nil {
		return nil, nil, err
	}
	return &ri, json.RawMessage(eventsJSON), nil
}

// StoreReplay stores session replay data.
func (s *Store) StoreReplay(ctx context.Context, sessionID string, eventsJSON json.RawMessage, durationMS int, maxSeconds int) error {
	if maxSeconds <= 0 {
		maxSeconds = 300
	}
	id := fmt.Sprintf("rp%d", time.Now().UnixNano())
	expiresAt := time.Now().UTC().Add(time.Duration(maxSeconds) * time.Second)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_replays(id,session_id,events_json,duration_ms,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		id, sessionID, string(eventsJSON), durationMS, time.Now().UTC(), expiresAt)
	return err
}

// PurgeExpiredReplays deletes replays past their expiry.
func (s *Store) PurgeExpiredReplays(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM analytics_replays WHERE expires_at<?`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Helper ───────────────────────────────────────────────────────────────────

func normalizePathExtended(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSpace(p)
	if p == "" {
		p = "/"
	}
	if len(p) > 512 {
		p = p[:512]
	}
	return p
}

func referrerHostExtended(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}
