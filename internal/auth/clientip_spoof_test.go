// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// ClientIP decides who a request "is". Everything that defends the site keys on
// it — the rate limiter, the reputation jail, auto-block, and the auth lockout —
// so a client able to choose its own answer is not rate-limited, not jailed, and
// not locked out however many passwords it tries.
//
// The hole was specific and easy to miss: CF-Connecting-IP was honoured whenever
// the immediate peer was a TRUSTED PROXY, and on every ordinary install the peer
// is the local nginx, trusted because TRUSTED_PROXIES defaults to loopback. nginx
// forwards unknown request headers untouched, so the header arrived attacker-
// controlled and was believed. It applied whether or not the "behind a CDN"
// switch was on, since that switch only widened which peers count as trusted.

func withTrustedLoopback(t *testing.T) {
	t.Helper()
	saved := config.Cfg.TrustedProxies
	savedCF := config.TrustCloudflareEnabled()
	t.Cleanup(func() {
		config.Cfg.TrustedProxies = saved
		config.SetTrustCloudflare(savedCF)
	})
	_, v4, _ := net.ParseCIDR("127.0.0.0/8")
	_, v6, _ := net.ParseCIDR("::1/128")
	config.Cfg.TrustedProxies = []*net.IPNet{v4, v6}
}

// proxiedRequest models what the app receives from a local nginx that sets
// X-Real-IP / X-Forwarded-For from the real connection and passes everything else
// through — i.e. deploy/nginx-vayupress.conf.
func proxiedRequest(realIP string, attackerHeaders map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	for k, v := range attackerHeaders {
		r.Header.Set(k, v)
	}
	// proxy_set_header OVERWRITES, so these are nginx's word, not the client's.
	r.Header.Set("X-Real-IP", realIP)
	r.Header.Set("X-Forwarded-For", realIP)
	return r
}

func TestClientIPRejectsForgedEdgeHeadersBehindLocalProxy(t *testing.T) {
	withTrustedLoopback(t)
	const real = "203.0.113.50"

	for _, behindCDN := range []bool{false, true} {
		config.SetTrustCloudflare(behindCDN)
		for _, hdr := range []string{"CF-Connecting-IP", "True-Client-IP"} {
			got := ClientIP(proxiedRequest(real, map[string]string{hdr: "1.2.3.4"}))
			if got != real {
				t.Errorf("behind_cdn=%v, forged %s: ClientIP = %q, want %q — the client chose its own identity",
					behindCDN, hdr, got, real)
			}
		}
	}
}

// TestClientIPHonoursEdgeHeaderOnlyFromAGenuineEdge — the legitimate case must
// still work: when the origin is reached DIRECTLY by Cloudflare (no local proxy),
// the peer really is an edge address and the header really is the visitor.
func TestClientIPHonoursEdgeHeaderOnlyFromAGenuineEdge(t *testing.T) {
	saved := config.Cfg.TrustedProxies
	savedCF := config.TrustCloudflareEnabled()
	t.Cleanup(func() {
		config.Cfg.TrustedProxies = saved
		config.SetTrustCloudflare(savedCF)
	})
	config.Cfg.TrustedProxies = nil
	config.SetTrustCloudflare(true)

	// 172.64.0.0/13 is a published Cloudflare range.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.64.10.10:443"
	r.Header.Set("CF-Connecting-IP", "203.0.113.50")
	if got := ClientIP(r); got != "203.0.113.50" {
		t.Errorf("ClientIP = %q, want the visitor 203.0.113.50 — a genuine edge peer must still be honoured", got)
	}

	// ...and with the switch off, even a genuine edge peer is not trusted for it.
	config.SetTrustCloudflare(false)
	if got := ClientIP(r); got == "203.0.113.50" {
		t.Error("edge header honoured while 'behind a CDN' is switched off")
	}
}

// TestClientIPIgnoresHeadersFromAnUntrustedPeer is the pre-existing guarantee
// (audit F-3) — kept pinned so the fix above cannot be "simplified" into
// re-trusting a direct connection.
func TestClientIPIgnoresHeadersFromAnUntrustedPeer(t *testing.T) {
	withTrustedLoopback(t)
	config.SetTrustCloudflare(true)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.7:33333" // a direct visitor, not a proxy
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.Header.Set("X-Real-IP", "1.2.3.4")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := ClientIP(r); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want the real peer 198.51.100.7", got)
	}
}

// TestClientIPXForwardedForPrependIsDefeated — the other classic: a client sends
// its own X-Forwarded-For and nginx APPENDS the real address
// ($proxy_add_x_forwarded_for). Walking right-to-left is what makes the real
// address win; walking left-to-right would hand the attacker the answer.
func TestClientIPXForwardedForPrependIsDefeated(t *testing.T) {
	withTrustedLoopback(t)
	config.SetTrustCloudflare(false)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	// Attacker sent "1.2.3.4"; nginx appended the real peer.
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.50")
	r.Header.Del("X-Real-IP")

	if got := ClientIP(r); got != "203.0.113.50" {
		t.Errorf("ClientIP = %q, want 203.0.113.50 — a prepended XFF entry was believed", got)
	}
}
