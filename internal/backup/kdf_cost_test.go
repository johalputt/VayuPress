// SPDX-License-Identifier: Apache-2.0

package backup

// kdf_cost_test.go — the three checks that make lowering the KDF cost in tests
// safe rather than convenient.
//
// The format suite flips every byte of the header and re-opens the archive each
// time. At the shipped Argon2id cost that is a few hundred 64 MiB derivations,
// which under -race took this package past the CI timeout on its own. Lowering
// the cost for the tests is the right trade — the suite is about the sealed
// stream, not about how expensive the passphrase stretch is — but only if
// nothing that ships can make the same move. That is what this file enforces.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kdfAtInit captures activeKDF before TestMain touches it. Package-level
// variables are initialised before TestMain runs, so this is the value a
// non-test build would use — the only way to assert on it from inside a test
// binary that deliberately changes it.
var kdfAtInit = activeKDF

// testKDF is the cheap profile. keyLen stays 32 because that is not a cost
// knob: AES-256 needs 32 bytes, and a suite that derived 16 would be exercising
// a cipher configuration this package never ships. memory is the Argon2 floor
// for one thread.
var testKDF = kdfCost{time: 1, memory: 8, threads: 1, keyLen: 32}

func TestMain(m *testing.M) {
	activeKDF = testKDF
	os.Exit(m.Run())
}

// TestShippedKDFCostIsWhatWeThinkItIs pins the numbers. Without this, "the
// tests lower the cost" and "someone lowered the cost" look identical in a
// diff.
func TestShippedKDFCostIsWhatWeThinkItIs(t *testing.T) {
	want := kdfCost{time: 3, memory: 64 * 1024, threads: 2, keyLen: 32}
	if shippedKDF != want {
		t.Fatalf("shipped Argon2id cost changed: got %+v, want %+v", shippedKDF, want)
	}
	if kdfAtInit != want {
		t.Fatalf("a non-test build would derive at %+v, not the shipped %+v", kdfAtInit, want)
	}
}

// TestOnlyTestsMayLowerTheKDFCost parses every non-test file in the package and
// fails on any assignment to activeKDF. The var DECLARATION lives in backup.go
// and is a GenDecl, not an AssignStmt, so this cannot fire on the line that
// creates it — and it cannot fire on itself, because it never reads _test.go
// files.
func TestOnlyTestsMayLowerTheKDFCost(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range v.Lhs {
					if rootIdent(lhs) == "activeKDF" {
						t.Errorf("%s:%d assigns activeKDF — a shipped code path must never "+
							"be able to weaken the passphrase stretch",
							name, fset.Position(lhs.Pos()).Line)
					}
				}
			case *ast.IncDecStmt:
				if rootIdent(v.X) == "activeKDF" {
					t.Errorf("%s:%d mutates activeKDF", name, fset.Position(v.Pos()).Line)
				}
			case *ast.UnaryExpr:
				// &activeKDF hands the knob to whoever holds the pointer, which
				// is the same hole with one more step in it.
				if v.Op == token.AND && rootIdent(v.X) == "activeKDF" {
					t.Errorf("%s:%d takes the address of activeKDF — a pointer to the "+
						"cost is a writable cost", name, fset.Position(v.Pos()).Line)
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no non-test files; the guard proved nothing")
	}
}

// rootIdent peels a target expression back to the variable it ultimately
// writes. The first version of this guard matched a bare *ast.Ident only, which
// meant `activeKDF = cheap` was caught and `activeKDF.memory = 8` sailed
// straight through — the second being the easier edit to make and the harder
// one to notice in review.
func rootIdent(e ast.Expr) string {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.SelectorExpr:
			e = v.X
		case *ast.IndexExpr:
			e = v.X
		case *ast.StarExpr:
			e = v.X
		case *ast.ParenExpr:
			e = v.X
		default:
			return ""
		}
	}
}

// TestShippedKDFCostActuallyDerivesAKey runs the real parameters. Argon2
// rejects some combinations outright, and a 32-byte key is the whole contract
// Seal and Open depend on — neither of them cares what the cost was, only that
// what came back fits AES-256. That is why the end-to-end suite can run cheap
// and this one derivation covers the shipped profile.
func TestShippedKDFCostActuallyDerivesAKey(t *testing.T) {
	salt := bytes.Repeat([]byte{0x5a}, saltLen)
	shipped := deriveKeyWith(shippedKDF, "pw", salt)
	if len(shipped) != dekLen {
		t.Fatalf("shipped cost derived %d bytes, need %d for AES-256", len(shipped), dekLen)
	}
	// And the knob is real: a different cost must produce a different key, or
	// the tests would be silently running at the shipped cost anyway and the
	// timeout this change exists to fix would come straight back.
	cheap := deriveKeyWith(testKDF, "pw", salt)
	if bytes.Equal(shipped, cheap) {
		t.Fatal("the shipped and test costs derive the same key — the cost is not being applied")
	}
	// deriveKey must route through activeKDF and nothing else.
	if !bytes.Equal(deriveKey("pw", salt), cheap) {
		t.Fatal("deriveKey ignored activeKDF")
	}
}
