package main

import (
	"testing"
	"time"
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
