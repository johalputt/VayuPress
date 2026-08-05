// SPDX-License-Identifier: Apache-2.0

// Package flowaudit computes an honest posture report for the automation
// engine (ADR-0151 §10), following anonaudit and shieldaudit.
//
// The rule these reports share, and the one that matters most here: a check
// reports what is NOT true as prominently as what is. A posture panel that only
// counts green rows teaches an operator to stop reading it.
//
// The specific temptation on this page is to summarise "3 flows armed, all
// within budget" and call that a posture. It is not. The questions an operator
// actually has are: what can run without me, whose authority is it borrowing,
// is any of it reaching the network, and what does this report NOT cover.
package flowaudit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/vayuflow"
)

// Status is a check's verdict.
type Status uint8

const (
	// statusUnset is the zero value and not a valid verdict — the same reason
	// the engine's contract fields refuse theirs.
	statusUnset Status = iota
	// Pass — verified true right now, from live state.
	Pass
	// Warn — true but wants a human. A ceiling reached is the archetype: it is
	// a ceiling doing its job OR a ceiling set wrong, and only a person can say
	// which.
	Warn
	// Fail — something is wrong and an operator should act.
	Fail
	// Context — a fact worth stating that is neither good nor bad. The honest
	// ceiling is one of these: it is not a defect, and hiding it would be.
	Context
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	case Context:
		return "context"
	}
	return "unset"
}

// Check is one line of the report.
type Check struct {
	Title  string
	Status Status
	// Detail says what was measured and what it means, in prose. A status with
	// no detail is a colour, not a finding.
	Detail string
}

// Inputs are the live facts a report is computed from. Everything here is
// measured by the caller rather than by this package, so the report can be
// rendered and asserted on without a database.
type Inputs struct {
	// Wired reports whether the engine is actually running in this build.
	Wired bool
	// Flows is every stored flow, armed or not.
	Flows []vayuflow.Flow
	// Rejected maps flow ID to why its stored definition no longer validates.
	Rejected map[string]error
	// OwnerRoles maps owner ID to the role resolved from the LIVE account, or
	// an empty string when it could not be resolved.
	OwnerRoles map[string]string
	// Stats is the run summary over the reporting window.
	Stats vayuflow.Stats
	// PendingInbox is how many event triggers are waiting to be drained.
	PendingInbox int
	// OnionMode reports whether the install is running as a Tor Space.
	OnionMode bool
	// ModelLocal reports whether the configured model provider runs on this
	// host. It is meaningless unless ModelConfigured is true.
	ModelLocal bool
	// ModelConfigured reports whether any provider is configured at all.
	ModelConfigured bool
}

