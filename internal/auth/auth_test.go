package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
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
	req.Header.Set("X-API-Key", "test-key-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("valid key: want 200, got %d", rr.Code)
	}
}
