// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// THE ROW THAT DECIDES ALL OF THIS WAS COMPARING A VALUE WITH ITSELF.
//
// realIPMiddleware is second in the chain and does r.RemoteAddr =
// auth.ClientIP(r). Every handler downstream therefore sees a RemoteAddr that
// has ALREADY been resolved. shieldResolvesVisitorIP then asks
//
//	auth.ClientIP(r) != stripPort(r.RemoteAddr)
//
// which re-runs the same resolver over its own output. On a working install the
// second pass finds a peer that is a real visitor — not a trusted proxy, so no
// header is honoured — and returns it unchanged. The two sides match, and the
// row reports FAILURE on an install where resolution is perfect.
//
// This is the exact shape already recorded as a lesson in this repository: a
// test that calls the function under test to judge its output compares the code
// with itself. It was sitting in the product, deciding a posture row, and every
// conclusion drawn from that row this session rests on it.
func TestTheRealIPRowSurvivesTheMiddlewareThatRewritesRemoteAddr(t *testing.T) {
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
	forgetCDNSightings(t)

	// An install where nginx IS resolving the reader: the local proxy is the
	// peer and hands over a real visitor address, exactly as it does once the
	// hardening control has taken effect.
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.RemoteAddr = "127.0.0.1:53000"
	req.Header.Set("X-Real-IP", "203.0.113.55")

	var resolved bool
	realIPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved, _ = shieldResolvesVisitorIP(r)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if !resolved {
		t.Fatal("resolution WORKED and the row reports that it did not.\n\n" +
			"realIPMiddleware already rewrote RemoteAddr to the resolved address, so asking " +
			"auth.ClientIP again and comparing is a value against itself: the second pass sees a " +
			"real visitor as the peer, honours no header, and returns it unchanged.\n\n" +
			"Every judgement made from this row — including the country-rule warning built on " +
			"it — is unfounded while this holds.")
	}
}

// The other direction, which must keep working: a genuinely unresolved request
// still reports unresolved. A fix that makes the row always green would be worse
// than the bug.
func TestAnUnresolvedRequestStillReportsUnresolved(t *testing.T) {
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
	forgetCDNSightings(t)

	// The local proxy forwards nothing: no header, so nothing to resolve.
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.RemoteAddr = "127.0.0.1:53000"

	var resolved bool
	realIPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved, _ = shieldResolvesVisitorIP(r)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if resolved {
		t.Error("a request carrying no forwarded address was reported as resolving to a visitor")
	}
}

// THE FAILURE THAT HAD NO DETECTOR: resolution "worked" and landed on the edge.
//
// nginx's real_ip_header naming a header the CDN does not send leaves
// $remote_addr as the edge, and the vhost then forwards THAT in X-Real-IP. From
// inside the app a forwarding header was honoured and the address did change —
// so a peer comparison says resolved. It changed to the wrong thing: every
// per-IP control is still metering the CDN, and every country lookup still
// returns the edge's location.
//
// This is the situation on the install that reported the whole problem, and
// nothing in the product could see it.
func TestAnAddressInsideTheCDNsOwnRangesIsNotTheReader(t *testing.T) {
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
	forgetCDNSightings(t)

	// 172.64.0.0/13 is one of Cloudflare's published ranges.
	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.RemoteAddr = "127.0.0.1:53000"
	req.Header.Set("X-Real-IP", "172.64.12.34")

	var resolved bool
	realIPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved, _ = shieldResolvesVisitorIP(r)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if resolved {
		t.Error("an address inside Cloudflare's published ranges was accepted as the reader's.\n\n" +
			"The header was honoured and the value changed, so a peer comparison is satisfied — " +
			"but every per-IP limit is still counting the edge and every country lookup still " +
			"returns the edge's location. That is the state the reporting install was in, and " +
			"nothing could see it.")
	}
}

// And a reader who genuinely arrives through that same edge must still count.
// Refusing every proxied request would make the row permanently red on exactly
// the installs it exists to serve.
func TestARealReaderBehindTheSameEdgeStillCounts(t *testing.T) {
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
	forgetCDNSightings(t)

	req := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	req.RemoteAddr = "127.0.0.1:53000"
	req.Header.Set("X-Real-IP", "203.0.113.55") // a reader, not an edge

	var resolved bool
	realIPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved, _ = shieldResolvesVisitorIP(r)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if !resolved {
		t.Error("a real reader's address behind the proxy was rejected")
	}
}

// ASKED WITHOUT A REQUEST, THE REPORT SAID FAIL AND CALLED IT A MEASUREMENT.
//
// vayushield_posture over the connector calls shieldAuditInputs(nil), and the
// comment there says the client-IP row "reports from recent visitor traffic
// rather than from a request that does not exist". It did not. ClientIPResolved
// stayed at its zero value, so on any proxied install the row could only ever
// say FAIL — and an entire investigation was run off that row, through five
// releases, before anyone noticed it was not a measurement at all.
func TestTheNilRequestPathReportsFromRealVisitorTraffic(t *testing.T) {
	restore := func() (prevOK bool, prevAt int64) {
		return visitorResolvedOK.Load(), visitorResolvedUnix.Load()
	}
	ok0, at0 := restore()
	t.Cleanup(func() { visitorResolvedOK.Store(ok0); visitorResolvedUnix.Store(at0) })

	a := newShieldApp(t, "on")

	for _, tc := range []struct {
		name        string
		observed    bool
		wantResolve bool
	}{
		{"visitors resolve to their own addresses", true, true},
		{"visitors do not resolve", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			visitorResolvedOK.Store(tc.observed)
			visitorResolvedUnix.Store(timeNowUnixForTest())

			// Through the REAL nil path, not the helper it happens to call — the
			// helper was never the broken part.
			in := a.shieldAuditInputs(nil)
			if in.ClientIPResolved != tc.wantResolve {
				t.Errorf("ClientIPResolved = %v, want %v.\n\n"+
					"Asked without a request, the report must say what ordinary traffic showed. "+
					"Leaving it at the zero value pins the row to FAIL on every proxied install, "+
					"which is what it did through five releases of diagnosis.",
					in.ClientIPResolved, tc.wantResolve)
			}
			if !in.ClientIPFromVisitorTraffic {
				t.Error("the answer is not marked as coming from visitor traffic, so the row " +
					"claims to have measured the request that asked — and there was none")
			}
		})
	}

	// And with nothing observed, it must NOT invent an answer.
	visitorResolvedUnix.Store(0)
	if _, seen := lastVisitorResolution(); seen {
		t.Error("an unobserved install reported an observation")
	}
}

func timeNowUnixForTest() int64 { return time.Now().Unix() }
