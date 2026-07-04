package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPublicDiscoveryRateLimit verifies the discovery limiter allows up to the
// configured budget per IP and then returns 429 with a Retry-After header,
// while a different IP is unaffected.
func TestPublicDiscoveryRateLimit(t *testing.T) {
	old := discoveryLimit
	discoveryLimit = 3
	defer func() { discoveryLimit = old }()

	h := PublicDiscoveryRateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	call := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/vayumail/autoconfig.json", nil)
		req.RemoteAddr = ip + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	const ip = "203.0.113.9"
	for i := 1; i <= 3; i++ {
		if rec := call(ip); rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, rec.Code)
		}
	}
	rec := call(ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget request: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}

	// A different IP has its own budget.
	if rec := call("198.51.100.4"); rec.Code != http.StatusOK {
		t.Errorf("other IP throttled: got %d, want 200", rec.Code)
	}
}
