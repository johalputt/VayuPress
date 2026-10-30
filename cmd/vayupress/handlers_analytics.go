// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/worldmap"
)

// ── Ingest rate limiting ─────────────────────────────────────────────────────

// analyticsIngestLimiter is a small fixed-window per-IP limiter that protects
// the public, unauthenticated collect endpoint from storage-exhaustion abuse.
// It keeps no PII — only a coarse IP key and a count — and evicts stale windows.
type ingestLimiter struct {
	mu      sync.Mutex
	windows map[string]*ingestWindow
	limit   int
	window  time.Duration
}

type ingestWindow struct {
	count int
	start time.Time
}

func newIngestLimiter(limit int, window time.Duration) *ingestLimiter {
	return &ingestLimiter{windows: make(map[string]*ingestWindow), limit: limit, window: window}
}

// allow reports whether the key may record another event in the current window.
func (l *ingestLimiter) allow(key string) bool {
	return l.allowLimit(key, l.limit)
}

// allowLimit is allow with a per-call budget, so one limiter can serve keys with
// individual limits (VayuAPI per-key rate_per_min) while sharing window state.
func (l *ingestLimiter) allowLimit(key string, limit int) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) > l.window {
		l.windows[key] = &ingestWindow{count: 1, start: now}
		// Opportunistic eviction to bound memory under churn.
		if len(l.windows) > 4096 {
			for k, v := range l.windows {
				if now.Sub(v.start) > l.window {
					delete(l.windows, k)
				}
			}
		}
		return true
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}

// analyticsLimiter caps each client IP to 120 collect events per minute.
var analyticsLimiter = newIngestLimiter(120, time.Minute)

// ── Tracking script ──────────────────────────────────────────────────────────

// GET /static/vp-analytics.js — serves the privacy-first tracking script.
//
// The script sets NO cookies and writes NO identifier to localStorage or
// sessionStorage. Visitor/session identity is derived server-side from a
// daily-rotating salted hash that stores no PII (see internal/analytics).
func (a *App) handleAnalyticsScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// An hour, not the year versioned scripts get (publicAssetCache): what this
	// script sends follows the engagement-beacon setting below, and a copy
	// kept for a year would go on measuring readers after the operator turned
	// the beacon off.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprint(w, `!function(){
