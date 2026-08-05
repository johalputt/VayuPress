// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// Effects is the ONLY way a step causes anything, and it is where the ceilings
// are enforced.
//
// The ADR is specific that ceilings are checked in the EFFECT PATH rather than
// in the planner, so a bug in step expansion cannot route around them. That is
// what this type is: an action cannot write without asking, the asking is the
// charging, and the charging is what the run records. An action that wrote
// without calling Write would be a bug the registry cannot express — which is
// why P3's actions are tested for exactly that.
type Effects struct {
	mode   RunMode
	cap    Capability
	budget Budget
	spend  *Spend
	// refusals collects what a dry run would have done. In dry-run mode this is
	// the diff the panel renders; a dry run that captured nothing would be the
	// "dry-run lie" the ADR names as an attack.
	refusals []string
	// notes collects what the step actually DID that is worth showing but is
	// not a refused effect — a model call is the case that matters, because a
	// reader of a dry run needs to know the generation really happened.
	notes []string
}

// ErrDryRun is what the effect path returns instead of performing an effect
// when the flow is in dry-run mode. It is not a failure: the step continues,
// the effect is captured, and the run is judged on whether it got that far.
var ErrDryRun = errors.New("vayuflow: dry run — effect captured, not performed")

// Write asks permission to make one persistent change. desc is what the panel
// shows in the diff, so it must describe the change, not the intent.
//
// The write ceiling is charged BEFORE the dry-run check. A dry run that did not
// spend its budget would under-report what the live run costs, which is the
// same class of lie as skipping the model step.
func (e *Effects) Write(level WritePolicy, desc string) error {
	if !level.atMost(e.cap.Writes) {
		return fmt.Errorf("vayuflow: action %q may write at most %s but attempted %s",
			e.cap.Action, e.cap.Writes, level)
	}
	if err := e.spend.chargeWrite(e.budget); err != nil {
		return err
	}
	if e.mode != RunLive {
		e.refusals = append(e.refusals, "would write: "+desc)
		return ErrDryRun
	}
	return nil
}

// Fetch asks permission to reach a remote host. Every egress action is inert in
// a Tor Space; this refuses there regardless of what the call site does, so the
// guarantee does not depend on a caller remembering.
func (e *Effects) Fetch(desc string) error {
	if e.cap.Onion == OnionInert && clearnetBlocked() {
		return fmt.Errorf("vayuflow: action %q is inert in a Tor Space; refusing outbound fetch", e.cap.Action)
	}
	if err := e.spend.chargeEgress(e.budget); err != nil {
		return err
	}
	if e.mode != RunLive {
		e.refusals = append(e.refusals, "would fetch: "+desc)
		return ErrDryRun
	}
	return nil
}

// Model asks permission to call a model provider.
//
// A REMOTE provider is an outbound call: it is refused in a Tor Space and it
// spends the egress ceiling, because it is egress. A LOCAL provider is neither,
// and charging it against a fetch budget would make an operator raise a ceiling
// that has nothing to do with what they are limiting.
func (e *Effects) Model(local bool, desc string) error {
	if !local {
		if clearnetBlocked() {
			return fmt.Errorf("vayuflow: action %q uses a remote model provider, which is an "+
				"outbound call; refusing in a Tor Space", e.cap.Action)
		}
		if err := e.spend.chargeEgress(e.budget); err != nil {
			return err
		}
	}
	// NOT gated on dry-run, and that is deliberate. ADR §8 requires a dry run to
	// execute the whole flow with model steps GENUINELY CALLED: one that stubbed
	// the generation would tell the operator nothing about what the live run
	// produces, which is the dry-run lie the ADR names as an attack.
	//
	// A model call is how the value is produced, not an effect on the world. The
	// effect a dry run refuses is the WRITE that consumes the value — so the
	// operator sees the real generated text in the captured diff.
	//
	// The Tor refusal above is NOT relaxed for a dry run, because a remote call
	// from a Tor Space is a leak whether or not anything is written afterwards.
	where := "local"
	if !local {
		where = "remote"
	}
	e.notes = append(e.notes, "called the "+where+" model: "+desc)
	return nil
}

