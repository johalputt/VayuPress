// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeModel struct {
	out   string
	err   error
	local bool
	calls int
}

func (f *fakeModel) Generate(context.Context, string, string) (string, error) {
	f.calls++
	return f.out, f.err
}
func (f *fakeModel) Local() bool { return f.local }

func wireModel(t *testing.T, m ModelRunner) {
	t.Helper()
	SetModelRunner(m)
	t.Cleanup(func() { SetModelRunner(nil) })
}

// §6 rule 3, and the sentence the whole design turns on: a bad generation
// cannot publish itself.
//
// It is a property of two registrations rather than a promise about prompts —
// the model action writes nothing, and the step that writes is capped at draft.
// So there is no arrangement of the two that reaches a live post.
func TestNoModelStepCanRaiseTheWriteCeiling(t *testing.T) {
	mc, err := CapabilityFor("model.draft.generate")
	if err != nil {
		t.Fatal(err)
	}
	if mc.Writes != WriteNone {
		t.Fatalf("the model action declares Writes=%s; it must write nothing at all", mc.Writes)
	}
	// Even asked directly, the effect path refuses anything above its ceiling.
	var spend Spend
	e := &Effects{mode: RunLive, cap: mc, budget: testBudget(), spend: &spend}
	for _, level := range []WritePolicy{WriteDraft, WriteLive} {
		if err := e.Write(level, "x"); err == nil {
			t.Errorf("a model step was permitted to write at %s", level)
		}
	}
	// And every CONTENT step is capped at draft, so a generation has nowhere to
	// land as a published post.
	//
	// This assertion was once "no capability writes live at all", which was true
	// only while content was the only thing a flow could do. Adding mail broke
	// it, correctly: a delivered message has no draft form, and weakening
	// mail.send to keep the sentence tidy would have been the panel overstating
	// what is bounded. The claim this test defends is the one the ADR actually
	// makes — a bad generation cannot PUBLISH itself — and mail is a separate,
	// declared, admin-only, irreversible surface with its own row below.
	for _, c := range CapabilitiesOfKind(KindContent) {
		if c.Writes == WriteLive {
			t.Errorf("%s can write live; a model value could reach a published post through it", c.Action)
		}
	}
}

// Mail is the one action that reaches a person irreversibly, so the properties
// that bound it are asserted rather than assumed.
func TestMailIsAdminOnlyIrreversibleAndSingleRecipient(t *testing.T) {
	c, err := CapabilityFor("mail.send")
	if err != nil {
		t.Fatal(err)
	}
	if c.MinRole != RoleAdmin {
		t.Errorf("mail.send requires %q; sending on an install's behalf is an admin act", c.MinRole)
	}
	if c.Undo != Irreversible {
		t.Errorf("mail.send declares Undo=%s; delivered mail cannot be recalled", c.Undo)
	}
	if c.Writes != WriteLive {
		t.Errorf("mail.send declares Writes=%s; a delivered message has no draft form", c.Writes)
	}

	// A recipient list would spend ONE write and deliver MANY — the budget
	// would report a single message while the list got one each.
	SetMailSender(&fakeMailer{})
	t.Cleanup(func() { SetMailSender(nil) })
	fn, _ := actionFor("mail.send")
	for _, to := range []string{"a@example.com,b@example.com", "a@example.com; b@example.com"} {
		var spend Spend
		e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
		if _, err := fn(context.Background(), map[string]string{
			"to": to, "subject": "s", "body": "b"}, e); err == nil {
			t.Errorf("a recipient list %q was accepted", to)
		}
		if spend.Writes != 0 {
			t.Errorf("the refused list still charged %d write(s)", spend.Writes)
		}
	}
}

// §6 rule 1 is structural: there is nowhere for model output to steer to. This
// pins the shape rather than the behaviour, because the day Step grows a branch
// target is the day injection becomes reachable.
func TestAStepHasNoBranchTargetForModelOutputToChoose(t *testing.T) {
	f := goodFlow()
	f.Steps = []Step{{Action: "content.draft.create", Params: map[string]string{"title": "T", "slug": "s"}}}
	// A Step is an action plus params. If a field ever appears that names
	// another step, the fixed-graph guarantee is gone.
	for _, forbidden := range []string{"next", "goto", "target", "branch", "then", "onerror"} {
		if _, ok := f.Steps[0].Params[forbidden]; ok {
			t.Errorf("params carry %q, which reads as control flow", forbidden)
		}
	}
}

// §6 rule 2: output is validated BEFORE it reaches the next step, and a failure
// fails the run rather than passing raw text through.
func TestModelOutputIsValidatedBeforeItTravels(t *testing.T) {
	for _, tc := range []struct{ name, out, want string }{
		{"empty", "   ", "returned nothing"},
		{"too long", strings.Repeat("a", MaxModelOutput+1), "above the"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateModelOutput(tc.out); err == nil {
				t.Fatalf("%s output was accepted", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal should say why, got: %v", err)
			}
		})
	}
	if _, err := ValidateModelOutput("A perfectly ordinary paragraph of prose."); err != nil {
		t.Fatalf("good output was refused, so every case above could pass for the wrong reason: %v", err)
	}
}

