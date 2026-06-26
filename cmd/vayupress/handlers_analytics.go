package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
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
	if w.count >= l.limit {
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
window.VayuPress=window.VayuPress||{};window.VayuPress.track=function(n,d){send(base(2,n,d||{}))};
}();`)
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
	if err := a.analytics.Collect(r.Context(), req, ip, r.UserAgent()); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// geoFromHeaders extracts coarse visitor location from trusted reverse-proxy
// headers. VayuPress performs no GeoIP lookup and bundles no GeoIP database; if
// the operator fronts VayuPress with a proxy that injects these headers (e.g.
// Cloudflare's CF-IPCountry), the data is recorded — otherwise it stays empty.
// No IP is ever stored. Country is normalised to an uppercase ISO alpha-2 code;
// Cloudflare's "XX"/"T1" placeholders (unknown / Tor) are dropped.
func geoFromHeaders(r *http.Request) analytics.GeoInfo {
	pick := func(keys ...string) string {
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
	country := strings.ToUpper(pick(
		"CF-IPCountry",              // Cloudflare (all plans)
		"CloudFront-Viewer-Country", // AWS CloudFront
		"X-Vercel-IP-Country",       // Vercel
		"Fastly-Geo-Country",        // Fastly (when configured)
		"X-Geo-Country", "X-Country", "X-AppEngine-Country",
	))
	if country == "XX" || country == "T1" || len(country) != 2 {
		country = ""
	}
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
	return analytics.GeoInfo{Country: country, Region: region, City: city}
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
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.Realtime(r.Context())
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
	Code  string `json:"code"`
	Name  string `json:"name"`
	Flag  string `json:"flag"`  // served SVG URL, or "" when unavailable
	Label string `json:"label"` // = Name; kept so older cached JS still shows text
	Count int    `json:"count"`
}

// realtimeResponse is the live-analytics payload: identical to the store's
// RealtimeStats except countries carry a name + flag URL for direct display.
type realtimeResponse struct {
	ActiveVisitors  int                      `json:"active_visitors"`
	ActivePages     []analytics.RealtimePage `json:"active_pages"`
	ActiveCountries []realtimeCountry        `json:"active_countries"`
	ActiveReferrers []analytics.AudienceStat `json:"active_referrers"`
	WindowMinutes   int                      `json:"window_minutes"`
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

func (a *App) handleAnalyticsRetention(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	data, err := a.analytics.Retention(r.Context(), queryInt(r, "weeks", 12))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
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
	id, err := a.analytics.CreateGoal(r.Context(), in.Name, in.Kind, in.Target)
	if err != nil {
		writeAPIError(w, r, 400, "validation_error", err.Error(), "")
		return
	}
	writeJSON(w, r, 201, map[string]string{"id": id, "name": in.Name})
}

func (a *App) handleAnalyticsDeleteGoal(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.analytics.DeleteGoal(r.Context(), id); err != nil {
		writeAPIError(w, r, 404, "not_found", err.Error(), "")
		return
	}
	writeJSON(w, r, 200, map[string]bool{"deleted": true})
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