// clearnetBlocked reports whether the install is running as a Tor Space.
//
// It IS safefetch's kill-switch — the same one that closes every other outbound
// path (IndexNow, webhooks, remote images, embed unfurl, WKD). Pointing at the
// central switch rather than reading VAYUOS_MODE again is deliberate: a second
// reading of the same fact is a second thing that can disagree with the first,
// and the disagreement would be an automation still reaching the clearnet after
// every other subsystem had stopped.
//
// It stays a variable so the tests can drive both worlds.
var clearnetBlocked = safefetch.ClearnetBlocked

// Action performs one step. It returns the value the step produced and an
// error; ErrDryRun from the effect path is NOT an error the action should
// translate — the runner recognises it.
type Action func(ctx context.Context, params map[string]string, e *Effects) (string, error)

var (
	actionMu    sync.RWMutex
	actionImpls = map[string]Action{}
)

// RegisterAction wires an implementation to a registered capability. It panics
// on an action with no capability, because an action that can run without a
// declared blast radius is the one thing this package exists to prevent, and a
// silent skip at startup would leave the operator with a flow that does nothing
// for a reason nobody can see.
func RegisterAction(name string, fn Action) {
	if _, err := CapabilityFor(name); err != nil {
		panic("vayuflow: " + err.Error() + " — an action cannot be registered without one")
	}
	actionMu.Lock()
	defer actionMu.Unlock()
	if _, dup := actionImpls[name]; dup {
		panic("vayuflow: duplicate action implementation for " + name)
	}
	actionImpls[name] = fn
}

func actionFor(name string) (Action, bool) {
	actionMu.RLock()
	defer actionMu.RUnlock()
	fn, ok := actionImpls[name]
	return fn, ok
}