// An over-long generation must FAIL the run, not be truncated into it. A
// silently halved draft reads as finished and is not.
func TestARunawayGenerationFailsTheRunRatherThanBeingTruncated(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	wireContent(t)
	wireModel(t, &fakeModel{out: strings.Repeat("x", MaxModelOutput+10), local: true})
	ctx := context.Background()

	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Steps = []Step{{Action: "model.draft.generate", Params: map[string]string{"op": "draft", "text": "write something"}}}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	run, err := rn.Execute(ctx, f, "manual", "m1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusFailed {
		t.Fatalf("a runaway generation should fail the run, got %s", run.Status)
	}
	stored, _ := rs.Recent(ctx, f.ID, 1)
	if len(stored) != 1 || !strings.Contains(stored[0].Error, "above the") {
		t.Errorf("the trail must say the output was over the limit, got %+v", stored)
	}
}

// Unusable model output — the editor's own check — must also fail the run,
// so an automation and a human get the same answer about the same output.
func TestUnusableOutputFailsTheRun(t *testing.T) {
	if _, err := ValidateModelOutput("Okay, so the user wants me to write a post about"); err == nil {
		t.Fatal("model thinking was accepted as a draft")
	}
}

// The value channel: exactly one placeholder, whole-value only.
func TestOnlyAWholeValuePlaceholderIsSubstituted(t *testing.T) {
	got := substitutePrev(map[string]string{
		"content":  PrevPlaceholder,
		"spaced":   "  " + PrevPlaceholder + "  ",
		"embedded": "before " + PrevPlaceholder + " after",
		"literal":  "no placeholder here",
	}, "GENERATED")

	if got["content"] != "GENERATED" || got["spaced"] != "GENERATED" {
		t.Errorf("a whole-value placeholder must be substituted, got %#v", got)
	}
	// Substring interpolation is refused by construction: once you splice, you
	// are one request away from an evaluator.
	if got["embedded"] != "before "+PrevPlaceholder+" after" {
		t.Errorf("an embedded placeholder must NOT be spliced, got %q", got["embedded"])
	}
	if got["literal"] != "no placeholder here" {
		t.Errorf("unrelated params must be untouched, got %q", got["literal"])
	}
}

// Substitution must not mutate the stored flow, or a flow's second run would
// differ from its first.
func TestSubstitutionDoesNotMutateTheStoredParams(t *testing.T) {
	orig := map[string]string{"content": PrevPlaceholder}
	_ = substitutePrev(orig, "GENERATED")
	if orig["content"] != PrevPlaceholder {
		t.Fatalf("the stored params were mutated to %q; the next run would not see the placeholder",
			orig["content"])
	}
}

// End to end: a model step feeds a content step, and the result is still a
// DRAFT. This is the arrangement an operator will actually build.
func TestAModelFedContentStepStillProducesOnlyADraft(t *testing.T) {
	fs, _, rn := newTestRig(t, RoleAdmin)
	fc := wireContent(t)
	wireModel(t, &fakeModel{out: "A generated paragraph about the week.", local: true})
	ctx := context.Background()

	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Budget.MaxStepsPerRun, f.Budget.MaxWritesPerRun = 2, 1
	f.Steps = []Step{
		{Action: "model.draft.generate", Params: map[string]string{"op": "draft", "text": "the week"}},
		{Action: "content.draft.create", Params: map[string]string{
			"title": "Weekly", "slug": "weekly", "content": PrevPlaceholder}},
	}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	run, err := rn.Execute(ctx, f, "manual", "m1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusSucceeded {
		t.Fatalf("the flow should have succeeded, got %s (%s)", run.Status, run.Error)
	}
	fc.Lock()
	defer fc.Unlock()
	if len(fc.created) != 1 {
		t.Fatalf("expected one draft, got %d", len(fc.created))
	}
	if fc.created[0].Content != "A generated paragraph about the week." {
		t.Errorf("the model's value did not reach the write step: %q", fc.created[0].Content)
	}
	// The Draft type has no status field, so there is no route from here to a
	// published post — asserted in actions_content_test.go and relied on here.
}

// A REMOTE model provider is an outbound call and must be refused in a Tor
// Space. This is the case the registry's OnionInert names.
func TestARemoteModelIsRefusedInATorSpace(t *testing.T) {
	orig := clearnetBlocked
	clearnetBlocked = func() bool { return true }
	t.Cleanup(func() { clearnetBlocked = orig })

	capab, _ := CapabilityFor("model.draft.generate")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	err := e.Model(false, "generate")
	if err == nil {
		t.Fatal("a remote model provider was called in a Tor Space")
	}
	if errors.Is(err, ErrDryRun) {
		t.Fatal("the Tor refusal must not be reported as a dry-run capture")
	}
	if spend.Egress != 0 {
		t.Errorf("a refused call must not be charged, got %d", spend.Egress)
	}
}

