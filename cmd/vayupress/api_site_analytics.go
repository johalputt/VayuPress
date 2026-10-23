// SPDX-License-Identifier: Apache-2.0

package main

// api_site_analytics.go — one site's analytics over the API (row 5, §17):
// /api/v1/sites/{site}/analytics/…, where {site} is a site's id or its host.
//
// The install-wide /api/v1/analytics family adds up every site, so a key
// bound to one site is refused it (keyMayCall). This family is the one such a
// key may read, and it reads only the site the key is bound to: any other
// site answers 404, the same as a site that does not exist, so a key cannot
// learn which other sites an install hosts. An install key with the analytics
// grant reads any site. Every figure comes from the readers the console's
// own per-site page uses, so the API and the page never disagree.

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/domain"
)

type ctxSiteAnalyticsKey struct{}

// siteAnalyticsScope is the site a request under /api/v1/sites/{site} reads:
// the domain, and the scope its events are stored under (the primary's are
// stored under "").
type siteAnalyticsScope struct {
	d     domain.Domain
	scope string
}

// siteAnalyticsRoutes mounts the per-site analytics API on r, a router that
// already authenticates and checks the key's analytics grant.
func (a *App) siteAnalyticsRoutes(r chi.Router) {
	r.Route("/api/v1/sites/{site}/analytics", func(sr chi.Router) {
		sr.Use(a.apiSiteScope)
		sr.Get("/overview", a.handleSiteAnalyticsOverview)
		sr.Get("/realtime", a.handleSiteAnalyticsRealtime)
		sr.Get("/pages", a.handleSiteAnalyticsPages)
		sr.Get("/sources", a.handleSiteAnalyticsSources)
		sr.Get("/events", a.handleSiteAnalyticsEvents)
		sr.Get("/goals", a.handleSiteAnalyticsGoals)
		sr.Get("/funnels", a.handleSiteAnalyticsFunnels)
		sr.Get("/funnels/{fid}", a.handleSiteAnalyticsFunnel)
		sr.Get("/performance", a.handleSiteAnalyticsPerformance)
	})
}

// apiSiteScope resolves {site} and refuses a key bound to another site.
func (a *App) apiSiteScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.analytics == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
			return
		}
		d, ok := a.siteByIDOrHost(r.Context(), chi.URLParam(r, "site"))
		if ki, keyed := auth.KeyInfoFromContext(r.Context()); keyed && ki.DomainID != "" && (!ok || ki.DomainID != d.ID) {
			ok = false
		}
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "no-site", "no such site", "")
			return
		}
		sc := siteAnalyticsScope{d: d, scope: d.ID}
		if d.IsPrimary {
			sc.scope = ""
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxSiteAnalyticsKey{}, sc)))
	})
}

// siteByIDOrHost finds a registered site by its id, or else by its host.
func (a *App) siteByIDOrHost(ctx context.Context, site string) (domain.Domain, bool) {
	if a.domains == nil || site == "" {
		return domain.Domain{}, false
	}
	if d, err := a.domains.ByID(ctx, site); err == nil {
		return d, true
	}
	if d, err := a.domains.Resolve(ctx, site); err == nil {
		return d, true
	}
	return domain.Domain{}, false
}

// siteScopeOf is the scope apiSiteScope resolved for r.
func siteScopeOf(r *http.Request) siteAnalyticsScope {
	sc, _ := r.Context().Value(ctxSiteAnalyticsKey{}).(siteAnalyticsScope)
	return sc
}

// siteDays is the ?days= window, 1 to 365, 30 when absent.
func siteDays(r *http.Request) int {
	d := queryInt(r, "days", 30)
	if d < 1 {
		return 1
	}
	if d > 365 {
		return 365
	}
	return d
}

// windowBefore is the range of days days just before the trailing window of
// the same length, as OverviewBetweenScoped takes it.
func windowBefore(days int) (from, to string) {
	now := time.Now().UTC()
	return now.AddDate(0, 0, -(2*days - 1)).Format("2006-01-02"), now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}

// siteHead is what every answer says about what it covers.
type siteHead struct {
	Site string `json:"site"`
	Host string `json:"host"`
	Days int    `json:"days,omitempty"`
}

