// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/geoip"
)

// ONE COUNTRY PER REQUEST, FOR EVERY CONSUMER.
//
// A live install refused Singapore and kept seeing Singapore in Analytics.
// Neither side was broken: VayuShield resolved the country from an address
// through a table compiled into the binary, and Analytics read CF-IPCountry,
// which the edge computes per request. Two answers for one visitor, and no
// surface anywhere that could show they differed. Every control the operator
// could see was reading one source; every control that could refuse a visitor
// was reading the other.
//
// requestCountry is now the only place either side gets an answer.

// cdnCountryHeaders are the per-request country headers a CDN sets. Ordered by
// how specific the vendor is about the visitor rather than alphabetically.
var cdnCountryHeaders = []string{
	"CF-IPCountry",              // Cloudflare (all plans)
	"CloudFront-Viewer-Country", // AWS CloudFront
	"X-Vercel-IP-Country",       // Vercel
	"Fastly-Geo-Country",        // Fastly (when configured)
	"X-Geo-Country", "X-Country", "X-AppEngine-Country",
}

// countryDisagreements counts requests where the edge and the embedded table
// named DIFFERENT countries for the same visitor.
//
// It exists because that disagreement was invisible, and being invisible is what
// made it cost a week. A non-zero count means the table shipped in this binary
// is behind the edge's view of some address space — actionable, and the panel
// says so rather than leaving two screens quietly contradicting each other.
var (
	countryDisagreements atomic.Int64
	countryEdgeAnswers   atomic.Int64
	countryTableAnswers  atomic.Int64
)

// edgeCountryHeader returns the country a TRUSTED EDGE stated for this request.
//
// The trust check is the whole security of this function and is the same one
// auth.ClientIP applies before honouring a forwarding address.
//
// Audit H6: peer-trust alone was not enough. TRUSTED_PROXIES defaults to
// loopback, and on the standard install that loopback peer is the local nginx —
// which forwards unknown client headers untouched. Any visitor could therefore
// hand the server `CF-IPCountry: US` themselves and be judged, counted, and
// admitted under whatever country they named, including one the operator had
// refused. A genuine edge always connects from a public address, so country
// assertions from loopback/private peers are refused outright; the shipped
// proxy config blanks these headers as belt-and-braces (see
// deploy/nginx-vayupress.conf). Operators fronted by a real CDN add its public
// ranges to TRUSTED_PROXIES and keep working unchanged.
func edgeCountryHeader(r *http.Request) string {
	if !auth.PeerIsTrustedProxy(r) {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return "" // local proxy: it cannot vouch for a header the client may have set
	}
	for _, k := range cdnCountryHeaders {
		if v := strings.TrimSpace(r.Header.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

// requestCountry is the single answer to "what country is this visitor in".
//
// Signature matches vayushield.Config.CountryFn so the shield calls exactly what
// Analytics calls. ip is passed rather than re-derived because the shield has
// already narrowed it (port stripped) and re-deriving it here would be a second
// copy of that rule.
func requestCountry(r *http.Request, ip string) string {
	country, source := geoip.Resolve(edgeCountryHeader(r), ip)
	switch source {
	case geoip.SourceEdge:
		countryEdgeAnswers.Add(1)
		// Only meaningful when BOTH could answer. A table that simply does not
		// know an address is not disagreeing with anyone, and counting silence as
		// a conflict would bury the real ones.
		if table := geoip.Country(ip); table != "" && !strings.EqualFold(table, country) {
			countryDisagreements.Add(1)
		}
	case geoip.SourceTable:
		countryTableAnswers.Add(1)
	}
	return country
}

// countrySourceStats reports what has been answering, for the posture report.
func countrySourceStats() (edge, table, disagree int64) {
	return countryEdgeAnswers.Load(), countryTableAnswers.Load(), countryDisagreements.Load()
}
