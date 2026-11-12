// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/i18n"
)

// TestEverySentenceGivenToTIsInTheCatalogue — and the catalogue holds no
// sentence the code no longer says. A sentence written straight into T and
// not added to locales/en.json would be shown in English in every language;
// a sentence left in the files after its code went is work for translators
// that nobody reads. T's first argument is always a literal, so it can be
// read here.
func TestEverySentenceGivenToTIsInTheCatalogue(t *testing.T) {
	used := map[string]bool{}
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" || len(call.Args) < 2 {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "i18n" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: i18n.T is given a sentence that is not written there", fset.Position(call.Pos()))
				return true
			}
			msg, _ := strconv.Unquote(lit.Value)
			used[msg] = true
			if !i18n.Known(msg) {
				t.Errorf("%s: %q is not in locales/en.json", fset.Position(call.Pos()), msg)
			}
			return true
		})
	}
	for msg := range i18n.Catalogue("en") {
		if !used[msg] {
			t.Errorf("locales/en.json has %q, which no code says", msg)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "internal", "i18n", "locales", "en.json")); err != nil {
		t.Fatal(err)
	}
}

// TestALinksPageIsInTheVisitorsLanguage — the browser's, then the one
// chosen from the page's menu, kept for the next visit; the page says
// which it is in.
func TestALinksPageIsInTheVisitorsLanguage(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "plan.txt", "the plan")
	token := makeLink(t, a, driveIDs(t, a, "dana", 0)["plan.txt"], url.Values{"ends": {"7"}})
	visit := func(target string, h http.Header) *httptest.ResponseRecorder {
		router := chi.NewRouter()
		router.Get("/drive/s/{token}", a.handleDriveShare)
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header = h
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	es := visit("/drive/s/"+token, http.Header{"Accept-Language": {"es-MX,es;q=0.9"}})
	if body := es.Body.String(); !strings.Contains(body, `<html lang="es">`) || !strings.Contains(body, "8 B, compartido por dana@example.com") || !strings.Contains(body, ">Descargar<") {
		t.Fatalf("a Spanish browser:\n%s", body)
	}
	chosen := visit("/drive/s/"+token+"?lang=de", http.Header{"Accept-Language": {"es"}})
	var kept *http.Cookie
	for _, c := range chosen.Result().Cookies() {
		if c.Name == langCookie {
			kept = c
		}
	}
	if !strings.Contains(chosen.Body.String(), ">Herunterladen<") || kept == nil || kept.Value != "de" || !kept.HttpOnly {
		t.Fatalf("choosing German: %v\n%s", kept, chosen.Body.String())
	}
	again := visit("/drive/s/"+token, http.Header{"Accept-Language": {"es"}, "Cookie": {langCookie + "=de"}})
	if !strings.Contains(again.Body.String(), `<html lang="de">`) || again.Header().Get("Content-Language") != "de" {
		t.Errorf("the next visit is not in the chosen language:\n%s", again.Body.String())
	}
	if odd := visit("/drive/s/"+token+"?lang=<x>", http.Header{}); strings.Contains(odd.Body.String(), "<x>") || len(odd.Result().Cookies()) != 0 {
		t.Errorf("a language that is not ours was kept or shown")
	}
	// The pseudo-language reaches every sentence of the page: none is left
	// in plain English.
	pseudo := visit("/drive/s/"+token+"?lang="+i18n.Pseudo, http.Header{}).Body.String()
	for _, english := range []string{">Download<", "shared by", ">Language<", ">Change<"} {
		if strings.Contains(pseudo, english) {
			t.Errorf("the pseudo-language left %q in English", english)
		}
	}
}
