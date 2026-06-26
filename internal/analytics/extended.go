// Package analytics — extended VayuAnalytics features.
//
// This file layers session-grouped pageview analytics, custom events, funnels,
// retention, and revenue on top of the privacy-first daily-aggregate foundation
// in analytics.go.
//
// Privacy by architecture (VayuPress Constitution):
//   - NO cookies and NO localStorage identifiers are ever set on the visitor.
//   - NO IP address and NO User-Agent string is ever persisted.
//   - The visitor identifier is a one-way hash of (rotating-daily-salt + IP +
//     User-Agent + hostname). The salt is generated with crypto/rand, kept only
//     in memory, and rotated every UTC day, so a visitor is unlinkable across
//     days and nothing in the database can re-identify a reader even on a full
//     database compromise. This is the Plausible/Umami "no-PII" model.
//   - Browser / OS / device are coarse buckets derived server-side from the
//     User-Agent and immediately discarded; the raw UA is never stored.
package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── Visitor identity (privacy-preserving, non-persistent) ────────────────────

// dailySalt holds the in-memory, daily-rotating salt used to derive
// non-reversible visitor identifiers. It is never written to disk.
var dailySalt = &saltRotator{}

type saltRotator struct {
	mu   sync.Mutex
	day  string
	salt []byte
}

// current returns today's salt, rotating (and discarding yesterday's) on day change.
func (s *saltRotator) current() []byte {
	day := time.Now().UTC().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.day != day || len(s.salt) == 0 {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			// Fall back to a time-seeded salt; still rotates daily and stores no PII.
			buf = []byte(day + "vayu-fallback-salt")
		}
		s.salt = buf
		s.day = day
	}
	return s.salt
}

// visitorID derives a stable-for-today, unlinkable-across-days visitor hash.
// ip and ua are used only to compute the hash and are never stored.
func visitorID(ip, ua, host string) string {
	h := sha256.New()
	h.Write(dailySalt.current())
	h.Write([]byte{0})
	h.Write([]byte(ip))
	h.Write([]byte{0})
	h.Write([]byte(ua))
	h.Write([]byte{0})
	h.Write([]byte(host))
	return "v" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))[:21]
}

// sessionID buckets a visitor into a 30-minute session window without storing
// any additional identifier.
func sessionID(vid string) string {
	bucket := time.Now().UTC().Unix() / (30 * 60)
	h := sha256.Sum256([]byte(vid + fmt.Sprintf(":%d", bucket)))
	return "s" + base64.RawURLEncoding.EncodeToString(h[:])[:21]
}

// coarseBrowser, coarseOS and coarseDevice reduce a User-Agent to a privacy-safe
// bucket. The raw UA is discarded immediately after this call.
func coarseBrowser(ua string) string {
	switch {
	case strings.Contains(ua, "Firefox"):
		return "Firefox"
	case strings.Contains(ua, "Edg"):
		return "Edge"
	case strings.Contains(ua, "OPR"), strings.Contains(ua, "Opera"):
		return "Opera"
	case strings.Contains(ua, "Chrome"):
		return "Chrome"
	case strings.Contains(ua, "Safari"):
		return "Safari"
	case ua == "":
		return "Unknown"
	default:
		return "Other"
	}
}

func coarseOS(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iOS"):
		return "iOS"
	case strings.Contains(ua, "Mac OS"), strings.Contains(ua, "Macintosh"):
		return "macOS"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	case ua == "":
		return "Unknown"
	default:
		return "Other"
	}
}

func coarseDevice(ua string) string {
	switch {
	case strings.Contains(ua, "Mobile"), strings.Contains(ua, "iPhone"), strings.Contains(ua, "Android"):
		return "Mobile"
	case strings.Contains(ua, "iPad"), strings.Contains(ua, "Tablet"):
		return "Tablet"
	case ua == "":
		return "Unknown"
	default:
		return "Desktop"
	}
}

// ── Collect request ──────────────────────────────────────────────────────────

