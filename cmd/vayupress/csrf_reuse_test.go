package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// The console used to mint a fresh CSRF token on EVERY page render. Opening a
// second tab — or just loading a page twice — overwrote the cookie while the
// first page's form still carried the previous token, so submitting that form
// failed with "CSRF token missing or invalid" and reloading did not help. The
// contributed fix in #319 corrected one handler; the same bug was in every admin
// page, because writeOSHTML minted unconditionally and had no request to read the
// existing cookie from.
//
// auth.CSRFTokenMiddleware has always had the right rule — reissue only when the
// cookie is missing, empty or invalid. csrfTokenFor is that rule for the HTML
// writers, so the two can no longer contradict each other.

// TestCSRFTokenIsReusedWhenStillValid pins the core behaviour.
func TestCSRFTokenIsReusedWhenStillValid(t *testing.T) {
	existing := auth.GenerateCSRFToken()
	if existing == "" {
		t.Fatal("could not mint a token to test with")
	}
	req := httptest.NewRequest(http.MethodGet, "/os", nil)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: existing})
	rec := httptest.NewRecorder()

	if got := csrfTokenFor(rec, req); got != existing {
		t.Errorf("csrfTokenFor returned a new token %q, want the existing %q", got, existing)
	}
	// The cookie is still rewritten, which refreshes its 1-hour lifetime without
	// changing the value — an idle tab must not expire out from under the operator.
	var seen bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vp_csrf" {
			seen = true
			if c.Value != existing {
				t.Errorf("cookie rewritten with %q, want the existing token", c.Value)
			}
		}
	}
	if !seen {
		t.Error("the CSRF cookie must still be written, to refresh its lifetime")
	}
}

// TestCSRFTokenIsMintedWhenAbsentOrInvalid covers the other half: a missing,
// empty or forged cookie must be replaced, or an operator whose token expired
// (the secret rotates on restart) would be stuck unable to submit anything.
func TestCSRFTokenIsMintedWhenAbsentOrInvalid(t *testing.T) {
	for _, tc := range []struct{ name, cookie string }{
		{"no cookie", ""},
		{"empty cookie", " "},
		{"forged cookie", "bm90LWEtdmFsaWQtdG9rZW4"},
		{"stale cookie", auth.GenerateCSRFToken() + "tampered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/os", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: strings.TrimSpace(tc.cookie)})
			}
			rec := httptest.NewRecorder()
			got := csrfTokenFor(rec, req)
			if got == "" || !auth.ValidateCSRFToken(got) {
				t.Fatalf("csrfTokenFor returned %q, want a freshly minted valid token", got)
			}
			if got == tc.cookie {
				t.Error("an unusable token must not be handed back")
			}
		})
	}
}

// TestNoHandlerMintsCSRFTokensDirectly is the regression guard that matters. The
// bug was not one handler getting it wrong; it was fifteen places each minting
// their own token with no way to see the browser's. Every writer must go through
// csrfTokenFor, so there is one rule rather than fifteen copies of one.
func TestNoHandlerMintsCSRFTokensDirectly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)
		// middleware.go holds csrfTokenFor itself — the one legitimate caller.
		if name == "middleware.go" {
			continue
		}
		if strings.Contains(src, "auth.GenerateCSRFToken()") {
			t.Errorf("%s calls auth.GenerateCSRFToken() directly — use csrfTokenFor(w, r), "+
				"or a second tab's form will 403 when this page rotates the cookie", name)
		}
	}
}

// TestWriteOSHTMLTakesTheRequest guards the signature that makes the fix possible:
// without the request there is no cookie to read, which is exactly how the whole
// console ended up rotating on every render.
func TestWriteOSHTMLTakesTheRequest(t *testing.T) {
	b, err := os.ReadFile("admin_os_ui.go")
	if err != nil {
		t.Fatalf("read admin_os_ui.go: %v", err)
	}
	if !strings.Contains(string(b), "func writeOSHTML(w http.ResponseWriter, r *http.Request, body string)") {
		t.Error("writeOSHTML must take the request, or it cannot reuse the browser's existing CSRF token")
	}
}
