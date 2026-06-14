// Package budget implements governance error budgets: bounded windows of
// severity-classified events that accumulate "debt" and, once exhausted, imply a
// defined escalation. It turns the severity taxonomy from description into
// accounting — e.g. repeated VIOLATIONs consume a breach budget that, when spent,
// recommends ESCALATION.
//
// Scope boundary (deliberate): this package ACCOUNTS and RECOMMENDS. It does not
// itself drive mode transitions — letting a budget actuate the fault→mode
// engine is a control-loop change that belongs behind its own safety design and
// tests. The recommendation is surfaced (API + timeline) for an operator or a
// future, explicitly-gated actuator.
package budget

import (
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/severity"
)

// Rule defines one governance budget: how many events of a tracked severity are
// tolerated within a rolling window before the budget is exhausted, and the
// escalation severity implied when that happens.
type Rule struct {
	Name      string
	Tracks    severity.Level
	Limit     int
	Window    time.Duration
	OnExhaust severity.Level
}

// Status is the live state of a budget.
type Status struct {
	Name        string `json:"name"`
	Tracks      string `json:"tracks"`
	Consumed    int    `json:"consumed"`
	Limit       int    `json:"limit"`
	Remaining   int    `json:"remaining"`
	WindowSec   int    `json:"window_seconds"`
	State       string `json:"state"`                 // healthy | at-risk | exhausted
	OnExhaust   string `json:"on_exhaust"`            // escalation severity implied at exhaustion
	Recommended string `json:"recommended,omitempty"` // set to OnExhaust only while exhausted
}

// DefaultRules encodes the governance error-budget doctrine:
//
//	5 WARN      within 10m → degradation debt (NOTICE)
//	3 VIOLATION within 10m → ESCALATION recommended
//	1 CRITICAL  within 1h  → CONTAINMENT immediately
func DefaultRules() []Rule {
	return []Rule{
		{Name: "degradation-debt", Tracks: severity.Warn, Limit: 5, Window: 10 * time.Minute, OnExhaust: severity.Notice},
		{Name: "governance-breach", Tracks: severity.Violation, Limit: 3, Window: 10 * time.Minute, OnExhaust: severity.Escalation},
		{Name: "integrity-exhaustion", Tracks: severity.Critical, Limit: 1, Window: time.Hour, OnExhaust: severity.Containment},
	}
}

// Ledger tracks budget consumption. Safe for concurrent use.
type Ledger struct {
	mu    sync.Mutex
	rules []Rule
	hits  map[string][]time.Time
}

// NewLedger builds a ledger over the given rules.
func NewLedger(rules []Rule) *Ledger {
	return &Ledger{rules: rules, hits: make(map[string][]time.Time, len(rules))}
}

// Global is the process-wide governance budget ledger.
var Global = NewLedger(DefaultRules())

// Record charges every rule that tracks level with one event at now.
func (l *Ledger) Record(level severity.Level, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.rules {
		if r.Tracks == level {
			l.hits[r.Name] = append(l.hits[r.Name], now)
		}
	}
}

// Status returns the live state of every budget at now, pruning expired events.
func (l *Ledger) Status(now time.Time) []Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Status, 0, len(l.rules))
	for _, r := range l.rules {
		cutoff := now.Add(-r.Window)
		kept := l.hits[r.Name][:0]
		for _, t := range l.hits[r.Name] {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		l.hits[r.Name] = kept

		consumed := len(kept)
		remaining := r.Limit - consumed
		if remaining < 0 {
			remaining = 0
		}
		state, recommended := "healthy", ""
		switch {
		case consumed >= r.Limit:
			state, recommended = "exhausted", r.OnExhaust.String()
		case r.Limit > 0 && consumed >= r.Limit-1:
			state = "at-risk"
		}
		out = append(out, Status{
			Name: r.Name, Tracks: r.Tracks.String(), Consumed: consumed, Limit: r.Limit,
			Remaining: remaining, WindowSec: int(r.Window.Seconds()), State: state,
			OnExhaust: r.OnExhaust.String(), Recommended: recommended,
		})
	}
	return out
}

// ExhaustedCount returns how many budgets are currently exhausted (for metrics).
func (l *Ledger) ExhaustedCount(now time.Time) int {
	n := 0
	for _, s := range l.Status(now) {
		if s.State == "exhausted" {
			n++
		}
	}
	return n
}
