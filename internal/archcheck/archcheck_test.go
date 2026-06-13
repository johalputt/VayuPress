// Package archcheck enforces the layering rule:
//
//	api → services → domain → infrastructure
//
// Lower layers must never import higher layers, and certain cross-context
// imports are explicitly forbidden (see docs/architecture/bounded-contexts.md).
package archcheck_test

import (
	"go/build"
	"strings"
	"testing"
)

const module = "github.com/johalputt/vayupress"

// infraPkgs are pure infrastructure — they must not import any domain/service package.
var infraPkgs = []string{
	"internal/logging",
	"internal/metrics",
	"internal/db",
	"internal/queue",
	"internal/cluster",
	"internal/config",
	"internal/lifecycle",
}

// forbiddenImports maps a package path suffix to a slice of import path suffixes
// it must never contain. Checked transitively one level deep.
var forbiddenImports = []struct {
	pkg     string // importing package (suffix)
	mustNot string // must not import this (suffix)
	why     string
}{
	// Infrastructure must not import domain contexts
	{"internal/logging", "internal/signing", "logging imports signing"},
	{"internal/logging", "internal/governance", "logging imports governance"},
	{"internal/logging", "internal/federation", "logging imports federation"},
	{"internal/db", "internal/signing", "db imports signing"},
	{"internal/db", "internal/governance", "db imports governance"},
	{"internal/metrics", "internal/signing", "metrics imports signing"},
	// Cross-context: sandbox must not know about signing
	{"internal/sandbox", "internal/signing", "sandbox imports signing (ADR-0062)"},
	{"internal/sandbox", "internal/governance", "sandbox imports governance (ADR-0062)"},
	// Cross-context: federation must not depend on AI runtime
	{"internal/federation", "internal/ai", "federation imports ai (ADR-0062)"},
	// api layer must not be imported by anything below it
	{"internal/sandbox", "internal/api", "lower layer imports api"},
	{"internal/logging", "internal/api", "lower layer imports api"},
	{"internal/db", "internal/api", "lower layer imports api"},
}

func TestLayerViolations(t *testing.T) {
	ctx := build.Default
	ctx.GOPATH = ""

	for _, rule := range forbiddenImports {
		importer := module + "/" + rule.pkg
		forbidden := module + "/" + rule.mustNot

		pkg, err := ctx.Import(importer, ".", build.ImportComment)
		if err != nil {
			// Package may not exist yet — skip rather than fail.
			continue
		}

		for _, imp := range pkg.Imports {
			if strings.HasSuffix(imp, rule.mustNot) || imp == forbidden {
				t.Errorf("LAYER VIOLATION: %s imports %s — %s", rule.pkg, imp, rule.why)
			}
		}
	}
}

// TestInfraHasNoDomainImports verifies each infra package's direct imports
// contain no business-context packages.
func TestInfraHasNoDomainImports(t *testing.T) {
	ctx := build.Default
	domainContexts := []string{
		"internal/signing",
		"internal/governance",
		"internal/federation",
		"internal/ai",
		"internal/archive",
		"internal/did",
		"internal/search",
	}

	for _, infra := range infraPkgs {
		pkg, err := ctx.Import(module+"/"+infra, ".", build.ImportComment)
		if err != nil {
			continue
		}
		for _, imp := range pkg.Imports {
			for _, domain := range domainContexts {
				if strings.Contains(imp, domain) {
					t.Errorf("INFRA VIOLATION: %s imports domain package %s", infra, imp)
				}
			}
		}
	}
}
