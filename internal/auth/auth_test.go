package auth

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"golang.org/x/crypto/argon2"
)

func init() {
	// Populate required config without calling config.Load() (avoids MustEnv fatal).
	config.Cfg.APIKey = "test-key-abc"
	config.Cfg.PprofRateLimit = 5
	config.Cfg.Domain = "localhost"
	InitCSRFSecret()
}

func TestCSRFTokenRoundTrip(t *testing.T) {
	token := GenerateCSRFToken()
	if token == "" {
		t.Fatal("GenerateCSRFToken returned empty string")
	}
	if !ValidateCSRFToken(token) {
		t.Fatal("token should be valid immediately after generation")
	}
}

func TestCSRFTokenInvalid(t *testing.T) {
	if ValidateCSRFToken("") {
		t.Fatal("empty token should be invalid")
	}
	if ValidateCSRFToken("garbage-not-base64!!!") {
		t.Fatal("garbage token should be invalid")
	}
}

func TestAuthLockout(t *testing.T) {
	ip := "192.0.2.1"
	// Clear any existing state
	authFailMu.Lock()
	delete(authFailBuckets, ip)
	authFailMu.Unlock()

	locked, _ := CheckAuthLockout(ip)
	if locked {
		t.Fatal("fresh IP should not be locked")
	}
	// Record failures up to threshold
	for i := 0; i < authFailMax; i++ {
		RecordAuthFailure(ip)
	}
	locked, until := CheckAuthLockout(ip)
	if !locked {
		t.Fatal("IP should be locked after max failures")
	}
	if until.IsZero() {
		t.Fatal("lockout time should be set")
	}
	if time.Until(until) <= 0 {
		t.Fatal("lockout should be in the future")
	}
}

func TestAuthLockoutClearedOnSuccess(t *testing.T) {
	ip := "192.0.2.2"
	authFailMu.Lock()
	delete(authFailBuckets, ip)
	authFailMu.Unlock()

	for i := 0; i < authFailMax-1; i++ {
		RecordAuthFailure(ip)
	}
	RecordAuthSuccess(ip)
	locked, _ := CheckAuthLockout(ip)
	if locked {
		t.Fatal("IP should not be locked after success reset")
	}
}

func TestArgon2idRoundTrip(t *testing.T) {
	secret := "hunter2"
	encoded, err := HashSecretArgon2id(secret)
	if err != nil {
		t.Fatalf("HashSecretArgon2id: %v", err)
	}
	if !VerifySecretArgon2id(secret, encoded) {
		t.Fatal("verify should return true for correct secret")
	}
	if VerifySecretArgon2id("wrong", encoded) {
		t.Fatal("verify should return false for wrong secret")
	}
}

func TestArgon2idInvalidEncoding(t *testing.T) {
	if VerifySecretArgon2id("x", "") {
		t.Fatal("empty encoding should return false")
	}
	if VerifySecretArgon2id("x", "notvalid") {
		t.Fatal("encoding without $ separator should return false")
	}
}

func TestRequireAPIKeyMissing(t *testing.T) {
	handler := RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.10:1234" // distinct from lockout tests (TEST-NET-2)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("missing key: want 401, got %d", rr.Code)
	}
}

func TestRequireAPIKeyValid(t *testing.T) {
	handler := RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.11:1234"
	req.Header.Set("X-API-Key", "test-key-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("valid key: want 200, got %d", rr.Code)
	}
}

