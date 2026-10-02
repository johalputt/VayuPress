// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every escape is a call the analyzer can see. Code scanning's reflected-XSS
// query takes html.EscapeString as a barrier only where it is called by name:
// through a local alias (esc := html.EscapeString) or a parameter of type
// func(string) string, it carries the request value straight through the
// escape and reports an unescaped write. Escaped output read as raw that way,
// and alerts that say nothing true are alerts nobody reads: the next real
// one would stand among them unseen. The escape is spelled
// html.EscapeString(...), or esc(...) for the package's own esc, and is never
// passed around as a value.
func TestEscapesAreCalledByName(t *testing.T) {
	// The console's own package and every package it renders through.
	var files []string
	for _, root := range []string{".", filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && (root != "." || filepath.Dir(p) == ".") {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range escapeValueFaults(t, name, src) {
			t.Error(f)
		}
	}
}

// escapers are the escaping functions code scanning models as barriers, by
// import path.
var escapers = map[string]map[string]bool{
	"html":          {"EscapeString": true},
	"html/template": {"HTMLEscapeString": true, "JSEscapeString": true},
}

func escapeValueFaults(t *testing.T, name string, src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	// The local name each escaping package is imported under.
	pkgs := map[string]string{}
	for _, im := range file.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if escapers[p] == nil {
			continue
		}
		local := filepath.Base(p)
		if im.Name != nil {
			local = im.Name.Name
		}
		pkgs[local] = p
	}
	// What is called: the function position of every call.
	called := map[ast.Expr]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			called[c.Fun] = true
		}
		return true
	})
	var faults []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if ok && escapers[pkgs[id.Name]][x.Sel.Name] && !called[x] {
				faults = append(faults, fset.Position(x.Pos()).String()+": "+id.Name+"."+x.Sel.Name+" taken as a value; call it by name")
			}
		case *ast.FuncType:
			for _, p := range fieldList(x.Params) {
				if isStringFunc(p.Type) {
					for _, n := range p.Names {
						faults = append(faults, fset.Position(n.Pos()).String()+": a func(string) string parameter "+n.Name+" hides the escape it is handed; call it by name")
					}
				}
			}
		}
		return true
	})
	return faults
}

func fieldList(l *ast.FieldList) []*ast.Field {
	if l == nil {
		return nil
	}
	return l.List
}

// isStringFunc is func(string) string, the shape of an escaper.
func isStringFunc(e ast.Expr) bool {
	ft, ok := e.(*ast.FuncType)
	if !ok || len(fieldList(ft.Params)) != 1 || len(fieldList(ft.Results)) != 1 {
		return false
	}
	in, iok := ft.Params.List[0].Type.(*ast.Ident)
	out, ook := ft.Results.List[0].Type.(*ast.Ident)
	return iok && ook && in.Name == "string" && out.Name == "string" && len(ft.Params.List[0].Names) <= 1
}

// The gate is seen to refuse each way of hiding an escape, one seed per rule.
func TestTheEscapeGateRefusesEachHiding(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"an alias":              {"package p\nimport \"html\"\nfunc f(s string) string { esc := html.EscapeString; return esc(s) }", "html.EscapeString taken as a value"},
		"a renamed import":      {"package p\nimport h \"html\"\nvar e = h.EscapeString", "h.EscapeString taken as a value"},
		"the template alias":    {"package p\nimport htmpl \"html/template\"\nfunc f(s string) string { esc := htmpl.HTMLEscapeString; return esc(s) }", "htmpl.HTMLEscapeString taken as a value"},
		"passed as an argument": {"package p\nimport \"html\"\nfunc g(func(string) string) {}\nfunc f() { g(html.EscapeString) }", "html.EscapeString taken as a value"},
		"a parameter":           {"package p\nfunc f(esc func(string) string, s string) string { return esc(s) }", "parameter esc hides the escape"},
	} {
		got := strings.Join(escapeValueFaults(t, name+".go", []byte(c.src)), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the gate said %q, want %q", name, got, c.want)
		}
	}
	for name, src := range map[string]string{
		"a call":            "package p\nimport \"html\"\nfunc f(s string) string { return html.EscapeString(s) }",
		"the package's esc": "package p\nfunc f(s string) string { return esc(s) }",
		"a closure":         "package p\nimport \"html\"\nfunc f(s string) string { esc := func(v string) string { return html.EscapeString(v) }; return esc(s) }",
		"another package":   "package p\nimport \"strings\"\nvar u = strings.ToUpper",
		"an escaped string": "package p\nimport \"html\"\nfunc f(s string) string { esc := html.EscapeString(s); return esc }",
	} {
		if f := escapeValueFaults(t, name+".go", []byte(src)); len(f) > 0 {
			t.Errorf("%s: refused %v", name, f)
		}
	}
}
