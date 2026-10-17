// SPDX-License-Identifier: Apache-2.0

package main

// analytics_served.go — a page view counted where the page is served.
//
// Only pages VayuPress draws itself carry the beacon (vp-analytics.js). A site
// that serves its own uploaded files (customsite) or a template website
// (sitedoc) sent none, so on johal.in eleven of fourteen sites had never
// recorded a visit, and each one's Analytics page read nothing at all (row 5,
// step 0.0). Rather than write a script into a site's own files, the server
// counts the view as it serves the page, which is design decision 2 of the
// row 5 plan.
//
// The beacon only ever ran in a browser, so the count here admits only what
// VayuShield classifies as a person: a crawler fetches pages but never runs the
// script, and counting its fetches would put every crawler into a site's
// figures. The other rules are the beacon's own (handleAnalyticsCollect): an
// opt-out drops the view, a refused country does not count, one address has
// the beacon's budget, and the domain is the one this install resolved, never
// one the request names.

import (
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// recordServedPage counts one view of the page r asked for.
func (a *App) recordServedPage(r *http.Request) {
	if a.analytics == nil || a.vayuShield == nil || r.Method != http.MethodGet || dntHonored(r) {
		return
	}
	// A prefetch or prerender is the browser guessing, not a reader arriving.
	if r.Header.Get("Sec-Purpose") != "" || r.Header.Get("Purpose") == "prefetch" {
		return
	}
	if a.vayuShield.Classify(r).Result.ClientType != botdb.TypeHuman {
		return
	}
	geo := geoFromHeaders(r)
	if a.analyticsGeoRefused(geo.Country) {
		return
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	q := r.URL.Query()
	req := analytics.CollectRequest{
		URL:         r.URL.RequestURI(),
		Referrer:    r.Referer(),
		Hostname:    host,
		UTMSource:   q.Get("utm_source"),
		UTMMedium:   q.Get("utm_medium"),
		UTMCampaign: q.Get("utm_campaign"),
		UTMContent:  q.Get("utm_content"),
		UTMTerm:     q.Get("utm_term"),
		EventType:   1,
		Geo:         geo,
		Lang:        r.Header.Get("Accept-Language"),
	}
	// The beacon's per-address budget, shared: a view counted here is one the
	// beacon would have sent, so one address gets one budget for both.
	ip := loginClientIP(r)
	if !analyticsLimiter.allow(ip) {
		return
	}
	a.analytics.CollectAsync(req, ip, r.UserAgent(), a.contentScope(r))
}

// isPagePath reports whether an uploaded site's path names a page rather than
// one of its assets: a directory (its index.html), or an .html file.
func isPagePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case "", ".html", ".htm":
		return true
	}
	return false
}

// servedStatus remembers the status a handler answered with, so a view is
// counted for a page that was served (200, or 304 to a browser revalidating
// it) and not for a redirect to it or a missing file.
type servedStatus struct {
	http.ResponseWriter
	code int
}

func (s *servedStatus) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *servedStatus) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *servedStatus) served() bool {
	return s.code == http.StatusOK || s.code == http.StatusNotModified
}
