// SPDX-License-Identifier: Apache-2.0

package main

// vayuflow_author_audit_test.go — the adversarial pass over the authoring
// surface, which is the newest attack surface in this release and the one no
// earlier audit could have covered because it did not exist.
//
// Each finding below failed against the code as first written.

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuflow"
)

// ── Finding. The dangerous operation was easier than the safe one. ───────────
//
// Deleting a flow that is enabled AND armed live is refused: the operator has
// to disarm it first, so that switching off something which can write to the
// world is its own decision. Editing that same flow was not refused at all.
//
// Editing is the more dangerous of the two. Deleting a live automation STOPS
// its effects; editing one CHANGES them, silently, with no re-confirmation and
// no second look. An attacker — or an operator with the wrong flow open in
// another tab — could turn a draft-writing automation into a mail-sending one
// while it stayed armed, and the next trigger would send. The flow keeps its
// owner, so the run-time authority check passes; it keeps its live mode, so
// nothing asks again.
//
// The guard has to cover both, or it is theatre on the operation that needed it
// less.
func TestEditingAnArmedLiveFlowIsRefusedTheWayDeletingOneIs(t *testing.T) {
	db := newFlowTestDB(t)
	a := &App{flowStore: vayuflow.NewStore(db)}
	ctx := context.Background()

	stored, err := a.flowFromInput(signedIn(t, "owner-1"), minimalInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.flowStore.Save(ctx, &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := a.flowStore.SetEnabled(ctx, stored.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.flowStore.SetMode(ctx, stored.ID, vayuflow.RunLive); err != nil {
		t.Fatal(err)
	}

	// The attack: repoint a live, armed automation at a different action.
	edit := minimalInput()
	edit.ID = stored.ID
	edit.Steps = []flowStepInput{{Action: "mail.send", Params: map[string]string{
		"to": "victim@example.com", "subject": "s", "body": "b"}}}

	_, err = a.flowFromInput(signedIn(t, "owner-1"), edit)
	if err == nil {
		t.Fatal("what an armed, live automation does was changed with no disarm and no second " +
			"confirmation — while DELETING the same flow is refused until it is disarmed")
	}
	if !strings.Contains(err.Error(), "dry-run") && !strings.Contains(err.Error(), "switch it off") {
		t.Errorf("the refusal does not tell the operator how to proceed: %q", err)
	}
}

// The guard must not seize up the ordinary case. A flow that is switched off,
// or armed only to dry-run, is edited freely — that is the whole working loop,
// and a guard that blocked it would push operators to delete-and-recreate,
// which loses the run trail's link to the flow.
func TestEditingAFlowThatCannotWriteLiveIsUnobstructed(t *testing.T) {
	db := newFlowTestDB(t)
	a := &App{flowStore: vayuflow.NewStore(db)}
	ctx := context.Background()

	for name, setup := range map[string]struct{ enabled, live bool }{
		"off and dry-run": {false, false},
		"off but armed":   {false, true},
		"on but dry-run":  {true, false},
	} {
		stored, err := a.flowFromInput(signedIn(t, "owner-1"), minimalInput())
		if err != nil {
			t.Fatal(err)
		}
		if err := a.flowStore.Save(ctx, &stored); err != nil {
			t.Fatal(err)
		}
		if _, err := a.flowStore.SetEnabled(ctx, stored.ID, setup.enabled); err != nil {
			t.Fatal(err)
		}
		mode := vayuflow.RunDryRun
		if setup.live {
			mode = vayuflow.RunLive
		}
		if _, err := a.flowStore.SetMode(ctx, stored.ID, mode); err != nil {
			t.Fatal(err)
		}

		edit := minimalInput()
		edit.ID = stored.ID
		edit.Name = "Edited " + name
		if _, err := a.flowFromInput(signedIn(t, "owner-1"), edit); err != nil {
			t.Errorf("%s: editing was refused, which pushes operators to delete-and-recreate: %v",
				name, err)
		}
	}
}

// ── Attacks that found nothing, recorded because trying is the evidence. ─────
//
//   - Creating a flow with no operator identity. Refused: an owner that does not
//     resolve to an account would make the run-time authority check fail closed
//     forever, producing a flow that saves cleanly and refuses on every fire.
//     Covered by TestAFlowCannotBeCreatedWithoutAnIdentifiableOwner.
//   - Smuggling mode, enabled or owner through the save document. The wire
//     struct has no field for any of them and unknown fields are refused rather
//     than ignored. Covered by TestTheSaveDocumentRefusesToCarryArmingFields.
//   - Re-pointing a flow's owner by editing it. The stored owner is carried
//     across; a demoted owner cannot restore their flow's reach by renaming it.
//     Covered by TestAnEditNeverRePointsTheOwnerOrArmsTheFlow.
//   - Exceeding a ceiling through the form. Every budget field goes through
//     Budget.Complete, which refuses zero and refuses anything above its own
//     cap, so "unlimited" is no more expressible from the form than from a
//     literal. Confirmed below.
//   - Naming an action the registry does not have. Flow.Complete checks every
//     step against the registry before the row is written. Confirmed below.

func TestTheFormCannotStoreACeilingTheEngineWouldRefuse(t *testing.T) {
	a := &App{}
	for name, mutate := range map[string]func(*flowSaveInput){
		"zero writes":     func(in *flowSaveInput) { in.Budget.MaxWritesPerRun = 0 },
		"zero steps":      func(in *flowSaveInput) { in.Budget.MaxStepsPerRun = 0 },
		"zero runs":       func(in *flowSaveInput) { in.Budget.MaxRunsPerHour = 0 },
		"no timeout":      func(in *flowSaveInput) { in.Budget.TimeoutSeconds = 0 },
		"absurd ceiling":  func(in *flowSaveInput) { in.Budget.MaxWritesPerRun = 1 << 30 },
		"absurd timeout":  func(in *flowSaveInput) { in.Budget.TimeoutSeconds = 60 * 60 * 24 * 30 },
		"negative writes": func(in *flowSaveInput) { in.Budget.MaxWritesPerRun = -1 },
	} {
		in := minimalInput()
		mutate(&in)
		f, err := a.flowFromInput(signedIn(t, "user-1"), in)
		if err != nil {
			continue // refused during mapping, which is also a refusal
		}
		if err := f.Complete(); err == nil {
			t.Errorf("%s: a flow the engine should refuse was mapped and would store", name)
		}
	}
}

func TestTheFormCannotNameAnActionTheRegistryDoesNotHave(t *testing.T) {
	a := &App{}
	in := minimalInput()
	in.Steps = []flowStepInput{{Action: "settings.write", Params: map[string]string{"k": "v"}}}
	f, err := a.flowFromInput(signedIn(t, "user-1"), in)
	if err != nil {
		return // refused during mapping
	}
	if err := f.Complete(); err == nil {
		t.Error("a step naming an unregistered action would be stored; the registry is the only " +
			"thing that says what an automation may touch")
	}
}
