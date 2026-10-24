// SPDX-License-Identifier: Apache-2.0

// Package analytics — extended VayuAnalytics features.
//
// This file layers session-grouped pageview analytics, custom events, funnels,
// retention, and revenue on top of the privacy-first daily-aggregate foundation
// in analytics.go.
//
// Privacy by architecture (VayuPress Constitution):
//   - NO cookies and NO localStorage identifiers are ever set on the visitor.
//   - NO IP address and NO User-Agent string is ever persisted.
//   - The visitor identifier is a one-way hash of (rotating daily salt + IP +
//     User-Agent + Accept-Language), from the session package the engagement
//     store also uses. The salt is derived from the service keyring (or
//     crypto/rand without one) and rotated every UTC day, so a visitor is
//     unlinkable across days and nothing in the database can re-identify a
//     reader even on a full database compromise. This is the Plausible/Umami
//     "no-PII" model.
//   - Browser / OS / device are coarse buckets derived server-side from the
//     User-Agent and immediately discarded; the raw UA is never stored.
package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
)

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
	EventType   int               `json:"event_type"` // 1 page view, 2 custom event, 4 link out, 5 download (links.go)
	EventName   string            `json:"event_name"`
	EventData   map[string]string `json:"event_data"`

	// Geo is populated server-side from trusted reverse-proxy headers (never
	// from the client beacon — hence json:"-"). VayuPress performs NO GeoIP
	// lookups and bundles no GeoIP database: if the operator's proxy (e.g.
	// Cloudflare) supplies country/region/city headers they are recorded,
	// otherwise these stay empty. No IP is ever persisted.
	Geo GeoInfo `json:"-"`
	// Lang is the request's Accept-Language, server-side like Geo: one input
	// of the visitor hash, never stored.
	Lang string `json:"-"`
	// search marks a search, set by RecordSearch alone (searches.go). It is
	// unexported so that no beacon body can set it.
	search *searchMark
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

// Collect records one beacon event synchronously, through the same
// normalisation as CollectAsync (buildCollectEvent): one path decides every
// event's visit, so the two cannot count differently.
//
// domainID is the domain THIS INSTALL RESOLVED for the request, passed by the
// handler. It is never read from req: /api/v1/analytics/collect is public and
// unauthenticated, so a field the body supplies is a field an attacker chooses,
// and a visitor able to choose their own domain could write traffic into any
// client's report on the install. req.Hostname exists for the session record and
// is client-supplied by construction — it must not be used for attribution.
func (s *Store) Collect(ctx context.Context, req CollectRequest, ip, ua, domainID string) error {
	ev := s.buildCollectEvent(req, ip, ua, domainID)
	if ev == nil {
		return nil
	}
	return s.applyCollectBatch(ctx, []collectEvent{*ev})
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ── Overview ─────────────────────────────────────────────────────────────────

// Overview holds aggregate stats for a date range. Every figure is defined in
// docs/ANALYTICS.md and computed by overviewWindow alone, for every caller.
type Overview struct {
	TotalPageviews int `json:"total_pageviews"`
	// UniqueVisitors counts distinct same-day visitor hashes among the
	// window's events: a reader who came on three days is three.
	UniqueVisitors int `json:"unique_visitors"`
	// TotalVisits counts the visits that began in the window.
	TotalVisits int `json:"total_visits"`
	// BounceRate is the percentage of those visits with one event, a page
	// view, and nothing else.
	BounceRate float64 `json:"bounce_rate"`
	// AvgDuration is the mean length, first event to last, of the visits with
	// two or more events, in seconds. A visit of one event has no measured
	// length (nothing reports when its reader left), so it is left out rather
	// than averaged in as 0; those visits are the bounces.
	AvgDuration float64 `json:"avg_duration"`
}

// overviewWindow computes the overview of [from, to) ("YYYY-MM-DD" or any
// later-sorting timestamp string; to "" is open-ended) for one site, or for
// every site when scope is ScopeAllDomains.
func (s *Store) overviewWindow(ctx context.Context, scope, from, to string) (*Overview, error) {
	where := "created_at>=?"
	args := []any{from}
	if to != "" {
		where += " AND created_at<?"
		args = append(args, to)
	}
	clause, carg := domainClause(scope)
	where += clause
	args = append(args, carg...)
	o := &Overview{}
	db := s.readDB()
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM analytics_pageviews WHERE `+where+` AND event_type=1`, args...).
		Scan(&o.TotalPageviews); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT +visitor_id) FROM analytics_pageviews WHERE `+where, args...).
		Scan(&o.UniqueVisitors); err != nil {
		return nil, err
	}
	// The visits begun in the window, each with all of its events wherever
	// they fall: a visit crossing the window's end is measured whole, and
	// counted once, in the window it began. One row per visit, kept current at
	// ingest (migration 113).
	var bounces int
	var avg sql.NullFloat64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1),COALESCE(SUM(events=1 AND views=1),0),AVG(CASE WHEN events>1 THEN (julianday(last_at)-julianday(created_at))*86400.0 END)
		 FROM analytics_sessions WHERE events>0 AND `+where, args...).
		Scan(&o.TotalVisits, &bounces, &avg); err != nil {
		return nil, err
	}
	if o.TotalVisits > 0 {
		o.BounceRate = 100 * float64(bounces) / float64(o.TotalVisits)
	}
	o.AvgDuration = avg.Float64
	return o, nil
}

// OverviewSince returns aggregate analytics for the trailing N days, every
// site together.
func (s *Store) OverviewSince(ctx context.Context, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	return s.overviewWindow(ctx, ScopeAllDomains, s.now().UTC().AddDate(0, 0, -(days-1)).Format("2006-01-02"), "")
}

// OverviewBetween returns aggregate analytics for the half-open date window
// [fromInclusive, toExclusive), where both bounds are "YYYY-MM-DD" strings. It
// powers the dashboard's period-over-period percentage deltas by letting the
// caller fetch the immediately-preceding window of equal length.
func (s *Store) OverviewBetween(ctx context.Context, fromInclusive, toExclusive string) (*Overview, error) {
	return s.overviewWindow(ctx, ScopeAllDomains, fromInclusive, toExclusive)
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
	return s.PageviewSeriesScoped(ctx, ScopeAllDomains, days)
}

// PageviewSeriesScoped is PageviewSeries for one site.
func (s *Store) PageviewSeriesScoped(ctx context.Context, scope string, days int) ([]DayPageviews, error) {
	if days <= 0 {
		days = 14
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT DATE(created_at) as d,SUM(event_type=1),COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE created_at>=?`+clause+` GROUP BY d ORDER BY d`, append([]any{from}, carg...)...)
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
	return s.TopPagesScoped(ctx, ScopeAllDomains, days, limit)
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
	return s.TopReferrersScoped(ctx, ScopeAllDomains, days, limit)
}

