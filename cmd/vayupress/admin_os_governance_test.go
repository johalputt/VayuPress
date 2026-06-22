package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/policy"
)

func TestPolicyStatusPill(t *testing.T) {
	cases := []struct {
		name      string
		in        policy.PolicyResult
		wantClass string
		wantLabel string
	}{
		{"pass", policy.PolicyResult{Passed: true}, "tool-status--on", "pass"},
		{"blocking-fail", policy.PolicyResult{Passed: false, Severity: policy.SeverityBlocking}, "tool-status--off", "fail"},
		{"warning", policy.PolicyResult{Passed: false, Severity: policy.SeverityWarning}, "tool-status--idle", "warn"},
	}
	for _, c := range cases {
		gotClass, gotLabel := policyStatusPill(c.in)
		if gotClass != c.wantClass || gotLabel != c.wantLabel {
			t.Errorf("%s: want (%q,%q), got (%q,%q)", c.name, c.wantClass, c.wantLabel, gotClass, gotLabel)
		}
	}
}