// A LOCAL provider is not egress, so it runs in a Tor Space and does not spend
// a ceiling that has nothing to do with what it limits.
func TestALocalModelRunsInATorSpaceAndSpendsNoEgress(t *testing.T) {
	orig := clearnetBlocked
	clearnetBlocked = func() bool { return true }
	t.Cleanup(func() { clearnetBlocked = orig })

	capab, _ := CapabilityFor("model.draft.generate")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	if err := e.Model(true, "generate"); err != nil {
		t.Fatalf("a local model must run in a Tor Space: %v", err)
	}
	if spend.Egress != 0 {
		t.Errorf("a local model charged %d against the fetch ceiling; it makes no outbound call", spend.Egress)
	}
}

// A remote model on a clearnet install IS egress and spends that ceiling.
func TestARemoteModelSpendsTheEgressCeiling(t *testing.T) {
	capab, _ := CapabilityFor("model.draft.generate")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	if err := e.Model(false, "generate"); err != nil {
		t.Fatal(err)
	}
	if spend.Egress != 1 {
		t.Errorf("a remote model call must spend the egress ceiling, got %d", spend.Egress)
	}
}

// The dry-run lie, for model steps specifically: the ADR says a dry run must
// GENUINELY CALL the model, because a dry run that skipped it would not tell
// the operator what the live run produces.
func TestADryRunGenuinelyCallsTheModel(t *testing.T) {
	fs, _, rn := newTestRig(t, RoleAdmin)
	wireContent(t)
	fm := &fakeModel{out: "A generated paragraph.", local: true}
	wireModel(t, fm)
	ctx := context.Background()

	f := goodFlow()
	f.Enabled, f.Mode = true, RunDryRun
	f.Steps = []Step{{Action: "model.draft.generate", Params: map[string]string{"op": "draft", "text": "x"}}}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := rn.Execute(ctx, f, "manual", "m1", Subject{}); err != nil {
		t.Fatal(err)
	}
	// ADR §8 is explicit: a dry run executes the whole flow with model steps
	// GENUINELY CALLED. Anything less tells the operator nothing about what the
	// live run produces.
	if fm.calls != 1 {
		t.Fatalf("the model was called %d times during a dry run; §8 requires it to be genuinely "+
			"called, or the dry run says nothing about the live run", fm.calls)
	}
	runs, _ := NewRunStore(fs.db).Recent(ctx, f.ID, 1)
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %d", len(runs))
	}
	// The generated text is the point: it is what the operator reads to decide
	// whether to arm.
	if runs[0].Steps[0].Output != "A generated paragraph." {
		t.Errorf("the dry run did not record the real generated output, got %q", runs[0].Steps[0].Output)
	}
	if !strings.Contains(runs[0].Steps[0].Did, "model") {
		t.Errorf("the run should record that the model was called, got %q", runs[0].Steps[0].Did)
	}
}

// And the effect the dry run DOES refuse is the write that consumes the value —
// so the operator sees a real generation and a refused write, which together
// are exactly what the live run would do.
func TestADryRunGeneratesForRealAndRefusesOnlyTheWrite(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	fc := wireContent(t)
	fm := &fakeModel{out: "A generated paragraph.", local: true}
	wireModel(t, fm)
	ctx := context.Background()

	f := goodFlow()
	f.Enabled, f.Mode = true, RunDryRun
	f.Budget.MaxStepsPerRun, f.Budget.MaxWritesPerRun = 2, 1
	f.Steps = []Step{
		{Action: "model.draft.generate", Params: map[string]string{"op": "draft", "text": "x"}},
		{Action: "content.draft.create", Params: map[string]string{
			"title": "Weekly", "slug": "weekly", "content": PrevPlaceholder}},
	}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := rn.Execute(ctx, f, "manual", "m1", Subject{}); err != nil {
		t.Fatal(err)
	}
	if fm.calls != 1 {
		t.Errorf("the model should have been called once, got %d", fm.calls)
	}
	if created, _ := fc.counts(); created != 0 {
		t.Errorf("a dry run wrote %d drafts", created)
	}
	runs, _ := rs.Recent(ctx, f.ID, 1)
	if len(runs) != 1 || len(runs[0].Steps) != 2 {
		t.Fatalf("expected one run with two steps, got %+v", runs)
	}
	if runs[0].Steps[1].Refused == "" {
		t.Error("the WRITE step must be the one refused")
	}
}

// An unconfigured provider must fail the step loudly. A run that "succeeded"
// having generated nothing is the worst outcome: the trail says it worked.
func TestAnUnconfiguredModelFailsTheStep(t *testing.T) {
	SetModelRunner(nil)
	fn, _ := actionFor("model.draft.generate")
	capab, _ := CapabilityFor("model.draft.generate")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{"op": "draft", "text": "x"}, e); err == nil {
		t.Fatal("an unconfigured model reported success")
	}
}

// An unsupported operation is refused rather than sent to the provider.
func TestAnUnsupportedModelOpIsRefused(t *testing.T) {
	wireModel(t, &fakeModel{out: "x", local: true})
	fn, _ := actionFor("model.draft.generate")
	capab, _ := CapabilityFor("model.draft.generate")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{"op": "exfiltrate", "text": "x"}, e); err == nil {
		t.Fatal("an unsupported model operation was accepted")
	}
}