// TopReferrersScoped is TopReferrers for one site.
func (s *Store) TopReferrersScoped(ctx context.Context, scope string, days, limit int) ([]ReferrerStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	// Two filters, both of which were missing and both of which inflated this
	// table past the total pageview count it is a breakdown of.
	//
	// event_type=1 because a custom event is not an arrival. TopPages already
	// filtered it and this did not, so the two panels were counting different
	// things and could not be reconciled.
	//
	// The site's own host and its subdomains are excluded because internal
	// navigation is not a referrer. The classifier already knows this — it
	// classifies a same-site referrer as Direct/"internal" — but it records
	// ReferrerDomain BEFORE reaching that decision and never clears it, so the
	// host survived into this table while the Audience card correctly counted it
	// as direct. One dataset, two panels, opposite answers: the referrer list was
	// topped by the operator's own webmail and MCP hosts, each with a count larger
	// than the site's entire pageview total.
	notSelf, selfArgs := s.selfHostExclusion("referrer")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(1) as cnt FROM analytics_pageviews
		 WHERE created_at>=? AND event_type=1 AND referrer!=''`+clause+notSelf+
			` GROUP BY referrer ORDER BY cnt DESC LIMIT ?`,
		append(append(append([]any{from}, carg...), selfArgs...), limit)...)
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
	return s.AudienceScoped(ctx, ScopeAllDomains, "browser", days)
}

// Devices returns visitor counts by device bucket.
func (s *Store) Devices(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.AudienceScoped(ctx, ScopeAllDomains, "device", days)
}

// OperatingSystems returns visitor counts by OS bucket.
func (s *Store) OperatingSystems(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.AudienceScoped(ctx, ScopeAllDomains, "os", days)
}

// Countries returns visitor counts by country (ISO alpha-2), populated only
// when a reverse proxy supplies geo headers (see CollectRequest.Geo).
func (s *Store) Countries(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.AudienceScoped(ctx, ScopeAllDomains, "country", days)
}

// Regions returns visitor counts by region/state (proxy-supplied).
func (s *Store) Regions(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.AudienceScoped(ctx, ScopeAllDomains, "region", days)
}

// Cities returns visitor counts by city (proxy-supplied; often empty unless the
// CDN/proxy provides a city header).
func (s *Store) Cities(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.AudienceScoped(ctx, ScopeAllDomains, "city", days)
}

// AudienceScoped breaks one site's visitors down by column (browser, device,
// os, country, region or city), counted over the visits that began in the
// range.
func (s *Store) AudienceScoped(ctx context.Context, scope, column string, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	// column is a fixed internal identifier (never user input), so this is safe.
	switch column {
	case "browser", "device", "os", "country", "region", "city":
	default:
		column = "browser"
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT `+column+`,COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=? AND `+column+`!=''`+clause+` GROUP BY +`+column+` ORDER BY 2 DESC LIMIT 100`, append([]any{from}, carg...)...)
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

