package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// sessionCookie returns the vp_session cookie from a recorded response, or nil.
func sessionCookie(rr *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	return nil
}

// TestSetSessionCookieRememberPersistent verifies that "Remember me" issues a
// persistent cookie whose Max-Age equals the session TTL, so the sign-in
// survives a browser restart.
func TestSetSessionCookieRememberPersistent(t *testing.T) {
	rr := httptest.NewRecorder()
	SetSessionCookieRemember(rr, "tok-abc", true)

	c := sessionCookie(rr)
	if c == nil {
		t.Fatal("expected a session cookie to be set")
	}
	if c.Value != "tok-abc" {
		t.Errorf("cookie value = %q, want tok-abc", c.Value)
	}
	wantMaxAge := int(DefaultSessionTTL.Seconds())
	if c.MaxAge != wantMaxAge {
		t.Errorf("MaxAge = %d, want %d (persistent, survives browser close)", c.MaxAge, wantMaxAge)
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
}

// TestSetSessionCookieRememberSession verifies that leaving "Remember me"
// unchecked issues a browser-session cookie (no Max-Age / no Expires), so the
// browser drops it on close and the operator is logged out on the next visit.
func TestSetSessionCookieRememberSession(t *testing.T) {
	rr := httptest.NewRecorder()
	SetSessionCookieRemember(rr, "tok-xyz", false)

	c := sessionCookie(rr)
	if c == nil {
		t.Fatal("expected a session cookie to be set")
	}
	if c.MaxAge != 0 {
		t.Errorf("MaxAge = %d, want 0 (browser-session cookie, dropped on close)", c.MaxAge)
	}
	if !c.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero (browser-session cookie)", c.Expires)
	}
	// Even a session-scoped cookie must keep the hardened attributes.
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
}
