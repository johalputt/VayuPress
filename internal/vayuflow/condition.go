// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"fmt"
	"strings"
)

// Conditions are a small closed set of typed predicates over the trigger
// payload and site state. They are deliberately NOT a scriptable expression
// language.
//
// A general expression evaluator inside a rules engine is a sandbox with a
// different name, and this project already carries one real sandbox
// (internal/sandbox) whose large dead surface is a standing reminder of what an
// unfinished isolation surface costs. Every condition here is total: it
// terminates, it allocates nothing unbounded, and it cannot call out.
//
// Totality is enforced structurally rather than promised. Depth is bounded at
// save time by Complete(), so evaluation cannot recurse further than
// MaxConditionDepth no matter what is stored — including if a row is edited in
// the database by hand.
type CondKind uint8

const (
	// CondAlways is the zero value and, uniquely in this package, IS a valid
	// answer. An absent condition is not an unanswered question; it is the
	// answer "run every time the trigger fires". The ADR states this
	// explicitly, which is why it does not follow the unset-is-invalid pattern
	// the contract fields use.
	CondAlways CondKind = iota
	// CondTagEquals — the subject carries this tag.
	CondTagEquals
	// CondAuthorIs — the subject's author handle equals Value.
	CondAuthorIs
	// CondStatusIs — the subject's status equals Value (draft, published, ...).
	CondStatusIs
	// CondTitleContains — the subject's title contains Value, compared
	// case-insensitively.
	CondTitleContains
	// CondAll — every Sub must hold. An empty Sub holds, matching the ordinary
	// reading of "all of nothing".
	CondAll
	// CondAny — at least one Sub must hold. An empty Sub does NOT hold.
	CondAny
	// CondNot — the single Sub must not hold.
	CondNot
)

func (k CondKind) String() string {
	switch k {
	case CondAlways:
		return "always"
	case CondTagEquals:
		return "tag-equals"
	case CondAuthorIs:
		return "author-is"
	case CondStatusIs:
		return "status-is"
	case CondTitleContains:
		return "title-contains"
	case CondAll:
		return "all"
	case CondAny:
		return "any"
	case CondNot:
		return "not"
	}
	return "unknown"
}

// MaxConditionDepth bounds nesting. Five is far beyond anything the panel can
// build and still small enough that evaluation is trivially bounded.
const MaxConditionDepth = 5

// MaxConditionChildren bounds the breadth of one composite node, so a stored
// document cannot make evaluation expensive by width instead of depth.
const MaxConditionChildren = 16

// Condition is one predicate. Leaf kinds use Value; composite kinds use Sub.
type Condition struct {
	Kind  CondKind
	Value string
	Sub   []Condition
}

// Complete reports whether the condition tree is well formed and within its
// bounds. It is what makes evaluation total: anything Complete accepts can be
// evaluated in bounded time without recursion guards scattered through the
// evaluator.
func (c Condition) Complete() error { return c.complete(1) }

func (c Condition) complete(depth int) error {
	if depth > MaxConditionDepth {
		return fmt.Errorf("vayuflow: condition nests deeper than %d", MaxConditionDepth)
	}
	switch c.Kind {
	case CondAlways:
		if c.Value != "" || len(c.Sub) > 0 {
			return fmt.Errorf("vayuflow: an 'always' condition takes no value and no children")
		}
	case CondTagEquals, CondAuthorIs, CondStatusIs, CondTitleContains:
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("vayuflow: condition %s needs a value", c.Kind)
		}
		if len(c.Sub) > 0 {
			return fmt.Errorf("vayuflow: condition %s is a leaf and takes no children", c.Kind)
		}
	case CondAll, CondAny:
		if c.Value != "" {
			return fmt.Errorf("vayuflow: condition %s composes children and takes no value", c.Kind)
		}
		if len(c.Sub) > MaxConditionChildren {
			return fmt.Errorf("vayuflow: condition %s has %d children, above the %d limit",
				c.Kind, len(c.Sub), MaxConditionChildren)
		}
		for _, s := range c.Sub {
			if err := s.complete(depth + 1); err != nil {
				return err
			}
		}
	case CondNot:
		if c.Value != "" {
			return fmt.Errorf("vayuflow: condition not takes no value")
		}
		if len(c.Sub) != 1 {
			return fmt.Errorf("vayuflow: condition not takes exactly one child, got %d", len(c.Sub))
		}
		return c.Sub[0].complete(depth + 1)
	default:
		return fmt.Errorf("vayuflow: unknown condition kind %d", c.Kind)
	}
	return nil
}

// Subject is the state a condition is evaluated against. It is a plain value
// assembled by the caller, so evaluation touches no database and no network —
// which is what "side-effect-free" means here in practice rather than in
// principle.
type Subject struct {
	Tags   []string
	Author string
	Status string
	Title  string
}

// Holds evaluates the condition against a subject. It is total for any
// condition that passed Complete, and it defends its own depth anyway: a row
// edited by hand in the database has not been through Complete, and an
// evaluator that trusts its input because "the save path checks it" is one
// direct UPDATE away from a stack overflow.
func (c Condition) Holds(s Subject) bool { return c.holds(s, 1) }

func (c Condition) holds(s Subject, depth int) bool {
	if depth > MaxConditionDepth {
		return false
	}
	switch c.Kind {
	case CondAlways:
		return true
	case CondTagEquals:
		for _, t := range s.Tags {
			if strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(c.Value)) {
				return true
			}
		}
		return false
	case CondAuthorIs:
		return strings.EqualFold(strings.TrimSpace(s.Author), strings.TrimSpace(c.Value))
	case CondStatusIs:
		return strings.EqualFold(strings.TrimSpace(s.Status), strings.TrimSpace(c.Value))
	case CondTitleContains:
		return strings.Contains(strings.ToLower(s.Title), strings.ToLower(strings.TrimSpace(c.Value)))
	case CondAll:
		for _, sub := range c.Sub {
			if !sub.holds(s, depth+1) {
				return false
			}
		}
		return true
	case CondAny:
		for _, sub := range c.Sub {
			if sub.holds(s, depth+1) {
				return true
			}
		}
		return false
	case CondNot:
		if len(c.Sub) != 1 {
			return false
		}
		return !c.Sub[0].holds(s, depth+1)
	}
	// An unknown kind does not hold. A condition nobody can evaluate must not
	// be read as "always" — that would turn a corrupted row into an automation
	// that fires on everything.
	return false
}
