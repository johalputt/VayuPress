// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/johalputt/vayupress/internal/aiassist"
)

// A model step produces a VALUE. It never selects a step.
//
// That sentence is the whole security posture of this file, and it is already
// structural rather than enforced: Step has no branch target, and the step list
// is fixed at save time. There is nowhere for model output to steer execution
// to, so a hostile instruction arriving in a comment, an inbound mail or a
// fetched page has no edge to take. Nothing here needs to detect injection,
// because injection has nothing to reach.
//
// What remains is bounding the value itself, which is what this file does:
//   - its length, so a runaway generation cannot become a post;
//   - its usability, on the same check the editor uses;
//   - and where it can flow, which is exactly one place — the NEXT step's
//     params, via a single literal placeholder.

// ModelRunner is the narrow slice of generation VayuFlow needs.
type ModelRunner interface {
	// Generate runs op over text and returns the model's output.
	Generate(ctx context.Context, op, text string) (string, error)
	// Local reports whether the provider runs on this host.
	//
	// This is not a performance detail. A remote provider makes a model step an
	// outbound call, which is precisely what a Tor Space forbids; a local one
	// does not. Reporting "AI: enabled" without this distinction would tell an
	// operator running a Tor Space nothing about whether their automation
	// reaches the clearnet.
	Local() bool
}

var (
	modelMu sync.RWMutex
	model   ModelRunner
)

// SetModelRunner wires the generation backend. Passing nil detaches it.
func SetModelRunner(m ModelRunner) {
	modelMu.Lock()
	defer modelMu.Unlock()
	model = m
}

func modelRunner() (ModelRunner, error) {
	modelMu.RLock()
	defer modelMu.RUnlock()
	if model == nil {
		return nil, fmt.Errorf("vayuflow: no model provider is configured on this install")
	}
	return model, nil
}

// MaxModelOutput bounds what a model step may hand to the next one.
//
// The number is generous for a post and small enough that a runaway generation
// fails the run instead of becoming one. It is a hard refusal rather than a
// truncation: silently cutting a draft in half would produce a post that reads
// as finished and is not, which is worse than a failed run an operator can see.
const MaxModelOutput = 40000

// ValidateModelOutput is the gate every model value passes before it can reach
// another step. A failure fails the RUN — raw text never passes through.
func ValidateModelOutput(raw string) (string, error) {
	out := strings.TrimSpace(raw)
	if out == "" {
		return "", fmt.Errorf("vayuflow: the model returned nothing")
	}
	if n := utf8.RuneCountInString(out); n > MaxModelOutput {
		return "", fmt.Errorf("vayuflow: the model returned %d characters, above the %d limit; "+
			"the run failed rather than publishing a truncated draft", n, MaxModelOutput)
	}
	// The same usability check the editor applies, so an automation and a human
	// get the same answer about the same output rather than two standards.
	if bad, why := aiassist.Unusable(out); bad {
		return "", fmt.Errorf("vayuflow: the model output is not usable: %s", why)
	}
	return out, nil
}

// PrevPlaceholder is the ONLY way a value moves between steps.
//
// A param whose entire value is this literal is replaced with the previous
// step's validated output. Deliberately not a template language and not an
// expression: substring interpolation would let model output be spliced into a
// larger string, and once you are splicing you are one feature request away
// from an evaluator — which §5 already declined for conditions, for the same
// reason.
//
// Whole-value only also means the substitution cannot be used to build a param
// the operator did not intend: the placeholder either IS the param or it is
// literal text.
const PrevPlaceholder = "$prev"

// substitutePrev returns params with any whole-value $prev replaced. It returns
// a copy: mutating the flow's stored step params would make the second run of a
// flow different from the first.
func substitutePrev(params map[string]string, prev string) map[string]string {
	if len(params) == 0 {
		return params
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		if strings.TrimSpace(v) == PrevPlaceholder {
			out[k] = prev
			continue
		}
		out[k] = v
	}
	return out
}

func init() {
	RegisterAction("model.draft.generate", func(ctx context.Context, p map[string]string, e *Effects) (string, error) {
		op := strings.TrimSpace(p["op"])
		if op == "" {
			op = aiassist.OpDraft
		}
		if !supportedModelOp(op) {
			return "", fmt.Errorf("vayuflow: %q is not a supported model operation", op)
		}
		text := strings.TrimSpace(p["text"])
		if text == "" {
			return "", fmt.Errorf("vayuflow: a model step needs text to work from")
		}
		m, err := modelRunner()
		if err != nil {
			return "", err
		}
		// Permission BEFORE the call. A remote provider is egress and is
		// charged and Tor-checked as such; a local one is neither.
		if err := e.Model(m.Local(), "generate ("+op+")"); err != nil {
			return "", err
		}
		raw, err := m.Generate(ctx, op, text)
		if err != nil {
			return "", err
		}
		return ValidateModelOutput(raw)
	})
}

func supportedModelOp(op string) bool {
	for _, s := range aiassist.SupportedOps() {
		if s == op {
			return true
		}
	}
	return false
}
