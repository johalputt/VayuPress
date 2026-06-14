// Package severity defines VayuPress's formal operational severity taxonomy.
//
// It is a fixed, ordered set of levels, each carrying explicit operational
// semantics: what it means, what the operator should expect, how the runtime
// escalates, how it presents in the timeline and topology, and how it interacts
// with the policy engine. This is the shared vocabulary for every governance and
// runtime signal, so the operational model stays unambiguous as the number of
// signals grows. Levels are totally ordered (Observe < … < Critical), so
// thresholds and "at least this severe" comparisons are well defined.
package severity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Level is an operational severity. The zero value is Observe.
type Level int

const (
	Observe     Level = iota // informational state; no action implied
	Notice                   // expected but relevant; worth recording
	Warn                     // degraded condition; watch / investigate
	Violation                // governance breach; review + remediate
	Escalation               // adaptive response triggered; the system is reacting
	Containment              // isolation activated; blast radius deliberately limited
	Critical                 // kernel/data integrity threatened; immediate operator action
)

// Meta is the full semantic definition of a severity level.
type Meta struct {
	Level             Level  `json:"-"`
	Name              string `json:"name"`
	Rank              int    `json:"rank"`
	Meaning           string `json:"meaning"`
	OperatorExpect    string `json:"operator_expectation"`
	Escalation        string `json:"escalation_behavior"`
	TimelineClass     string `json:"timeline_class"` // tl-* CSS severity class
	TopologyColor     string `json:"topology_color"` // hex for topology node rendering
	PolicyInteraction string `json:"policy_interaction"`
}

// registry is the ordered, authoritative taxonomy. Order MUST match the Level
// constants so registry[level] is O(1).
var registry = []Meta{
	{
		Level: Observe, Name: "OBSERVE", Rank: 0,
		Meaning:           "Informational runtime state. The system is operating as designed.",
		OperatorExpect:    "No action. Context only.",
		Escalation:        "None.",
		TimelineClass:     "tl-info",
		TopologyColor:     "#64748b",
		PolicyInteraction: "Ignored by policy evaluation.",
	},
	{
		Level: Notice, Name: "NOTICE", Rank: 1,
		Meaning:           "An expected but operationally relevant event (e.g. a normal mode entry or armed control).",
		OperatorExpect:    "Awareness; no intervention.",
		Escalation:        "None; recorded for narrative continuity.",
		TimelineClass:     "tl-info",
		TopologyColor:     "#06b6d4",
		PolicyInteraction: "Recorded; does not affect policy verdicts.",
	},
	{
		Level: Warn, Name: "WARN", Rank: 2,
		Meaning:           "A degraded condition or a counter advancing toward a threshold.",
		OperatorExpect:    "Investigate; confirm whether self-correcting.",
		Escalation:        "May advance an escalation counter; no automatic state change yet.",
		TimelineClass:     "tl-warn",
		TopologyColor:     "#f59e0b",
		PolicyInteraction: "Surfaced in policy reports as a soft signal.",
	},
	{
		Level: Violation, Name: "VIOLATION", Rank: 3,
		Meaning:           "A governance constraint was breached (e.g. a CSP violation, a rejected invariant).",
		OperatorExpect:    "Review the breach and remediate the cause; confirm no exploitation.",
		Escalation:        "Counts toward governance breach budgets; repeated breaches can trigger escalation.",
		TimelineClass:     "tl-warn",
		TopologyColor:     "#8b5cf6",
		PolicyInteraction: "Fails the relevant policy assertion.",
	},
	{
		Level: Escalation, Name: "ESCALATION", Rank: 4,
		Meaning:           "The adaptive runtime has reacted — a fault→mode rule fired or recovery began.",
		OperatorExpect:    "Track the escalation chain; verify the response is appropriate.",
		Escalation:        "An automatic mode transition is in progress or imminent.",
		TimelineClass:     "tl-err",
		TopologyColor:     "#f97316",
		PolicyInteraction: "Reflects an active policy-driven adaptation.",
	},
	{
		Level: Containment, Name: "CONTAINMENT", Rank: 5,
		Meaning:           "Isolation has been activated (read-only or quarantined) to bound blast radius.",
		OperatorExpect:    "Confirm containment held; plan recovery once the cause clears.",
		Escalation:        "Writes/plugins/federation are suspended per the mode contract.",
		TimelineClass:     "tl-err",
		TopologyColor:     "#ef4444",
		PolicyInteraction: "Enforced by the mode state machine; policy holds the system here until safe.",
	},
	{
		Level: Critical, Name: "CRITICAL", Rank: 6,
		Meaning:           "Kernel or data integrity is threatened (corruption, checksum drift, integrity-check failure).",
		OperatorExpect:    "Immediate action: stop ingress, preserve evidence, restore from a verified backup.",
		Escalation:        "Hard transition to a protective mode; refuses to serve untrusted state.",
		TimelineClass:     "tl-err",
		TopologyColor:     "#b91c1c",
		PolicyInteraction: "A non-negotiable invariant failure; overrides softer signals.",
	},
}

// Meta returns the level's full semantic definition.
func (l Level) Meta() Meta {
	if int(l) < 0 || int(l) >= len(registry) {
		return registry[Observe]
	}
	return registry[l]
}

// String returns the canonical uppercase name (OBSERVE … CRITICAL).
func (l Level) String() string { return l.Meta().Name }

// TimelineClass returns the tl-* CSS class for rendering this level in the timeline.
func (l Level) TimelineClass() string { return l.Meta().TimelineClass }

// AtLeast reports whether l is at least as severe as threshold.
func (l Level) AtLeast(threshold Level) bool { return l >= threshold }

// MarshalJSON emits the canonical name so JSON consumers read a stable vocabulary.
func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

// UnmarshalJSON accepts the canonical name (case-insensitive).
func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	lvl, ok := Parse(s)
	if !ok {
		return fmt.Errorf("severity: unknown level %q", s)
	}
	*l = lvl
	return nil
}

// Parse resolves a level name (case-insensitive) to its Level.
func Parse(name string) (Level, bool) {
	up := strings.ToUpper(strings.TrimSpace(name))
	for _, m := range registry {
		if m.Name == up {
			return m.Level, true
		}
	}
	return Observe, false
}

// All returns the full taxonomy in severity order — used to publish a
// self-documenting, auditable description of the operational vocabulary.
func All() []Meta {
	out := make([]Meta, len(registry))
	copy(out, registry)
	return out
}
