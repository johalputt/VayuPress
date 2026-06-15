package budget

import (
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/severity"
)

// fakeMode is a minimal modeController for actuator tests: it records requested
// transitions and honours a configurable allowed-transition predicate so the
// "mode graph refused" path can be exercised without a real journal.
type fakeMode struct {
	mu      sync.Mutex
	current mode.Mode
	allow   func(from, to mode.Mode) bool
	calls   []mode.Mode
}

func newFakeMode(start mode.Mode) *fakeMode {
	return &fakeMode{current: start, allow: func(_, _ mode.Mode) bool { return true }}
}

func (f *fakeMode) Current() mode.Mode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *fakeMode) Transition(to mode.Mode, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, to)
	if !f.allow(f.current, to) {
		return errRefused
	}
	f.current = to
	return nil
}

var errRefused = &transitionErr{"not permitted"}

type transitionErr struct{ s string }

func (e *transitionErr) Error() string { return e.s }

// exhaust charges a budget past its limit so it reads as exhausted.
func exhaust(l *Ledger, level severity.Level, n int, now time.Time) {
	for i := 0; i < n; i++ {
		l.RecordFrom(level, "test", now)
	}
}

func TestActuator_DisabledIsNoOp(t *testing.T) {
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeNormal)
	a := NewActuator(l, fm, false) // disabled

	now := time.Now()
	exhaust(l, severity.Violation, 3, now) // governance-breach → Escalation

	acts := a.Evaluate(now)
	if len(acts) != 1 {
		t.Fatalf("expected 1 actuation record, got %d", len(acts))
	}
	if acts[0].Applied {
		t.Errorf("disabled actuator must not apply a transition")
	}
	if acts[0].Refusal != "actuation disabled" {
		t.Errorf("refusal = %q, want %q", acts[0].Refusal, "actuation disabled")
	}
	if len(fm.calls) != 0 {
		t.Errorf("disabled actuator requested %d transitions, want 0", len(fm.calls))
	}
	if fm.Current() != mode.ModeNormal {
		t.Errorf("mode changed to %s while disabled", fm.Current())
	}
}

func TestActuator_EnabledEscalatesToDegraded(t *testing.T) {
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeNormal)
	a := NewActuator(l, fm, true)

	now := time.Now()
	exhaust(l, severity.Violation, 3, now) // governance-breach → Escalation → Degraded

	acts := a.Evaluate(now)
	if len(acts) != 1 || !acts[0].Applied {
		t.Fatalf("expected 1 applied actuation, got %+v", acts)
	}
	if acts[0].TargetMode != string(mode.ModeDegraded) {
		t.Errorf("target mode = %s, want %s", acts[0].TargetMode, mode.ModeDegraded)
	}
	if fm.Current() != mode.ModeDegraded {
		t.Errorf("mode = %s, want degraded", fm.Current())
	}
}

func TestActuator_OneShotDebounce(t *testing.T) {
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeNormal)
	a := NewActuator(l, fm, true)

	now := time.Now()
	exhaust(l, severity.Violation, 3, now)

	// First evaluation actuates.
	if acts := a.Evaluate(now); len(acts) != 1 {
		t.Fatalf("first evaluate: want 1 actuation, got %d", len(acts))
	}
	// A second evaluation while still exhausted must NOT re-actuate.
	if acts := a.Evaluate(now.Add(time.Second)); len(acts) != 0 {
		t.Fatalf("second evaluate while exhausted: want 0 actuations, got %d", len(acts))
	}
	if got := len(fm.calls); got != 1 {
		t.Errorf("transition requested %d times, want exactly 1 (debounced)", got)
	}
}