func headOf(sc siteAnalyticsScope, days int) siteHead {
	return siteHead{Site: sc.d.ID, Host: sc.d.Host, Days: days}
}

func (a *App) handleSiteAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	ov, err := a.analytics.OverviewSinceScoped(r.Context(), sc.scope, days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	from, to := windowBefore(days)
	prev, _ := a.analytics.OverviewBetweenScoped(r.Context(), sc.scope, from, to)
	series, _ := a.analytics.PageviewSeriesScoped(r.Context(), sc.scope, days)
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Overview *analytics.Overview      `json:"overview"`
		Previous *analytics.Overview      `json:"previous"`
		Series   []analytics.DayPageviews `json:"series"`
	}{headOf(sc, days), ov, prev, series})
}

func (a *App) handleSiteAnalyticsRealtime(w http.ResponseWriter, r *http.Request) {
	a.writeRealtime(w, r, siteScopeOf(r).scope)
}

func (a *App) handleSiteAnalyticsPages(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	top, err := a.analytics.TopPagesScoped(r.Context(), sc.scope, days, 50)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	landing, _ := a.analytics.LandingPagesScoped(r.Context(), sc.scope, days, 50)
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Top     []analytics.PageStat    `json:"top"`
		Landing []analytics.LandingStat `json:"landing"`
	}{headOf(sc, days), top, landing})
}

func (a *App) handleSiteAnalyticsSources(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	refs, err := a.analytics.TopReferrersScoped(r.Context(), sc.scope, days, 50)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	channels, _ := a.analytics.ChannelsScoped(r.Context(), sc.scope, days)
	utm, _ := a.analytics.UTMStatsScoped(r.Context(), sc.scope, days)
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Referrers []analytics.ReferrerStat `json:"referrers"`
		Channels  []analytics.AudienceStat `json:"channels"`
		Campaigns []analytics.UTMStat      `json:"campaigns"`
	}{headOf(sc, days), refs, channels, utm})
}

func (a *App) handleSiteAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	events, err := a.analytics.CustomEventsScoped(r.Context(), sc.scope, days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Events []analytics.EventStat `json:"events"`
	}{headOf(sc, days), events})
}

func (a *App) handleSiteAnalyticsGoals(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	goals, err := a.analytics.GoalResultsScoped(r.Context(), sc.scope, days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Goals []analytics.GoalResult `json:"goals"`
	}{headOf(sc, days), goals})
}

func (a *App) handleSiteAnalyticsFunnels(w http.ResponseWriter, r *http.Request) {
	sc := siteScopeOf(r)
	funnels, err := a.analytics.ListFunnelsScoped(r.Context(), sc.scope)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Funnels []analytics.Funnel `json:"funnels"`
	}{headOf(sc, 0), funnels})
}

func (a *App) handleSiteAnalyticsFunnel(w http.ResponseWriter, r *http.Request) {
	sc := siteScopeOf(r)
	f, results, err := a.analytics.GetFunnelScoped(r.Context(), sc.scope, chi.URLParam(r, "fid"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "funnel not found", "")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		Funnel  *analytics.Funnel        `json:"funnel"`
		Results []analytics.FunnelResult `json:"results"`
	}{headOf(sc, 0), f, results})
}

func (a *App) handleSiteAnalyticsPerformance(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	type vitals struct {
		LCPMs   int     `json:"lcp_ms_p75"`
		INPMs   int     `json:"inp_ms_p75"`
		CLS     float64 `json:"cls_p75"`
		Samples int64   `json:"samples"`
	}
	out := struct {
		siteHead
		Vitals *vitals `json:"vitals"`
	}{siteHead: headOf(sc, days)}
	if a.vaEngagement != nil {
		if v, err := a.vaEngagement.WebVitalsP75Scoped(r.Context(), sc.scope, days); err == nil && v != nil && v.Samples > 0 {
			out.Vitals = &vitals{LCPMs: v.P75LCPMs, INPMs: v.P75INPMs, CLS: float64(v.P75CLSX100) / 100, Samples: v.Samples}
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}
