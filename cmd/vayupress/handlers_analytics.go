package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
)

// GET /static/vp-analytics.js — serves the tracking script.
func (a *App) handleAnalyticsScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprint(w, `!function(){
"use strict";
var salt=new Date().toISOString().slice(0,10);
function h(s){var h=0,i,c;for(i=0;i<s.length;i++){c=s.charCodeAt(i);h=((h<<5)-h)+c;h|=0}return'h'+Math.abs(h).toString(36)}
function vid(){var s=localStorage.getItem('vp_vid');if(s)return s;var v=h(navigator.userAgent+salt+Math.random());localStorage.setItem('vp_vid',v);return v}
function sid(){var s=sessionStorage.getItem('vp_sid');if(s)return s;var v='s'+Date.now().toString(36)+Math.random().toString(36).slice(2,7);sessionStorage.setItem('vp_sid',v);return v}
function br(){var u=navigator.userAgent;if(u.indexOf('Firefox')>-1)return'Firefox';if(u.indexOf('Edg')>-1)return'Edge';if(u.indexOf('Chrome')>-1)return'Chrome';if(u.indexOf('Safari')>-1)return'Safari';return'Other'}
function osf(){var u=navigator.userAgent;if(u.indexOf('Win')>-1)return'Windows';if(u.indexOf('Mac')>-1)return'Mac';if(u.indexOf('Linux')>-1)return'Linux';if(u.indexOf('Android')>-1)return'Android';if(u.indexOf('iPhone')>-1||u.indexOf('iPad')>-1)return'iOS';return'Other'}
function dv(){var w=screen.width;if(w<=600)return'Mobile';if(w<=1024)return'Tablet';return'Desktop'}
function utm(){var p=new URLSearchParams(window.location.search);return{utm_source:p.get('utm_source')||'',utm_medium:p.get('utm_medium')||'',utm_campaign:p.get('utm_campaign')||'',utm_content:p.get('utm_content')||'',utm_term:p.get('utm_term')||''}}
function send(d){try{var x=new XMLHttpRequest();x.open('POST','/api/v1/analytics/collect',true);x.setRequestHeader('Content-Type','application/json');x.send(JSON.stringify(d))}catch(e){}}
function pv(){var u=utm();send({u:location.pathname+location.search,r:document.referrer||'',t:document.title,h:location.hostname,sc:screen.width+'x'+screen.height,sl:navigator.language||'',br:br(),os:osf(),dv:dv(),utm_source:u.utm_source,utm_medium:u.utm_medium,utm_campaign:u.utm_campaign,utm_content:u.utm_content,utm_term:u.utm_term,event_type:1,event_name:'',vid:vid(),sid:sid()})}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',pv)}else{pv()}
document.addEventListener('click',function(e){var el=e.target.closest('[data-vp-event]');if(!el)return;var n=el.getAttribute('data-vp-event');var d={};Array.from(el.attributes).forEach(function(a){if(a.name.indexOf('data-vp-')===0&&a.name!=='data-vp-event'){d[a.name.slice(8)]=a.value}});var u=utm();send({u:location.pathname,r:document.referrer||'',t:document.title,h:location.hostname,sc:screen.width+'x'+screen.height,sl:navigator.language||'',br:br(),os:osf(),dv:dv(),utm_source:u.utm_source,utm_medium:u.utm_medium,utm_campaign:u.utm_campaign,utm_content:u.utm_content,utm_term:u.utm_term,event_type:2,event_name:n,event_data:d,vid:vid(),sid:sid()})});
window.VayuPress=window.VayuPress||{};window.VayuPress.track=function(n,d){var u=utm();send({u:location.pathname,r:document.referrer||'',t:document.title,h:location.hostname,sc:screen.width+'x'+screen.height,sl:navigator.language||'',br:br(),os:osf(),dv:dv(),utm_source:u.utm_source,utm_medium:u.utm_medium,utm_campaign:u.utm_campaign,utm_content:u.utm_content,utm_term:u.utm_term,event_type:2,event_name:n,event_data:d||{},vid:vid(),sid:sid()})};
}();`)
}

// GET /api/v1/admin/analytics?days=30&limit=20
// Returns the cookieless, privacy-first page-view summary.
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

// POST /api/v1/analytics/collect (public — no auth)
func (a *App) handleAnalyticsCollect(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(205)
		return
	}
	var req analytics.CollectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(200)
		return
	}
	if err := a.analytics.Collect(r.Context(), req); err != nil {
		w.WriteHeader(200)
		return
	}
	w.WriteHeader(204)
}

// GET /api/v1/analytics/overview?days=14 (protected)
func (a *App) handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	sum, err := a.analytics.OverviewSince(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, sum)
}

// GET /api/v1/analytics/pageviews?days=14 (protected)
func (a *App) handleAnalyticsPageviews(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.PageviewSeries(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/pages?days=14&limit=20 (protected)
func (a *App) handleAnalyticsPages(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	limit := queryInt(r, "limit", 20)
	data, err := a.analytics.TopPages(r.Context(), days, limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/referrers?days=14&limit=20 (protected)
func (a *App) handleAnalyticsReferrers(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	limit := queryInt(r, "limit", 20)
	data, err := a.analytics.TopReferrers(r.Context(), days, limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/countries?days=14 (protected)
func (a *App) handleAnalyticsCountries(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.Countries(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/browsers?days=14 (protected)
func (a *App) handleAnalyticsBrowsers(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.Browsers(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/devices?days=14 (protected)
func (a *App) handleAnalyticsDevices(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.Devices(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/utm?days=14 (protected)
func (a *App) handleAnalyticsUTM(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.UTMStats(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/events?days=14 (protected)
func (a *App) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 14)
	data, err := a.analytics.CustomEvents(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/realtime (protected)
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
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/sessions?days=7&limit=50 (protected)
func (a *App) handleAnalyticsSessions(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 7)
	limit := queryInt(r, "limit", 50)
	data, err := a.analytics.RecentSessions(r.Context(), days, limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/funnels (protected)
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

// POST /api/v1/analytics/funnels (protected)
func (a *App) handleAnalyticsCreateFunnel(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	var in struct {
		Name       string             `json:"name"`
		Steps      []analytics.FunnelStep `json:"steps"`
		TimeWindow int                `json:"time_window"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
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

// GET /api/v1/analytics/funnels/{id} (protected)
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

// GET /api/v1/analytics/retention?weeks=12 (protected)
func (a *App) handleAnalyticsRetention(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	weeks := queryInt(r, "weeks", 12)
	data, err := a.analytics.Retention(r.Context(), weeks)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/revenue?days=30 (protected)
func (a *App) handleAnalyticsRevenue(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	days := queryInt(r, "days", 30)
	data, err := a.analytics.RevenueStats(r.Context(), days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// POST /api/v1/analytics/revenue (protected)
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
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

// GET /api/v1/analytics/replays?limit=50 (protected)
func (a *App) handleAnalyticsReplays(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	limit := queryInt(r, "limit", 50)
	data, err := a.analytics.ListReplays(r.Context(), limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, data)
}

// GET /api/v1/analytics/replays/{id} (protected)
func (a *App) handleAnalyticsGetReplay(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	rep, events, err := a.analytics.GetReplay(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, 404, "not_found", "replay not found", "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"replay": rep, "events": events})
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
