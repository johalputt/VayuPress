// SPDX-License-Identifier: Apache-2.0

package vayuflow

import "testing"

// The zero Condition means "always", and that IS a valid answer — unlike every
// contract field in this package. It is worth a test of its own precisely
// because it breaks the pattern: someone applying the unset-is-invalid rule
// mechanically would "fix" this and turn every unconditioned flow into an
// unsaveable one.
func TestTheZeroConditionMeansAlways(t *testing.T) {
	var c Condition
	if err := c.Complete(); err != nil {
		t.Fatalf("the zero condition must be valid: %v", err)
	}
	if !c.Holds(Subject{}) {
		t.Error("the zero condition must hold for any subject")
	}
}

func TestLeafConditionsEvaluate(t *testing.T) {
	s := Subject{Tags: []string{"Release", "notes"}, Author: "ana", Status: "draft", Title: "Weekly Digest"}
	for _, tc := range []struct {
		name string
		c    Condition
		want bool
	}{
		{"tag matches case-insensitively", Condition{Kind: CondTagEquals, Value: "release"}, true},
		{"tag absent", Condition{Kind: CondTagEquals, Value: "security"}, false},
		{"author matches", Condition{Kind: CondAuthorIs, Value: "Ana"}, true},
		{"author differs", Condition{Kind: CondAuthorIs, Value: "bo"}, false},
		{"status matches", Condition{Kind: CondStatusIs, Value: "draft"}, true},
		{"title contains", Condition{Kind: CondTitleContains, Value: "digest"}, true},
		{"title does not contain", Condition{Kind: CondTitleContains, Value: "monthly"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Holds(s); got != tc.want {
				t.Errorf("Holds = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompositesFollowTheStatedEmptyCases(t *testing.T) {
	s := Subject{Status: "draft"}
	// "All of nothing" holds; "any of nothing" does not. Both are stated in the
	// constant comments, and both are easy to get backwards.
	if !(Condition{Kind: CondAll}).Holds(s) {
		t.Error("an empty ALL must hold")
	}
	if (Condition{Kind: CondAny}).Holds(s) {
		t.Error("an empty ANY must not hold")
	}
	not := Condition{Kind: CondNot, Sub: []Condition{{Kind: CondStatusIs, Value: "published"}}}
	if !not.Holds(s) {
		t.Error("NOT(status is published) should hold for a draft")
	}
}

// Totality is the property that makes a closed predicate set safe. Depth is
// bounded at save time so evaluation cannot recurse further than the limit.
func TestConditionDepthIsBoundedAtSave(t *testing.T) {
	deep := Condition{Kind: CondStatusIs, Value: "draft"}
	for i := 0; i < MaxConditionDepth+2; i++ {
		deep = Condition{Kind: CondNot, Sub: []Condition{deep}}
	}
	if err := deep.Complete(); err == nil {
		t.Fatal("a condition nested past the limit was accepted")
	}
}

// Evaluation defends its own depth as well, because a row edited directly in
// the database has never been through Complete. An evaluator that trusts its
// input because "the save path checks it" is one UPDATE away from a stack
// overflow.
func TestEvaluationDefendsItsOwnDepth(t *testing.T) {
	deep := Condition{Kind: CondAlways}
	for i := 0; i < 5000; i++ {
		deep = Condition{Kind: CondNot, Sub: []Condition{deep}}
	}
	// The assertion is that this RETURNS at all rather than exhausting the
	// stack; the value is secondary.
	_ = deep.Holds(Subject{})
}

// A condition nobody can evaluate must not be read as "always". Reading an
// unknown kind as true would turn one corrupted row into an automation that
// fires on everything — the worst available failure direction.
func TestAnUnknownConditionKindDoesNotHold(t *testing.T) {
	c := Condition{Kind: CondKind(200)}
	if c.Holds(Subject{}) {
		t.Fatal("an unrecognised condition kind held; a corrupted row must not fire on everything")
	}
	if err := c.Complete(); err == nil {
		t.Error("an unrecognised condition kind must not save")
	}
}

// Incoherent nodes are refused rather than half-honoured: a leaf carrying
// children, or a composite carrying a value, is two intentions in one node and
// accepting it means silently ignoring one of them.
func TestIncoherentConditionsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Condition
	}{
		{"always with a value", Condition{Kind: CondAlways, Value: "x"}},
		{"always with children", Condition{Kind: CondAlways, Sub: []Condition{{Kind: CondAlways}}}},
		{"leaf with no value", Condition{Kind: CondTagEquals}},
		{"leaf with children", Condition{Kind: CondTagEquals, Value: "a", Sub: []Condition{{Kind: CondAlways}}}},
		{"composite with a value", Condition{Kind: CondAll, Value: "a"}},
		{"not with two children", Condition{Kind: CondNot, Sub: []Condition{{Kind: CondAlways}, {Kind: CondAlways}}}},
		{"not with no child", Condition{Kind: CondNot}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.c.Complete(); err == nil {
				t.Errorf("%s was accepted", tc.name)
			}
		})
	}
}

func TestBreadthIsBoundedToo(t *testing.T) {
	wide := Condition{Kind: CondAny}
	for i := 0; i < MaxConditionChildren+1; i++ {
		wide.Sub = append(wide.Sub, Condition{Kind: CondStatusIs, Value: "draft"})
	}
	if err := wide.Complete(); err == nil {
		t.Fatal("a composite wider than the limit was accepted; bounding depth alone is not enough")
	}
}
