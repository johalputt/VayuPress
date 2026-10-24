// SPDX-License-Identifier: Apache-2.0

package main

// analytics_view.go — which site an Analytics page or request is about.
//
// Each site has its own Analytics: the primary at /os/analytics, a hosted
// site at /os/d/{id}/analytics. Every site added together is the install's
// aggregate, an administrator's view under Platform at /os/platform/analytics,
// and the install-wide API. One request is about exactly one of the three, and
// the handlers shared by them read it here rather than each deciding again.

import (
	"context"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/alerts"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
)

// Where each view's pages are.
const (
	primaryAnalyticsBase  = "/os/analytics"
	platformAnalyticsBase = "/os/platform/analytics"
)

// analyticsView is the site a request is about.
type analyticsView struct {
	// scope is whose visits are counted: a hosted site's id, "" the
	// primary's (the value the ingest writes for it), or every site's.
	scope string
	// defs is whose goals, funnels and segments are listed and written: a
	// hosted site's own, or the install's ('*'), which are the operator's,
	// counted over the primary's visits on its page and every site's on
	// Platform.
	defs string
	// alert is whose alerts: a hosted site's, the primary's (""), or the
	// install's, which count every site together.
	alert string
	// base is the view's page; its explorer, live panel and exports are
	// under it.
	base string
	// d is the hosted site, when the view is one.
	d      domain.Domain
	hosted bool
}

// all reports whether the view adds up every site.
func (v analyticsView) all() bool { return v.scope == analytics.ScopeAllDomains }

// viewOf is the site r is about: the hosted site its path names, the primary
// for a path under /os/analytics, or else every site (Platform, the
// install-wide console API and the public API).
func viewOf(r *http.Request) analyticsView {
	if d, ok := osScopedDomain(r); ok {
		return hostedView(d)
	}
	if r.URL.Path == primaryAnalyticsBase || strings.HasPrefix(r.URL.Path, primaryAnalyticsBase+"/") {
		return primaryView
	}
	return platformView
}

// The primary's view and every site's.
var (
	primaryView  = analyticsView{scope: "", defs: analytics.ScopeAllDomains, alert: "", base: primaryAnalyticsBase}
	platformView = analyticsView{scope: analytics.ScopeAllDomains, defs: analytics.ScopeAllDomains, alert: alerts.Install, base: platformAnalyticsBase}
)

// hostedView is a hosted site's view.
func hostedView(d domain.Domain) analyticsView {
	return analyticsView{scope: d.ID, defs: d.ID, alert: d.ID, base: "/os/d/" + d.ID + "/analytics", d: d, hosted: true}
}

// title is the view's page heading: a hosted site's Visitors, the primary's
// Analytics (the rail's name for it), or every site's.
func (v analyticsView) title() string {
	switch {
	case v.hosted:
		return "Visitors"
	case v.all():
		return platformAnalyticsTitle
	}
	return "Analytics"
}

// alertsAPI is where the view's alerts are written.
func (v analyticsView) alertsAPI() string {
	switch {
	case v.hosted:
		return "/os/d/" + v.d.ID + "/api/analytics/alerts"
	case v.all():
		return "/os/api/analytics/alerts"
	}
	return primaryAnalyticsBase + "/alerts"
}

// goalsAPI and segmentsAPI are where the view's goals and segments are
// written: a hosted site's own, or the install's for the primary and every
// site.
func (v analyticsView) goalsAPI() string {
	if v.hosted {
		return v.base + "/goals"
	}
	return "/os/api/analytics/goals"
}

func (v analyticsView) segmentsAPI() string {
	if v.hosted {
		return "/os/d/" + v.d.ID + "/api/analytics/segments"
	}
	return "/os/api/analytics/segments"
}

// primaryHost is the primary site's host, as its pages name it.
func (a *App) primaryHost(ctx context.Context) string {
	if a.domains != nil {
		if d, ok := a.domains.Primary(ctx); ok && d.Host != "" {
			return d.Host
		}
	}
	return config.Cfg.Domain
}