// CollectRequest is the payload sent by the tracking script. It deliberately
// carries NO visitor/session identifier and NO device fingerprint — those are
// derived server-side and never stored in raw form.
type CollectRequest struct {
	URL         string            `json:"u"`
	Referrer    string            `json:"r"`
	PageTitle   string            `json:"t"`
	Hostname    string            `json:"h"`
	UTMSource   string            `json:"utm_source"`
	UTMMedium   string            `json:"utm_medium"`
	UTMCampaign string            `json:"utm_campaign"`
	UTMContent  string            `json:"utm_content"`
	UTMTerm     string            `json:"utm_term"`
	EventType   int               `json:"event_type"` // 1=pageview 2=customEvent
	EventName   string            `json:"event_name"`
	EventData   map[string]string `json:"event_data"`

	// Geo is populated server-side from trusted reverse-proxy headers (never
	// from the client beacon — hence json:"-"). VayuPress performs NO GeoIP
	// lookups and bundles no GeoIP database: if the operator's proxy (e.g.
	// Cloudflare) supplies country/region/city headers they are recorded,
	// otherwise these stay empty. No IP is ever persisted.
	Geo GeoInfo `json:"-"`
}

// GeoInfo carries coarse, proxy-supplied location for a visit.
type GeoInfo struct {
	Country string // ISO-3166 alpha-2 (e.g. "US"), uppercased
	Region  string
	City    string
}

// maxEventDataProps bounds how many custom-event properties a single beacon may
// persist, preventing storage-exhaustion abuse via the public ingest endpoint.
const maxEventDataProps = 24

// Collect stores a pageview or custom event. Visitor and session identity is
// derived server-side from (ip, ua) which are NEVER persisted. It creates the
// session row on first sight within the 30-minute window.
func (s *Store) Collect(ctx context.Context, req CollectRequest, ip, ua string) error {
	path := normalizePathExtended(req.URL)
	query := ""
	if i := strings.IndexAny(req.URL, "?#"); i >= 0 && i+1 < len(req.URL) {
		query = req.URL[i+1:]
		if len(query) > 512 {
			query = query[:512]
		}
	}
	host := strings.TrimSpace(req.Hostname)
	vid := visitorID(ip, ua, host)
	sid := sessionID(vid)

	browser := coarseBrowser(ua)
	os := coarseOS(ua)
	device := coarseDevice(ua)

	// Upsert the session. country/region/city are populated only from trusted
	// reverse-proxy headers (see CollectRequest.Geo); VayuPress itself performs
	// no GeoIP lookups and retains no IP.
	var exists string
	_ = s.db.QueryRowContext(ctx, `SELECT id FROM analytics_sessions WHERE id=?`, sid).Scan(&exists)
	if exists == "" {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO analytics_sessions(id,visitor_id,browser,os,device,screen,language,country,region,city,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			sid, vid, browser, os, device, "", "",
			trunc(req.Geo.Country, 2), trunc(req.Geo.Region, 80), trunc(req.Geo.City, 120),
			time.Now().UTC()); err != nil {
			return err
		}
	}

	eventType := req.EventType
	if eventType != 2 {
		eventType = 1
	}
	eventName := req.EventName
	if len(eventName) > 200 {
		eventName = eventName[:200]
	}

	eventID := fmt.Sprintf("e%d", time.Now().UnixNano())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		eventID, sid, path, query, trunc(req.PageTitle, 300), referrerHostExtended(req.Referrer), trunc(host, 200),
		trunc(req.UTMSource, 100), trunc(req.UTMMedium, 100), trunc(req.UTMCampaign, 100), trunc(req.UTMContent, 100), trunc(req.UTMTerm, 100),
		eventType, eventName, time.Now().UTC()); err != nil {
		return err
	}

	if eventType == 2 && len(req.EventData) > 0 {
		n := 0
		for k, v := range req.EventData {
			if n >= maxEventDataProps {
				break
			}
			n++
			_, _ = s.db.ExecContext(ctx,
				`INSERT INTO analytics_event_data(event_id,property_key,property_value,created_at) VALUES(?,?,?,?)`,
				eventID, trunc(k, 100), trunc(v, 500), time.Now().UTC())
		}
	}
	return nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
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
	// Pageviews and visits (sessions).
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=?`, from).
		Scan(&o.TotalPageviews, &o.TotalVisits)
	// Unique visitors counts distinct visitor_id (NOT sessions).
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=?`, from).
		Scan(&o.UniqueVisitors)
	// Bounce rate: share of sessions with exactly one pageview.
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(CASE WHEN v.cnt=1 THEN 100.0 ELSE 0.0 END),0) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? GROUP BY session_id) v`, from).
		Scan(&o.BounceRate)
	return o, nil
}

// OverviewBetween returns aggregate analytics for the half-open date window
// [fromInclusive, toExclusive), where both bounds are "YYYY-MM-DD" strings. It
// powers the dashboard's period-over-period percentage deltas by letting the
// caller fetch the immediately-preceding window of equal length.
func (s *Store) OverviewBetween(ctx context.Context, fromInclusive, toExclusive string) (*Overview, error) {
	o := &Overview{}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND created_at<?`, fromInclusive, toExclusive).
		Scan(&o.TotalPageviews, &o.TotalVisits)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=? AND created_at<?`, fromInclusive, toExclusive).
		Scan(&o.UniqueVisitors)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(CASE WHEN v.cnt=1 THEN 100.0 ELSE 0.0 END),0) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? AND created_at<? GROUP BY session_id) v`, fromInclusive, toExclusive).
		Scan(&o.BounceRate)
	return o, nil
}

