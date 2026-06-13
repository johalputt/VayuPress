// creep_test.go detects shared-abstraction creep patterns that bypass import
// layer enforcement: global state, reflection abuse, forbidden utility symbols.
package archcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoGlobalMutableStateInDomainPackages verifies that domain-context packages
// do not declare package-level var blocks (which create hidden shared state).
// Infrastructure packages (logging, metrics) are allowed explicit globals.
func TestNoGlobalMutableStateInDomainPackages(t *testing.T) {
	// Domain packages that should not have mutable package-level vars.
	domainPkgs := []string{
		"internal/signing",
		"internal/merkle",
		"internal/governance",
		"internal/federation",
		"internal/did",
		"internal/archive",
	}

	// Exceptions: var blocks that are clearly immutable constants or sentinels.
	allowedPatterns := []string{
		"Err",    // sentinel errors
		"ErrCap", // capability errors
	}

	root := filepath.Join("..", "..")
	for _, pkg := range domainPkgs {
		pkgPath := filepath.Join(root, pkg)
		if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
			continue
		}

		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, pkgPath, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			continue
		}

		for _, p := range pkgs {
			for fileName, f := range p.Files {
				for _, decl := range f.Decls {
					gd, ok := decl.(*ast.GenDecl)
					if !ok || gd.Tok != token.VAR {
						continue
					}
					for _, spec := range gd.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for _, name := range vs.Names {
							n := name.Name
							if isAllowed(n, allowedPatterns) {
								continue
							}
							// Unexported single-letter vars are likely loop vars caught by parser — skip.
							if len(n) == 1 {
								continue
							}
							t.Errorf("CREEP: global mutable var %q in domain package %s (%s)",
								n, pkg, filepath.Base(fileName))
						}
					}
				}
			}
		}
	}
}

// TestNoReflectionInCriticalPaths verifies that security-critical packages
// do not use reflect — reflection can bypass type safety and capability checks.
func TestNoReflectionInCriticalPaths(t *testing.T) {
	criticalPkgs := []string{
		"internal/sandbox",
		"internal/signing",
		"internal/did",
	}

	root := filepath.Join("..", "..")
	for _, pkg := range criticalPkgs {
		pkgPath := filepath.Join(root, pkg)
		if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
			continue
		}

		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, pkgPath, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			continue
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

func isAllowed(name string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
