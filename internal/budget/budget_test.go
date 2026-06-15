package budget

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/severity"
)

func TestBudgetExhaustionRecommendsEscalation(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()

	// Two violations: governance-breach budget (limit 3) is at-risk after the 2nd.
	l.Record(severity.Violation, now)
	l.Record(severity.Violation, now)
	st := statusByName(l.Status(now), "governance-breach")
	if st.State != "at-risk" {
		t.Fatalf("expected at-risk at 2/3, got %s", st.State)
	}
	if st.Recommended != "" {
		t.Errorf("no recommendation until exhausted, got %q", st.Recommended)
	}

	// Third violation exhausts it → ESCALATION recommended.
	l.Record(severity.Violation, now)
	st = statusByName(l.Status(now), "governance-breach")
	if st.State != "exhausted" {
		t.Fatalf("expected exhausted at 3/3, got %s", st.State)
	}
	if st.Recommended != "ESCALATION" {
		t.Errorf("expected ESCALATION recommendation, got %q", st.Recommended)
	}
}

func TestCriticalExhaustsImmediately(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	l.Record(severity.Critical, now)
	st := statusByName(l.Status(now), "integrity-exhaustion")
	if st.State != "exhausted" || st.Recommended != "CONTAINMENT" {
		t.Errorf("one CRITICAL should exhaust → CONTAINMENT, got %s/%s", st.State, st.Recommended)
	}
}

func TestBudgetWindowExpires(t *testing.T) {
	l := NewLedger([]Rule{
		{Name: "short", Tracks: severity.Warn, Limit: 2, Window: 50 * time.Millisecond, OnExhaust: severity.Notice},
	})
	base := time.Now()
	l.Record(severity.Warn, base)
	l.Record(severity.Warn, base)
	if statusByName(l.Status(base), "short").State != "exhausted" {
		t.Fatal("expected exhausted within window")
	}
	// After the window, the events expire and the budget recovers.
	later := base.Add(100 * time.Millisecond)
	if got := statusByName(l.Status(later), "short"); got.Consumed != 0 || got.State != "healthy" {
		t.Errorf("expected recovery after window, got consumed=%d state=%s", got.Consumed, got.State)
	}
}

func TestExhaustedCount(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	if l.ExhaustedCount(now) != 0 {
		t.Error("fresh ledger should have 0 exhausted budgets")
	}
	l.Record(severity.Critical, now)
	if l.ExhaustedCount(now) != 1 {
		t.Error("one CRITICAL should exhaust exactly one budget")
	}
}

func TestBudgetAttributesContributors(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	l.RecordFrom(severity.Violation, "csp:script-src", now)
	l.RecordFrom(severity.Violation, "csp:script-src", now)
	l.RecordFrom(severity.Violation, "csp:img-src", now)
	st := statusByName(l.Status(now), "governance-breach")
	if st.State != "exhausted" {
		t.Fatalf("expected exhausted at 3/3, got %s", st.State)
	}
	// Most-consuming source must lead.
	if len(st.Contributors) != 2 || st.Contributors[0] != "csp:script-src" {
		t.Errorf("expected csp:script-src to lead contributors, got %v", st.Contributors)
	}
}

func TestBudgetAcknowledgeClearsDebt(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	l.Record(severity.Violation, now)
	l.Record(severity.Violation, now)
	l.Record(severity.Violation, now)
	if statusByName(l.Status(now), "governance-breach").State != "exhausted" {
		t.Fatal("precondition: budget should be exhausted")
	}
	if !l.Acknowledge("governance-breach", now) {
		t.Fatal("Acknowledge should succeed for a real budget")
	}
	st := statusByName(l.Status(now), "governance-breach")
	if st.State != "healthy" || st.Consumed != 0 {
		t.Errorf("acknowledging should clear debt, got state=%s consumed=%d", st.State, st.Consumed)
	}
	if l.Acknowledge("does-not-exist", now) {
		t.Error("Acknowledge must reject unknown budgets")
	}
}

// TestDefaultRulesAreOntologicallyConsistent is an ontology-drift guard: every
// governance budget must track and escalate to real severity levels drawn from the
// taxonomy and have a well-formed window/limit. This keeps the budget doctrine
// semantically coherent with the severity taxonomy as both evolve independently.
func TestDefaultRulesAreOntologicallyConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range DefaultRules() {
		if r.Name == "" || r.Limit <= 0 || r.Window <= 0 {
			t.Errorf("budget %q has an invalid shape: %+v", r.Name, r)
		}
		if seen[r.Name] {
			t.Errorf("duplicate budget name %q — names must be unique to be addressable", r.Name)
		}
		seen[r.Name] = true
		if _, ok := severity.Parse(r.Tracks.String()); !ok {
			t.Errorf("budget %q tracks an unknown severity %v", r.Name, r.Tracks)
		}
		if _, ok := severity.Parse(r.OnExhaust.String()); !ok {
			t.Errorf("budget %q escalates to an unknown severity %v", r.Name, r.OnExhaust)
		}
	}
}

func statusByName(ss []Status, name string) Status {
	for _, s := range ss {
		if s.Name == name {
			return s
		}
	}
	return Status{}
}