"use strict";
function utm(){var p=new URLSearchParams(window.location.search);return{utm_source:p.get('utm_source')||'',utm_medium:p.get('utm_medium')||'',utm_campaign:p.get('utm_campaign')||'',utm_content:p.get('utm_content')||'',utm_term:p.get('utm_term')||''}}
function send(d){try{var b=JSON.stringify(d);if(navigator.sendBeacon){navigator.sendBeacon('/api/v1/analytics/collect',new Blob([b],{type:'application/json'}));return}var x=new XMLHttpRequest();x.open('POST','/api/v1/analytics/collect',true);x.setRequestHeader('Content-Type','application/json');x.send(b)}catch(e){}}
function base(t,n,d){var u=utm();return{u:location.pathname+location.search,r:document.referrer||'',t:document.title,h:location.hostname,utm_source:u.utm_source,utm_medium:u.utm_medium,utm_campaign:u.utm_campaign,utm_content:u.utm_content,utm_term:u.utm_term,event_type:t,event_name:n||'',event_data:d||undefined}}
function pv(){send(base(1,''))}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',pv)}else{pv()}
document.addEventListener('click',function(e){var el=e.target.closest('[data-vp-event]');if(!el)return;var n=el.getAttribute('data-vp-event');var d={};Array.from(el.attributes).forEach(function(a){if(a.name.indexOf('data-vp-')===0&&a.name!=='data-vp-event'){d[a.name.slice(8)]=a.value}});send(base(2,n,d))});
var FILES=/\.(pdf|zip|gz|tgz|rar|7z|dmg|exe|msi|apk|epub|mobi|docx?|xlsx?|pptx?|odt|ods|odp|csv|txt|mp3|mp4|m4a|mov|wav|ogg|webm)$/i;
function link(e){if(e.type==='auxclick'&&e.button!==1)return;var a=e.target.closest&&e.target.closest('a[href]');if(!a)return;var u;try{u=new URL(a.href,location.href)}catch(x){return}if(u.protocol!=='http:'&&u.protocol!=='https:')return;var to=u.origin+u.pathname;if(a.hasAttribute('download')||FILES.test(u.pathname)){send(base(5,to));return}if(u.host!==location.host){send(base(4,to))}}
document.addEventListener('click',link,true);document.addEventListener('auxclick',link,true);
window.VayuPress=window.VayuPress||{};window.VayuPress.track=function(n,d){send(base(2,n,d||{}))};
}();`)
	// VayuAnalytics Enterprise engagement beacon (time on page / scroll depth).
	// Appended to the already-injected analytics script — cookieless, no PII —
	// so it needs no extra <script> tag in the theme. Operator-toggleable.
	if a.siteSettings != nil && a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyAnalyticsBeacon) != "off" {
		fmt.Fprint(w, vaEngagementJS)
	}
	// The click map's recorder, on a site whose operator turned it on
	// (analytics_clickmap.go); the hour's cache above bounds how long a
	// copy sends after it is turned off, and the endpoint refuses those.
	if n := a.clickMapSample(r.Context(), a.contentScope(r)); n > 0 {
		fmt.Fprint(w, clickMapJS(n))
	}
}

// ── Legacy privacy-first summary (unchanged) ─────────────────────────────────

// GET /api/v1/admin/analytics?days=30&limit=20
func (a *App) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	sum, err := a.analytics.Since(r.Context(), days, limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, sum)
}

// ── Public ingest (no auth) ──────────────────────────────────────────────────

// dntHonored reports whether this beacon carries an explicit tracking
// opt-out signal: DNT: 1 or Sec-GPC: 1. VayuPress is cookieless and stores no
// PII, so honoring it is a courtesy rather than a legal necessity — but a
// visitor who flipped either switch asked not to be measured, and "we are
// privacy-friendly" should include the people who verify (2025 plan Wave 4).
func dntHonored(r *http.Request) bool {
	return r.Header.Get("DNT") == "1" || strings.EqualFold(r.Header.Get("Sec-GPC"), "1")
}

// POST /api/v1/analytics/collect
//
// Unauthenticated by design (it ingests visitor beacons). It is hardened with a
// strict body-size cap and per-IP rate limiting, and it derives visitor/session
// identity server-side without persisting the IP or User-Agent.
func (a *App) handleAnalyticsCollect(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// An explicit browser-level opt-out drops the measurement entirely rather
	// than storing it differently.
	if dntHonored(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ip := loginClientIP(r)
	if !analyticsLimiter.allow(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var req analytics.CollectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		// Swallow malformed beacons silently; never leak detail to the public.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Geo is set server-side from trusted proxy headers, never from the beacon.
	req.Geo = geoFromHeaders(r)
	req.Lang = r.Header.Get("Accept-Language")
	// Only a reader is counted, by the rule every ingest path shares
	// (countsAsReader). This beacon used to drop bad bots alone, so a
	// verified crawler or an AI agent that ran the page's script was counted
	// as a visitor here and not by the server's own count.
	if a.vayuShield != nil {
		if !countsAsReader(a.vayuShield.Classify(r).Result.ClientType, true) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	// An operator's "never serve" country does not get to write into their own
	// analytics. The page path refuses these visitors outright — verified by test,
	// including that a solved challenge does not escape it — so a beacon claiming
	// to be a page view from there did not come from a page this site served.
	if a.analyticsGeoRefused(req.Geo.Country) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The domain is resolved by THIS INSTALL from the host it served, never from
	// the beacon body. This endpoint is public and unauthenticated: a domain a
	// visitor could name is a domain a visitor could write traffic into.
	// Normalisation happens here (identity needs the request), then the event
	// is queued in memory — the batched flusher owns every database write, so
	// beacon volume no longer serialises on the single writer connection.
	a.analytics.CollectAsync(req, ip, r.UserAgent(), a.contentScope(r))
	w.WriteHeader(http.StatusNoContent)
}

// geoFromHeaders extracts coarse visitor location. It prefers trusted
// reverse-proxy headers (e.g. Cloudflare's CF-IPCountry) when the operator
// fronts VayuPress with a CDN. When no proxy header supplies a country — the
// common case for a sovereign VPS deployment with no CDN — it falls back to an
// offline, embedded IP→country lookup (internal/geoip), so live analytics show
// real countries out of the box with no external service and no GeoIP database
// to download. Region/city still come only from proxy headers. No IP is ever
// stored; the fallback uses the trusted-proxy-aware client IP purely for an
// in-process lookup. Country is an uppercase ISO alpha-2 code; Cloudflare's
// "XX"/"T1" placeholders (unknown / Tor) are dropped.
func geoFromHeaders(r *http.Request) analytics.GeoInfo {
	// A proxy header is worth what the peer that sent it is worth.
	//
	// Every header below is a statement by a CDN about somebody else, and it was
	// being read from whoever happened to connect. The ingest endpoint that
	// consumes this is public, unauthenticated and on the shield's bypass list,
	// so a client could post beacons declaring any country it liked and the
	// operator's audience report would show it — including the country they had
	// just refused, which is precisely the sort of contradiction that sends
	// someone looking for a fault in the shield.
	//
	// Gated on the same trust ClientIP requires, and no further: with a local
	// reverse proxy in front the peer is that proxy, so ordinary installs are
	// unaffected and keep the country their CDN reports. Untrusted peers fall
	// through to the offline lookup below, which for a direct connection reads
	// the real address and is the more accurate answer anyway.
	trusted := auth.PeerIsTrustedProxy(r)
	pick := func(keys ...string) string {
		if !trusted {
			return ""
		}
		for _, k := range keys {
			if v := strings.TrimSpace(r.Header.Get(k)); v != "" {
				return v
			}
		}
		return ""
	}
	// Some proxies (notably Vercel) URL-encode city/region values ("San%20Jose").
	decode := func(s string) string {
		if strings.IndexByte(s, '%') >= 0 {
			if d, err := url.QueryUnescape(s); err == nil {
				return strings.TrimSpace(d)
			}
		}
		return s
	}
	// THE SAME resolver the shield uses, not a second copy of the rule.
	//
	// These were two independent implementations of "which country is this
	// visitor in", and on a live install they disagreed for a week: this side
	// reported a country as 91% of the audience while the enforcement side had
	// never seen a single request from it. An operator refused that country,
	// watched it keep arriving, and was right to conclude something was broken —
	// it was the disagreement itself.
	ip := auth.ClientIP(r)
	country := requestCountry(r, ip)
	region := decode(pick(
		"CF-Region",                             // Cloudflare (full name, e.g. "California")
		"CloudFront-Viewer-Country-Region-Name", // AWS CloudFront (name)
		"CloudFront-Viewer-Country-Region",      // AWS CloudFront (code)
		"X-Vercel-IP-Country-Region",            // Vercel (code)
		"X-Geo-Region", "X-AppEngine-Region",
	))
	city := decode(pick(
		"CF-IPCity",              // Cloudflare
		"CloudFront-Viewer-City", // AWS CloudFront
		"X-Vercel-IP-City",       // Vercel (URL-encoded)
		"X-Geo-City", "X-City", "X-AppEngine-City",
	))
	// No city from the edge: the downloaded city table, when it is on, which
	// also places the city on the map. An edge's city has no coordinate.
	g := analytics.GeoInfo{Country: country, Region: region, City: city}
	if city == "" {
		if p := tableCity(ip, country); p.City != "" {
			g.City = p.City
			if region == "" {
				g.Region = p.Region
			}
			g.Lat, g.Lon, g.Located = p.Lat, p.Lon, p.Located
		}
	}
	return g
}

// ── Protected extended endpoints ─────────────────────────────────────────────

func (a *App) handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.OverviewSince(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsPageviews(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.PageviewSeries(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsPages(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.TopPages(r.Context(), queryInt(r, "days", 14), queryInt(r, "limit", 20))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsReferrers(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.TopReferrers(r.Context(), queryInt(r, "days", 14), queryInt(r, "limit", 20))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsBrowsers(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.Browsers(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsDevices(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.Devices(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsOS(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.OperatingSystems(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsUTM(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.UTMStats(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.CustomEvents(r.Context(), queryInt(r, "days", 14))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsRealtime(w http.ResponseWriter, r *http.Request) {
	a.writeRealtime(w, r, analytics.ScopeAllDomains)
}

// handleOSScopedRealtime is one site's last five minutes, for the live panel
// on that site's Analytics: the primary's at /os/analytics/realtime, a hosted
// site's at /os/d/{id}/analytics/realtime (admin-only with the rest of
// /os/d). Every site's is /os/api/analytics/realtime, an administrator's.
func (a *App) handleOSScopedRealtime(w http.ResponseWriter, r *http.Request) {
	a.writeRealtime(w, r, viewOf(r).scope)
}

// writeRealtime answers the live panel for scope.
func (a *App) writeRealtime(w http.ResponseWriter, r *http.Request, scope string) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	// The live panel of a narrowed page counts those readers alone.
	data, err := a.analytics.RealtimeScoped(a.narrowed(r.Context(), focusOf(r.URL.Query())), scope)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// Re-shape live countries for the client: send the full name + a self-hosted
	// flag-SVG URL (resolved here because the country-name table and flag assets
	// live in the cmd layer), keeping the analytics store presentation-free.
	resp := realtimeResponse{WindowMinutes: 5}
	if data != nil {
		resp.ActiveVisitors = data.ActiveVisitors
		resp.ActivePages = data.ActivePages
		resp.ActiveReferrers = data.ActiveReferrers
		resp.ActiveDevices = data.ActiveDevices
		resp.ActiveBrowsers = data.ActiveBrowsers
		resp.ActiveChannels = data.ActiveChannels
		resp.ActiveByMinute = data.ActiveByMinute
		resp.Countries = data.Countries
		for _, c := range data.ActiveCities {
			name := c.City
			if cn := countryName(c.Country); cn != "" {
				name += ", " + cn
			}
			city := realtimeCountry{Code: c.Country, Key: c.City, Name: name, Flag: countryFlagURL(c.Country), Label: name, Count: c.Count}
			if c.Located {
				// Placed where the map draws it, by the map's own projection,
				// to the tenth of a unit the drawn dots are.
				x, y := worldmap.Project(c.Lat, c.Lon)
				city.X, city.Y = math.Round(x*10)/10, math.Round(y*10)/10
				city.Located = true
			}
			resp.ActiveCities = append(resp.ActiveCities, city)
		}
		if data.WindowMinutes > 0 {
			resp.WindowMinutes = data.WindowMinutes
		}
		for _, c := range data.ActiveCountries {
			name := countryName(c.Label)
			if strings.TrimSpace(c.Label) == "" {
				name = "Unknown"
			}
			resp.ActiveCountries = append(resp.ActiveCountries, realtimeCountry{
				Code:  c.Label,
				Key:   c.Label,
				Name:  name,
				Flag:  countryFlagURL(c.Label),
				Label: name, // back-compat for older cached clients reading "label"
				Count: c.Count,
			})
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// realtimeCountry is the display-enriched live country row sent to the browser.
type realtimeCountry struct {
	Code string `json:"code"`
	// Key is what a filter on the row is given: the country's code, or the
	// city's own name (Name is "City, Country").
	Key   string `json:"key,omitempty"`
	Name  string `json:"name"`
	Flag  string `json:"flag"`  // served SVG URL, or "" when unavailable
	Label string `json:"label"` // = Name; kept so older cached JS still shows text
	Count int    `json:"count"`
	// X and Y are a placed city's point on the live map (worldmap.Project).
	X       float64 `json:"x,omitempty"`
	Y       float64 `json:"y,omitempty"`
	Located bool    `json:"located,omitempty"`
}

// realtimeResponse is the live-analytics payload: identical to the store's
// RealtimeStats except countries carry a name + flag URL for direct display.
type realtimeResponse struct {
	ActiveVisitors  int                      `json:"active_visitors"`
	ActivePages     []analytics.RealtimePage `json:"active_pages"`
	ActiveCountries []realtimeCountry        `json:"active_countries"`
	ActiveCities    []realtimeCountry        `json:"active_cities"` // Name is "City, Country"; Flag the country's
	ActiveDevices   []analytics.AudienceStat `json:"active_devices"`
	ActiveBrowsers  []analytics.AudienceStat `json:"active_browsers"`
	ActiveChannels  []analytics.AudienceStat `json:"active_channels"`
	ActiveReferrers []analytics.AudienceStat `json:"active_referrers"`
	WindowMinutes   int                      `json:"window_minutes"`
	ActiveByMinute  []int                    `json:"active_by_minute"`
	// Countries is each active country's pages and ways in, for the live
	// map's card.
	Countries map[string]analytics.CountryNow `json:"countries"`
}

func (a *App) handleAnalyticsSessions(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.RecentSessions(r.Context(), queryInt(r, "days", 7), queryInt(r, "limit", 50))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsFunnels(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.ListFunnels(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsCreateFunnel(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	var in struct {
		Name       string                 `json:"name"`
		Steps      []analytics.FunnelStep `json:"steps"`
		TimeWindow int                    `json:"time_window"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if in.Name == "" || len(in.Steps) < 2 {
		writeAPIError(w, r, 400, "validation_error", "name required and at least 2 steps", "")
		return
	}
	id, err := a.analytics.CreateFunnel(r.Context(), in.Name, in.Steps, in.TimeWindow)
	if err != nil {
		writeAPIError(w, r, 500, "db-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("analytics.funnel.create", dbpkg.AuditActor(r), id, in.Name)
	writeJSON(w, r, 201, map[string]string{"id": id, "name": in.Name})
}

func (a *App) handleAnalyticsGetFunnel(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	f, results, err := a.analytics.GetFunnel(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, 404, "not_found", "funnel not found", "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"funnel": f, "results": results})
}

// handleAnalyticsRetention answers that week-over-week retention cannot be
// measured here, and why. A visitor is recognised by a hash that changes
// every day (docs/ANALYTICS.md), so nobody can be seen to come back on a
// later day. The report this replaced drew cohorts from that hash anyway:
// every cohort's later weeks were empty by construction, and because its
// weeks were fixed dates rather than counted from each cohort, it reported
// each visitor as "retained" in the week of their only visit. The route
// stays, so a client reading it learns the reason rather than a 404.
func (a *App) handleAnalyticsRetention(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, retentionAnswer{
		Measurable: false,
		Identity:   "daily",
		Reason:     "A visitor is recognised by a salted hash that changes every day, so no visitor can be seen to return on a later day. Week-over-week retention would need an identity that follows a person across days, which this install does not keep.",
		Cohorts:    []struct{}{},
	})
}

// retentionAnswer is the retention route's whole answer.
type retentionAnswer struct {
	Measurable bool       `json:"measurable"`
	Identity   string     `json:"identity"`
	Reason     string     `json:"reason"`
	Cohorts    []struct{} `json:"cohorts"`
}

func (a *App) handleAnalyticsRevenue(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.RevenueStats(r.Context(), queryInt(r, "days", 30))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsRecordRevenue(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	var in struct {
		Amount    float64 `json:"amount"`
		Currency  string  `json:"currency"`
		OrderID   string  `json:"order_id"`
		EventName string  `json:"event_name"`
		SessionID string  `json:"session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	if in.Amount <= 0 {
		writeAPIError(w, r, 400, "validation_error", "amount must be positive", "")
		return
	}
	id, err := a.analytics.RecordRevenue(r.Context(), in.SessionID, in.Currency, in.OrderID, in.EventName, in.Amount)
	if err != nil {
		writeAPIError(w, r, 500, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, 201, map[string]string{"id": id})
}

// ── Goals (conversion targets) ───────────────────────────────────────────────

func (a *App) handleAnalyticsGoals(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.GoalResults(r.Context(), queryInt(r, "days", 30))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func (a *App) handleAnalyticsCreateGoal(w http.ResponseWriter, r *http.Request) {
	a.createGoal(w, r, analytics.ScopeAllDomains, "")
}

// handleOSScopedCreateGoal adds a goal to the site the URL names
// (/os/d/{id}/analytics/goals): it counts only that site's traffic.
func (a *App) handleOSScopedCreateGoal(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "no-site", "no such site", "")
		return
	}
	a.createGoal(w, r, d.ID, d.Host)
}

// handleOSScopedDeleteGoal removes one of the URL's site's goals; another
// site's goal, or the install's, is not found from here.
func (a *App) handleOSScopedDeleteGoal(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "no-site", "no such site", "")
		return
	}
	a.deleteGoal(w, r, d.ID, d.Host, chi.URLParam(r, "gid"))
}

// createGoal stores a goal for scope (a site, whose host is host, or the
// install) from the request body.
func (a *App) createGoal(w http.ResponseWriter, r *http.Request, scope, host string) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	var in struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "")
		return
	}
	id, err := a.analytics.CreateGoalScoped(r.Context(), scope, in.Name, in.Kind, in.Target)
	if err != nil {
		writeAPIError(w, r, 400, "validation_error", err.Error(), "")
		return
	}
	a.refreshDefinitionReports(scope, host)
	dbpkg.AuditLog("analytics.goal.create", dbpkg.AuditActor(r), id, goalAuditSite(scope)+in.Kind+" "+in.Target)
	writeJSON(w, r, 201, map[string]string{"id": id, "name": in.Name})
}

func (a *App) handleAnalyticsDeleteGoal(w http.ResponseWriter, r *http.Request) {
	a.deleteGoal(w, r, analytics.ScopeAllDomains, "", chi.URLParam(r, "id"))
}

// deleteGoal removes scope's goal id. Inside /os/d/{id} the site holds {id},
// so the scoped route names the goal {gid} and passes it here.
func (a *App) deleteGoal(w http.ResponseWriter, r *http.Request, scope, host, id string) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	if err := a.analytics.DeleteGoalScoped(r.Context(), scope, id); err != nil {
		writeAPIError(w, r, 404, "not_found", err.Error(), "")
		return
	}
	a.refreshDefinitionReports(scope, host)
	dbpkg.AuditLog("analytics.goal.delete", dbpkg.AuditActor(r), id, goalAuditSite(scope))
	writeJSON(w, r, 200, map[string]bool{"deleted": true})
}

// refreshDefinitionReports rebuilds the cached report that shows scope's goals
// and segments: the install's Analytics, or that site's (whose host is host).
func (a *App) refreshDefinitionReports(scope, host string) {
	if scope == analytics.ScopeAllDomains {
		// The install's definitions are listed on Platform's page and on the
		// primary's, each counted over its own visits.
		a.refreshSiteAnalytics(platformView, "every site")
		a.refreshSiteAnalytics(primaryView, a.primaryHost(context.Background()))
		return
	}
	a.refreshSiteAnalytics(hostedView(domain.Domain{ID: scope, Host: host}), host)
}

// goalAuditSite names the site a goal change was made on, for the audit line.
func goalAuditSite(scope string) string {
	if scope == analytics.ScopeAllDomains {
		return ""
	}
	return "site " + scope + ": "
}

// ── Visitor journey / path flows ─────────────────────────────────────────────

func (a *App) handleAnalyticsJourney(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.PathFlows(r.Context(), queryInt(r, "days", 14), queryInt(r, "limit", 100))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
