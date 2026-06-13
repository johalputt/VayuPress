// Package slo tracks Service Level Objectives and error budgets for VayuPress.
// Each SLO has a target ratio, a window, and a live error budget that depletes
// as events are recorded. When the budget reaches zero, alerts fire.
package slo

import (
	"fmt"
	"sync"
	"time"
)

// SLO defines a named reliability objective.
type SLO struct {
	Name   string        // e.g. "signing.latency.p95"
	Target float64       // 0.0–1.0, e.g. 0.999 for 99.9%
	Window time.Duration // rolling window, e.g. 30*24*time.Hour
}

// Predefined VayuPress SLOs (see docs/reliability/slos.md).
var (
	SLOSigningP95    = SLO{Name: "signing.latency.p95", Target: 0.999, Window: 30 * 24 * time.Hour}
	SLOPluginSuccess = SLO{Name: "plugin.invocation.success", Target: 0.99, Window: 7 * 24 * time.Hour}
	SLOFederationLag = SLO{Name: "federation.inbox.lag", Target: 0.95, Window: 24 * time.Hour}
	SLORestoreRTO    = SLO{Name: "restore.rto.10min", Target: 1.0, Window: 365 * 24 * time.Hour}
	SLOWALRecovery   = SLO{Name: "wal.recovery.success", Target: 0.999, Window: 30 * 24 * time.Hour}
)

// Tracker records good/bad events against an SLO and tracks the error budget.
type Tracker struct {
	mu     sync.Mutex
	slo    SLO
	events []event
}

type event struct {
	ts   time.Time
	good bool
}

// NewTracker creates a tracker for the given SLO.
func NewTracker(s SLO) *Tracker {
	return &Tracker{slo: s}
}

// Record adds an event outcome. good=true is a success; good=false burns budget.
func (t *Tracker) Record(good bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	t.events = append(t.events, event{ts: time.Now(), good: good})
}

// Budget returns the remaining error budget as a fraction (0.0–1.0).
// 1.0 = full budget; 0.0 = exhausted.
func (t *Tracker) Budget() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()

	if len(t.events) == 0 {
		return 1.0
	}
	var good, total int
	for _, e := range t.events {
		total++
		if e.good {
			good++
		}
	}
	if total == 0 {
		return 1.0
	}
	actual := float64(good) / float64(total)
	if actual >= t.slo.Target {
		// Compute remaining budget as fraction of allowed bad events.
		allowedBadRate := 1.0 - t.slo.Target
		if allowedBadRate == 0 {
			return 1.0
		}
		actualBadRate := 1.0 - actual
		return 1.0 - (actualBadRate / allowedBadRate)
	}
	return 0.0
}

// Status returns a human-readable status string.
func (t *Tracker) Status() string {
	budget := t.Budget()
	t.mu.Lock()
	sloName := t.slo.Name
	target := t.slo.Target
	t.mu.Unlock()
	return fmt.Sprintf("slo=%s target=%.4f budget_remaining=%.4f", sloName, target, budget)
}

// BudgetExhausted returns true when the error budget is at zero.
func (t *Tracker) BudgetExhausted() bool {
	return t.Budget() <= 0.0
}

// prune removes events outside the rolling window. Caller must hold mu.
func (t *Tracker) prune() {
	cutoff := time.Now().Add(-t.slo.Window)
	i := 0
	for i < len(t.events) && t.events[i].ts.Before(cutoff) {
		i++
	}
	t.events = t.events[i:]
}

// Registry holds a named set of SLO trackers for the process.
type Registry struct {
	mu       sync.RWMutex
	trackers map[string]*Tracker
}

// NewRegistry returns an initialized Registry.
func NewRegistry() *Registry {
	return &Registry{trackers: make(map[string]*Tracker)}
}

// Global is the default SLO registry.
var Global = NewRegistry()

// Register adds a tracker for the given SLO. Panics on duplicate.
func (r *Registry) Register(s SLO) *Tracker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.trackers[s.Name]; exists {
		panic(fmt.Sprintf("slo: duplicate tracker %s", s.Name))
	}
	t := NewTracker(s)
	r.trackers[s.Name] = t
	return t
}

// Get returns the tracker for name, or nil if not registered.
func (r *Registry) Get(name string) *Tracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.trackers[name]
}

// Snapshot returns status strings for all registered SLOs.
func (r *Registry) Snapshot() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.trackers))
	ts := make([]*Tracker, 0, len(r.trackers))
	for name, t := range r.trackers {
		names = append(names, name)
		ts = append(ts, t)
	}
	r.mu.RUnlock()

	out := make([]string, len(names))
	for i, t := range ts {
		out[i] = t.Status()
	}
	return out
}
