package ai

import (
	"fmt"
	"regexp"
	"strings"
)

// PolicyAction defines what happens when a policy rule matches.
type PolicyAction string

const (
	PolicyBlock  PolicyAction = "block"
	PolicyRedact PolicyAction = "redact"
	PolicyWarn   PolicyAction = "warn"
)

// PolicyRule is a content governance rule.
type PolicyRule struct {
	Name    string       `json:"name"`
	Pattern string       `json:"pattern"` // regex
	Action  PolicyAction `json:"action"`
	re      *regexp.Regexp
}

// Policy is an ordered set of governance rules applied to AI inputs/outputs.
type Policy struct {
	Rules []PolicyRule
}

// PolicyViolation describes a matched rule.
type PolicyViolation struct {
	Rule    string
	Action  PolicyAction
	Message string
}

// DefaultPolicy returns a baseline content governance policy.
func DefaultPolicy() *Policy {
	return &Policy{
		Rules: []PolicyRule{
			{Name: "no-pii-email", Pattern: `[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, Action: PolicyRedact},
			{Name: "no-pii-phone", Pattern: `\+?[0-9]{10,13}`, Action: PolicyRedact},
			{Name: "no-api-keys", Pattern: `(?i)(api[_\-]?key|token|secret)[=:\s]+[a-zA-Z0-9\-_]{16,}`, Action: PolicyBlock},
		},
	}
}

// Compile pre-compiles rule regexes. Must be called before Check/Apply.
func (p *Policy) Compile() error {
	for i := range p.Rules {
		re, err := regexp.Compile(p.Rules[i].Pattern)
		if err != nil {
			return fmt.Errorf("ai policy: compile rule %q: %w", p.Rules[i].Name, err)
		}
		p.Rules[i].re = re
	}
	return nil
}

// Check scans text against all rules, returning violations (non-modifying).
func (p *Policy) Check(text string) []PolicyViolation {
	var violations []PolicyViolation
	for _, rule := range p.Rules {
		if rule.re == nil {
			continue
		}
		if rule.re.MatchString(text) {
			violations = append(violations, PolicyViolation{
				Rule:    rule.Name,
				Action:  rule.Action,
				Message: fmt.Sprintf("policy rule %q matched", rule.Name),
			})
		}
	}
	return violations
}

// Apply enforces policies: returns error for block rules, redacts for redact rules.
func (p *Policy) Apply(text string) (string, error) {
	for _, rule := range p.Rules {
		if rule.re == nil {
			continue
		}
		if !rule.re.MatchString(text) {
			continue
		}
		switch rule.Action {
		case PolicyBlock:
			return "", fmt.Errorf("ai policy: blocked by rule %q", rule.Name)
		case PolicyRedact:
			text = rule.re.ReplaceAllStringFunc(text, func(m string) string {
				return strings.Repeat("*", len(m))
			})
		case PolicyWarn:
			// warn-only: continue processing
		}
	}
	return text, nil
}