// ── Acquisition channels ─────────────────────────────────────────────────────

// channelLabels names each referrer category as the reports say it, in the
// order they list them.
var channelLabels = []struct {
	cat   classifier.Category
	label string
}{
	{classifier.Direct, "Direct"}, {classifier.Organic, "Organic search"}, {classifier.AIAssisted, "AI assistants"},
	{classifier.Social, "Social"}, {classifier.Email, "Email"}, {classifier.Corporate, "Workplace apps"},
	{classifier.Referral, "Referral"},
}

// ReferrerChannel is the acquisition channel of a stored referrer host:
// Direct (no referrer), Organic search, AI assistants, Social, Email,
// Workplace apps or Referral. It is the engagement store's classifier, so a
// host is in the same channel on every report; matched by label and domain,
// never by substring, so cnet.com is not t.co.
func ReferrerChannel(host string) string {
	return channelLabel(classifier.HostCategory(host))
}

func channelLabel(c classifier.Category) string {
	for _, l := range channelLabels {
		if l.cat == c {
			return l.label
		}
	}
	return "Referral"
}

// Channels groups pageviews into acquisition channels (Direct / Organic search /
// Social / Referral) by classifying the stored referrer host. All aggregate and
// no-PII. Empty buckets are omitted; the result is sorted by count descending.
func (s *Store) Channels(ctx context.Context, days int) ([]AudienceStat, error) {
	return s.ChannelsScoped(ctx, ScopeAllDomains, days)
}

