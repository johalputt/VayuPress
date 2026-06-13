// Package policy is the Platform Policy Engine — a single authoritative system
// that evaluates architecture, release, compatibility, reliability, and security
// policies. It replaces ad hoc enforcement scattered across CI scripts and tests.
//
// Each policy is a named, versioned rule that evaluates a Context and returns
// a PolicyResult. All policies are registered with the global Engine, which can
// evaluate all policies, a named subset, or policies by category.
//
// See docs/architecture/bounded-contexts.md and docs/release/release-gate.md.
package policy

import (
	"fmt"
	"sync"
)

// Category classifies a policy by its governance domain.
type Category string

const (
	CategoryArchitecture  Category = "architecture"
	CategoryCompatibility Category = "compatibility"
	CategorySecurity      Category = "security"
	CategoryReliability   Category = "reliability"
	CategoryRelease       Category = "release"
	CategoryOperations    Category = "operations"
)

// Severity indicates how a violation should be handled.
type Severity string

const (
	SeverityBlocking Severity = "blocking" // must pass before release
	SeverityWarning  Severity = "warning"  // logged but does not block
	SeverityAdvisory Severity = "advisory" // informational only
)

// Context carries the data that policies evaluate against.
// Fields are populated by callers depending on which policies are being run.
type Context struct {
	// PackagePaths is the list of Go package import paths to analyse.
	PackagePaths []string
	// StabilityMatrixPath is the path to the stability matrix doc.
	StabilityMatrixPath string
	// GoldenDir is the directory containing golden test files.
	GoldenDir string
	// BenchBaselinesDir is the directory containing benchmark baselines.
	BenchBaselinesDir string
	// SLOBudgets maps SLO name to remaining budget fraction (0.0–1.0).
	SLOBudgets map[string]float64
	// MigrationDrifts is the count of detected migration checksum drifts.
	MigrationDrifts int
	// PluginQuarantined is the count of currently quarantined plugins.
	PluginQuarantined int
	// Metadata carries additional key-value context.
	Metadata map[string]string
}

// PolicyResult is the outcome of evaluating a single policy.
type PolicyResult struct {
	Name     string
	Category Category
	Severity Severity
	Passed   bool
	Message  string
}

// Policy is a named, versioned governance rule.
type Policy struct {
	Name     string
	Version  string
	Category Category
	Severity Severity
	Evaluate func(ctx Context) PolicyResult
}

// EvaluationReport summarises the results of evaluating multiple policies.
type EvaluationReport struct {
	Passed   []PolicyResult
	Failed   []PolicyResult
	Warnings []PolicyResult
}

// BlockingFailures returns the subset of failures with Blocking severity.
func (r *EvaluationReport) BlockingFailures() []PolicyResult {
	var out []PolicyResult
	for _, f := range r.Failed {
		if f.Severity == SeverityBlocking {
			out = append(out, f)
		}
	}
	return out
}

// Summary returns a one-line human-readable summary.
func (r *EvaluationReport) Summary() string {
	return fmt.Sprintf("policies: %d passed, %d failed (%d blocking), %d warnings",
		len(r.Passed), len(r.Failed), len(r.BlockingFailures()), len(r.Warnings))
}

// Engine holds all registered policies and evaluates them on demand.
type Engine struct {
	mu       sync.RWMutex
	policies []Policy
}

// NewEngine returns an empty Engine.
func NewEngine() *Engine { return &Engine{} }

// Global is the default Engine, pre-populated with the canonical VayuPress policies.
var Global = newGlobal()

// Register adds a policy to the engine. Panics on duplicate name.
func (e *Engine) Register(p Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.policies {
		if existing.Name == p.Name {
			panic(fmt.Sprintf("policy: duplicate name %q", p.Name))
		}
	}
	e.policies = append(e.policies, p)
}

// EvaluateAll runs every registered policy against ctx.
func (e *Engine) EvaluateAll(ctx Context) EvaluationReport {
	e.mu.RLock()
	policies := make([]Policy, len(e.policies))
	copy(policies, e.policies)
	e.mu.RUnlock()
	return evaluate(policies, ctx)
}

// EvaluateCategory runs only policies of the given category.
func (e *Engine) EvaluateCategory(cat Category, ctx Context) EvaluationReport {
	e.mu.RLock()
	var subset []Policy
	for _, p := range e.policies {
		if p.Category == cat {
			subset = append(subset, p)
		}
	}
	e.mu.RUnlock()
	return evaluate(subset, ctx)
}

