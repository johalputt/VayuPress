// SPDX-License-Identifier: Apache-2.0

// creep_test.go detects shared-abstraction creep patterns that bypass import
// layer enforcement: reflection in security-critical code, shared utility packages.
package archcheck_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoReflectionInCriticalPaths verifies that security-critical packages
// do not use reflect — reflection can bypass type safety and capability checks.
func TestNoReflectionInCriticalPaths(t *testing.T) {
	criticalPkgs := []string{
		"internal/sandbox",
	}

	root := filepath.Join("..", "..")
	for _, pkg := range criticalPkgs {
		pkgPath := filepath.Join(root, pkg)

		fset := token.NewFileSet()
		//lint:ignore SA1019 ParseDir is sufficient for this build-tag-agnostic
		// architecture creep check; the go/packages alternative is overkill here.
		pkgs, err := parser.ParseDir(fset, pkgPath, func(fi os.FileInfo) bool { //nolint:staticcheck // SA1019: ParseDir is fine for this build-tag-agnostic scan
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil || len(pkgs) == 0 {
			t.Fatalf("%s is named by this check but does not parse: %v", pkg, err)
		}

		for _, p := range pkgs {
			for fileName, f := range p.Files {
				for _, imp := range f.Imports {
					path := strings.Trim(imp.Path.Value, `"`)
					if path == "reflect" {
						t.Errorf("CREEP: reflect imported in security-critical package %s (%s)",
							pkg, filepath.Base(fileName))
					}
				}
			}
		}
	}
}

// TestNoSharedDTOPackages verifies no "dto", "model", "types", or "common"
// utility packages exist that would become implicit coupling points.
func TestNoSharedDTOPackages(t *testing.T) {
	forbidden := []string{"dto", "model", "types", "common", "util", "utils", "helpers", "shared"}
	root := filepath.Join("..", "..", "internal")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir internal: %v", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		for _, f := range forbidden {
			if name == f {
				t.Errorf("CREEP: forbidden shared-abstraction package internal/%s — use bounded-context packages instead", e.Name())
			}
		}
	}
}
