// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"errors"
	"testing"
	"time"
)

func testBudget() Budget {
	return Budget{
		MaxStepsPerRun: 3, MaxRunsPerHour: 5, MaxWritesPerRun: 2,
		MaxEgressPerRun: 1, Timeout: time.Minute,
	}
}

// The ceilings are checked BEFORE the effect, not reported after it. That
// distinction is the whole value of the type: a ledger that recorded an
// overspend would be an audit trail of damage already done.
func TestAChargeIsRefusedBeforeTheEffect(t *testing.T) {
	b := testBudget()
	var s Spend

	if err := s.chargeWrite(b); err != nil {
		t.Fatalf("first write must be permitted: %v", err)
	}
	if err := s.chargeWrite(b); err != nil {
		t.Fatalf("second write must be permitted: %v", err)
	}
	err := s.chargeWrite(b)
	if err == nil {
		t.Fatal("the third write crossed MaxWritesPerRun=2 and was permitted")
	}
	// The spend must NOT have advanced on the refused attempt — otherwise the
	// trail reports 3/2, which reads as the ceiling having failed rather than
	// having worked.
	if s.Writes != 2 {
		t.Errorf("a refused charge advanced the ledger to %d; it must stay at the ceiling", s.Writes)
	}
	var exceeded ErrBudgetExceeded
	if !errors.As(err, &exceeded) {
		t.Fatalf("the refusal must be typed so callers can tell it from an action failure, got %T", err)
	}
	// "budget exceeded" on its own sends an operator to read code.
	if exceeded.Ceiling != "MaxWritesPerRun" || exceeded.Limit != 2 || exceeded.Attempt != 3 {
		t.Errorf("the refusal must name which ceiling, its limit and the attempt: %+v", exceeded)
	}
}

func TestEachCeilingIsSeparate(t *testing.T) {
	b := testBudget()
	var s Spend
	// Exhausting egress must not consume writes, and vice versa — a shared
	// counter would make a fetch-heavy flow silently unable to write.
	if err := s.chargeEgress(b); err != nil {
		t.Fatal(err)
	}
	if err := s.chargeEgress(b); err == nil {
		t.Fatal("a second fetch crossed MaxEgressPerRun=1 and was permitted")
	}
	if err := s.chargeWrite(b); err != nil {
		t.Errorf("exhausting egress must not consume the write ceiling: %v", err)
	}
	if s.Egress != 1 || s.Writes != 1 {
		t.Errorf("ledgers bled into each other: %+v", s)
	}
}

func TestTheStepCeilingBoundsExpansion(t *testing.T) {
	b := testBudget()
	var s Spend
	for i := 0; i < b.MaxStepsPerRun; i++ {
		if err := s.chargeStep(b); err != nil {
			t.Fatalf("step %d must be permitted: %v", i+1, err)
		}
	}
	if err := s.chargeStep(b); err == nil {
		t.Fatal("step expansion ran past MaxStepsPerRun; this is the runaway the ceiling exists for")
	}
}

// A zero Spend against a valid Budget must permit the first of everything —
// otherwise the ceilings are off by one and every flow refuses its own first
// step, which is the failure that would look like "automation is broken"
// rather than like a bounds bug.
func TestTheFirstOfEverythingIsPermitted(t *testing.T) {
	b := testBudget()
	var s Spend
	if err := s.chargeStep(b); err != nil {
		t.Errorf("first step refused: %v", err)
	}
	if err := s.chargeWrite(b); err != nil {
		t.Errorf("first write refused: %v", err)
	}
	if err := s.chargeEgress(b); err != nil {
		t.Errorf("first fetch refused: %v", err)
	}
}

func TestCompleteAcceptsAWellFormedBudget(t *testing.T) {
	if err := testBudget().Complete(); err != nil {
		t.Fatalf("a well-formed budget was refused, so every refusal test above could be passing "+
			"for the wrong reason: %v", err)
	}
}
