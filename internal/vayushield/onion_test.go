// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// TestOnionModeNeverChallenges: in a Tor Space the browser has no crypto.subtle,
// so a challenge would lock everyone out. Under forced surge — which challenges
// every unproven browser on a clearnet install — an OnionMode shield must fail
// OPEN and serve content instead.
func TestOnionModeNeverChallenges(t *testing.T) {
	m := New(Config{
		Enabled:   true,
		OnionMode: true,
		Signer:    challenge.NewSigner([]byte("s")),
		ClientIP:  func(r *http.Request) string { return "203.0.113.9:1" },
	})
	m.ApplySettings(Settings{Enabled: true, Surge: true})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0) Chrome/130")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("OnionMode must fail open (serve content), got %d", rr.Code)
	}
	if m.underSurge(m.live()) {
		t.Fatal("surge must never engage in OnionMode")
	}
}
