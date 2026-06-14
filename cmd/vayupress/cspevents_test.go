package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

func resetCSPRing() {
	cspRingMu.Lock()
	cspRing = nil
	cspRingMu.Unlock()
}

func TestCSPRingIsBounded(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	for i := 0; i < cspRingMax+5; i++ {
		recordCSPViolation("style-src", "https://evil.example/x")
	}
	got := recentCSPViolations()
	if len(got) != cspRingMax {
		t.Fatalf("ring should cap at %d, got %d", cspRingMax, len(got))
	}
}

func TestCSPEnforcementModeReflectsConfig(t *testing.T) {
	orig := config.Cfg.CSPReportOnly
	t.Cleanup(func() { config.Cfg.CSPReportOnly = orig })

	config.Cfg.CSPReportOnly = false
	if got := cspEnforcementMode(); got != "enforcing" {
		t.Errorf("expected enforcing, got %q", got)
	}
	config.Cfg.CSPReportOnly = true
	if got := cspEnforcementMode(); got != "report-only" {
		t.Errorf("expected report-only, got %q", got)
	}
}

func TestCSPPostureAlwaysInTimeline(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	// Even with no violations, the boot sequence must state the CSP posture.
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	entries := buildOperationalTimeline(snap, nil, nil)
	var posture bool
	for _, e := range entries {
		if e.Cat == "csp" && strings.Contains(e.Msg, "csp.policy") {
			posture = true
		}
	}
	if !posture {
		t.Error("expected a csp.policy posture entry in the timeline boot sequence")
	}
}

func TestCSPViolationSurfacesInTimeline(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	recordCSPViolation("script-src", "https://tracker.example/beacon.js")

	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	entries := buildOperationalTimeline(snap, nil, nil)

	var found bool
	for _, e := range entries {
		if e.Cat == "csp" {
			found = true
			if e.CatClass == "" || e.Sev == "" {
				t.Errorf("csp timeline entry missing class/severity: %+v", e)
			}
		}
	}
	if !found {
		t.Error("expected a csp entry in the operational timeline after a violation")
	}
}
