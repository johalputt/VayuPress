package budget

// Actuator is the explicitly-gated control loop the package doc describes: when
// enabled, it drives an exhausted budget's recommended escalation into the mode
// engine. It is the bridge from "recommend" to "act" — and it is OFF unless an
// operator deliberately turns it on, so the default posture remains the
// accounting-only one the rest of the package guarantees.
//
// Safety properties (all enforced here, none assumed of callers):
//
//   - Opt-in: a disabled actuator is a hard no-op. With it off, the system
//     behaves exactly as the recommend-only design — no mode ever changes from
//     budget pressure.
//   - One-shot / debounced: each budget actuates once on the edge into
//     "exhausted" and will not fire again until it recovers below exhausted and
//     re-exhausts. A budget that sits exhausted across many evaluations actuates
//     once, not on every tick — no flapping.
//   - Graph-respecting: transitions are requested through the mode manager's
//     normal Transition (not ForceTransition), so the allowed-transition graph
//     still governs. An escalation that isn't permitted from the current mode is
//     a logged refusal, never a forced jump.
//   - Audited: every actuation and every refusal emits a structured governance
//     log line naming the budget, its contributors, and the target mode.

import (
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/severity"
)

// modeController is the slice of mode.Manager the actuator needs. Defining it as
// an interface keeps the actuator unit-testable without a real journal.
type modeController interface {
	Current() mode.Mode
	Transition(to mode.Mode, reason, cause string) error
}

// Actuation records one decision the actuator made on an exhausted budget, for
// the API surface and tests. Applied is false when the escalation mapped to no
// mode (informational debt) or the mode graph refused the transition.
type Actuation struct {
	Budget     string    `json:"budget"`
	OnExhaust  string    `json:"on_exhaust"`
	TargetMode string    `json:"target_mode,omitempty"`
	Applied    bool      `json:"applied"`
	Refusal    string    `json:"refusal,omitempty"`
	At         time.Time `json:"at"`
}

// Actuator watches a Ledger and, when enabled, escalates the system mode in
// response to exhausted budgets. Safe for concurrent use.
type Actuator struct {
	mu          sync.Mutex
	ledger      *Ledger
	manager     modeController
	enabled     bool
	exhausted   map[string]bool // budgets currently in the exhausted edge state (for one-shot)
	last        []Actuation     // actuations from the most recent Evaluate (for the API)
	lastApplied *Actuation      // the most recent APPLIED actuation, sticky across ticks (observability)
}

// NewActuator builds an actuator over a ledger and mode controller. enabled
// gates the entire control loop: when false, Evaluate accounts for edge state
// but never requests a transition.
func NewActuator(l *Ledger, mgr modeController, enabled bool) *Actuator {
	return &Actuator{
		ledger:    l,
		manager:   mgr,
		enabled:   enabled,
		exhausted: make(map[string]bool),
	}
}

// GlobalActuator is the process-wide actuator over the global ledger and mode
// manager. It is disabled by default — main enables it only when
// GOVERNANCE_ACTUATION is explicitly set, preserving the recommend-only posture
// out of the box.
var GlobalActuator = NewActuator(Global, mode.Global, false)

// Enabled reports whether actuation is active.
func (a *Actuator) Enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enabled
}

// SetEnabled toggles actuation. Used at startup from configuration.
func (a *Actuator) SetEnabled(on bool) {
	a.mu.Lock()
	a.enabled = on
	a.mu.Unlock()
}

// LastActuations returns a copy of the actuations from the most recent Evaluate.
func (a *Actuator) LastActuations() []Actuation {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Actuation, len(a.last))
	copy(out, a.last)
	return out
}

// LastApplied returns the most recent actuation that actually drove a mode
// transition, or nil if none has. Unlike LastActuations it is sticky across
// evaluation ticks, so the API can show "last actuation: <budget> at <time>"
// long after the poll that performed it.
func (a *Actuator) LastApplied() *Actuation {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastApplied == nil {
		return nil
	}
	cp := *a.lastApplied
	return &cp
}

// targetModeFor maps a budget's on-exhaust escalation severity to the protective
// mode it implies. Severities below Escalation are informational debt and map to
// no mode — the actuator records them but changes nothing.
func targetModeFor(s severity.Level) (mode.Mode, bool) {
	switch s {
	case severity.Escalation:
		return mode.ModeDegraded, true
	case severity.Containment:
		return mode.ModeReadOnly, true
	case severity.Critical:
		return mode.ModeQuarantined, true
	default:
		return "", false
	}
}

// Evaluate inspects every budget at now and, for each one that has just become
// exhausted, drives its recommended escalation into the mode engine (when
// enabled). It is idempotent across ticks: a budget that stays exhausted is
// actuated once, on the entering edge. It returns the actuations taken this call.
func (a *Actuator) Evaluate(now time.Time) []Actuation {
	statuses := a.ledger.Status(now)

	a.mu.Lock()
	defer a.mu.Unlock()

	var acted []Actuation
	for _, st := range statuses {
		isExhausted := st.State == "exhausted"
		wasExhausted := a.exhausted[st.Name]
		a.exhausted[st.Name] = isExhausted

		// Only act on the rising edge into exhausted; recovery just clears state.
		if !isExhausted || wasExhausted {
			continue
		}

		act := Actuation{
			Budget:    st.Name,
			OnExhaust: st.OnExhaust,
			At:        now,
		}
		lvl, _ := severity.Parse(st.OnExhaust)
		target, mapped := targetModeFor(lvl)
		switch {
		case !a.enabled:
			act.Refusal = "actuation disabled"
		case !mapped:
			act.Refusal = "informational escalation — no protective mode implied"
		default:
			act.TargetMode = string(target)
			from := a.manager.Current()
			if from == target {
				act.Applied = true // already in the protective mode; goal satisfied
			} else if err := a.manager.Transition(target, "governance budget exhausted: "+st.Name, "budget."+st.Name); err != nil {
				act.Refusal = "mode graph refused: " + err.Error()
			} else {
				act.Applied = true
			}
		}

		if act.Applied {
			applied := act
			a.lastApplied = &applied
			logging.LogJSON(logging.LogFields{
				Level: "warn", Component: "budget-actuator", Severity: "escalation",
				Msg: "budget " + st.Name + " exhausted → mode " + act.TargetMode + " (contributors: " + joinContributors(st.Contributors) + ")",
			})
		} else {
			logging.LogJSON(logging.LogFields{
				Level: "info", Component: "budget-actuator", Severity: "notice",
				Msg: "budget " + st.Name + " exhausted → no transition (" + act.Refusal + ")",
			})
		}
		acted = append(acted, act)
	}

	a.last = acted
	return acted
}

// joinContributors renders a contributor list compactly for the audit log.
func joinContributors(c []string) string {
	if len(c) == 0 {
		return "anonymous"
	}
	out := c[0]
	for _, s := range c[1:] {
		out += ", " + s
	}
	return out
}