// Run computes the report.
func Run(in Inputs) []Check {
	var out []Check

	if !in.Wired {
		out = append(out, Check{
			Title: "Engine", Status: Fail,
			Detail: "The automation engine is not running in this build. Nothing on this page can " +
				"fire, and any flow listed is stored rather than armed.",
		})
		return out
	}

	armedLive, armedDry, disabled := 0, 0, 0
	for _, f := range in.Flows {
		switch {
		case !f.Enabled:
			disabled++
		case f.Mode == vayuflow.RunLive:
			armedLive++
		default:
			armedDry++
		}
	}
	out = append(out, Check{
		Title:  "Armed",
		Status: statusFor(armedLive == 0, Pass, Warn),
		Detail: fmt.Sprintf("%d flow(s) armed live, %d in dry-run, %d disabled. A live flow acts "+
			"without being asked; a dry-run flow executes fully and refuses its effects.",
			armedLive, armedDry, disabled),
	})

	// Authority that has outlived its grant. This is the check the ADR calls
	// out by name, and it is a Fail rather than a Warn: a flow in this state is
	// broken from the operator's point of view, whatever the trail says.
	var stale []string
	for _, f := range in.Flows {
		if !f.Enabled {
			continue
		}
		have, known := in.OwnerRoles[f.Owner]
		need := f.MinOwnerRole()
		if !known || !vayuflow.OwnerSatisfies(have, need) {
			stale = append(stale, fmt.Sprintf("%s (owner %s holds %q, needs %q)",
				f.Name, f.Owner, displayRole(have), need))
		}
	}
	sort.Strings(stale)
	out = append(out, Check{
		Title:  "Owner authority",
		Status: statusFor(len(stale) == 0, Pass, Fail),
		Detail: authorityDetail(stale),
	})

	// A flow the engine will not fire.
	var broken []string
	for id, err := range in.Rejected {
		broken = append(broken, id+" — "+err.Error())
	}
	sort.Strings(broken)
	out = append(out, Check{
		Title:  "Definitions",
		Status: statusFor(len(broken) == 0, Pass, Fail),
		Detail: brokenDetail(broken),
	})

	// A ceiling reached is a ceiling doing its job OR a ceiling set wrong.
	out = append(out, Check{
		Title:  "Budgets",
		Status: statusFor(in.Stats.BudgetCapped == 0, Pass, Warn),
		Detail: fmt.Sprintf("%d of %d run(s) in the window finished at one of their ceilings. "+
			"A ceiling reached is either the limit working or the limit set wrong — only a person "+
			"can tell which, which is why this is not reported as a success.",
			in.Stats.BudgetCapped, in.Stats.Runs),
	})

	// Reach. Egress-capable flows under OnionMode, and the confirmation that
	// they are inert rather than merely expected to be.
	egressActions := map[string]bool{}
	for _, c := range vayuflow.CapabilitiesOfKind(vayuflow.KindEgress) {
		egressActions[c.Action] = true
	}
	var reaching []string
	for _, f := range in.Flows {
		if !f.Enabled {
			continue
		}
		for _, st := range f.Steps {
			if egressActions[st.Action] {
				reaching = append(reaching, f.Name)
				break
			}
		}
	}
	sort.Strings(reaching)
	out = append(out, Check{
		Title:  "Outbound reach",
		Status: reachStatus(len(reaching), in.OnionMode),
		Detail: reachDetail(reaching, in.OnionMode),
	})

	// Model steps, split by provider locality. "AI: enabled" would be the
	// overstatement the ADR names — it tells an operator running a Tor Space
	// nothing about whether their automation reaches the clearnet.
	modelFlows := 0
	for _, f := range in.Flows {
		if !f.Enabled {
			continue
		}
		for _, s := range f.Steps {
			if c, err := vayuflow.CapabilityFor(s.Action); err == nil && c.Kind == vayuflow.KindModel {
				modelFlows++
				break
			}
		}
	}
	out = append(out, Check{
		Title:  "Model steps",
		Status: modelStatus(modelFlows, in),
		Detail: modelDetail(modelFlows, in),
	})

	// Irreversible reach — the row that exists because mail widened what a
	// model value can touch. A generation cannot publish itself, but it CAN be
	// carried into a delivered message, and an operator should read that here
	// rather than work it out from a step list.
	var irreversible []string
	for _, f := range in.Flows {
		if !f.Enabled || f.Mode != vayuflow.RunLive {
			continue
		}
		if flowHasModel(f) && flowHasIrreversible(f) {
			irreversible = append(irreversible, f.Name)
		}
	}
	sort.Strings(irreversible)
	if len(irreversible) > 0 {
		out = append(out, Check{
			Title: "Model output reaching an irreversible action", Status: Warn,
			Detail: "These flows are armed live, generate with a model, and carry a step that cannot " +
				"be undone: " + strings.Join(irreversible, ", ") + ". Nothing here is wrong, and " +
				"nothing about it is bounded by the draft ceiling — a delivered message has no draft " +
				"form. Worth knowing you armed it.",
		})
	}

	// The inbox backlog. A queue that is not shrinking means the drainer has
	// stopped, and an event flow that has silently stopped firing is the exact
	// failure this subsystem is built to make visible.
	out = append(out, Check{
		Title:  "Event backlog",
		Status: statusFor(in.PendingInbox < 50, Pass, Warn),
		Detail: fmt.Sprintf("%d event trigger(s) waiting to be considered. A backlog that does not "+
			"shrink between readings means the drainer has stopped, and event flows are no longer "+
			"firing.", in.PendingInbox),
	})

	// The honest ceiling. Last, and never a Pass.
	out = append(out, Check{
		Title: "What this report does not cover", Status: Context,
		Detail: "VayuFlow bounds what an AUTHORISED automation can do. It is not a defence against " +
			"an operator account that has been taken over: someone holding admin can arm a flow, and " +
			"every ceiling on this page is one they chose. It also says nothing about whether a " +
			"flow's output is any good — only about what it was permitted to touch.",
	})

	return out
}

