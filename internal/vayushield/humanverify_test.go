// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"strings"
	"testing"
)

// TestInterstitialIsHumanVerifiable checks the jailed/challenge interstitial is a
// real "verify you are human" page — a visible, tickable control plus the embedded
// signed challenge and the same-origin solver — while keeping the strings the rest
// of the suite and crawlers rely on.
func TestInterstitialIsHumanVerifiable(t *testing.T) {
	html := interstitialHTML(`{"salt":"abc","difficulty":4,"exp":123,"sig":"deadbeef"}`)
	for _, want := range []string{
		"Verify you are human",       // the human-facing prompt
		`id="vayushield-verify"`,     // the tickable control
		`type="checkbox"`,            // it is a checkbox, not a dead page
		`data-challenge=`,            // signed challenge embedded
		"/__vayushield/challenge.js", // the solver script
		"Verifying your browser",     // kept for crawlers + existing tests
	} {
		if !strings.Contains(html, want) {
			t.Errorf("interstitial missing %q", want)
		}
	}
}

// TestChallengeJSIsCheckboxDriven verifies the solver waits for the human tick,
// still proves work via Web Crypto, and posts to the unchanged verify endpoint.
func TestChallengeJSIsCheckboxDriven(t *testing.T) {
	js := ChallengeJS()
	for _, want := range []string{"vayushield-verify", "crypto.subtle.digest", "/__vayushield/pow"} {
		if !strings.Contains(js, want) {
			t.Errorf("challenge JS missing %q", want)
		}
	}
}

// TestAcceptsHTML only treats a top-level HTML GET as a navigation.
func TestAcceptsHTML(t *testing.T) {
	nav, _ := http.NewRequest(http.MethodGet, "/", nil)
	nav.Header.Set("Accept", "text/html,application/xhtml+xml")
	if !acceptsHTML(nav) {
		t.Error("a GET asking for text/html should be a navigation")
	}
	post, _ := http.NewRequest(http.MethodPost, "/", nil)
	post.Header.Set("Accept", "text/html")
	if acceptsHTML(post) {
		t.Error("a POST is not a navigation")
	}
	api, _ := http.NewRequest(http.MethodGet, "/api", nil)
	api.Header.Set("Accept", "application/json")
	if acceptsHTML(api) {
		t.Error("a JSON GET is not a navigation")
	}
	if acceptsHTML(nil) {
		t.Error("nil request is not a navigation")
	}
}

// TestThrottledHTMLAutoRetries confirms the friendly throttle page carries a
// numeric meta-refresh (so a jailed human is bounced back automatically) and that
// digitsOr refuses to interpolate anything non-numeric.
func TestThrottledHTMLAutoRetries(t *testing.T) {
	page := throttledHTML("10")
	if !strings.Contains(page, `http-equiv="refresh" content="10"`) {
		t.Error("throttle page should auto-refresh after the Retry-After")
	}
	if !strings.Contains(page, "Just a moment") {
		t.Error("throttle page should be the friendly interstitial, not a dead end")
	}
	if got := digitsOr(`5"><script>`, "5"); got != "5" {
		t.Errorf("digitsOr must reject non-numeric input, got %q", got)
	}
	if got := digitsOr("30", "5"); got != "30" {
		t.Errorf("digitsOr should pass a clean number, got %q", got)
	}
}
