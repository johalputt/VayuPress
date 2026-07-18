package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleOSLoginRendersFormWhenAnonymous verifies that an unauthenticated
// GET /os/login renders the sign-in form (200) rather than redirecting. With no
// session store wired, hasValidConsoleSession must report false safely.
func TestHandleOSLoginRendersFormWhenAnonymous(t *testing.T) {
	a := &App{} // no sessions/userStore → treated as signed out
	req := httptest.NewRequest(http.MethodGet, "/os/login", nil)
	rec := httptest.NewRecorder()

	a.handleOSLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (login form for an anonymous visitor)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("anonymous visitor must not be redirected, got Location %q", loc)
	}
	if !strings.Contains(rec.Body.String(), "Sign in") {
		t.Error("expected the sign-in form to render for an anonymous visitor")
	}
	// The login page MUST be uncacheable. Without no-store the browser can
	// heuristically cache the rendered form and serve it on a later visit without
	// hitting the server, so the "already signed in → redirect" check never runs
	// and a logged-in operator keeps seeing the login page. This is the exact
	// caching defect behind the reported bug — guard it.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("login page Cache-Control = %q, want it to contain no-store", cc)
	}
}

// TestOSLoginPageRememberCheckbox verifies the sign-in page renders the
// "Remember me" control wired to the remember form field, checked by default
// (the seamless persistent posture), and stays CSP-safe (no inline styles or
// external asset hosts).
func TestOSLoginPageRememberCheckbox(t *testing.T) {
	out := osLoginPage("", "", "")
	assertCSPSafe(t, "osLoginPage", out)

	if !strings.Contains(out, `name="remember"`) {
		t.Error("login page missing the remember form field")
	}
	if !strings.Contains(out, `type="checkbox"`) {
		t.Error("login page missing the remember checkbox input")
	}
	// Default posture is persistent: the box ships checked so a returning
	// operator stays signed in across browser restarts unless they opt out.
	idx := strings.Index(out, `name="remember"`)
	if idx < 0 {
		t.Fatal("remember field not found")
	}
	// The `checked` attribute sits on the same <input> tag as name="remember".
	tagStart := strings.LastIndex(out[:idx], "<input")
	tagEnd := strings.Index(out[idx:], ">")
	if tagStart < 0 || tagEnd < 0 {
		t.Fatal("could not isolate the remember input tag")
	}
	tag := out[tagStart : idx+tagEnd]
	if !strings.Contains(tag, "checked") {
		t.Errorf("remember checkbox should be checked by default, tag = %q", tag)
	}
	if !strings.Contains(out, "Remember me") {
		t.Error("login page missing the human-readable Remember me label")
	}
}

// TestIsLocalURL is the open-redirect guard for the post-login return path (used
// by the OAuth authorize bounce). Only site-rooted same-origin paths pass.
func TestIsLocalURL(t *testing.T) {
	ok := []string{"/os", "/oauth/authorize?client_id=x&state=y", "/os/connector"}
	for _, s := range ok {
		if !isLocalURL(s) {
			t.Errorf("safe local path %q should be accepted", s)
		}
	}
	bad := []string{
		"", "os", "https://evil.com", "//evil.com", "/\\evil.com",
		"http://evil.com", "/x://evil", "/a\tb", "javascript:alert(1)",
	}
	for _, s := range bad {
		if isLocalURL(s) {
			t.Errorf("unsafe next %q must be rejected", s)
		}
	}
}
