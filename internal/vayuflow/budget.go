// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"fmt"
	"time"
)

// Budget is what a flow is permitted to spend on ONE run and across a window.
// Every field is a hard ceiling checked before the effect, not after.
//
// This is the part that makes the headline claim true, and it has no equivalent
// anywhere else in the codebase. "Unlimited" being inexpressible is the design,
// not an oversight: an operator who genuinely wants a thousand writes types
// 1000, and that number appears in the audit trail beside what the run actually
// spent. The interesting column on the runs table is `writes: 3 / 20` — a flow
// consistently near its ceiling is one to look at.
type Budget struct {
	// MaxStepsPerRun refuses runaway step expansion.
	MaxStepsPerRun int
	// MaxRunsPerHour refuses a trigger storm.
	MaxRunsPerHour int
	// MaxWritesPerRun bounds posts created or updated, mails sent, and any
	// other persistent effect.
	MaxWritesPerRun int
	// MaxEgressPerRun bounds outbound fetches. Egress is via safefetch only.
	MaxEgressPerRun int
	// Timeout is the whole-run wall clock.
	Timeout time.Duration
}

// budgetCeiling is an upper bound on what any single field may declare.
//
// It exists because "unlimited is inexpressible" is only true if a very large
// number is not a synonym for it. Without this, MaxWritesPerRun: 1<<62 passes
// Complete() and reinstates exactly the unbounded blast radius the type was
// written to prevent — the rule obeyed in the letter and voided in the spirit.
//
// The number is deliberately generous. It is not a policy about what an
// operator should want; it is the point past which a figure has stopped being a
// considered ceiling and become a way to switch the ceiling off.
const budgetCeiling = 10000

// maxTimeout bounds the wall clock for the same reason. A run allowed to hold a
// worker for a week is a resource leak wearing a configuration field.
const maxTimeout = 6 * time.Hour

// Complete reports whether every ceiling was declared and is usable. A Budget
// with any zero field is refused at save time.
func (b Budget) Complete() error {
	for _, f := range []struct {
		name string
		v    int
	}{
		{"MaxStepsPerRun", b.MaxStepsPerRun},
		{"MaxRunsPerHour", b.MaxRunsPerHour},
		{"MaxWritesPerRun", b.MaxWritesPerRun},
		{"MaxEgressPerRun", b.MaxEgressPerRun},
	} {
		if f.v <= 0 {
			return fmt.Errorf("vayuflow: budget %s must be a positive ceiling, got %d "+
				"(there is no way to express 'unlimited', by design)", f.name, f.v)
		}
		if f.v > budgetCeiling {
			return fmt.Errorf("vayuflow: budget %s is %d, above the %d ceiling — a number that large "+
				"is a way of switching the limit off rather than a limit", f.name, f.v, budgetCeiling)
		}
	}
	if b.Timeout <= 0 {
		return fmt.Errorf("vayuflow: budget Timeout must be positive, got %s", b.Timeout)
	}
	if b.Timeout > maxTimeout {
		return fmt.Errorf("vayuflow: budget Timeout is %s, above the %s ceiling", b.Timeout, maxTimeout)
	}
	// A run cannot write more times than it has steps: each write is performed
	// BY a step, so a writes ceiling above the step ceiling is unreachable and
	// therefore misleading in the audit trail, where it would read as headroom
	// the flow never had.
	if b.MaxWritesPerRun > b.MaxStepsPerRun {
		return fmt.Errorf("vayuflow: MaxWritesPerRun (%d) exceeds MaxStepsPerRun (%d); "+
			"every write is performed by a step, so that ceiling can never be reached",
			b.MaxWritesPerRun, b.MaxStepsPerRun)
	}
	if b.MaxEgressPerRun > b.MaxStepsPerRun {
		return fmt.Errorf("vayuflow: MaxEgressPerRun (%d) exceeds MaxStepsPerRun (%d); "+
			"every fetch is performed by a step, so that ceiling can never be reached",
			b.MaxEgressPerRun, b.MaxStepsPerRun)
	}
	return nil
}

// Spend is what one run actually consumed, recorded beside the Budget it was
// checked against so the audit trail can show `writes: 3 / 20` rather than a
// bare count that means nothing without its ceiling.
type Spend struct {
	Steps  int
	Writes int
	Egress int
}

// ErrBudgetExceeded is returned by the effect path when a ceiling would be
// crossed. It carries which ceiling, because "budget exceeded" on its own sends
// an operator to read code.
type ErrBudgetExceeded struct {
	Ceiling string
	Limit   int
	Attempt int
}

func (e ErrBudgetExceeded) Error() string {
	return fmt.Sprintf("vayuflow: %s ceiling reached (%d); refusing attempt %d",
		e.Ceiling, e.Limit, e.Attempt)
}

// chargeStep, chargeWrite and chargeEgress are the ONLY way spend is recorded,
// and each refuses BEFORE the effect rather than reporting after it.
//
// They live on Spend rather than on the runner so that the check cannot be
// routed around by a bug in step expansion: an action that wants to write must
// obtain permission from the ledger, and the ledger is what the run records.
func (s *Spend) chargeStep(b Budget) error {
	if s.Steps >= b.MaxStepsPerRun {
		return ErrBudgetExceeded{Ceiling: "MaxStepsPerRun", Limit: b.MaxStepsPerRun, Attempt: s.Steps + 1}
	}
	s.Steps++
	return nil
}

func (s *Spend) chargeWrite(b Budget) error {
	if s.Writes >= b.MaxWritesPerRun {
		return ErrBudgetExceeded{Ceiling: "MaxWritesPerRun", Limit: b.MaxWritesPerRun, Attempt: s.Writes + 1}
	}
	s.Writes++
	return nil
}

func (s *Spend) chargeEgress(b Budget) error {
	if s.Egress >= b.MaxEgressPerRun {
		return ErrBudgetExceeded{Ceiling: "MaxEgressPerRun", Limit: b.MaxEgressPerRun, Attempt: s.Egress + 1}
	}
	s.Egress++
	return nil
}
