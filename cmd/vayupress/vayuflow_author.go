// SPDX-License-Identifier: Apache-2.0

package main

// vayuflow_author.go — creating, editing, enabling and deleting a flow.
//
// This file exists because the engine shipped without it. Every other piece of
// ADR-0151 was built and wired — the registry, the budgets, the runner, the
// ticker, the durable inbox, the posture report and the panel — and there was
// no way to put a flow into the table. `Store.Save` and `Store.Delete` had no
// non-test caller anywhere in the binary; renaming both left it linking
// cleanly. The panel listed flows, armed them and ran them, on a table nothing
// could write to.
//
// It survived a release because the adversarial pass asked the seven questions
// the ADR pre-declared — trigger storms, budget bypass, authority outliving its
// grant, injection, Tor leaks, idempotency, the dry-run lie — and every one of
// those was about what the engine would do to an operator who HAD a flow. None
// of them asked whether one could exist. An audit scoped to a threat model
// cannot find a missing feature, and a feature that is missing does not fail
// any test that was written on the assumption it was there.
//
// Two rules shape what follows.
//
// THE DOCUMENT IS VALIDATED BY THE STORE, NOT BY THIS FILE. `Store.Save` calls
// `Flow.Complete()` and refuses anything incomplete. These handlers parse and
// map; they do not re-implement the contract. A second validator is a second
// thing that can disagree with the first, and the one that would win is the one
// nobody is looking at.
//
// EDITING IS NOT ARMING. A save never changes Mode or Enabled. Those move only
// through their own endpoints, each with its own audit entry recording the
// actor and the prior value — because "I edited the prompt" and "I put this
// live" are different decisions and must be separately answerable months later.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayuflow"
)

// flowStepInput is one step as the form sends it.
type flowStepInput struct {
	Action string            `json:"action"`
	Params map[string]string `json:"params"`
}

