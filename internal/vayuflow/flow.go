// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"fmt"
	"strings"
)

// RunMode is whether a flow's effects reach the world.
type RunMode uint8

const (
	// runUnset is the zero value and is NOT a valid answer.
	//
	// The stakes here are higher than anywhere else this pattern is used: a
	// Flow{} literal that defaulted to live is an automation somebody forgot to
	// arm, running anyway. Making dry-run the zero value would be the safer
	// mistake but still a mistake — the type would be answering, on the
	// author's behalf, the one question the operator is supposed to answer.
	runUnset RunMode = iota
	// RunDryRun executes the WHOLE flow — conditions evaluated against live
	// state, model steps genuinely called, every effect captured and rendered
	// as a diff — while the effect path refuses at the capability boundary.
	//
	// A dry run that skipped the model, or stubbed the condition, would tell
	// the operator nothing about what the live run will do. That is the "dry-run
	// lie" the ADR names as an attack to test for.
	RunDryRun
	// RunLive executes and permits effects. Reaching it is an explicit,
	// per-flow, logged action.
	RunLive
)

func (m RunMode) String() string {
	switch m {
	case RunDryRun:
		return "dry-run"
	case RunLive:
		return "live"
	}
	return "unset"
}

// TriggerKind is what causes a flow to run. There are exactly three, and the
// absence of a fourth is deliberate: an inbound webhook trigger is an
// unauthenticated remote party choosing when this install does work, which is a
// denial-of-service surface and a rate-limit problem before it is a feature. It
// lands only once it can be metered by the same machinery as /api, and it
// carries its own ADR.
type TriggerKind uint8

const (
	// triggerUnset — zero value, not a valid answer.
	triggerUnset TriggerKind = iota
	// TriggerSchedule fires on a cron expression in the install timezone.
	TriggerSchedule
	// TriggerEvent fires on a typed domain event, drained from a durable inbox
	// rather than straight off the in-process bus — internal/events is lossy by
	// design, so a crash between the event and the action would lose the run
	// silently.
	TriggerEvent
	// TriggerManual fires when an operator presses Run.
	TriggerManual
)

func (k TriggerKind) String() string {
	switch k {
	case TriggerSchedule:
		return "schedule"
	case TriggerEvent:
		return "event"
	case TriggerManual:
		return "manual"
	}
	return "unset"
}

// Trigger is what causes a flow to run, with the one field its kind needs.
type Trigger struct {
	Kind TriggerKind
	// Cron is required for TriggerSchedule and must be empty otherwise.
	Cron string
	// Event is the domain event type name, required for TriggerEvent and empty
	// otherwise.
	Event string
}

// Complete reports whether the trigger is fully and coherently specified.
// "Coherently" is doing real work here: a schedule trigger carrying an event
// name is not a partially-filled form, it is two different intentions in one
// value, and accepting it means one of them is silently ignored.
func (t Trigger) Complete() error {
	switch t.Kind {
	case triggerUnset:
		return fmt.Errorf("vayuflow: trigger does not say what fires it")
	case TriggerSchedule:
		if strings.TrimSpace(t.Cron) == "" {
			return fmt.Errorf("vayuflow: a schedule trigger needs a cron expression")
		}
		// Parsed at SAVE time, not at fire time. An expression that only fails
		// when the ticker reaches it produces a flow the operator believes is
		// armed and which silently never runs — and they would find out by the
		// digest that did not arrive.
		if _, err := ParseCron(t.Cron); err != nil {
			return err
		}
		if t.Event != "" {
			return fmt.Errorf("vayuflow: a schedule trigger must not also name an event (%q)", t.Event)
		}
	case TriggerEvent:
		if strings.TrimSpace(t.Event) == "" {
			return fmt.Errorf("vayuflow: an event trigger needs an event type")
		}
		// An event nothing publishes will never arrive, so the flow would sit
		// armed and silent. Refusing it here is the same reasoning as parsing
		// the cron at save time.
		if !knownEvent(t.Event) {
			return fmt.Errorf("vayuflow: %q is not an event this install publishes (known: %v)",
				t.Event, KnownEvents())
		}
		if t.Cron != "" {
			return fmt.Errorf("vayuflow: an event trigger must not also carry a cron expression (%q)", t.Cron)
		}
	case TriggerManual:
		if t.Cron != "" || t.Event != "" {
			return fmt.Errorf("vayuflow: a manual trigger takes neither a cron expression nor an event")
		}
	default:
		return fmt.Errorf("vayuflow: unknown trigger kind %d", t.Kind)
	}
	return nil
}

// Step is one action invocation. Its Action must name a registered capability;
// there is no way to invoke an action the registry does not know about.
//
// Note what a Step does NOT have: a branch target. The step graph is a fixed,
// ordered list settled at save time. This is the structural half of the ADR's
// rule that a model step produces a VALUE and never SELECTS a step — there is
// simply nowhere for model output to steer execution to, so injected text in a
// comment or an inbound mail has no edge to take.
type Step struct {
	Action string
	// Params are the action's inputs, opaque to this package and validated by
	// the action itself.
	Params map[string]string
}

