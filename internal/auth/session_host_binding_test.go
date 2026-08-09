// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// S8 — VERIFIED CLEAN, and pinned because the control is an ABSENCE.
//
// The cross-site map raised this: the sessions table is (token_hash, user_id,
// created_at, expires_at) with no host column, and Validate takes no host
// parameter. Read on its own that says an operator's console session is valid on
// every hostname this install answers for — including a client's.
//
// It is not, and the reason is not in the table. vp_session is written with no
// Domain attribute, which under RFC 6265 makes it a HOST-ONLY cookie: the
// browser sends it back to the exact host that set it and to nothing else, not
// even a subdomain. An operator who signs in at their own hostname never
// presents that cookie to client.example, so Validate is never reached there —
// SessionTokenFromRequest returns "" and the resolution stops before any query.
//
// WHY THIS FILE EXISTS ANYWAY. The binding holds because a field is not set. It
// is invisible in review, it has no test, and it is one plausible, well-meaning
// line from being gone: setting Domain to a parent so the session also works on
// mail.<host> and mcp.<host> would widen the cookie to every subdomain at once.
// (It could not reach a client's domain even then — a Domain attribute only
// widens to a parent of the setting host — but the operator's own subdomains
// include the MCP vhost, which is provisioned with the CDN proxy deliberately
// off.) A control that exists only as an omission is the shape this audit track
// keeps finding on the losing side; this one is on the winning side and stays
// there.

// TestTheConsoleSessionCookieIsHostOnly asserts the property on the artifact —
// the Set-Cookie header a browser actually receives — rather than on the source
// that produces it.
func TestTheConsoleSessionCookieIsHostOnly(t *testing.T) {
	for _, c := range []struct {
		name  string
		write func(http.ResponseWriter)
	}{
		{"login", func(w http.ResponseWriter) { SetSessionCookieRemember(w, "tok", true) }},
		{"login without remember", func(w http.ResponseWriter) { SetSessionCookieRemember(w, "tok", false) }},
		{"logout", func(w http.ResponseWriter) { ClearSessionCookie(w) }},
	} {
		rec := httptest.NewRecorder()
		c.write(rec)

		raw := rec.Header().Get("Set-Cookie")
		if raw == "" {
			t.Fatalf("%s: no Set-Cookie header written", c.name)
		}
		if strings.Contains(strings.ToLower(raw), "domain=") {
			t.Fatalf("%s: the console session cookie carries a Domain attribute (%q). That makes "+
				"it a domain cookie sent to every subdomain of the value, and the host binding "+
				"of an operator session is precisely that this cookie goes back to one host and "+
				"nowhere else", c.name, raw)
		}

		// Parse it as a client would, and confirm the fields that keep it from
		// travelling. Secure and HttpOnly are asserted elsewhere; SameSite=Strict
		// is here because it is the second half of "this cookie does not go
		// anywhere it was not set" — it is not sent on a cross-site navigation.
		resp := http.Response{Header: rec.Header()}
		cookies := resp.Cookies()
		if len(cookies) != 1 {
			t.Fatalf("%s: expected exactly one cookie, got %d", c.name, len(cookies))
		}
		got := cookies[0]
		if got.Name != SessionCookie {
			t.Fatalf("%s: wrote %q, want %q", c.name, got.Name, SessionCookie)
		}
		if got.Domain != "" {
			t.Errorf("%s: Domain = %q, want empty (host-only)", c.name, got.Domain)
		}
		if got.SameSite != http.SameSiteStrictMode {
			t.Errorf("%s: SameSite = %v, want Strict", c.name, got.SameSite)
		}
	}
}

// The cookie's host scoping is the WHOLE binding, so a second way to present the
// same token would go around it entirely. A header or query fallback is exactly
// the convenience that would do it: an attacker-supplied header is not subject
// to any browser cookie rule, and a token in a URL leaks through referrers,
// logs and shared links.
func TestASessionTokenIsAcceptedFromTheCookieAndNowhereElse(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://client.example/?vp_session=stolen", nil)
	r.Header.Set("Authorization", "Bearer stolen")
	r.Header.Set("X-Session-Token", "stolen")
	r.Header.Set(SessionCookie, "stolen")

	if got := SessionTokenFromRequest(r); got != "" {
		t.Fatalf("a session token was accepted from something other than the cookie (%q). The "+
			"host binding of a console session is the browser's host-only cookie rule, and a "+
			"second channel is not subject to it", got)
	}

	// The cookie itself must still work, or this test passes by refusing
	// everything — which would be a broken product, not a secure one.
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "real"})
	if got := SessionTokenFromRequest(r); got != "real" {
		t.Fatalf("the session cookie is no longer read: got %q", got)
	}
}
