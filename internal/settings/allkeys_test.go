// SPDX-License-Identifier: Apache-2.0

package settings

// allkeys_test.go — a key that is not in AllKeys is a write that silently does
// nothing.
//
// SetMany's loop is `if !AllKeys[k] { continue }`, documented as "unknown keys
// are silently ignored". That is right for a caller passing attacker-influenced
// input, and a landmine for the person adding a key: declare the constant, use
// it, watch the write report success, and find an empty table.
//
// It is not hypothetical, and it was not one key. ADR-0155 P2 added KeyTalkHost,
// wired a CLI to it, ran the CLI, saw "talk host set to talk.example.com" and
// read back nothing. Writing this guard then found FIVE more already shipped
// that way — including every VayuKeep setting, so an operator switching on
// continuous encrypted replication was told it was on while nothing was stored.
//
// Nothing in the compiler, the linter or any existing test said a word about any
// of them. The only reason the first was caught is that the command was actually
// run against a database instead of being assumed to work.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// parsePackageFiles parses this package's non-test sources.
//
// A directory walk plus ParseFile rather than parser.ParseDir, which Go 1.25
// deprecated because it ignores build tags when associating files with packages.
// Nothing here is build-tagged, but a deprecated call inside a guard is a guard
// that stops compiling a release from now.
func parsePackageFiles(t *testing.T, mode parser.Mode) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, mode)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("no package sources parsed; this guard is blind")
	}
	return out
}

// declaredKeys returns every exported `Key*` string constant and its value,
// skipping any marked Deprecated.
//
// Parsed rather than reflected over: a constant is not addressable at runtime,
// so there is no way to enumerate them without reading the declarations.
func declaredKeys(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, file := range parsePackageFiles(t, parser.ParseComments) {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// A key marked `Deprecated:` is read-only BY DESIGN — retained so
				// older stored values still resolve, and it must not become
				// writable again merely to satisfy a guard. That is a real
				// category rather than an escape hatch: KeyFeatureMeili names a
				// search backend that was removed, and making it settable would
				// re-expose a toggle the product deliberately retired.
				if vs.Doc != nil && strings.Contains(vs.Doc.Text(), "Deprecated:") {
					continue
				}
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Key") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if v, err := strconv.Unquote(lit.Value); err == nil {
						out[name.Name] = v
					}
				}
			}
		}
	}
	return out
}

// THE test. A declared, non-deprecated key that SetMany would drop is a control
// an operator can set and that never takes.
func TestEveryDeclaredKeyIsWritable(t *testing.T) {
	declared := declaredKeys(t)
	if len(declared) < 20 {
		t.Fatalf("only %d Key* constants parsed; this guard is not seeing the package", len(declared))
	}
	for name, value := range declared {
		if !AllKeys[value] {
			t.Errorf("%s = %q is declared but missing from AllKeys.\n"+
				"\tSetMany silently ignores keys it does not know, so every write of this "+
				"setting reports success and changes nothing — which is how every VayuKeep "+
				"setting shipped inert. Add it here, or mark the constant Deprecated: if it "+
				"is deliberately read-only.", name, value)
		}
	}
}

// And the reverse, so AllKeys cannot accumulate strings no constant names. A key
// nothing declares is one nothing reads, and it survives a rename that should
// have broken loudly.
func TestAllKeysHasNoEntriesNothingDeclares(t *testing.T) {
	values := map[string]bool{}
	for _, file := range parsePackageFiles(t, 0) {
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					values[v] = true
				}
			}
			return true
		})
	}
	for k := range AllKeys {
		if !values[k] {
			t.Errorf("AllKeys contains %q, which no string literal in this package produces — "+
				"a writable key nothing declares or reads", k)
		}
	}
}

// The keys this guard was written over, pinned by name so a future tidy-up
// cannot quietly drop one back out of AllKeys. These were found INERT in shipped
// code: their panels wrote them and nothing was stored.
func TestThePreviouslyInertKeysStayWritable(t *testing.T) {
	for _, k := range []string{
		KeyVayuKeepEnabled, KeyVayuKeepTarget, KeyVayuKeepRetainDays,
		KeyVayuKeepRetainGen, KeyMailQueueRetentionDays, KeyTalkHost,
	} {
		if !AllKeys[k] {
			t.Errorf("%q is not writable again; the panel that sets it will report success "+
				"and store nothing", k)
		}
	}
}
