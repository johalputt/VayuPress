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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
// A nil result means entropy is unavailable and NO identifier may be derived:
// the previous fallback (a time-derived constant anyone reading this source
// could reproduce) would have made every "pseudonymous" ID constant across all
// installs and trivially linkable — so this now fails closed instead.
func (s *saltRotator) current() []byte {
	day := time.Now().UTC().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.day != day || len(s.salt) == 0 {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			saltUnavailable.Store(true)
			s.salt = nil
			s.day = day
			return nil
		}
		saltUnavailable.Store(false)
		s.salt = buf
		s.day = day
	}
	return s.salt
}

// saltUnavailable records a crypto/rand failure. While set, visitor/session
// derivation refuses to run: events are dropped rather than written under a
// predictable pseudonym.
var saltUnavailable atomic.Bool

// visitorID derives a stable-for-today, unlinkable-across-days visitor hash.
// ip and ua are used only to compute the hash and are never stored. Returns ""
// when no safe salt exists (entropy failure) — callers must not fabricate one.
func visitorID(ip, ua, host string) string {
	salt := dailySalt.current()
	if len(salt) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write(salt)
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
// Collect records one beacon event.
//
// domainID is the domain THIS INSTALL RESOLVED for the request, passed by the
// handler. It is never read from req: /api/v1/analytics/collect is public and
// unauthenticated, so a field the body supplies is a field an attacker chooses,
// and a visitor able to choose their own domain could write traffic into any
// client's report on the install. req.Hostname exists for the session record and
// is client-supplied by construction — it must not be used for attribution.
func (s *Store) Collect(ctx context.Context, req CollectRequest, ip, ua, domainID string) error {
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
	if vid == "" {
		// Fail closed: entropy is unavailable, so no safe pseudonym exists.
		// Drop the event rather than merge all visitors into one predictable
		// session or invent an identity we cannot stand behind.
		return nil
	}
	sid := sessionID(vid)

	browser := coarseBrowser(ua)
	os := coarseOS(ua)
	device := coarseDevice(ua)

	// Upsert the session. country/region/city are populated only from trusted
	// reverse-proxy headers (see CollectRequest.Geo); VayuPress itself performs
	// no GeoIP lookups and retains no IP.
	var exists string
	_ = s.readDB().QueryRowContext(ctx, `SELECT id FROM analytics_sessions WHERE id=?`, sid).Scan(&exists)
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
		`INSERT INTO analytics_pageviews(id,session_id,url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,created_at,domain_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		eventID, sid, path, query, trunc(req.PageTitle, 300), referrerHostExtended(req.Referrer), trunc(host, 200),
		trunc(req.UTMSource, 100), trunc(req.UTMMedium, 100), trunc(req.UTMCampaign, 100), trunc(req.UTMContent, 100), trunc(req.UTMTerm, 100),
		eventType, eventName, time.Now().UTC(), domainID); err != nil {
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

// avgSessionSecondsSQL computes average visit duration in seconds: per session,
// the span from its first pageview to its last, averaged across sessions.
//
// This field was declared, returned in JSON, and written to the CSV export
// WITHOUT EVER BEING ASSIGNED — every install has always reported an average
// visit duration of exactly 0. That is worse than omitting it: 0 reads as a
// measurement ("everyone leaves instantly") rather than as "not measured", and
// there is no way to tell the two apart from the outside. The data to compute it
// was already being recorded the whole time; nothing ever queried it.
//
// Known limit, stated rather than hidden: with no exit beacon, the time spent on
// the LAST page of a visit is unmeasurable, so a single-pageview visit scores 0
// and every visit is undercounted by its final dwell. That is the same
// approximation the mainstream analytics products make, and single-pageview
// visits counting as 0 is also what makes this consistent with BounceRate above.
const avgSessionSecondsSQL = `(julianday(MAX(created_at))-julianday(MIN(created_at)))*86400.0`

// Overview holds aggregate stats for a date range.
type Overview struct {
	TotalPageviews int     `json:"total_pageviews"`
	UniqueVisitors int     `json:"unique_visitors"`
	TotalVisits    int     `json:"total_visits"`
	BounceRate     float64 `json:"bounce_rate"`
	// AvgDuration is the mean visit length in seconds. See avgSessionSecondsSQL
	// for what it can and cannot measure.
	AvgDuration float64 `json:"avg_duration"`
}

// OverviewSince returns aggregate analytics for the trailing N days.
func (s *Store) OverviewSince(ctx context.Context, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	o := &Overview{}
	// Pageviews and visits (sessions).
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND event_type=1`, from).
		Scan(&o.TotalPageviews, &o.TotalVisits)
	// Unique visitors counts distinct visitor_id (NOT sessions).
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=?`, from).
		Scan(&o.UniqueVisitors)
	// Bounce rate: share of sessions with exactly one pageview.
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(CASE WHEN v.cnt=1 THEN 100.0 ELSE 0.0 END),0) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? GROUP BY session_id) v`, from).
		Scan(&o.BounceRate)
	// Average visit duration.
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(v.dur),0) FROM (SELECT `+avgSessionSecondsSQL+` dur FROM analytics_pageviews WHERE created_at>=? GROUP BY session_id) v`, from).
		Scan(&o.AvgDuration)
	return o, nil
}

// OverviewBetween returns aggregate analytics for the half-open date window
// [fromInclusive, toExclusive), where both bounds are "YYYY-MM-DD" strings. It
// powers the dashboard's period-over-period percentage deltas by letting the
// caller fetch the immediately-preceding window of equal length.
func (s *Store) OverviewBetween(ctx context.Context, fromInclusive, toExclusive string) (*Overview, error) {
	o := &Overview{}
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND created_at<? AND event_type=1`, fromInclusive, toExclusive).
		Scan(&o.TotalPageviews, &o.TotalVisits)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=? AND created_at<?`, fromInclusive, toExclusive).
		Scan(&o.UniqueVisitors)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(CASE WHEN v.cnt=1 THEN 100.0 ELSE 0.0 END),0) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? AND created_at<? AND event_type=1 GROUP BY session_id) v`, fromInclusive, toExclusive).
		Scan(&o.BounceRate)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(v.dur),0) FROM (SELECT `+avgSessionSecondsSQL+` dur FROM analytics_pageviews WHERE created_at>=? AND created_at<? GROUP BY session_id) v`, fromInclusive, toExclusive).
		Scan(&o.AvgDuration)
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
	rows, err := s.readDB().QueryContext(ctx,
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
		`SELECT '/' || RTRIM(SUBSTR(url_path,2),'/') AS p,COUNT(1) as pv,COUNT(DISTINCT session_id) as uv
		 FROM analytics_pageviews WHERE created_at>=? AND event_type=1
		 GROUP BY '/' || RTRIM(SUBSTR(url_path,2),'/') ORDER BY pv DESC LIMIT ?`, from, limit)
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
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(1) as cnt FROM analytics_pageviews
		 WHERE created_at>=? AND event_type=1 AND referrer!=''`+notSelf+
			` GROUP BY referrer ORDER BY cnt DESC LIMIT ?`,
		append(append([]any{from}, selfArgs...), limit)...)
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
	rows, err := s.readDB().QueryContext(ctx,
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

// ── Acquisition channels ─────────────────────────────────────────────────────

// ReferrerChannel classifies a stored referrer host into a coarse acquisition
// channel: Direct (no referrer), Organic search, Social, or Referral. The
// classification is purely lexical over the host (no lookups, no PII) so it is
// cheap and offline. Exported so it can be unit-tested independently.
func ReferrerChannel(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "Direct"
	}
	h = strings.TrimPrefix(h, "www.")
	// Search engines → organic. Matched as substrings so regional TLDs
	// (google.co.uk, bing.de, …) all fold into one bucket.
	for _, s := range []string{
		"google.", "bing.", "duckduckgo.", "yahoo.", "yandex.", "baidu.",
		"ecosia.", "startpage.", "qwant.", "search.brave.", "naver.",
		"seznam.", "sogou.", "ask.com", "search.marginalia",
	} {
		if strings.Contains(h, s) {
			return "Organic search"
		}
	}
	// Social / community platforms.
	for _, s := range []string{
		"facebook.", "fb.com", "instagram.", "t.co", "twitter.", "x.com",
		"linkedin.", "lnkd.in", "reddit.", "youtube.", "youtu.be",
		"pinterest.", "tiktok.", "mastodon", "threads.net", "t.me",
		"telegram", "whatsapp", "vk.com", "tumblr.", "quora.",
		"news.ycombinator.com", "lobste.rs", "bsky.app", "bluesky",
	} {
		if strings.Contains(h, s) {
			return "Social"
		}
	}
	return "Referral"
}