// Flow is one automation. It is inert until armed, and every field that can
// cause an effect on the world is bounded before it can run.
type Flow struct {
	ID      string
	Name    string
	Enabled bool
	Trigger Trigger
	// Condition is a closed set of typed predicates. Its zero value means
	// "always", which IS an explicitly allowed answer here — unlike the
	// contract fields, an absent condition is not an unanswered question, it is
	// the answer "no condition". The ADR says so in as many words.
	Condition Condition
	// Steps execute in order and short-circuit on failure.
	Steps []Step
	// Budget is required. See budget.go.
	Budget Budget
	// Owner is the operator who armed it. The authority is theirs, and it is
	// re-checked against the capability floor at RUN time.
	Owner string
	// Mode is dry-run or live. Zero value is invalid.
	Mode RunMode
	// Version bumps on every edit; runs record the version they executed, so a
	// run in the trail can always be read against the flow as it was then
	// rather than as it is now.
	Version int
}

// MaxSteps bounds the step list at save time, independently of the per-run
// budget. The budget stops a run; this stops a flow document from being
// unreasonable in the first place.
const MaxSteps = 32

// Complete reports whether the flow may be saved. It checks the contract
// fields, then every step against the registry, then the coherence between
// them — a flow whose declared budget cannot accommodate its own step list is
// refused here rather than failing on its first run.
func (f Flow) Complete() error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("vayuflow: flow has no name")
	}
	if f.Mode == runUnset {
		return fmt.Errorf("vayuflow: flow %q does not say whether it is a dry run or live", f.Name)
	}
	if strings.TrimSpace(f.Owner) == "" {
		return fmt.Errorf("vayuflow: flow %q has no owner", f.Name)
	}
	if err := f.Trigger.Complete(); err != nil {
		return fmt.Errorf("vayuflow: flow %q: %w", f.Name, err)
	}
	if err := f.Budget.Complete(); err != nil {
		return fmt.Errorf("vayuflow: flow %q: %w", f.Name, err)
	}
	if err := f.Condition.Complete(); err != nil {
		return fmt.Errorf("vayuflow: flow %q: %w", f.Name, err)
	}
	if len(f.Steps) == 0 {
		return fmt.Errorf("vayuflow: flow %q has no steps", f.Name)
	}
	if len(f.Steps) > MaxSteps {
		return fmt.Errorf("vayuflow: flow %q has %d steps, above the %d limit", f.Name, len(f.Steps), MaxSteps)
	}
	if len(f.Steps) > f.Budget.MaxStepsPerRun {
		return fmt.Errorf("vayuflow: flow %q declares %d steps but a MaxStepsPerRun of %d, "+
			"so it could never finish a run", f.Name, len(f.Steps), f.Budget.MaxStepsPerRun)
	}
	for i, s := range f.Steps {
		if _, err := CapabilityFor(s.Action); err != nil {
			return fmt.Errorf("vayuflow: flow %q step %d: %w", f.Name, i+1, err)
		}
	}
	return nil
}

// The owner's ROLE is deliberately not a field on Flow. It is resolved and
// checked against the live account at run time, because storing it would freeze
// an authority answer that is supposed to be able to change underneath the
// flow — which is the whole point of re-checking.

// HighestWrite returns the strongest write policy any of the flow's steps
// carries. It is what the panel shows as the flow's blast radius, and what §6
// clamps model output against.
func (f Flow) HighestWrite() WritePolicy {
	highest := WriteNone
	for _, s := range f.Steps {
		c, err := CapabilityFor(s.Action)
		if err != nil {
			continue
		}
		if c.Writes > highest {
			highest = c.Writes
		}
	}
	return highest
}

// NeedsEgress reports whether any step reaches a remote host.
//
// It keys on the KIND, not on the Onion policy, and the difference is a claim
// the panel was getting wrong. model.draft.generate is registered OnionInert —
// correctly, because a hosted provider makes it an outbound call — so an
// Onion-keyed answer told an operator whose only step generated a draft against
// a model on this very machine that their flow "reaches a remote host". The
// posture report asks CapabilitiesOfKind(KindEgress) and got the other answer:
// two implementations of one question, disagreeing on the same flow.
func (f Flow) NeedsEgress() bool {
	for _, s := range f.Steps {
		if c, err := CapabilityFor(s.Action); err == nil && c.Kind == KindEgress {
			return true
		}
	}
	return false
}

// NeedsModel reports whether any step calls a model provider.
//
// It is separate from NeedsEgress because the two are different questions with
// different answers: a model step is outbound only when the configured provider
// is remote, and the flow document cannot know which. The panel has to ask the
// install that, so it needs to be able to ask this first.
func (f Flow) NeedsModel() bool {
	for _, s := range f.Steps {
		if c, err := CapabilityFor(s.Action); err == nil && c.Kind == KindModel {
			return true
		}
	}
	return false
}

// MinOwnerRole returns the highest authority floor across the flow's steps —
// the role its owner must still hold for the whole flow to run.
func (f Flow) MinOwnerRole() string {
	need := RoleAuthor
	for _, s := range f.Steps {
		c, err := CapabilityFor(s.Action)
		if err != nil {
			continue
		}
		if ownerAtLeast(c.MinRole, need) {
			need = c.MinRole
		}
	}
	return need
}
