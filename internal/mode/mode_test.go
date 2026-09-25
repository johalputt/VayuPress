// SPDX-License-Identifier: Apache-2.0

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

// A restart resumes the mode AND the transitions that explain it. Restoring the
// mode alone had System state say "has not changed mode since it started", in a
// degraded mode nothing on the page accounted for.
func TestRestoreBringsBackTheModeAndWhyItIsSo(t *testing.T) {
	m := New()
	fired := 0
	m.OnTransition(func(Transition) { fired++ })
	past := []Transition{
		{From: ModeNormal, To: ModeDegraded, Reason: "budget exhausted", Cause: "budget"},
	}
	m.Restore(past)
	if m.Current() != ModeDegraded {
		t.Fatalf("restored mode = %s, want degraded", m.Current())
	}
	if h := m.History(); len(h) != 1 || h[0].Reason != "budget exhausted" {
		t.Errorf("restored history = %+v, want the journalled transition", h)
	}
	if fired != 0 {
		t.Errorf("restoring fired %d hooks; the journal would write its own rows again", fired)
	}

	long := make([]Transition, restoredHistory+5)
	for i := range long {
		long[i] = Transition{From: ModeNormal, To: ModeNormal, Reason: "r"}
	}
	long[len(long)-1].To = ModeReadOnly
	m.Restore(long)
	if len(m.History()) != restoredHistory || m.Current() != ModeReadOnly {
		t.Errorf("a long journal restored %d rows in %s, want the last %d in read-only", len(m.History()), m.Current(), restoredHistory)
	}

	n := New()
	n.Restore([]Transition{{From: ModeNormal, To: Mode("bogus")}})
	if n.Current() != ModeNormal || len(n.History()) != 0 {
		t.Errorf("an unknown journalled mode was taken: %s, %d rows", n.Current(), len(n.History()))
	}
}