func flowHasModel(f vayuflow.Flow) bool {
	for _, s := range f.Steps {
		if c, err := vayuflow.CapabilityFor(s.Action); err == nil && c.Kind == vayuflow.KindModel {
			return true
		}
	}
	return false
}

func flowHasIrreversible(f vayuflow.Flow) bool {
	for _, s := range f.Steps {
		if c, err := vayuflow.CapabilityFor(s.Action); err == nil && c.Undo == vayuflow.Irreversible {
			return true
		}
	}
	return false
}

func statusFor(good bool, yes, no Status) Status {
	if good {
		return yes
	}
	return no
}

func displayRole(r string) string {
	if strings.TrimSpace(r) == "" {
		return "no resolvable role"
	}
	return r
}

func authorityDetail(stale []string) string {
	if len(stale) == 0 {
		return "Every armed flow's owner still holds the authority its actions require. This is " +
			"re-checked on every run against the live account, not stored on the flow."
	}
	return fmt.Sprintf("%d armed flow(s) will refuse because their owner no longer holds the "+
		"required authority: %s. This is the grant expiring as it should — but the flow is not "+
		"doing what its author expected.", len(stale), strings.Join(stale, "; "))
}

func brokenDetail(broken []string) string {
	if len(broken) == 0 {
		return "Every stored flow still validates against the current build."
	}
	return fmt.Sprintf("%d enabled flow(s) no longer validate and will NOT fire: %s. An automation "+
		"an operator believes is armed and which silently never runs is the worst state this engine "+
		"can be in.", len(broken), strings.Join(broken, "; "))
}

func reachStatus(n int, onion bool) Status {
	switch {
	case n == 0:
		return Pass
	case onion:
		// Present but closed. Stated as context rather than a pass: the flows
		// exist and would reach out if the mode changed.
		return Context
	default:
		return Warn
	}
}

func reachDetail(reaching []string, onion bool) string {
	if len(reaching) == 0 {
		return "No armed flow reaches a remote host."
	}
	list := strings.Join(reaching, ", ")
	if onion {
		return fmt.Sprintf("%d armed flow(s) carry an outbound step (%s) and are INERT: this install "+
			"is running as a Tor Space, and every one of those steps is refused in the effect path by "+
			"the same kill-switch that closes the rest of the binary. They would reach out if the mode "+
			"changed.", len(reaching), list)
	}
	return fmt.Sprintf("%d armed flow(s) reach a remote host: %s. Each fetch spends the flow's egress "+
		"ceiling and goes through the guarded client.", len(reaching), list)
}

func modelStatus(n int, in Inputs) Status {
	if n == 0 {
		return Pass
	}
	if !in.ModelConfigured {
		return Fail
	}
	if in.OnionMode && !in.ModelLocal {
		return Fail
	}
	if !in.ModelLocal {
		return Warn
	}
	return Pass
}

func modelDetail(n int, in Inputs) string {
	if n == 0 {
		return "No armed flow calls a model."
	}
	switch {
	case !in.ModelConfigured:
		return fmt.Sprintf("%d armed flow(s) call a model and NO provider is configured. Those runs "+
			"will fail at the model step.", n)
	case in.OnionMode && !in.ModelLocal:
		return fmt.Sprintf("%d armed flow(s) call a REMOTE model provider while this install runs as "+
			"a Tor Space. Those steps are refused — a remote generation is an outbound call — so the "+
			"flows will fail rather than leak. Point the provider at a local model to run them here.", n)
	case !in.ModelLocal:
		return fmt.Sprintf("%d armed flow(s) call a REMOTE model provider. Each generation is an "+
			"outbound request and spends the flow's egress ceiling.", n)
	default:
		return fmt.Sprintf("%d armed flow(s) call a LOCAL model provider. No generation leaves this "+
			"host, so these run in a Tor Space too.", n)
	}
}

// Summary counts verdicts, for the panel's tiles.
func Summary(checks []Check) (pass, warn, fail, context int) {
	for _, c := range checks {
		switch c.Status {
		case Pass:
			pass++
		case Warn:
			warn++
		case Fail:
			fail++
		case Context:
			context++
		}
	}
	return
}
