package main

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

func TestTimelineEntriesCarryProvenance(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	recordCSPViolation("script-src", "https://x.example/b.js")

	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	entries := buildOperationalTimeline(snap, nil, nil)

	var sawCSP, sawBootBuild bool
	for _, e := range entries {
		if e.Prov.Source == "" {
			t.Errorf("every timeline entry must carry a provenance source; got empty for %q", e.Msg)
		}
		if e.Cat == "runtime" {
			if e.Prov.Build != Version {
				t.Errorf("boot entry must carry exact build provenance, got %q", e.Prov.Build)
			}
			sawBootBuild = true
		}
		if e.Prov.Source == "csp" && e.Prov.Actor == "browser" {
			sawCSP = true
			if e.Prov.Cause == "" {
				t.Error("csp violation provenance should record the violated directive as cause")
			}
		}
	}
	if !sawBootBuild {
		t.Error("expected a runtime boot entry with build provenance")
	}
	if !sawCSP {
		t.Error("expected a csp violation entry attributed to the browser")
	}
}

func TestModeTransitionActorClassification(t *testing.T) {
	if tlActor("") != "operator" || tlActor("operator") != "operator" {
		t.Error("empty/operator cause should classify as operator")
	}
	if tlActor("slo.exhausted") != "policy" {
		t.Error("a named policy cause should classify as policy")
	}
}

func TestPostureCarriesPolicyRevision(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	for _, e := range buildOperationalTimeline(snap, nil, nil) {
		if e.Cat == "csp" && e.Prov.PolicyRev != config.ConfigVersion {
			t.Errorf("csp.policy entry should carry policy revision %q, got %q", config.ConfigVersion, e.Prov.PolicyRev)
		}
	}
}
