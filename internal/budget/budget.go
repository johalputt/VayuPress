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
//
// Two governance affordances make exhaustion explainable and recoverable rather
// than opaque:
//
//   - Provenance: every charge can carry a source label, so an exhausted budget
//     names which subsystems consumed it (debt is attributable, not anonymous).
//   - Recovery: an operator can Acknowledge a budget, clearing its current debt
//     window and stamping when — bounded recovery semantics that stop short of
//     autonomous actuation.
package budget

import (
	"sort"
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

// charge is a single consumption event: when it happened and (optionally) which
// subsystem caused it, so accumulated debt remains attributable.
type charge struct {
	at     time.Time
	source string
}

// Status is the live state of a budget.
type Status struct {
	Name         string   `json:"name"`
	Tracks       string   `json:"tracks"`
	Consumed     int      `json:"consumed"`
	Limit        int      `json:"limit"`
	Remaining    int      `json:"remaining"`
	WindowSec    int      `json:"window_seconds"`
	State        string   `json:"state"`                       // healthy | at-risk | exhausted
	OnExhaust    string   `json:"on_exhaust"`                  // escalation severity implied at exhaustion
	Recommended  string   `json:"recommended,omitempty"`       // set to OnExhaust only while exhausted
	Contributors []string `json:"contributors,omitempty"`      // distinct sources that consumed this budget (most→least)
	AckedAgoSec  int      `json:"acked_ago_seconds,omitempty"` // seconds since an operator acknowledged, if recent
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
	hits  map[string][]charge
	acks  map[string]time.Time
}

// NewLedger builds a ledger over the given rules.
func NewLedger(rules []Rule) *Ledger {
	return &Ledger{
		rules: rules,
		hits:  make(map[string][]charge, len(rules)),
		acks:  make(map[string]time.Time, len(rules)),
	}
}

// Global is the process-wide governance budget ledger.
var Global = NewLedger(DefaultRules())

// Record charges every rule that tracks level with one anonymous event at now.
func (l *Ledger) Record(level severity.Level, now time.Time) {
	l.RecordFrom(level, "", now)
}

// RecordFrom charges every rule that tracks level with one event at now,
// attributing it to source so exhaustion can name what consumed the budget.
func (l *Ledger) RecordFrom(level severity.Level, source string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.rules {
		if r.Tracks == level {
			l.hits[r.Name] = append(l.hits[r.Name], charge{at: now, source: source})
		}
	}
}

// Acknowledge clears the current debt window for the named budget and stamps the
// acknowledgment time — operator-driven recovery that resets accumulation without
// waiting for the window to roll off. Returns false if no such budget exists.
func (l *Ledger) Acknowledge(name string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	found := false
	for _, r := range l.rules {
		if r.Name == name {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	delete(l.hits, name)
	l.acks[name] = now
	return true
}

// Status returns the live state of every budget at now, pruning expired events.
func (l *Ledger) Status(now time.Time) []Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Status, 0, len(l.rules))
	for _, r := range l.rules {
		cutoff := now.Add(-r.Window)
		kept := l.hits[r.Name][:0]
		sources := map[string]int{}
		for _, c := range l.hits[r.Name] {
			if c.at.After(cutoff) {
				kept = append(kept, c)
				if c.source != "" {
					sources[c.source]++
				}
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
		case consumed > 0 && consumed >= r.Limit-1:
			// "at-risk" means one event away from exhaustion AND something has
			// already been consumed. Requiring consumed>0 stops a limit-1 budget
			// (e.g. integrity-exhaustion) from reporting at-risk while it has had
			// zero events — previously `consumed >= Limit-1` was `0 >= 0`, so such
			// a budget could never show healthy.
			state = "at-risk"
		}

		// An acknowledgment only stays relevant for one window — after that the
		// budget has fully rolled over on its own and the ack is no longer news.
		acked := 0
		if t, ok := l.acks[r.Name]; ok {
			if since := now.Sub(t); since >= 0 && since <= r.Window {
				acked = int(since.Seconds())
			}
		}

		out = append(out, Status{
			Name: r.Name, Tracks: r.Tracks.String(), Consumed: consumed, Limit: r.Limit,
			Remaining: remaining, WindowSec: int(r.Window.Seconds()), State: state,
			OnExhaust: r.OnExhaust.String(), Recommended: recommended,
			Contributors: rankSources(sources), AckedAgoSec: acked,
		})
	}
	return out
}

// rankSources orders contributing sources by consumption (most first, then name)
// so the most significant cause of debt leads.
func rankSources(m map[string]int) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
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