func TestActuator_ReArmsAfterRecovery(t *testing.T) {
	rules := []Rule{{Name: "breach", Tracks: severity.Violation, Limit: 2, Window: time.Minute, OnExhaust: severity.Escalation}}
	l := NewLedger(rules)
	fm := newFakeMode(mode.ModeNormal)
	fm.allow = func(_, _ mode.Mode) bool { return true }
	a := NewActuator(l, fm, true)

	t0 := time.Now()
	exhaust(l, severity.Violation, 2, t0)
	if acts := a.Evaluate(t0); len(acts) != 1 || !acts[0].Applied {
		t.Fatalf("t0: want applied actuation, got %+v", acts)
	}

	// Let the window roll off so the budget recovers (no longer exhausted).
	t1 := t0.Add(2 * time.Minute)
	if acts := a.Evaluate(t1); len(acts) != 0 {
		t.Fatalf("t1 recovery tick: want 0 actuations, got %d", len(acts))
	}

	// Simulate the mode recovering back to Normal (operator / recovery flow), so a
	// re-exhaustion produces a genuine transition rather than a no-op "already there".
	fm.mu.Lock()
	fm.current = mode.ModeNormal
	fm.mu.Unlock()

	// Re-exhaust → must actuate again (edge re-armed) and drive a real transition.
	exhaust(l, severity.Violation, 2, t1)
	if acts := a.Evaluate(t1); len(acts) != 1 || !acts[0].Applied {
		t.Fatalf("t1 re-exhaust: want applied actuation, got %+v", acts)
	}
	if got := len(fm.calls); got != 2 {
		t.Errorf("transition requested %d times, want 2 (one per exhaustion edge)", got)
	}
}

func TestActuator_RespectsModeGraphRefusal(t *testing.T) {
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeNormal)
	fm.allow = func(_, _ mode.Mode) bool { return false } // graph refuses everything
	a := NewActuator(l, fm, true)

	now := time.Now()
	exhaust(l, severity.Violation, 3, now)

	acts := a.Evaluate(now)
	if len(acts) != 1 {
		t.Fatalf("want 1 actuation record, got %d", len(acts))
	}
	if acts[0].Applied {
		t.Errorf("actuation must not be marked applied when the graph refuses")
	}
	if acts[0].Refusal == "" {
		t.Errorf("expected a refusal reason")
	}
	if fm.Current() != mode.ModeNormal {
		t.Errorf("mode changed to %s despite graph refusal", fm.Current())
	}
}

func TestActuator_InformationalEscalationNoTransition(t *testing.T) {
	// degradation-debt's OnExhaust is NOTICE — informational, maps to no mode.
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeNormal)
	a := NewActuator(l, fm, true)

	now := time.Now()
	exhaust(l, severity.Warn, 5, now) // degradation-debt → Notice

	acts := a.Evaluate(now)
	if len(acts) != 1 {
		t.Fatalf("want 1 actuation record, got %d", len(acts))
	}
	if acts[0].Applied {
		t.Errorf("informational escalation must not apply a transition")
	}
	if len(fm.calls) != 0 {
		t.Errorf("requested %d transitions for informational debt, want 0", len(fm.calls))
	}
}

func TestActuator_AlreadyInTargetModeIsApplied(t *testing.T) {
	l := NewLedger(DefaultRules())
	fm := newFakeMode(mode.ModeDegraded) // already degraded
	a := NewActuator(l, fm, true)

	now := time.Now()
	exhaust(l, severity.Violation, 3, now) // wants Degraded — already there

	acts := a.Evaluate(now)
	if len(acts) != 1 || !acts[0].Applied {
		t.Fatalf("want applied (goal already satisfied), got %+v", acts)
	}
	if len(fm.calls) != 0 {
		t.Errorf("should not request a transition when already in target mode, got %d calls", len(fm.calls))
	}
}

func TestActuator_TargetModeMapping(t *testing.T) {
	cases := []struct {
		level severity.Level
		want  mode.Mode
		ok    bool
	}{
		{severity.Escalation, mode.ModeDegraded, true},
		{severity.Containment, mode.ModeReadOnly, true},
		{severity.Critical, mode.ModeQuarantined, true},
		{severity.Notice, "", false},
		{severity.Warn, "", false},
		{severity.Violation, "", false},
	}
	for _, c := range cases {
		got, ok := targetModeFor(c.level)
		if got != c.want || ok != c.ok {
			t.Errorf("targetModeFor(%s) = (%s,%v), want (%s,%v)", c.level, got, ok, c.want, c.ok)
		}
	}
}

func TestActuator_EnableToggle(t *testing.T) {
	a := NewActuator(NewLedger(DefaultRules()), newFakeMode(mode.ModeNormal), false)
	if a.Enabled() {
		t.Fatal("new actuator should be disabled")
	}
	a.SetEnabled(true)
	if !a.Enabled() {
		t.Fatal("SetEnabled(true) did not enable")
	}
}
