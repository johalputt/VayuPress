// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/netip"
	"strings"
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
		VerifiedBotFn: func(_ netip.Addr, _ string) vayushield.BotFastPath { return vayushield.BotVerified },
	})
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, Surge: true})

	res := a.runShieldCanary()
	if !res.ok() {
		t.Fatalf("SEO canary must pass with verified bots wired; failed: %v", res.failed)
	}
	if want := len(canaryCrawlers) + len(canaryReaders); len(res.passed) != want {
		t.Fatalf("expected all %d probes to pass, got %d", want, len(res.passed))
	}
}

// TestShieldCanaryRealReadersAreNeverChallenged: the other half of the promise —
// an ordinary first-time visitor with no clearance cookie must be served content,
// not a verification page. The verifier reports "not a crawler" for everything
// here, so the readers pass on their own merits rather than via the SEO lane.
func TestShieldCanaryRealReadersAreNeverChallenged(t *testing.T) {
	a := &App{}
	a.vayuShield = vayushield.New(vayushield.Config{
		Enabled:       true,
		Signer:        challenge.NewSigner([]byte("s")),
		ClientIP:      func(r *http.Request) string { return r.RemoteAddr },
		VerifiedBotFn: func(_ netip.Addr, _ string) vayushield.BotFastPath { return vayushield.BotNotRecognised },
	})
	// The shipped defaults: classification on, no surge, no anti-DDoS gates.
	a.vayuShield.ApplySettings(vayushield.Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, Tarpit: true,
	})

	res := a.runShieldCanary()
	if res.readers != len(canaryReaders) {
		var bad []string
		for _, p := range res.probes {
			if p.Group == "Readers" && !p.OK {
				bad = append(bad, p.Name)
			}
		}
		t.Fatalf("every real reader must be served content; challenged/blocked: %v", bad)
	}
	// The report must carry per-probe detail for the admin panel.
	if len(res.probes) != len(canaryReaders)+len(canaryCrawlers) {
		t.Errorf("probes = %d, want one per canary", len(res.probes))
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
		VerifiedBotFn: func(_ netip.Addr, _ string) vayushield.BotFastPath { return vayushield.BotNotRecognised },
	})
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true, Surge: true})

	res := a.runShieldCanary()
	if res.ok() {
		t.Fatal("canary must FAIL when crawlers are challenged under surge (else it is useless)")
	}
}

// TestShieldSelfTestPanelReportsHonestly: the operator-facing panel must show a
// clear all-clear when nothing is hurdled, and must NAME the problem (rather than
// quietly showing green) when a real reader is challenged.
func TestShieldSelfTestPanelReportsHonestly(t *testing.T) {
	allGood := shieldCanaryResult{
		readers: len(canaryReaders),
		probes: []canaryProbeResult{
			{Name: canaryReaders[0].name, Group: "Readers", Status: 200, OK: true},
			{Name: "Googlebot", Group: "Crawlers", Status: 200, OK: true},
		},
	}
	// Fill the remaining reader probes so readers == len(canaryReaders).
	for i := 1; i < len(canaryReaders); i++ {
		allGood.probes = append(allGood.probes, canaryProbeResult{
			Name: canaryReaders[i].name, Group: "Readers", Status: 200, OK: true,
		})
	}
	if got := shieldSelfTestChip(allGood); !strings.Contains(got, "All clear") {
		t.Errorf("chip = %q, want All clear", got)
	}
	body := shieldSelfTestBody(allGood)
	for _, want := range []string{"Readers", "Crawlers", "✓ Served", "Every ordinary visitor"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	// A hurdled reader must be surfaced, not hidden.
	hurdled := shieldCanaryResult{
		readers: 0,
		failed:  []string{"Reader · Chrome (Windows)→503"},
		probes: []canaryProbeResult{
			{Name: "Reader · Chrome (Windows)", Group: "Readers", Status: 503, OK: false},
		},
	}
	if got := shieldSelfTestChip(hurdled); !strings.Contains(got, "Readers hurdled") {
		t.Errorf("chip = %q, want a readers-hurdled warning", got)
	}
	hb := shieldSelfTestBody(hurdled)
	if !strings.Contains(hb, "Real visitors are being hurdled") || !strings.Contains(hb, "✕ Challenged") {
		t.Error("body must name the hurdled-reader problem and mark the failing probe")
	}

	// Not initialised — say so rather than implying a pass.
	if got := shieldSelfTestChip(shieldCanaryResult{}); !strings.Contains(got, "Unavailable") {
		t.Errorf("chip = %q, want Unavailable", got)
	}
}
