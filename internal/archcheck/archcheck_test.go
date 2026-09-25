// SPDX-License-Identifier: Apache-2.0

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
	"internal/config",
	"internal/lifecycle",
}

// forbiddenImports maps a package path suffix to a slice of import path suffixes
// it must never contain. Checked on direct imports.
var forbiddenImports = []struct {
	pkg     string // importing package (suffix)
	mustNot string // must not import this (suffix)
	why     string
}{
	// api layer must not be imported by anything below it
	{"internal/sandbox", "internal/api", "lower layer imports api"},
	{"internal/logging", "internal/api", "lower layer imports api"},
	{"internal/db", "internal/api", "lower layer imports api"},
}

// importOf loads a package by its module path. A package a rule names that no
// longer loads is a failure, not a skip: skipping let every rule over a deleted
// package pass for as long as nobody read the list.
func importOf(t *testing.T, pkg string) *build.Package {
	t.Helper()
	p, err := build.Default.Import(module+"/"+pkg, ".", build.ImportComment)
	if err != nil {
		t.Fatalf("%s is named by a layering rule but does not load: %v", pkg, err)
	}
	return p
}

func TestLayerViolations(t *testing.T) {
	for _, rule := range forbiddenImports {
		importOf(t, rule.mustNot)
		for _, imp := range importOf(t, rule.pkg).Imports {
			if imp == module+"/"+rule.mustNot {
				t.Errorf("LAYER VIOLATION: %s imports %s — %s", rule.pkg, imp, rule.why)
			}
		}
	}
}

// TestInfraHasNoDomainImports verifies each infra package's direct imports
// contain no business-context packages.
func TestInfraHasNoDomainImports(t *testing.T) {
	domainContexts := []string{
		"internal/plugins",
		"internal/sandbox",
		"internal/search",
	}
	for _, domain := range domainContexts {
		importOf(t, domain)
	}
	for _, infra := range infraPkgs {
		for _, imp := range importOf(t, infra).Imports {
			for _, domain := range domainContexts {
				if imp == module+"/"+domain || strings.HasPrefix(imp, module+"/"+domain+"/") {
					t.Errorf("INFRA VIOLATION: %s imports domain package %s", infra, imp)
				}
			}
		}
	}
}