// ChannelsScoped is Channels for one site.
func (s *Store) ChannelsScoped(ctx context.Context, scope string, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause+` GROUP BY +referrer`, append([]any{from}, carg...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := map[string]int{}
	for rows.Next() {
		var ref string
		var cnt int
		if err := rows.Scan(&ref, &cnt); err != nil {
			return nil, err
		}
		buckets[s.channelOf(ref)] += cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]AudienceStat, 0, len(channelLabels))
	for _, l := range channelLabels {
		if buckets[l.label] > 0 {
			out = append(out, AudienceStat{Label: l.label, Count: buckets[l.label]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
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
	return s.UTMStatsScoped(ctx, ScopeAllDomains, days)
}

// UTMStatsScoped is UTMStats for one site.
func (s *Store) UTMStatsScoped(ctx context.Context, scope string, days int) ([]UTMStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT utm_source,utm_medium,utm_campaign,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND (utm_source!='' OR utm_medium!='' OR utm_campaign!='')`+clause+` GROUP BY utm_source,utm_medium,utm_campaign ORDER BY 4 DESC LIMIT 50`, append([]any{from}, carg...)...)
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
	return s.CustomEventsScoped(ctx, ScopeAllDomains, days)
}

// CustomEventsScoped is CustomEvents for one site.
func (s *Store) CustomEventsScoped(ctx context.Context, scope string, days int) ([]EventStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT event_name,COUNT(1) FROM analytics_pageviews WHERE created_at>=? AND event_type=2 AND event_name!=''`+clause+` GROUP BY event_name ORDER BY 2 DESC LIMIT 50`, append([]any{from}, carg...)...)
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
	return s.RealtimeScoped(ctx, ScopeAllDomains)
}

// RealtimeScoped is the last five minutes on one site (or every site, for
// ScopeAllDomains). Active readers are counted as visitors, by the
// definition every other figure uses: one person reading two tabs, or back
// after a 30-minute pause, is one reader right now, not two.
func (s *Store) RealtimeScoped(ctx context.Context, scope string) (*RealtimeStats, error) {
	since := s.now().UTC().Add(-5 * time.Minute).Format("2006-01-02 15:04:05")
	rs := &RealtimeStats{
		ActivePages:     []RealtimePage{},
		ActiveCountries: []AudienceStat{},
		ActiveReferrers: []AudienceStat{},
		WindowMinutes:   5,
	}
	clause, carg := domainClause(scope)
	pclause, _ := domainClauseOn("p", scope)
	args := append([]any{since}, carg...)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE created_at>=?`+clause, args...).
		Scan(&rs.ActiveVisitors)

	if rows, err := s.readDB().QueryContext(ctx,
		`SELECT url_path,COUNT(DISTINCT visitor_id) FROM analytics_pageviews WHERE created_at>=?`+clause+` AND event_type=1 GROUP BY +url_path ORDER BY 2 DESC LIMIT 10`, args...); err == nil {
		for rows.Next() {
			var p RealtimePage
			if err := rows.Scan(&p.Path, &p.Count); err == nil {
				rs.ActivePages = append(rs.ActivePages, p)
			}
		}
		rows.Close()
	}

	// Where active readers are (proxy-supplied country only; empty otherwise).
	if rows, err := s.readDB().QueryContext(ctx,
		`SELECT s.country,COUNT(DISTINCT p.visitor_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=?`+pclause+` AND s.country!='' GROUP BY s.country ORDER BY 2 DESC LIMIT 10`, args...); err == nil {
		for rows.Next() {
			var a AudienceStat
			if err := rows.Scan(&a.Label, &a.Count); err == nil {
				rs.ActiveCountries = append(rs.ActiveCountries, a)
			}
		}
		rows.Close()
	}

	// Active readers with no proxy-supplied country are bucketed as "Unknown"
	// (Label empty) so the live panel still accounts for everyone rather than
	// silently dropping them. VayuPress does no GeoIP — country is only known
	// when a reverse proxy sets a geo header.
	var unknownCountry int
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT p.visitor_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=?`+pclause+` AND COALESCE(s.country,'')=''`, args...).
		Scan(&unknownCountry)
	if unknownCountry > 0 {
		rs.ActiveCountries = append(rs.ActiveCountries, AudienceStat{Label: "", Count: unknownCountry})
	}

	// How active readers arrived (referrer host, recorded at ingest).
	//
	// The self-host exclusion belongs here for the same reason as the other two
	// referrer lists: internal navigation is not a referral. This is the third
	// place the same predicate was needed and the third place it was missing —
	// which is why they now share selfHostPatterns rather than each carrying a
	// copy of the WHERE clause.
	liveNotSelf, liveSelfArgs := s.selfHostExclusion("referrer")
	if rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(DISTINCT visitor_id) FROM analytics_pageviews
		 WHERE created_at>=?`+clause+` AND referrer!=''`+liveNotSelf+
			` GROUP BY +referrer ORDER BY 2 DESC LIMIT 10`,
		append(args, liveSelfArgs...)...); err == nil {
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
	return s.RecentSessionsScoped(ctx, ScopeAllDomains, days, limit)
}

// RecentSessionsScoped is RecentSessions for one site's visits, or every
// site's for ScopeAllDomains.
func (s *Store) RecentSessionsScoped(ctx context.Context, scope string, days, limit int) ([]SessionInfo, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	clause, carg := domainClauseOn("s", scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT s.id,s.visitor_id,s.browser,s.os,s.device,s.country,s.created_at,COUNT(p.id) FROM analytics_sessions s LEFT JOIN analytics_pageviews p ON s.id=p.session_id WHERE s.created_at>=?`+clause+` GROUP BY +s.id ORDER BY s.created_at DESC LIMIT ?`,
		append(append([]any{from}, carg...), limit)...)
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
	return s.CreateFunnelScoped(ctx, ScopeAllDomains, name, steps, timeWindow)
}

// CreateFunnelScoped stores a funnel for one site (or the install's, for
// ScopeAllDomains). A site's funnel walks only that site's visits.
func (s *Store) CreateFunnelScoped(ctx context.Context, scope, name string, steps []FunnelStep, timeWindow int) (string, error) {
	if timeWindow <= 0 {
		timeWindow = 30
	}
	id := fmt.Sprintf("f%d", time.Now().UnixNano())
	stepsJSON, _ := json.Marshal(steps)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_funnels(id,name,steps_json,time_window,created_at,domain_id) VALUES(?,?,?,?,?,?)`,
		id, name, string(stepsJSON), timeWindow, time.Now().UTC(), definitionSite(scope))
	return id, err
}

// GetFunnel returns a funnel and its conversion data.
//
// Order-enforced single-pass evaluation (2025 audit: the old per-step
// COUNT(DISTINCT) let step counts exceed step zero — "conversion rates"
// above 100% — because nothing tied hits to a journey). One scan over the
// window's pageviews walks each session's events chronologically, advancing
// a step pointer on every match, so a visitor can only ever reach step N
// after passing steps 0..N-1 IN ORDER.
func (s *Store) GetFunnel(ctx context.Context, id string) (*Funnel, []FunnelResult, error) {
	return s.GetFunnelScoped(ctx, ScopeAllDomains, id)
}

// GetFunnelScoped is one of scope's funnels and its conversion, over that
// site's visits (or every site's, for the install's funnels). Another site's
// funnel is not found from here.
func (s *Store) GetFunnelScoped(ctx context.Context, scope, id string) (*Funnel, []FunnelResult, error) {
	var f Funnel
	var stepsJSON string
	err := s.readDB().QueryRowContext(ctx,
		`SELECT id,name,steps_json,time_window,created_at FROM analytics_funnels WHERE id=? AND domain_id=?`, id, definitionSite(scope)).
		Scan(&f.ID, &f.Name, &stepsJSON, &f.TimeWindow, &f.CreatedAt)
	if err != nil {
		return nil, nil, err
	}
	_ = json.Unmarshal([]byte(stepsJSON), &f.Steps)
	if len(f.Steps) == 0 {
		return &f, []FunnelResult{}, nil
	}

	since := time.Now().UTC().AddDate(0, 0, -f.TimeWindow).Format("2006-01-02")
	// Pre-normalize step paths once; row paths are normalized to match.
	stepPaths := make([]string, len(f.Steps))
	for i, st := range f.Steps {
		stepPaths[i] = NormalizePath(st.URLPath)
	}

	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT session_id,url_path FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause+` ORDER BY session_id,created_at`,
		append([]any{since}, carg...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	reached := make([]int, len(f.Steps)) // reached[i] = sessions that reached step i
	curSession := ""
	curStep := 0
	hadSession := false
	for rows.Next() {
		var sid, path string
		if err := rows.Scan(&sid, &path); err != nil {
			continue
		}
		if !hadSession || sid != curSession {
			curSession, curStep, hadSession = sid, 0, true
		}
		if curStep >= len(stepPaths) {
			continue // already converted; later pages cannot re-enter earlier steps
		}
		if NormalizePath(path) == stepPaths[curStep] {
			reached[curStep]++
			curStep++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	totalVisitors := reached[0]
	results := make([]FunnelResult, len(f.Steps))
	for i, step := range f.Steps {
		rate := 0.0
		if totalVisitors > 0 {
			rate = float64(reached[i]) / float64(totalVisitors) * 100
		}
		results[i] = FunnelResult{Name: step.Name, URLPath: step.URLPath, Visitors: reached[i], Rate: rate}
	}
	return &f, results, nil
}

// ListFunnels returns the install-wide funnels.
func (s *Store) ListFunnels(ctx context.Context) ([]Funnel, error) {
	return s.ListFunnelsScoped(ctx, ScopeAllDomains)
}

// ListFunnelsScoped returns one site's funnels (or the install's).
func (s *Store) ListFunnelsScoped(ctx context.Context, scope string) ([]Funnel, error) {
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT id,name,time_window,created_at FROM analytics_funnels WHERE domain_id=? ORDER BY created_at DESC`, definitionSite(scope))
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
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount),0),COUNT(1),COALESCE(AVG(amount),0) FROM analytics_revenue WHERE created_at>=?`, from).
		Scan(&totalRevenue, &totalTransactions, &avgOrderValue)

	rows, err := s.readDB().QueryContext(ctx,
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
		// A revenue row keeps its session_id; it goes with the session.
		`DELETE FROM analytics_revenue WHERE created_at<?`,
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

// NormalizePath is a page's path as analytics records it: without its query
// or fragment, capped, and without a trailing slash. The reading beacon keys
// its rows by it too, so one page's views and readings meet.
func NormalizePath(p string) string {
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
	// Collapse the trailing slash so one page is one row. "/post" and "/post/"
	// are the same article, and storing both split its traffic in two: each
	// spelling got its own count, "Top pages" listed the page twice at partial
	// totals, and trending — which joins on the slug — matched only the spelling
	// without the slash and dropped the rest of the views entirely.
	//
	// The root is exempt: trimming "/" would leave "", which is not a path.
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
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
	// Strip credentials, then ":port" — see referrerHost for why the port has to
	// go. An IPv6 literal is bracketed, so only a colon after the closing bracket
	// is a port separator.
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[i+1:]
	}
	if b := strings.LastIndex(ref, "]"); b >= 0 {
		if i := strings.Index(ref[b:], ":"); i >= 0 {
			ref = ref[:b+i]
		}
	} else if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	if len(ref) > 200 {
		ref = ref[:200]
	}
	return ref
}

// ScopeAllDomains disables per-domain filtering, for the operator's own
// install-wide view. It is a value no domain id can be, so it cannot be reached
// by an id arriving in a request.
const ScopeAllDomains = "\x00*all-domains*"

// domainClause returns the WHERE fragment and argument for a domain scope.
//
// The empty string is the PRIMARY and filters on it — it does not mean "all".
// That distinction is the whole point: "" meaning everything is how a client's
// report would quietly include every other site on the install.
func domainClause(scope string) (string, []any) {
	if scope == ScopeAllDomains {
		return "", nil
	}
	return " AND domain_id=?", []any{scope}
}

// domainClauseOn is domainClause for a column of the table aliased as alias.
func domainClauseOn(alias, scope string) (string, []any) {
	if scope == ScopeAllDomains {
		return "", nil
	}
	return " AND " + alias + ".domain_id=?", []any{scope}
}

// OverviewSinceScoped is OverviewSince for one domain.
func (s *Store) OverviewSinceScoped(ctx context.Context, scope string, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	return s.overviewWindow(ctx, scope, s.now().UTC().AddDate(0, 0, -(days-1)).Format("2006-01-02"), "")
}

// OverviewBetweenScoped is OverviewBetween for one site: its previous period,
// by the same definitions.
func (s *Store) OverviewBetweenScoped(ctx context.Context, scope, fromInclusive, toExclusive string) (*Overview, error) {
	return s.overviewWindow(ctx, scope, fromInclusive, toExclusive)
}

// LandingStat is one landing page: the visits that began on it and their
// visitors.
type LandingStat struct {
	Path     string `json:"path"`
	Visits   int    `json:"visits"`
	Visitors int    `json:"visitors"`
}

// LandingPagesScoped lists where one site's visits began in the trailing
// days, busiest first: the landing page each visit row keeps (migration 113).
func (s *Store) LandingPagesScoped(ctx context.Context, scope string, days, limit int) ([]LandingStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT landing,COUNT(1),COUNT(DISTINCT visitor_id) FROM analytics_sessions
		 WHERE created_at>=? AND events>0 AND landing!=''`+clause+` GROUP BY +landing ORDER BY 2 DESC LIMIT ?`,
		append(append([]any{from}, carg...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LandingStat{}
	for rows.Next() {
		var l LandingStat
		if err := rows.Scan(&l.Path, &l.Visits, &l.Visitors); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// TopPagesScoped is TopPages for one domain.
func (s *Store) TopPagesScoped(ctx context.Context, scope string, days, limit int) ([]PageStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, arg := domainClause(scope)
	args := append([]any{from}, arg...)
	args = append(args, limit)

	// Group on the path with any trailing slash stripped. "/post" and "/post/"
	// are one page: a reader can arrive at either spelling, and grouping the raw
	// value listed the same page twice with its traffic split between the rows —
	// so a page's real total was under-reported and its rank was wrong. Merging
	// them here also keeps this panel agreeing with the public Trending widget,
	// which reads the same event log and deliberately mirrors these numbers.
	// New pageviews are normalised on the way in; the trim covers history that
	// was recorded before that.
	// The expression trims the slash off the REMAINDER and puts the leading one
	// back, so the homepage survives: a plain RTRIM(url_path,'/') turns "/" into
	// the empty string and the busiest page on the site would be listed with no
	// path at all.
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT '/' || RTRIM(SUBSTR(url_path,2),'/') AS p,COUNT(1) as pv,COUNT(DISTINCT visitor_id) as uv
		 FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause+`
		 GROUP BY '/' || RTRIM(SUBSTR(url_path,2),'/') ORDER BY pv DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PageStat{}
	for rows.Next() {
		var ps PageStat
		if err := rows.Scan(&ps.Path, &ps.Pageviews, &ps.UniqueVisitors); err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}
