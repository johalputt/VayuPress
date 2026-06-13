package mode

import (
	"testing"
)

func TestTransitionAllowed(t *testing.T) {
	m := New()
	if err := m.Transition(ModeDegraded, "test", "test.cause"); err != nil {
		t.Fatalf("Normal→Degraded: %v", err)
	}
	if m.Current() != ModeDegraded {
		t.Errorf("expected Degraded, got %s", m.Current())
	}
}

func TestTransitionForbidden(t *testing.T) {
	m := New()
	// Normal→ReadOnly allowed
	if err := m.Transition(ModeReadOnly, "test", "test"); err != nil {
		t.Fatalf("Normal→ReadOnly: %v", err)
	}
	// ReadOnly→Degraded NOT in allowed graph
	if err := m.Transition(ModeDegraded, "test", "test"); err == nil {
		t.Error("ReadOnly→Degraded should be forbidden")
	}
}

func TestTransitionNoOp(t *testing.T) {
	m := New()
	if err := m.Transition(ModeNormal, "same", "same"); err != nil {
		t.Errorf("same→same should be no-op, got %v", err)
	}
	if len(m.History()) != 0 {
		t.Error("no-op transition should not be recorded in history")
	}
}

func TestForceTransitionBypassesGraph(t *testing.T) {
	m := New()
	// ReadOnly→Degraded is forbidden normally but ForceTransition should work
	m.ForceTransition(ModeReadOnly, "force test")
	m.ForceTransition(ModeDegraded, "force test")
	if m.Current() != ModeDegraded {
		t.Errorf("expected Degraded after force, got %s", m.Current())
	}
	h := m.History()
	if len(h) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(h))
	}
	if h[1].Cause != "operator.force" {
		t.Errorf("expected operator.force cause, got %q", h[1].Cause)
	}
}

func TestHookFiredOnTransition(t *testing.T) {
	m := New()
	var fired []Transition
	m.OnTransition(func(t Transition) { fired = append(fired, t) })

	m.Transition(ModeDegraded, "r", "c") //nolint:errcheck
	if len(fired) != 1 {
		t.Fatalf("expected 1 hook call, got %d", len(fired))
	}
	if fired[0].To != ModeDegraded {
		t.Errorf("hook received wrong To: %s", fired[0].To)
	}
}

func TestHistoryOrderOldestFirst(t *testing.T) {
	m := New()
	m.Transition(ModeDegraded, "r1", "c1") //nolint:errcheck
	m.Transition(ModeNormal, "r2", "c2")   //nolint:errcheck
	m.Transition(ModeReadOnly, "r3", "c3") //nolint:errcheck

	h := m.History()
	if len(h) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(h))
	}
	if h[0].To != ModeDegraded || h[1].To != ModeNormal || h[2].To != ModeReadOnly {
		t.Error("history order wrong")
	}
}

func TestIsMultipleModes(t *testing.T) {
	m := New()
	if !m.Is(ModeNormal, ModeDegraded) {
		t.Error("Is(Normal, Degraded) should be true when current is Normal")
	}
	if m.Is(ModeDegraded, ModeReadOnly) {
		t.Error("Is(Degraded, ReadOnly) should be false when current is Normal")
	}
}

func TestEvaluateFromPolicySLO(t *testing.T) {
	m := New()
	m.EvaluateFromPolicy(true, false, false)
	if m.Current() != ModeDegraded {
		t.Errorf("sloExhausted should → Degraded, got %s", m.Current())
	}
}

func TestEvaluateFromPolicyMigrationDrift(t *testing.T) {
	m := New()
	m.EvaluateFromPolicy(false, true, false)
	if m.Current() != ModeReadOnly {
		t.Errorf("migrationDrift should → ReadOnly, got %s", m.Current())
	}
}

func TestEvaluateFromPolicyQuarantine(t *testing.T) {
	m := New()
	m.EvaluateFromPolicy(false, false, true)
	if m.Current() != ModeQuarantined {
		t.Errorf("pluginsQuarantined should → Quarantined, got %s", m.Current())
	}
}

func TestReset(t *testing.T) {
	m := New()
	m.Transition(ModeDegraded, "r", "c") //nolint:errcheck
	m.Reset()
	if m.Current() != ModeNormal {
		t.Errorf("after Reset expected Normal, got %s", m.Current())
	}
	if len(m.History()) != 0 {
		t.Error("after Reset history should be empty")
	}
}
