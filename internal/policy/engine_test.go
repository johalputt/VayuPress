package policy_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/policy"
)

func TestEngineEvaluateAll(t *testing.T) {
	e := policy.NewEngine()
	e.Register(policy.Policy{
		Name: "test.always-pass", Version: "1.0",
		Category: policy.CategoryArchitecture, Severity: policy.SeverityBlocking,
		Evaluate: func(ctx policy.Context) policy.PolicyResult {
			return policy.PolicyResult{Passed: true, Message: "ok"}
		},
	})
	report := e.EvaluateAll(policy.Context{})
	if len(report.Passed) != 1 {
		t.Errorf("expected 1 passed, got %d", len(report.Passed))
	}
	if len(report.Failed) != 0 {
		t.Errorf("expected 0 failed, got %d", len(report.Failed))
	}
}

func TestEngineBlockingFailure(t *testing.T) {
	e := policy.NewEngine()
	e.Register(policy.Policy{
		Name: "test.always-fail", Version: "1.0",
		Category: policy.CategorySecurity, Severity: policy.SeverityBlocking,
		Evaluate: func(ctx policy.Context) policy.PolicyResult {
			return policy.PolicyResult{Passed: false, Message: "blocked"}
		},
	})
	report := e.EvaluateAll(policy.Context{})
	if len(report.BlockingFailures()) != 1 {
		t.Errorf("expected 1 blocking failure, got %d", len(report.BlockingFailures()))
	}
}

func TestGlobalEngineHasPolicies(t *testing.T) {
	if policy.Global.Count() == 0 {
		t.Error("global policy engine has no registered policies")
	}
}

func TestMigrationDriftPolicy(t *testing.T) {
	report := policy.Global.EvaluateCategory(policy.CategoryArchitecture, policy.Context{
		MigrationDrifts: 2,
	})
	found := false
	for _, f := range report.Failed {
		if f.Name == "arch.migration-drift-zero" {
			found = true
		}
	}
	if !found {
		t.Error("migration-drift-zero policy did not fire on 2 drifts")
	}
}

func TestSLOBudgetExhaustedPolicy(t *testing.T) {
	report := policy.Global.EvaluateCategory(policy.CategoryReliability, policy.Context{
		SLOBudgets: map[string]float64{
			"signing.latency.p95": 0.0,
		},
	})
	found := false
	for _, f := range report.Failed {
		if f.Name == "reliability.slo-budgets-healthy" {
			found = true
		}
	}
	if !found {
		t.Error("slo-budgets-healthy policy did not fire on exhausted budget")
	}
}

func TestNoDuplicateRegistration(t *testing.T) {
	e := policy.NewEngine()
	e.Register(policy.Policy{Name: "x", Evaluate: func(policy.Context) policy.PolicyResult { return policy.PolicyResult{Passed: true} }})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate policy name")
		}
	}()
	e.Register(policy.Policy{Name: "x", Evaluate: func(policy.Context) policy.PolicyResult { return policy.PolicyResult{Passed: true} }})
}

func TestEvaluationSummary(t *testing.T) {
	e := policy.NewEngine()
	e.Register(policy.Policy{
		Name: "a", Severity: policy.SeverityBlocking,
		Evaluate: func(policy.Context) policy.PolicyResult { return policy.PolicyResult{Passed: true} },
	})
	e.Register(policy.Policy{
		Name: "b", Severity: policy.SeverityBlocking,
		Evaluate: func(policy.Context) policy.PolicyResult { return policy.PolicyResult{Passed: false} },
	})
	report := e.EvaluateAll(policy.Context{})
	s := report.Summary()
	if s == "" {
		t.Error("summary should not be empty")
	}
}