// ── Pageview time series ─────────────────────────────────────────────────────

// DayPageviews is a single day's pageview + visitor count.
type DayPageviews struct {
	Date     string `json:"date"`
	Count    int    `json:"pageviews"`
	Visitors int    `json:"visitors"`
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
	result := []DayPageviews{}
	for rows.Next() {
		var dp DayPageviews
		if err := rows.Scan(&dp.Date, &dp.Count, &dp.Visitors); err != nil {
			return nil, err
		}
		result = append(result, dp)
	}
	return result, rows.Err()
}

// ── Top pages ────────────────────────────────────────────────────────────────

// PageStat holds per-page analytics.
type PageStat struct {
	Path           string `json:"path"`
	Pageviews      int    `json:"pageviews"`
	UniqueVisitors int    `json:"unique_visitors"`
}

// TopPages returns the most-viewed pages.
func (s *Store) TopPages(ctx context.Context, days, limit int) ([]PageStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT url_path,COUNT(1) as pv,COUNT(DISTINCT session_id) as uv FROM analytics_pageviews WHERE created_at>=? AND event_type=1 GROUP BY url_path ORDER BY pv DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PageStat{}
	for rows.Next() {
		var ps PageStat
		if err := rows.Scan(&ps.Path, &ps.Pageviews, &ps.UniqueVisitors); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}
	return result, rows.Err()
}

// ── Referrers ────────────────────────────────────────────────────────────────

// ReferrerStat holds per-referrer analytics.
type ReferrerStat struct {
	Referrer string `json:"referrer"`
	Domain   string `json:"domain"`
	Count    int    `json:"count"`
}

// TopReferrers returns the most common referrer hosts.
func (s *Store) TopReferrers(ctx context.Context, days, limit int) ([]ReferrerStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT referrer,COUNT(1) as cnt FROM analytics_pageviews WHERE created_at>=? AND referrer!='' GROUP BY referrer ORDER BY cnt DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ReferrerStat{}
	for rows.Next() {
		var rs ReferrerStat
		if err := rows.Scan(&rs.Referrer, &rs.Count); err != nil {
			return nil, err
		}
		rs.Domain = rs.Referrer // referrer is already reduced to a host at ingest.
		result = append(result, rs)
	}
	return result, rows.Err()
}

// ── Audience (browsers, devices) ─────────────────────────────────────────────

// AudienceStat is a generic audience breakdown row.
type AudienceStat struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Browsers returns visitor counts by browser bucket.
func (s *Store) Browsers(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "browser", days)
}

// Devices returns visitor counts by device bucket.
func (s *Store) Devices(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "device", days)
}

// OperatingSystems returns visitor counts by OS bucket.
func (s *Store) OperatingSystems(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "os", days)
}

// Countries returns visitor counts by country (ISO alpha-2), populated only
// when a reverse proxy supplies geo headers (see CollectRequest.Geo).
func (s *Store) Countries(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "country", days)
}

// Regions returns visitor counts by region/state (proxy-supplied).
func (s *Store) Regions(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "region", days)
}

// Cities returns visitor counts by city (proxy-supplied; often empty unless the
// CDN/proxy provides a city header).
func (s *Store) Cities(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.audienceSince(ctx, "city", days)
}

