// Package fault — escalation wires fault injection counters to the mode state machine.
// When a named fault fires too many times within a window, the system escalates
// to a safer operating mode automatically.
package fault

import (
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/mode"
)

// EscalationRule maps a fault name to a mode transition that fires when the
// fault's trigger count exceeds Threshold within Window.
type EscalationRule struct {
	FaultName  string
	Threshold  int64         // fault triggers needed within Window to escalate
	Window     time.Duration // rolling window; 0 means lifetime count
	TargetMode mode.Mode
	Reason     string
	Cause      string
}

// Escalator watches one or more faults and triggers mode transitions.
// It is intentionally separate from Injector so production builds can
// enable escalation without enabling fault injection.
type Escalator struct {
	mu      sync.Mutex
	rules   []escalationState
	manager *mode.Manager
}

type escalationState struct {
	rule        EscalationRule
	windowStart time.Time
	windowCount int64
}

// NewEscalator creates an Escalator backed by the given mode.Manager.
func NewEscalator(mgr *mode.Manager) *Escalator {
	return &Escalator{manager: mgr}
}

// AddRule registers an escalation rule. Safe to call before or after Start.
func (e *Escalator) AddRule(r EscalationRule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, escalationState{
		rule:        r,
		windowStart: time.Now(),
	})
}

// Record notifies the Escalator that the named fault fired.
// Call this immediately after fault.Injector.Check returns a non-nil error.
// No-op if no rule matches the fault name.
func (e *Escalator) Record(faultName string) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.rules {
		s := &e.rules[i]
		if s.rule.FaultName != faultName {
			continue
		}

		// Reset window if expired.
		if s.rule.Window > 0 && now.Sub(s.windowStart) > s.rule.Window {
			s.windowStart = now
			s.windowCount = 0
		}

		s.windowCount++
		if s.windowCount >= s.rule.Threshold {
			// Fire and reset so it doesn't re-trigger every subsequent fault.
			s.windowStart = now
			s.windowCount = 0
			// Transition is best-effort; ignore error (may already be in target mode).
			_ = e.manager.Transition(s.rule.TargetMode, s.rule.Reason, s.rule.Cause)
		}
	}
}

// DefaultRules returns the canonical set of escalation rules for the platform.
// Callers add these to a NewEscalator to wire standard fault→mode behaviour.
func DefaultRules() []EscalationRule {
	return []EscalationRule{
		{
			FaultName:  FaultWALWrite,
			Threshold:  3,
			Window:     5 * time.Minute,
			TargetMode: mode.ModeReadOnly,
			Reason:     "WAL write faults exceeded threshold",
			Cause:      "fault.db.wal.write",
		},
		{
			FaultName:  FaultMigrationApply,
			Threshold:  1,
			Window:     0, // any migration fault → immediate read-only
			TargetMode: mode.ModeReadOnly,
			Reason:     "migration apply fault detected",
			Cause:      "fault.migrations.apply",
		},
		{
			FaultName:  FaultSigningSign,
			Threshold:  5,
			Window:     time.Minute,
			TargetMode: mode.ModeDegraded,
			Reason:     "signing faults degrading content integrity",
			Cause:      "fault.signing.sign",
		},
		{
			FaultName:  FaultFederationDeliver,
			Threshold:  10,
			Window:     time.Minute,
			TargetMode: mode.ModeDegraded,
			Reason:     "federation delivery failures above threshold",
			Cause:      "fault.federation.deliver",
		},
		{
			FaultName:  FaultPluginInvoke,
			Threshold:  5,
			Window:     2 * time.Minute,
			TargetMode: mode.ModeQuarantined,
			Reason:     "plugin invocation faults exceeded safety threshold",
			Cause:      "fault.sandbox.plugin.invoke",
		},
		{
			FaultName:  FaultOutboxCommit,
			Threshold:  3,
			Window:     5 * time.Minute,
			TargetMode: mode.ModeDegraded,
			Reason:     "outbox commit failures threatening event durability",
			Cause:      "fault.outbox.commit",
		},
	}
}

// GlobalEscalator is the process-wide escalator backed by mode.Global.
// Populated with DefaultRules by init so the wiring is automatic.
var GlobalEscalator = func() *Escalator {
	e := NewEscalator(mode.Global)
	for _, r := range DefaultRules() {
		e.AddRule(r)
	}
	return e
}()
