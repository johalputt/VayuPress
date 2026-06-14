package main

import (
	"strings"
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

func TestTimelineSeverityClassification(t *testing.T) {
	cases := []struct {
		e    tlEntry
		want string
	}{
		{tlEntry{Cat: "runtime", Sev: "tl-accent", Msg: "runtime.boot"}, "OBSERVE"},
		{tlEntry{Cat: "csp", Sev: "tl-warn", Msg: "csp.violation — script-src blocked x"}, "VIOLATION"},
		{tlEntry{Cat: "csp", Sev: "tl-ok", Msg: "csp.policy — enforcing"}, "NOTICE"},
		{tlEntry{Cat: "csp", Sev: "tl-warn", Msg: "csp.policy — REPORT-ONLY"}, "WARN"},
		{tlEntry{Cat: "mode", Sev: "tl-err", Msg: "mode.transition", Prov: tlProvenance{Cause: "operator"}}, "CONTAINMENT"},
		{tlEntry{Cat: "mode", Sev: "tl-err", Msg: "mode.transition", Prov: tlProvenance{Cause: "wal.corruption"}}, "CRITICAL"},
		{tlEntry{Cat: "mode", Sev: "tl-info", Msg: "mode.transition"}, "ESCALATION"},
		{tlEntry{Cat: "fault", Sev: "tl-err", Msg: "fault.trigger"}, "WARN"},
	}
	for _, c := range cases {
		if got := timelineSeverity(c.e).String(); got != c.want {
			t.Errorf("classify %q/%q/%q = %s; want %s", c.e.Cat, c.e.Sev, c.e.Msg, got, c.want)
		}
	}
}

func TestCausalLineageFormsTraversableGraph(t *testing.T) {
	resetCSPRing()
	t.Cleanup(resetCSPRing)
	recordCSPViolation("script-src", "https://x.example/b.js")

	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	entries := buildOperationalTimeline(snap, nil, nil)

	byID := map[string]tlEntry{}
	for _, e := range entries {
		if e.Prov.ID == "" {
			t.Fatalf("entry has no id: %q", e.Msg)
		}
		byID[e.Prov.ID] = e
	}

	var roots, cspViolation, cspPolicy string
	for _, e := range entries {
		if e.Prov.ParentID == "" {
			roots = e.Prov.ID
		}
		switch {
		case e.Cat == "csp" && strings.HasPrefix(e.Msg, "csp.policy"):
			cspPolicy = e.Prov.ID
		case e.Cat == "csp":
			cspViolation = e.Prov.ID
		}
		// Every non-root parent must resolve to a real entry within the window
		// (or be empty for the root) — no dangling references in a fresh build.
		if e.Prov.ParentID != "" {
			if _, ok := byID[e.Prov.ParentID]; !ok {
				t.Errorf("entry %q references missing parent %q", e.Msg, e.Prov.ParentID)
			}
		}
	}
	if roots == "" {
		t.Error("expected exactly one root event (runtime.boot)")
	}
	// A CSP violation must descend from the CSP policy it breached.
	if cspViolation != "" && cspPolicy != "" {
		if byID[cspViolation].Prov.ParentID != cspPolicy {
			t.Error("csp.violation should have csp.policy as its causal parent")
		}
	}
}

func TestEventIDIsDeterministic(t *testing.T) {
	e := tlEntry{Clock: "00:00:01", Msg: "mode.transition — NORMAL → DEGRADED", Prov: tlProvenance{Source: "mode"}}
	first := eventID(e)
	second := eventID(e)
	if first != second {
		t.Error("eventID must be deterministic for identical input")
	}
	e2 := e
	e2.Msg = "different"
	if first == eventID(e2) {
		t.Error("eventID must differ for different messages")
	}
}