// TestRequireAPIKeyEmptyConfigRejects guards the defense-in-depth branch: when
// no API key is configured, requests must never authenticate — not even an
// empty presented key (which a naive == comparison would have let through).
func TestRequireAPIKeyEmptyConfigRejects(t *testing.T) {
	prev := config.Cfg.APIKey
	config.Cfg.APIKey = ""
	defer func() { config.Cfg.APIKey = prev }()

	handler := RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	for i, presented := range []string{"", "anything"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", 20+i)
		if presented != "" {
			req.Header.Set("X-API-Key", presented)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 401 {
			t.Fatalf("empty configured key with presented=%q: want 401, got %d", presented, rr.Code)
		}
	}
}

// TestClientIPIgnoresSpoofedHeadersFromUntrustedPeer verifies the F-3 fix: a
// direct connection (peer not in TrustedProxies) cannot spoof its IP via
// X-Forwarded-For / X-Real-IP, so the real peer address is used for rate
// limiting and lockout keying.
func TestClientIPIgnoresSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	defer func() { config.Cfg.TrustedProxies = prev }()

	// Untrusted direct peer trying to spoof headers → headers ignored.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Forwarded-For", "10.0.0.9")
	req.Header.Set("X-Real-IP", "10.0.0.9")
	if got := ClientIP(req); got != "203.0.113.7" {
		t.Fatalf("untrusted peer: want real peer 203.0.113.7, got %q", got)
	}

	// Trusted proxy (loopback) → forwarding header is honoured.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "127.0.0.1:5555"
	req2.Header.Set("X-Forwarded-For", "198.51.100.42")
	if got := ClientIP(req2); got != "198.51.100.42" {
		t.Fatalf("trusted proxy: want forwarded 198.51.100.42, got %q", got)
	}

	// Trusted proxy with a spoofed extra hop prepended → right-most untrusted
	// entry (the address the proxy actually saw) wins.
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "127.0.0.1:5555"
	req3.Header.Set("X-Forwarded-For", "9.9.9.9, 198.51.100.42, 127.0.0.1")
	if got := ClientIP(req3); got != "198.51.100.42" {
		t.Fatalf("chained proxy: want 198.51.100.42, got %q", got)
	}
}

// TestArgon2idLegacyHashStillVerifies ensures the F-5 cost bump did not break
// pre-existing hashes stored in the old parameter-less "salt$hash" form.
func TestArgon2idLegacyHashStillVerifies(t *testing.T) {
	secret := "legacy-secret"
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = byte(i)
	}
	// Reproduce the legacy encoding (time cost = 1, no parameter metadata).
	h := argon2.IDKey([]byte(secret), salt, legacyArgonTime, argonMemory, argonThreads, argonKeyLen)
	legacy := base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(h)
	if !VerifySecretArgon2id(secret, legacy) {
		t.Fatal("legacy salt$hash encoding must still verify after the cost bump")
	}
	if VerifySecretArgon2id("wrong", legacy) {
		t.Fatal("legacy verify must reject a wrong secret")
	}
}

// TestCSRFMiddlewareRefreshesStaleCookie guards the recovery path: a GET that
// arrives with a stale/invalid vp_csrf cookie (e.g. after a CSRF-secret
// rotation on restart) must be re-issued a fresh, valid token so that simply
// reloading the page restores the ability to POST. Previously a present-but-
// invalid cookie was left untouched, trapping the user in a 403 loop that the
// "session token expired — reload" message could not resolve.
func TestCSRFMiddlewareRefreshesStaleCookie(t *testing.T) {
	handler := CSRFTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/os/vayuos/mail/compose", nil)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: "stale-invalid-token"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var issued string
	for _, c := range rr.Result().Cookies() {
		if c.Name == "vp_csrf" {
			issued = c.Value
		}
	}
	if issued == "" {
		t.Fatal("a stale cookie should be replaced with a freshly issued token on GET")
	}
	if !ValidateCSRFToken(issued) {
		t.Fatal("the re-issued token must be valid")
	}
}

// TestCSRFMiddlewareKeepsValidCookie ensures a GET that already carries a valid
// token is not needlessly re-issued one (stable token across page loads).
func TestCSRFMiddlewareKeepsValidCookie(t *testing.T) {
	handler := CSRFTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	valid := GenerateCSRFToken()
	req := httptest.NewRequest("GET", "/os/vayuos/mail/compose", nil)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: valid})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	for _, c := range rr.Result().Cookies() {
		if c.Name == "vp_csrf" {
			t.Fatalf("a valid cookie should be left untouched, but a new token was issued: %q", c.Value)
		}
	}
}

// TestCSRFMiddlewareBlocksStalePost confirms the POST path still rejects a
// stale token (the security property is unchanged by the GET-refresh fix).
func TestCSRFMiddlewareBlocksStalePost(t *testing.T) {
	handler := CSRFTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/os/vayuos/mail/send", nil)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: "stale-invalid-token"})
	req.Header.Set("X-CSRF-Token", "stale-invalid-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("stale token POST: want 403, got %d", rr.Code)
	}
}
