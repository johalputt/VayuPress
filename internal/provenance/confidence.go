// Package provenance defines VayuPress's epistemic confidence vocabulary and the
// rules for how confidence propagates when one operational fact is derived from
// others. It is the single source of truth for the canonical | derived | inferred
// taxonomy so that every subsystem — the timeline today, the canonical event
// substrate tomorrow — speaks one vocabulary and cannot silently disagree about
// how certain a signal is.
//
// The taxonomy answers one question per event: how much should an operator trust
// this as ground truth?
//
//	canonical — directly observed and durably backed (a recorded mode
//	            transition, an ingested CSP report, a live counter). The
//	            strongest claim.
//	derived   — computed deterministically from canonical inputs (a budget
//	            posture from recorded charges). True, but a function of other
//	            facts rather than an observation in its own right.
//	inferred  — a synthesized narrative assertion with no durable record of its
//	            own (a reconstructed boot sequence). Honest, but the weakest
//	            claim — never to be trusted as ground truth.
//
// Confidence is totally ordered Inferred < Derived < Canonical, so "at least this
// trustworthy" comparisons are well defined.
package provenance

import "strings"

// Confidence is an epistemic trust level for an operational fact.
type Confidence string

const (
	// The empty string is the unset zero value — deliberately NOT a valid level.
	// It ranks below inferred (Known reports false), so a forgotten annotation
	// fails safe toward zero trust and poisons any Combine rather than silently
	// claiming a level it was never assigned.
	Inferred  Confidence = "inferred"
	Derived   Confidence = "derived"
	Canonical Confidence = "canonical"
)

// rank orders the levels (higher = more trustworthy). Unknown values rank below
// inferred so they can never accidentally claim trust.
func (c Confidence) rank() int {
	switch c {
	case Canonical:
		return 2
	case Derived:
		return 1
	case Inferred:
		return 0
	default:
		return -1
	}
}

// Known reports whether c is a recognised confidence level.
func (c Confidence) Known() bool { return c.rank() >= 0 }

// String returns the canonical lowercase name.
func (c Confidence) String() string { return string(c) }

// AtLeast reports whether c is at least as trustworthy as threshold.
func (c Confidence) AtLeast(threshold Confidence) bool { return c.rank() >= threshold.rank() }

// Parse resolves a name (case-insensitive) to a Confidence, defaulting to
// Inferred for anything unrecognised — unknown provenance is untrusted provenance.
func Parse(name string) (Confidence, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "canonical":
		return Canonical, true
	case "derived":
		return Derived, true
	case "inferred":
		return Inferred, true
	default:
		return Inferred, false
	}
}

// All returns the vocabulary from weakest to strongest — used to publish a
// self-documenting, drift-checkable description of the confidence taxonomy.
func All() []Confidence { return []Confidence{Inferred, Derived, Canonical} }

// Combine computes the confidence of a conclusion drawn from the given inputs.
// It encodes two honest rules:
//
//  1. A conclusion is never more trustworthy than its weakest input — any
//     inferred input makes the conclusion inferred (an unknown input poisons it
//     entirely). This is the propagation guarantee: trust cannot be manufactured
//     by derivation.
//  2. A conclusion drawn purely from canonical observations is itself only
//     derived, never canonical — a derivation is a function of observations, not
//     an observation. Canonical is reserved for direct observation, which has no
//     inputs to combine.
//
// With no inputs there is no evidentiary basis, so the result is Inferred.
func Combine(inputs ...Confidence) Confidence {
	if len(inputs) == 0 {
		return Inferred
	}
	worst := Canonical
	for _, in := range inputs {
		if !in.Known() {
			return Inferred
		}
		if in.rank() < worst.rank() {
			worst = in
		}
	}
	if worst == Canonical {
		// All inputs were direct observations; the synthesized conclusion that
		// joins them is a derivation, not itself a direct observation.
		return Derived
	}
	return worst
}
