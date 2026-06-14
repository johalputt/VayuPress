// Package mode implements the VayuPress System Mode state machine.
// The policy engine controls mode transitions; subsystems query the current
// mode to adjust their behaviour (e.g., reject writes in ReadOnly, throttle
// federation in Degraded, refuse plugin starts in Quarantined).
//
// Mode transitions are append-only: the history is preserved for audit.
// See docs/architecture/system-modes.md for the full transition graph.
package mode

import (
	"fmt"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Mode is the operational state of the VayuPress runtime.
type Mode string

const (
	ModeNormal      Mode = "normal"      // all subsystems fully operational
	ModeDegraded    Mode = "degraded"    // partial capability (SLO budget low)
	ModeReadOnly    Mode = "read-only"   // writes refused (WAL corruption, migration failure)
	ModeRecovery    Mode = "recovery"    // active recovery operation in progress
	ModeMaintenance Mode = "maintenance" // operator-initiated; planned downtime
	ModeQuarantined Mode = "quarantined" // isolation active; plugins/federation suspended
)

// Transition records a mode change with its cause and timestamp.
type Transition struct {
	From       Mode
	To         Mode
	Reason     string
	Cause      string // e.g. "slo.exhausted", "wal.corruption", "operator"
	OccurredAt time.Time
}

// Manager holds the current system mode and its transition history.
// All methods are safe for concurrent use.
type Manager struct {
	mu      sync.RWMutex
	current Mode
	history []Transition
	hooks   []func(Transition)
}

// New returns a Manager starting in ModeNormal.
func New() *Manager {
	return &Manager{current: ModeNormal}
}

// Global is the default system mode manager.
var Global = New()

// Current returns the current system mode.
func (m *Manager) Current() Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Is returns true if the current mode matches any of the given modes.
func (m *Manager) Is(modes ...Mode) bool {
	cur := m.Current()
	for _, mo := range modes {
		if cur == mo {
			return true
		}
	}
	return false
}

// Transition attempts to move to the target mode.
// Returns an error if the transition is not permitted by the graph.
func (m *Manager) Transition(to Mode, reason, cause string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := m.current
	if from == to {
		return nil // no-op
	}
	if !isAllowed(from, to) {
		return fmt.Errorf("mode: transition %s→%s not permitted", from, to)
	}

	t := Transition{
		From:       from,
		To:         to,
		Reason:     reason,
		Cause:      cause,
		OccurredAt: time.Now().UTC(),
	}
	m.current = to
	m.history = append(m.history, t)

	logging.LogJSON(logging.LogFields{
		Level:     "warn",
		Component: "mode",
		Msg:       fmt.Sprintf("system mode: %s→%s reason=%q cause=%q", from, to, reason, cause),
	})

	// Fire transition hooks (e.g., alert, disable subsystems, flush queues).
	for _, h := range m.hooks {
		h(t)
	}
	return nil
}

// ForceTransition bypasses the allowed-transition graph — for operator use only.
// Records the transition with cause="operator.force".
func (m *Manager) ForceTransition(to Mode, reason string) {
	m.mu.Lock()
	from := m.current
	t := Transition{
		From:       from,
		To:         to,
		Reason:     reason,
		Cause:      "operator.force",
		OccurredAt: time.Now().UTC(),
	}
	m.current = to
	m.history = append(m.history, t)
	hooks := make([]func(Transition), len(m.hooks))
	copy(hooks, m.hooks)
	m.mu.Unlock()

	logging.LogJSON(logging.LogFields{
		Level:     "error",
		Component: "mode",
		Msg:       fmt.Sprintf("system mode FORCED: %s→%s reason=%q", from, to, reason),
	})
	for _, h := range hooks {
		h(t)
	}
}

// OnTransition registers a hook called on every successful transition.
// Hooks are called synchronously in the order registered; keep them fast.
func (m *Manager) OnTransition(h func(Transition)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = append(m.hooks, h)
}

// History returns all transitions in order, oldest first.
func (m *Manager) History() []Transition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Transition, len(m.history))
	copy(out, m.history)
	return out
}

// Reset returns the manager to ModeNormal, clearing history.
// Intended for test cleanup only.
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = ModeNormal
	m.history = nil
}

// EvaluateFromPolicy transitions mode based on policy evaluation results.
// - Any exhausted SLO → Degraded (if currently Normal)
// - Migration drift → ReadOnly (if currently Normal or Degraded)
// - Quarantined plugins → Quarantined (if currently Normal or Degraded)
func (m *Manager) EvaluateFromPolicy(sloExhausted bool, migrationDrift bool, pluginsQuarantined bool) {
	switch {
	case migrationDrift:
		_ = m.Transition(ModeReadOnly, "migration checksum drift detected", "policy.migration-drift")
	case pluginsQuarantined:
		_ = m.Transition(ModeQuarantined, "plugin quarantine threshold exceeded", "policy.plugin-quarantine")
	case sloExhausted:
		_ = m.Transition(ModeDegraded, "SLO error budget exhausted", "policy.slo-exhausted")
	}
}

// allowed defines the permitted mode transition graph.
// From any mode you can force via ForceTransition — this governs automatic transitions.
var allowed = map[Mode][]Mode{
	ModeNormal:      {ModeDegraded, ModeReadOnly, ModeRecovery, ModeMaintenance, ModeQuarantined},
	ModeDegraded:    {ModeNormal, ModeReadOnly, ModeQuarantined, ModeMaintenance},
	ModeReadOnly:    {ModeRecovery, ModeMaintenance},
	ModeRecovery:    {ModeNormal, ModeReadOnly},
	ModeMaintenance: {ModeNormal},
	ModeQuarantined: {ModeNormal, ModeMaintenance},
}

// AllowedFrom returns the modes reachable from the given mode via an automatic
// (non-forced) transition, in declaration order.
func AllowedFrom(from Mode) []Mode {
	return append([]Mode(nil), allowed[from]...)
}

// IsAllowed reports whether an automatic transition from→to is permitted.
func IsAllowed(from, to Mode) bool { return isAllowed(from, to) }

// AllModes returns every defined mode in canonical (severity) order.
func AllModes() []Mode {
	return []Mode{ModeNormal, ModeDegraded, ModeReadOnly, ModeRecovery, ModeMaintenance, ModeQuarantined}
}

func isAllowed(from, to Mode) bool {
	for _, permitted := range allowed[from] {
		if permitted == to {
			return true
		}
	}
	return false
}