// ImplementedActions lists the action names that have an implementation, so a
// test can hold the registry and the implementations against each other.
func ImplementedActions() []string {
	actionMu.RLock()
	defer actionMu.RUnlock()
	out := make([]string, 0, len(actionImpls))
	for name := range actionImpls {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RoleResolver returns the CURRENT role of an account. The runner calls it on
// every run rather than trusting anything stored on the flow, which is what
// makes a demotion take effect.
type RoleResolver func(ctx context.Context, owner string) (string, error)

// ErrRateCeiling is returned when a flow has already started MaxRunsPerHour
// runs in the last hour. No run row is written for it — see Execute.
var ErrRateCeiling = errors.New("vayuflow: flow has reached its runs-per-hour ceiling")

// Runner executes flows.
type Runner struct {
	flows *Store
	runs  *RunStore
	roles RoleResolver
	now   func() time.Time
}

// NewRunner creates a Runner.
func NewRunner(flows *Store, runs *RunStore, roles RoleResolver) *Runner {
	return &Runner{flows: flows, runs: runs, roles: roles, now: func() time.Time { return time.Now().UTC() }}
}

// Execute runs one flow for one trigger identity.
//
// ORDER MATTERS AND IS DELIBERATE:
//
//  1. The runs-per-hour ceiling is checked FIRST and writes no row. A trigger
//     storm must not be able to grow the trail without bound, and a refusal row
//     per storm event would do exactly that. Every other refusal DOES get a row
//     and does count toward the ceiling, so the table is bounded at
//     MaxRunsPerHour per flow per hour by construction.
//  2. Begin comes next, so the idempotency key is claimed before any work. A
//     redelivery collides here rather than after the first step has run.
//  3. Authority and condition are checked INSIDE the run, so a refusal is
//     visible in the trail with the role that was actually resolved. A flow
//     that stopped working because its owner was demoted must say so.
func (r *Runner) Execute(ctx context.Context, f Flow, cause, identity string, subj Subject) (*Run, error) {
	if err := f.Complete(); err != nil {
		return nil, err
	}
	started, err := r.runs.CountSince(ctx, f.ID, r.now().Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	if started >= f.Budget.MaxRunsPerHour {
		return nil, ErrRateCeiling
	}

	role, err := r.roles(ctx, f.Owner)
	if err != nil {
		// A resolver that cannot answer fails CLOSED. "We could not check
		// whether this account still has authority" must never read as "yes".
		role = ""
	}

	run, err := r.runs.Begin(ctx, f, cause, identity, role)
	if err != nil {
		return nil, err
	}

	refuse := func(reason string) (*Run, error) {
		run.Status, run.Error = StatusRefused, reason
		if err := r.runs.Finish(ctx, run); err != nil {
			return run, err
		}
		return run, nil
	}

	if need := f.MinOwnerRole(); !ownerAtLeast(role, need) {
		return refuse(fmt.Sprintf(
			"owner %s no longer holds the %s authority this flow's actions require (resolved: %q)",
			f.Owner, need, role))
	}
	if !f.Condition.Holds(subj) {
		return refuse("the condition did not hold for this subject")
	}

	runCtx := ctx
	if f.Budget.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, f.Budget.Timeout)
		defer cancel()
	}

	// prev carries one step's output to the next. It is deliberately a single
	// value rather than a map of every prior step: a flow that can reach back
	// arbitrarily is a dataflow graph, and a dataflow graph is the thing whose
	// edges model output must never be able to choose.
	var prev string
	for _, step := range f.Steps {
		rec := StepRecord{Action: step.Action, Params: step.Params}
		start := r.now()

		// The step ceiling is charged before the step, so runaway expansion is
		// bounded whatever the step list turns out to contain.
		if err := run.Spend.chargeStep(f.Budget); err != nil {
			rec.Error = err.Error()
			rec.Duration = r.now().Sub(start).String()
			run.Steps = append(run.Steps, rec)
			run.Status, run.Error = StatusFailed, err.Error()
			return run, r.runs.Finish(ctx, run)
		}

		capab, err := CapabilityFor(step.Action)
		if err != nil {
			rec.Error = err.Error()
			run.Steps = append(run.Steps, rec)
			run.Status, run.Error = StatusFailed, err.Error()
			return run, r.runs.Finish(ctx, run)
		}
		fn, ok := actionFor(step.Action)
		if !ok {
			msg := fmt.Sprintf("action %q is registered but has no implementation in this build", step.Action)
			rec.Error = msg
			run.Steps = append(run.Steps, rec)
			run.Status, run.Error = StatusFailed, msg
			return run, r.runs.Finish(ctx, run)
		}

		eff := &Effects{mode: f.Mode, cap: capab, budget: f.Budget, spend: &run.Spend}
		// The ONLY channel between steps. prev holds the previous step's
		// validated output; a param whose whole value is the placeholder gets
		// it. Substituting into a COPY matters: mutating the stored params
		// would make a flow's second run differ from its first.
		params := substitutePrev(step.Params, prev)
		rec.Params = params
		out, err := fn(runCtx, params, eff)
		rec.Duration = r.now().Sub(start).String()
		rec.Output = out
		if len(eff.refusals) > 0 {
			rec.Refused = joinRefusals(eff.refusals)
		}
		if len(eff.notes) > 0 {
			rec.Did = joinRefusals(eff.notes)
		}
		if err != nil && !errors.Is(err, ErrDryRun) {
			rec.Error = err.Error()
			run.Steps = append(run.Steps, rec)
			run.Status, run.Error = StatusFailed, err.Error()
			return run, r.runs.Finish(ctx, run)
		}
		run.Steps = append(run.Steps, rec)
		prev = out
	}

	run.Status = StatusSucceeded
	return run, r.runs.Finish(ctx, run)
}

func joinRefusals(rs []string) string {
	out := ""
	for i, s := range rs {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}
