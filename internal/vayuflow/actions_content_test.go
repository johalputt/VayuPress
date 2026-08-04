// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// THE P3 GATE, proved structurally rather than behaviourally.
//
// An article whose status is the empty string is treated as PUBLISHED:
// internal/db/article_repo.go defaults "" to "published" on insert and the read
// path COALESCEs it the same way. So for content the safe value is NOT the zero
// value, and a forgotten field publishes rather than fails.
//
// A guard that checked a status before writing would be one forgotten branch
// away from that. Draft therefore has no status field at all — the type cannot
// express "published", so no content action can publish however it is written.
// This test holds that shape, because the natural "improvement" someone will
// one day make is to add a Status field "for flexibility".
func TestADraftCannotExpressAPublishedStatus(t *testing.T) {
	tp := reflect.TypeOf(Draft{})
	for i := 0; i < tp.NumField(); i++ {
		name := strings.ToLower(tp.Field(i).Name)
		if strings.Contains(name, "status") || strings.Contains(name, "publish") ||
			strings.Contains(name, "state") || strings.Contains(name, "live") {
			t.Errorf("Draft has a %q field. The type must not be able to express publication — "+
				"an empty status is read as PUBLISHED by the article repo, so a status field here "+
				"is one forgotten assignment away from a flow publishing.", tp.Field(i).Name)
		}
	}
	if tp.NumField() == 0 {
		t.Fatal("Draft has no fields at all; this test is proving nothing")
	}
}

// Every content action's capability is capped at draft, and the effect path
// clamps to that cap. Together they mean a content flow cannot produce a live
// post even if an action asked to.
func TestAContentFlowCannotProduceALivePost(t *testing.T) {
	for _, c := range CapabilitiesOfKind(KindContent) {
		if c.Writes != WriteDraft {
			t.Fatalf("%s writes at %s; every v1 content action must be capped at draft",
				c.Action, c.Writes)
		}
		var spend Spend
		e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
		if err := e.Write(WriteLive, "publish"); err == nil {
			t.Errorf("%s was allowed to write live", c.Action)
		}
	}
}

func TestTheContentActionsWriteThroughTheEffectPath(t *testing.T) {
	fc := &fakeContent{}
	SetContentWriter(fc)
	t.Cleanup(func() { SetContentWriter(nil) })

	for _, name := range []string{"content.draft.create", "content.draft.update"} {
		t.Run(name, func(t *testing.T) {
			fn, ok := actionFor(name)
			if !ok {
				t.Fatalf("%s has no implementation", name)
			}
			capab, err := CapabilityFor(name)
			if err != nil {
				t.Fatal(err)
			}
			// Budget exhausted: the action must not write, because it cannot
			// obtain permission. This is the ceiling living in the effect path
			// rather than in the planner.
			spent := Spend{Writes: 1}
			e := &Effects{mode: RunLive, cap: capab, budget: Budget{
				MaxStepsPerRun: 2, MaxRunsPerHour: 1, MaxWritesPerRun: 1,
				MaxEgressPerRun: 1, Timeout: testBudget().Timeout,
			}, spend: &spent}
			before, beforeU := fc.counts()
			if _, err := fn(context.Background(), map[string]string{"title": "T", "slug": "s"}, e); err == nil {
				t.Fatal("the action wrote with its write ceiling already spent")
			}
			after, afterU := fc.counts()
			if after != before || afterU != beforeU {
				t.Error("a budget refusal did not stop the effect; the ceiling is not in the effect path")
			}
		})
	}
}

func TestADraftNeedsATitleAndASlug(t *testing.T) {
	fc := &fakeContent{}
	SetContentWriter(fc)
	t.Cleanup(func() { SetContentWriter(nil) })
	fn, _ := actionFor("content.draft.create")
	capab, _ := CapabilityFor("content.draft.create")

	for _, p := range []map[string]string{
		{},
		{"title": "T"},
		{"slug": "s"},
		{"title": "  ", "slug": "s"},
	} {
		var spend Spend
		e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
		if _, err := fn(context.Background(), p, e); err == nil {
			t.Errorf("params %v produced a draft", p)
		}
		if spend.Writes != 0 {
			t.Errorf("params %v charged the budget before validating", p)
		}
	}
}

func TestTagsAreSplitAndTrimmed(t *testing.T) {
	fc := &fakeContent{}
	SetContentWriter(fc)
	t.Cleanup(func() { SetContentWriter(nil) })
	fn, _ := actionFor("content.draft.create")
	capab, _ := CapabilityFor("content.draft.create")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}

	if _, err := fn(context.Background(), map[string]string{
		"title": "T", "slug": "s", "tags": " release , notes ,, ",
	}, e); err != nil {
		t.Fatal(err)
	}
	fc.Lock()
	defer fc.Unlock()
	if len(fc.created) != 1 {
		t.Fatalf("expected one draft, got %d", len(fc.created))
	}
	got := fc.created[0].Tags
	if len(got) != 2 || got[0] != "release" || got[1] != "notes" {
		t.Errorf("tags not split and trimmed: %#v", got)
	}
}

// With no storage wired the action must fail loudly rather than report success.
// A flow that "ran" and wrote nothing is the worst outcome: the trail says it
// worked.
func TestAnUnwiredContentWriterFailsTheStepRatherThanSucceedingQuietly(t *testing.T) {
	SetContentWriter(nil)
	fn, _ := actionFor("content.draft.create")
	capab, _ := CapabilityFor("content.draft.create")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	out, err := fn(context.Background(), map[string]string{"title": "T", "slug": "s"}, e)
	if err == nil {
		t.Fatalf("an unwired writer reported success (output %q)", out)
	}
	if !strings.Contains(err.Error(), "no content storage") {
		t.Errorf("the failure should say what is missing, got: %v", err)
	}
}

// A storage failure must surface as a step failure, not be swallowed.
func TestAStorageFailureFailsTheStep(t *testing.T) {
	fc := &fakeContent{fail: errors.New("disk full")}
	SetContentWriter(fc)
	t.Cleanup(func() { SetContentWriter(nil) })
	fn, _ := actionFor("content.draft.create")
	capab, _ := CapabilityFor("content.draft.create")
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{"title": "T", "slug": "s"}, e); err == nil {
		t.Fatal("a storage failure was swallowed")
	}
}

// DraftStatus is the value the adapter must persist. It is asserted here so a
// rename cannot silently become "" — which the repo reads as published.
func TestDraftStatusIsNotEmpty(t *testing.T) {
	if DraftStatus != "draft" {
		t.Fatalf("DraftStatus is %q; the article repo treats an empty status as PUBLISHED", DraftStatus)
	}
}