func (s *Store) audienceSince(ctx context.Context, column string, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	// column is a fixed internal identifier (never user input), so this is safe.
	switch column {
	case "browser", "device", "os", "country", "region", "city":
	default:
		column = "browser"
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+column+`,COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=? AND `+column+`!='' GROUP BY `+column+` ORDER BY 2 DESC LIMIT 100`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AudienceStat{}
	for rows.Next() {
		var a AudienceStat
		if err := rows.Scan(&a.Label, &a.Count); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
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
	result := []UTMStat{}
	for rows.Next() {
		var u UTMStat
		if err := rows.Scan(&u.Source, &u.Medium, &u.Campaign, &u.Count); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
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
		`SELECT event_name,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND event_type=2 AND event_name!='' GROUP BY event_name ORDER BY 2 DESC LIMIT 50`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []EventStat{}
	for rows.Next() {
		var e EventStat
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// ── Realtime ─────────────────────────────────────────────────────────────────

// RealtimeStats holds live visitor data.
type RealtimeStats struct {
	ActiveVisitors  int            `json:"active_visitors"`
	ActivePages     []RealtimePage `json:"active_pages"`
	ActiveCountries []AudienceStat `json:"active_countries"`
	ActiveReferrers []AudienceStat `json:"active_referrers"`
	WindowMinutes   int            `json:"window_minutes"`
}

// RealtimePage is a page with active visitor count.
type RealtimePage struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Realtime returns stats for the last 5 minutes: active visitors, the pages
// they're on, plus where they are (country) and how they arrived (referrer).
func (s *Store) Realtime(ctx context.Context) (*RealtimeStats, error) {
	since := time.Now().UTC().Add(-5 * time.Minute).Format("2006-01-02 15:04:05")
	rs := &RealtimeStats{
		ActivePages:     []RealtimePage{},
		ActiveCountries: []AudienceStat{},
		ActiveReferrers: []AudienceStat{},
		WindowMinutes:   5,
	}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=?`, since).
		Scan(&rs.ActiveVisitors)

	if rows, err := s.db.QueryContext(ctx,
		`SELECT url_path,COUNT(1) FROM analytics_pageviews WHERE created_at>=? GROUP BY url_path ORDER BY 2 DESC LIMIT 10`, since); err == nil {
		for rows.Next() {
			var p RealtimePage
			if err := rows.Scan(&p.Path, &p.Count); err == nil {
				rs.ActivePages = append(rs.ActivePages, p)
			}
		}
		rows.Close()
	}

	// Where active visitors are (proxy-supplied country only; empty otherwise).
	if rows, err := s.db.QueryContext(ctx,
		`SELECT s.country,COUNT(DISTINCT p.session_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=? AND s.country!='' GROUP BY s.country ORDER BY 2 DESC LIMIT 10`, since); err == nil {
		for rows.Next() {
			var a AudienceStat
			if err := rows.Scan(&a.Label, &a.Count); err == nil {
				rs.ActiveCountries = append(rs.ActiveCountries, a)
			}
		}
		rows.Close()
	}

	// Active visitors with no proxy-supplied country are bucketed as "Unknown"
	// (Label empty) so the live panel still accounts for everyone rather than
	// silently dropping them. VayuPress does no GeoIP — country is only known
	// when a reverse proxy sets a geo header.
	var unknownCountry int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT p.session_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=? AND COALESCE(s.country,'')=''`, since).
		Scan(&unknownCountry)
	if unknownCountry > 0 {
		rs.ActiveCountries = append(rs.ActiveCountries, AudienceStat{Label: "", Count: unknownCountry})
	}

	// How active visitors arrived (referrer host, recorded at ingest).
	if rows, err := s.db.QueryContext(ctx,
		`SELECT referrer,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND referrer!='' GROUP BY referrer ORDER BY 2 DESC LIMIT 10`, since); err == nil {
		for rows.Next() {
			var a AudienceStat
			if err := rows.Scan(&a.Label, &a.Count); err == nil {
				rs.ActiveReferrers = append(rs.ActiveReferrers, a)
			}
		}
		rows.Close()
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
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id,s.visitor_id,s.browser,s.os,s.device,s.country,s.created_at,COUNT(p.id) FROM analytics_sessions s LEFT JOIN analytics_pageviews p ON s.id=p.session_id WHERE s.created_at>=? GROUP BY s.id ORDER BY s.created_at DESC LIMIT ?`, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SessionInfo{}
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.ID, &si.VisitorID, &si.Browser, &si.OS, &si.Device, &si.Country, &si.CreatedAt, &si.Events); err != nil {
			return nil, err
		}
		result = append(result, si)
	}
	return result, rows.Err()
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
	results := []FunnelResult{}
	totalVisitors := 0
	for i, step := range f.Steps {
		var cnt int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND url_path=? AND event_type=1`,
			since, normalizePathExtended(step.URLPath)).Scan(&cnt)
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
	result := []Funnel{}
	for rows.Next() {
		var f Funnel
		if err := rows.Scan(&f.ID, &f.Name, &f.TimeWindow, &f.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

// ── Retention ────────────────────────────────────────────────────────────────

// CohortRow holds a single retention cohort.
type CohortRow struct {
	Date  string `json:"date"`
	Size  int    `json:"size"`
	Weeks []int  `json:"weeks"`
}

// maxRetentionWeeks bounds the retention cohort window. It is a compile-time
// constant so cohort slices are never sized from request-controlled input.
const maxRetentionWeeks = 12

// Retention returns weekly cohort retention computed in a single pass over the
// session table (bounded set), avoiding per-visitor N+1 queries.
func (s *Store) Retention(ctx context.Context, weeks int) ([]CohortRow, error) {
	// Hard-clamp the request-controlled window to a fixed maximum. weeks is only
	// ever used as a loop/slice bound below — never as an allocation size.
	if weeks <= 0 || weeks > maxRetentionWeeks {
		weeks = maxRetentionWeeks
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT visitor_id, DATE(created_at) FROM analytics_sessions ORDER BY created_at DESC LIMIT 50000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type vstate struct {
		first time.Time
		days  map[string]bool
	}
	visitors := map[string]*vstate{}
	for rows.Next() {
		var vid, day string
		if err := rows.Scan(&vid, &day); err != nil {
			continue
		}
		d, perr := time.Parse("2006-01-02", day)
		if perr != nil {
			continue
		}
		v, ok := visitors[vid]
		if !ok {
			v = &vstate{first: d, days: map[string]bool{}}
			visitors[vid] = v
		}
		if d.Before(v.first) {
			v.first = d
		}
		v.days[day] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	type agg struct {
		size  int
		weeks []int
	}
	cohorts := map[string]*agg{}
	for _, v := range visitors {
		cohortKey := v.first.Format("2006-01-02")
		a, ok := cohorts[cohortKey]
		if !ok {
			// Allocate a fixed-size slice (constant, never request-sized); only
			// indices [1, weeks) are populated below.
			a = &agg{weeks: make([]int, maxRetentionWeeks)}
			cohorts[cohortKey] = a
		}
		a.size++
		for w := 1; w < weeks; w++ {
			wStart := v.first.AddDate(0, 0, w*7)
			wEnd := wStart.AddDate(0, 0, 7)
			if wStart.After(now) {
				break
			}
			for day := range v.days {
				d, _ := time.Parse("2006-01-02", day)
				if !d.Before(wStart) && d.Before(wEnd) {
					a.weeks[w]++
					break
				}
			}
		}
	}

	result := []CohortRow{}
	for date, a := range cohorts {
		// Trim the fixed-size slice to the requested (clamped) window for output.
		result = append(result, CohortRow{Date: date, Size: a.size, Weeks: a.weeks[:weeks]})
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
		`SELECT DATE(created_at),SUM(amount),COUNT(1),AVG(amount),MAX(currency) FROM analytics_revenue WHERE created_at>=? GROUP BY DATE(created_at) ORDER BY DATE(created_at)`, from)
	daily := []RevenueStat{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var rs RevenueStat
			if err := rows.Scan(&rs.Date, &rs.Revenue, &rs.Transactions, &rs.AOV, &rs.Currency); err == nil {
				daily = append(daily, rs)
			}
		}
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

// ── Data retention ───────────────────────────────────────────────────────────

// PurgeOlderThan deletes detailed analytics rows older than retentionDays,
// honouring the Constitution's data-minimisation requirement. The daily
// aggregate table (analytics_daily) is intentionally untouched — it holds no
// per-visitor data and powers long-term trend charts.
func (s *Store) PurgeOlderThan(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 365
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var total int64
	for _, q := range []string{
		`DELETE FROM analytics_event_data WHERE created_at<?`,
		`DELETE FROM analytics_pageviews WHERE created_at<?`,
		`DELETE FROM analytics_sessions WHERE created_at<?`,
	} {
		res, err := s.db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return total, err
		}
		if n, e := res.RowsAffected(); e == nil {
			total += n
		}
	}
	return total, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

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
	// Strip scheme.
	if i := strings.Index(ref, "://"); i >= 0 {
		ref = ref[i+3:]
	}
	// Strip path/query/fragment.
	if i := strings.IndexAny(ref, "/?#"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	if len(ref) > 200 {
		ref = ref[:200]
	}
	return ref
}
