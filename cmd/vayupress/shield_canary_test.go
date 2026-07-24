package main

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// TestShieldCanaryPassesWithVerifiedBots: with the verified-bot fast path wired,
// every canary crawler must be served content — even with the shield enabled and
// surge forced on (the worst case for a crawler on a clearnet install).
func TestShieldCanaryPassesWithVerifiedBots(t *testing.T) {
	a := &App{}
	a.vayuShield = vayushield.New(vayushield.Config{
		Enabled:       true,
		Signer:        challenge.NewSigner([]byte("s")),
		ClientIP:      func(r *http.Request) string { return r.RemoteAddr },
		VerifiedBotFn: func(_ netip.Addr, _ string) (bool, bool) { return true, false },
	})
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, Surge: true})

	res := a.runShieldCanary()
	if !res.ok() {
		t.Fatalf("SEO canary must pass with verified bots wired; failed: %v", res.failed)
	}
	if len(res.passed) != len(canaryCrawlers) {
		t.Fatalf("expected all %d crawlers to pass, got %d", len(canaryCrawlers), len(res.passed))
	}
}

// TestShieldCanaryDetectsDeIndex: if the shield is misconfigured so crawlers are
// NOT recognised (verifier reports "not a crawler") and surge is on, the canary
// must FAIL — proving it actually detects a de-indexing regression rather than
// always passing.
func TestShieldCanaryDetectsDeIndex(t *testing.T) {
	a := &App{}
	a.vayuShield = vayushield.New(vayushield.Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return r.RemoteAddr },
		// Pretend nothing is a crawler: gate 0 never fast-paths, so under forced
		// surge every crawler probe is challenged (503).
		VerifiedBotFn: func(_ netip.Addr, _ string) (bool, bool) { return false, false },
	})
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, Surge: true})

	res := a.runShieldCanary()
	if res.ok() {
		t.Fatal("canary must FAIL when crawlers are challenged under surge (else it is useless)")
	}
}