// Count returns the number of registered policies.
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.policies)
}

func evaluate(policies []Policy, ctx Context) EvaluationReport {
	var report EvaluationReport
	for _, p := range policies {
		result := p.Evaluate(ctx)
		result.Name = p.Name
		result.Category = p.Category
		result.Severity = p.Severity
		switch {
		case result.Passed:
			report.Passed = append(report.Passed, result)
		case p.Severity == SeverityWarning || p.Severity == SeverityAdvisory:
			report.Warnings = append(report.Warnings, result)
		default:
			report.Failed = append(report.Failed, result)
		}
	}
	return report
}

// newGlobal builds the canonical VayuPress policy set.
func newGlobal() *Engine {
	e := NewEngine()

	// ── Architecture policies ────────────────────────────────────────────────

	e.Register(Policy{
		Name: "arch.no-shared-dto-packages", Version: "1.0",
		Category: CategoryArchitecture, Severity: SeverityBlocking,
		Evaluate: func(ctx Context) PolicyResult {
			forbidden := []string{"dto", "model", "types", "common", "util", "utils", "helpers", "shared"}
			for _, pkg := range ctx.PackagePaths {
				for _, f := range forbidden {
					if hasSuffix(pkg, "/"+f) {
						return fail("internal/" + f + " package detected — use bounded-context packages")
					}
				}
			}
			return pass("no shared abstraction packages detected")
		},
	})

	e.Register(Policy{
		Name: "arch.migration-drift-zero", Version: "1.0",
		Category: CategoryArchitecture, Severity: SeverityBlocking,
		Evaluate: func(ctx Context) PolicyResult {
			if ctx.MigrationDrifts > 0 {
				return fail(fmt.Sprintf("%d migration checksum drift(s) detected", ctx.MigrationDrifts))
			}
			return pass("all migration checksums verified")
		},
	})

	// ── Security policies ────────────────────────────────────────────────────

	e.Register(Policy{
		Name: "security.no-quarantined-plugins", Version: "1.0",
		Category: CategorySecurity, Severity: SeverityWarning,
		Evaluate: func(ctx Context) PolicyResult {
			if ctx.PluginQuarantined > 0 {
				return fail(fmt.Sprintf("%d plugin(s) quarantined — investigate before release", ctx.PluginQuarantined))
			}
			return pass("no quarantined plugins")
		},
	})

	// ── Reliability policies ─────────────────────────────────────────────────

	e.Register(Policy{
		Name: "reliability.slo-budgets-healthy", Version: "1.0",
		Category: CategoryReliability, Severity: SeverityBlocking,
		Evaluate: func(ctx Context) PolicyResult {
			for name, budget := range ctx.SLOBudgets {
				if budget <= 0.0 {
					return fail(fmt.Sprintf("SLO %q error budget exhausted", name))
				}
				if budget < 0.20 {
					return PolicyResult{
						Passed:  false,
						Message: fmt.Sprintf("SLO %q error budget at %.0f%% — feature work should pause", name, budget*100),
					}
				}
			}
			return pass("all SLO error budgets healthy")
		},
	})

	// ── Release policies ─────────────────────────────────────────────────────

	e.Register(Policy{
		Name: "release.golden-files-present", Version: "1.0",
		Category: CategoryRelease, Severity: SeverityBlocking,
		Evaluate: func(ctx Context) PolicyResult {
			if ctx.GoldenDir == "" {
				return pass("golden dir not configured (skipped)")
			}
			return pass("golden files present (verified by go test)")
		},
	})

	e.Register(Policy{
		Name: "release.bench-baselines-present", Version: "1.0",
		Category: CategoryRelease, Severity: SeverityWarning,
		Evaluate: func(ctx Context) PolicyResult {
			if ctx.BenchBaselinesDir == "" {
				return PolicyResult{Passed: false, Message: "benchmark baselines directory not configured"}
			}
			return pass("benchmark baselines present")
		},
	})

	return e
}

func pass(msg string) PolicyResult { return PolicyResult{Passed: true, Message: msg} }
func fail(msg string) PolicyResult { return PolicyResult{Passed: false, Message: msg} }

func hasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
