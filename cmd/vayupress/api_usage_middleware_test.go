package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
)

// TestServeWithKeyUsageBudget verifies the per-key budget: a key with an
// explicit rate_per_min is admitted exactly that many times per window and then
// receives 429 + Retry-After, while a different key is unaffected (budgets are
// keyed by key id, not IP).
func TestServeWithKeyUsageBudget(t *testing.T) {
	a := &App{}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	run := func(ki apikeys.KeyInfo) int {
		req := httptest.NewRequest("GET", "/api/v1/queue", nil)
		rec := httptest.NewRecorder()
		a.serveWithKeyUsage(rec, req, ki, okHandler)
		return rec.Code
	}

	tight := apikeys.KeyInfo{ID: "budget-key-1", Scope: apikeys.ScopeExternal, RatePerMin: 3}
	for i := 1; i <= 3; i++ {
		if got := run(tight); got != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 (within budget)", i, got)
		}
	}
	// 4th request exceeds the 3/min budget.
	req := httptest.NewRequest("GET", "/api/v1/queue", nil)
	rec := httptest.NewRecorder()
	a.serveWithKeyUsage(rec, req, tight, okHandler)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget request = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 must carry Retry-After")
	}

	// A different key has its own window.
	other := apikeys.KeyInfo{ID: "budget-key-2", Scope: apikeys.ScopeExternal, RatePerMin: 3}
	if got := run(other); got != http.StatusOK {
		t.Errorf("a different key must not share the exhausted budget, got %d", got)
	}

	// The internal system key is exempt from metering entirely.
	internal := apikeys.KeyInfo{ID: "internal-system", Scope: apikeys.ScopeInternal, RatePerMin: 1}
	for i := 0; i < 5; i++ {
		if got := run(internal); got != http.StatusOK {
			t.Fatalf("internal key must be exempt from the budget, got %d", got)
		}
	}
}

// TestAPIUsageMiddlewarePassthrough verifies the middleware only meters
// key-authenticated requests: without a KeyInfo in context it serves untouched.
func TestAPIUsageMiddlewarePassthrough(t *testing.T) {
	a := &App{}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := a.apiUsageMiddleware(okHandler)

	req := httptest.NewRequest("GET", "/api/v1/queue", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-identity request = %d, want 200 passthrough", rec.Code)
	}

	// With an identity it goes through the budget (fresh key: admitted).
	ki := apikeys.KeyInfo{ID: "mw-key", Scope: apikeys.ScopeExternal}
	req2 := auth.RequestWithKeyInfo(httptest.NewRequest("GET", "/api/v1/queue", nil), ki)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("fresh key = %d, want 200", rec2.Code)
	}
}

// TestAPIKeyDefaultRate pins the default budget and its env clamp behaviour.
func TestAPIKeyDefaultRate(t *testing.T) {
	t.Setenv("VAYU_API_RATE_PER_MIN", "")
	if got := apiKeyDefaultRatePerMin(); got != 600 {
		t.Errorf("default = %d, want 600", got)
	}
	t.Setenv("VAYU_API_RATE_PER_MIN", "120")
	if got := apiKeyDefaultRatePerMin(); got != 120 {
		t.Errorf("tuned = %d, want 120", got)
	}
	t.Setenv("VAYU_API_RATE_PER_MIN", "0") // out of band → fall back
	if got := apiKeyDefaultRatePerMin(); got != 600 {
		t.Errorf("clamped = %d, want 600", got)
	}
}