// flowSaveInput is the whole document as the form sends it.
//
// Mode and Enabled are ABSENT by design — see the file comment. A field that
// cannot be sent cannot be sent by accident.
type flowSaveInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Trigger struct {
		Kind  string `json:"kind"`
		Cron  string `json:"cron"`
		Event string `json:"event"`
	} `json:"trigger"`
	Condition struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"condition"`
	Steps  []flowStepInput `json:"steps"`
	Budget struct {
		MaxStepsPerRun  int `json:"max_steps_per_run"`
		MaxRunsPerHour  int `json:"max_runs_per_hour"`
		MaxWritesPerRun int `json:"max_writes_per_run"`
		MaxEgressPerRun int `json:"max_egress_per_run"`
		TimeoutSeconds  int `json:"timeout_seconds"`
	} `json:"budget"`
}

// maxFlowBody bounds the request. A flow document is small; anything larger is
// not a flow.
const maxFlowBody = 64 << 10

// handleOSVayuFlowSave creates or updates a flow.
func (a *App) handleOSVayuFlowSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.flowStore == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", "automation store unavailable", "")
		return
	}

	var in flowSaveInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFlowBody))
	// Unknown fields are refused rather than ignored. A form that sends `mode`
	// and is silently obeyed-but-ignored teaches an operator that the field
	// works; refusing teaches them where arming actually lives.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			"could not read the flow document: "+err.Error(), "")
		return
	}

	f, err := a.flowFromInput(r, in)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-flow", err.Error(), "")
		return
	}

	creating := strings.TrimSpace(in.ID) == ""
	// Save validates. Whatever Complete() refuses comes back here as the reason,
	// verbatim, because those messages are written to be read by the person who
	// filled the form — "does not say whether it is a dry run or live" is more
	// use than "validation failed".
	if err := a.flowStore.Save(r.Context(), &f); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-flow", err.Error(), "")
		return
	}

	verb := "vayuflow.edit"
	if creating {
		verb = "vayuflow.create"
	}
	dbpkg.AuditLog(verb, dbpkg.AuditActor(r), f.ID,
		fmt.Sprintf("%s · %s · %d step(s) · v%d", f.Name, vayuflow.DescribeTrigger(f.Trigger), len(f.Steps), f.Version))

	writeJSON(w, r, http.StatusOK, map[string]any{
		"status": "ok", "id": f.ID, "version": f.Version, "created": creating,
		// Echoed so the form can show what the flow will be allowed to do
		// without re-deriving it in JavaScript from a copy of the registry.
		"highest_write": f.HighestWrite().String(),
		"min_owner":     f.MinOwnerRole(),
		"mode":          f.Mode.String(),
		"enabled":       f.Enabled,
	})
}

// flowFromInput maps the wire form onto a Flow.
//
// On an EDIT it loads the stored flow first and carries Mode, Enabled and Owner
// across unchanged. Owner in particular: the authority a flow borrows is the
// person who armed it, and letting an edit silently re-point that at whoever
// happened to press Save would make the run-time authority check meaningless —
// a demoted owner could restore their flow's reach by editing its name.
func (a *App) flowFromInput(r *http.Request, in flowSaveInput) (vayuflow.Flow, error) {
	f := vayuflow.Flow{
		Name:  strings.TrimSpace(in.Name),
		Steps: make([]vayuflow.Step, 0, len(in.Steps)),
		Budget: vayuflow.Budget{
			MaxStepsPerRun:  in.Budget.MaxStepsPerRun,
			MaxRunsPerHour:  in.Budget.MaxRunsPerHour,
			MaxWritesPerRun: in.Budget.MaxWritesPerRun,
			MaxEgressPerRun: in.Budget.MaxEgressPerRun,
			Timeout:         time.Duration(in.Budget.TimeoutSeconds) * time.Second,
		},
	}

	if id := strings.TrimSpace(in.ID); id != "" {
		prior, err := a.flowStore.Get(r.Context(), id)
		if err != nil {
			return vayuflow.Flow{}, err
		}
		// An armed, live flow is disarmed before it is edited — the same guard
		// the delete path carries, and it belongs here MORE than there.
		//
		// Deleting a live automation stops its effects. Editing one changes
		// them, with no re-confirmation and no second look: the flow keeps its
		// owner so the run-time authority check still passes, and keeps its live
		// mode so nothing asks again. Guarding only the delete left the more
		// dangerous operation as the easier one.
		if prior.Enabled && prior.Mode == vayuflow.RunLive {
			return vayuflow.Flow{}, fmt.Errorf("this flow is switched on and armed live, so it can " +
				"write to the world on its next trigger. Return it to dry-run or switch it off " +
				"before changing what it does, then arm it again once you have looked at the diff")
		}
		f.ID, f.Mode, f.Enabled, f.Owner = prior.ID, prior.Mode, prior.Enabled, prior.Owner
	} else {
		// A new flow starts in dry-run and switched off. Both are the safe
		// answer AND the honest one: nobody has yet looked at what this flow
		// does, so it does not get to do it.
		f.Mode, f.Enabled = vayuflow.RunDryRun, false
		f.Owner = osActorID(r)
		if f.Owner == "" {
			return vayuflow.Flow{}, fmt.Errorf("cannot tell who is creating this flow, and a flow " +
				"borrows its owner's authority — refusing to store one with no owner")
		}
	}

	kind, ok := vayuflow.TriggerKindFor(in.Trigger.Kind)
	if !ok {
		return vayuflow.Flow{}, fmt.Errorf("%q is not a trigger this install has (schedule, event or manual)",
			in.Trigger.Kind)
	}
	f.Trigger = vayuflow.Trigger{
		Kind:  kind,
		Cron:  strings.TrimSpace(in.Trigger.Cron),
		Event: strings.TrimSpace(in.Trigger.Event),
	}

	cond, err := vayuflow.ConditionFor(in.Condition.Kind, strings.TrimSpace(in.Condition.Value))
	if err != nil {
		return vayuflow.Flow{}, err
	}
	f.Condition = cond

	for i, s := range in.Steps {
		action := strings.TrimSpace(s.Action)
		if action == "" {
			return vayuflow.Flow{}, fmt.Errorf("step %d has no action", i+1)
		}
		params := map[string]string{}
		for k, v := range s.Params {
			if strings.TrimSpace(v) == "" {
				continue
			}
			params[k] = v
		}
		f.Steps = append(f.Steps, vayuflow.Step{Action: action, Params: params})
	}
	return f, nil
}

// osActorID is the signed-in operator's user ID — the value the runner later
// hands to the role resolver.
//
// It deliberately does NOT fall back to an API key or a client IP. A flow's
// owner is an ACCOUNT whose role is re-read on every run; an owner string that
// does not resolve to an account makes ownerAtLeast fail closed forever, which
// would produce a flow that saves cleanly and then refuses every time it fires,
// for a reason nothing on the page could explain.
func osActorID(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return u.ID
	}
	return ""
}

// handleOSVayuFlowEnable switches a flow on or off.
func (a *App) handleOSVayuFlowEnable(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.flowStore == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", "automation store unavailable", "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "no flow id", "")
		return
	}
	on, ok := flowEnableFor(r.URL.Query().Get("on"))
	if !ok {
		// Same reasoning as armModeFor: an unrecognised value must not pick a
		// state on the operator's behalf, and off is not a "safe default" when
		// the operator may have meant on.
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			"expected on=true or on=false; nothing was changed", "")
		return
	}
	prior, err := a.flowStore.SetEnabled(r.Context(), id, on)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayuflow.enable", dbpkg.AuditActor(r), id,
		fmt.Sprintf("%s -> %s", flowEnabledWord(prior), flowEnabledWord(on)))
	writeJSON(w, r, http.StatusOK, map[string]any{
		"status": "ok", "enabled": on, "prior": prior,
	})
}

func flowEnableFor(v string) (bool, bool) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "true", "1", "on", "yes":
		return true, true
	case "false", "0", "off", "no":
		return false, true
	}
	return false, false
}

func flowEnabledWord(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

// handleOSVayuFlowDelete removes a flow.
//
// The flow goes; its runs stay. A run row is the record of something this
// install actually did, and deleting the description of the work would erase
// the only account of it — the audit trail would show effects with no cause.
// The runs age out on their own retention window instead.
func (a *App) handleOSVayuFlowDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.flowStore == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", "automation store unavailable", "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "no flow id", "")
		return
	}
	// Read it first, so the audit entry can say WHAT was deleted. An entry
	// carrying only an opaque id is a record that something happened, which is
	// the half of the story nobody needs.
	f, err := a.flowStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", err.Error(), "")
		return
	}
	// A live flow is disarmed by its own action, deliberately: deleting
	// something that is currently allowed to write to the world should be two
	// decisions, and the second one should be taken while looking at what the
	// first one turned off.
	if f.Enabled && f.Mode == vayuflow.RunLive {
		writeAPIError(w, r, http.StatusConflict, "still-live",
			"this flow is enabled and armed live. Return it to dry-run or switch it off first, "+
				"so that turning off something that can write to the world is its own decision.", "")
		return
	}
	if err := a.flowStore.Delete(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "delete-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayuflow.delete", dbpkg.AuditActor(r), id,
		f.Name+" · "+strconv.Itoa(len(f.Steps))+" step(s) · runs kept")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "deleted": id})
}