// Channels groups pageviews into acquisition channels (Direct / Organic search /
// Social / Referral) by classifying the stored referrer host. All aggregate and
// no-PII. Empty buckets are omitted; the result is sorted by count descending.
func (s *Store) Channels(ctx context.Context, days int) ([]AudienceStat, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(1) FROM analytics_pageviews WHERE created_at>=? GROUP BY referrer`, from)
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
		buckets[ReferrerChannel(ref)] += cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]AudienceStat, 0, 4)
	for _, k := range []string{"Direct", "Organic search", "Social", "Referral"} {
		if buckets[k] > 0 {
			out = append(out, AudienceStat{Label: k, Count: buckets[k]})
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
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.readDB().QueryContext(ctx,
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
	rows, err := s.readDB().QueryContext(ctx,
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
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=?`, since).
		Scan(&rs.ActiveVisitors)

	if rows, err := s.readDB().QueryContext(ctx,
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
	if rows, err := s.readDB().QueryContext(ctx,
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
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT p.session_id) FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id WHERE p.created_at>=? AND COALESCE(s.country,'')=''`, since).
		Scan(&unknownCountry)
	if unknownCountry > 0 {
		rs.ActiveCountries = append(rs.ActiveCountries, AudienceStat{Label: "", Count: unknownCountry})
	}

	// How active visitors arrived (referrer host, recorded at ingest).
	//
	// The self-host exclusion belongs here for the same reason as the other two
	// referrer lists: internal navigation is not a referral. This is the third
	// place the same predicate was needed and the third place it was missing —
	// which is why they now share selfHostPatterns rather than each carrying a
	// copy of the WHERE clause.
	liveNotSelf, liveSelfArgs := s.selfHostExclusion("referrer")
	if rows, err := s.readDB().QueryContext(ctx,
		`SELECT referrer,COUNT(1) FROM analytics_pageviews
		 WHERE created_at>=? AND referrer!=''`+liveNotSelf+
			` GROUP BY referrer ORDER BY 2 DESC LIMIT 10`,
		append([]any{since}, liveSelfArgs...)...); err == nil {
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
	rows, err := s.readDB().QueryContext(ctx,
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
//
// Order-enforced single-pass evaluation (2025 audit: the old per-step
// COUNT(DISTINCT) let step counts exceed step zero — "conversion rates"
// above 100% — because nothing tied hits to a journey). One scan over the
// window's pageviews walks each session's events chronologically, advancing
// a step pointer on every match, so a visitor can only ever reach step N
// after passing steps 0..N-1 IN ORDER.
func (s *Store) GetFunnel(ctx context.Context, id string) (*Funnel, []FunnelResult, error) {
	var f Funnel
	var stepsJSON string
	err := s.readDB().QueryRowContext(ctx,
		`SELECT id,name,steps_json,time_window,created_at FROM analytics_funnels WHERE id=?`, id).
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
		stepPaths[i] = normalizePathExtended(st.URLPath)
	}

	rows, err := s.readDB().QueryContext(ctx,
		`SELECT session_id,url_path FROM analytics_pageviews WHERE created_at>=? AND event_type=1 ORDER BY session_id,created_at`,
		since)
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
		if normalizePathExtended(path) == stepPaths[curStep] {
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

// ListFunnels returns all funnel definitions.
func (s *Store) ListFunnels(ctx context.Context) ([]Funnel, error) {
	rows, err := s.readDB().QueryContext(ctx,
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

// Retention returns weekly cohort retention.
//
// Unbiased by construction (2025 audit: the old newest-50k-row sample
// anchored "first seen" inside the sample, silently re-aging every veteran
// visitor and warping every cohort). First-seen comes from a global
// MIN(created_at) per visitor via one grouped query over idx_asession_visitor;
// only ACTIVITY days are window-bounded. Day comparisons are lexicographic on
// ISO dates — no per-day time.Parse in the aggregation loop (the old code
// re-parsed each visitor's day set once per week).
func (s *Store) Retention(ctx context.Context, weeks int) ([]CohortRow, error) {
	// Hard-clamp the request-controlled window to a fixed maximum. weeks is only
	// ever used as a loop/slice bound below — never as an allocation size.
	if weeks <= 0 || weeks > maxRetentionWeeks {
		weeks = maxRetentionWeeks
	}
	now := time.Now().UTC()
	windowStart := now.AddDate(0, 0, -(weeks*7 - 1)).Format("2006-01-02")

	// Pass 1 — global first-seen for every visitor active in the window:
	// one GROUP BY over the visitor index, no sampling.
	firstSeen := map[string]string{}
	frows, err := s.readDB().QueryContext(ctx,
		`SELECT s.visitor_id, MIN(x.created_at) FROM analytics_sessions s
		 JOIN analytics_sessions x ON x.visitor_id=s.visitor_id
		 WHERE s.created_at>=? GROUP BY s.visitor_id`, windowStart)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var vid, first string
		if err := frows.Scan(&vid, &first); err != nil {
			continue
		}
		firstSeen[vid] = first[:10] // ISO date prefix
	}
	if err := frows.Err(); err != nil {
		frows.Close()
		return nil, err
	}
	frows.Close()

	// Pass 2 — activity days inside the window (bounded rows, string keys).
	days := map[string]map[string]bool{}
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT visitor_id, DATE(created_at) FROM analytics_sessions WHERE created_at>=?`,
		windowStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var vid, day string
		if err := rows.Scan(&vid, &day); err != nil {
			continue
		}
		set, ok := days[vid]
		if !ok {
			set = map[string]bool{}
			days[vid] = set
		}
		set[day] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type agg struct {
		size  int
		weeks []int
	}
	cohorts := map[string]*agg{}
	for vid, daySet := range days {
		first, ok := firstSeen[vid]
		if !ok {
			continue
		}
		a, exists := cohorts[first]
		if !exists {
			// Allocate a fixed-size slice (constant, never request-sized); only
			// indices [1, weeks) are populated below.
			a = &agg{weeks: make([]int, maxRetentionWeeks)}
			cohorts[first] = a
		}
		a.size++
		for w := 1; w < weeks; w++ {
			wStart := now.AddDate(0, 0, -(weeks*7)).AddDate(0, 0, w*7).Format("2006-01-02")
			wEnd := now.AddDate(0, 0, -(weeks * 7)).AddDate(0, 0, (w+1)*7).Format("2006-01-02")
			for day := range daySet {
				if len(day) == 10 && day >= wStart && day < wEnd {
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

// OverviewSinceScoped is OverviewSince for one domain.
func (s *Store) OverviewSinceScoped(ctx context.Context, scope string, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, arg := domainClause(scope)
	o := &Overview{}

	args := append([]any{from}, arg...)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(1),COUNT(DISTINCT session_id) FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause,
		args...).Scan(&o.TotalPageviews, &o.TotalVisits)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(CASE WHEN v.cnt=1 THEN 100.0 ELSE 0.0 END),0) FROM (SELECT session_id,COUNT(1) cnt FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause+` GROUP BY session_id) v`,
		args...).Scan(&o.BounceRate)
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COALESCE(AVG(v.dur),0) FROM (SELECT `+avgSessionSecondsSQL+` dur FROM analytics_pageviews WHERE created_at>=?`+clause+` GROUP BY session_id) v`,
		args...).Scan(&o.AvgDuration)
	// Unique visitors come from the session table, which carries no domain, so
	// the distinct-session count above is used instead of an unscoped figure. An
	// unscoped visitor count beside scoped pageviews would be two different
	// populations presented as one.
	o.UniqueVisitors = o.TotalVisits
	return o, nil
}

// TopPagesScoped is TopPages for one domain.
func (s *Store) TopPagesScoped(ctx context.Context, scope string, days, limit int) ([]PageStat, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, arg := domainClause(scope)
	args := append([]any{from}, arg...)
	args = append(args, limit)

	rows, err := s.readDB().QueryContext(ctx,
		`SELECT '/' || RTRIM(SUBSTR(url_path,2),'/') AS p,COUNT(1) as pv,COUNT(DISTINCT session_id) as uv
		 FROM analytics_pageviews WHERE created_at>=? AND event_type=1`+clause+`
		 GROUP BY '/' || RTRIM(SUBSTR(url_path,2),'/') ORDER BY pv DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageStat
	for rows.Next() {
		var ps PageStat
		if err := rows.Scan(&ps.Path, &ps.Pageviews, &ps.UniqueVisitors); err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}
