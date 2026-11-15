// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// consoleReadsPublicPool names the console functions that read from the
// public pool on purpose, each with the reason.
var consoleReadsPublicPool = map[string]string{
	"loadPostMeta": "the public article page reads a post's options through it too",
}

// TestTheConsoleReadsFromItsOwnPool keeps the console's reads off the public
// read pool. The public pool serves every reader of every site, and a crawler
// flood or a cold-cache render storm can hold all of its connections; a console
// read waiting there is the operator locked out of their own install while it
// is busy. dbpkg.AdminReader is the pool reserved for the console. A console
// file is one whose name says so: admin_os_, vayuos_ or os_notif_.
func TestTheConsoleReadsFromItsOwnPool(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") ||
			!(strings.HasPrefix(f, "admin_os_") || strings.HasPrefix(f, "vayuos_") || strings.HasPrefix(f, "os_notif_")) {
			continue
		}
		af, err := parser.ParseFile(fs, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, ok := consoleReadsPublicPool[fd.Name.Name]; ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "dbpkg" && (sel.Sel.Name == "Reader" || sel.Sel.Name == "RDB") {
					t.Errorf("%s: %s reads from the public pool (dbpkg.%s); the console reads from dbpkg.AdminReader()",
						fs.Position(sel.Pos()), fd.Name.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}
}
