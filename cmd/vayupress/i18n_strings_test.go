// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
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
	"time"

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
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "i18n", "locales", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogue := map[string]string{}
	if err := json.Unmarshal(b, &catalogue); err != nil {
		t.Fatal(err)
	}
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
			if _, ok := catalogue[msg]; !ok {
				t.Errorf("%s: %q is not in locales/en.json", fset.Position(call.Pos()), msg)
			}
			// Every placeholder the sentence names is given a value here: one
			// left out is written on the page as <nil>. Names chosen at run
			// time are not read.
			given := map[string]bool{}
			for k := 2; k+1 < len(call.Args); k += 2 {
				key, ok := call.Args[k].(*ast.BasicLit)
				if !ok || key.Kind != token.STRING {
					return true
				}
				name, _ := strconv.Unquote(key.Value)
				given[name] = true
			}
			for _, name := range sentencePlaceholders(msg) {
				if !given[name] {
					t.Errorf("%s: %q names {%s} and is given no value for it", fset.Position(call.Pos()), msg, name)
				}
			}
			return true
		})
	}
	// The console's scripts say theirs with vpT (script_words.go).
	for _, msg := range scriptSentences() {
		used[msg] = true
		if _, ok := catalogue[msg]; !ok {
			t.Errorf("a script's vpT(%q) is not in locales/en.json", msg)
		}
	}
	for msg := range catalogue {
		if !used[msg] {
			t.Errorf("locales/en.json has %q, which no code says", msg)
		}
	}
}

// sentencePlaceholders is every name a sentence fills in: {name}, and the
// counted name of a plural with the names inside its branches. A branch's own
// words ("one {message}") are not names.
func sentencePlaceholders(msg string) []string {
	var out []string
	for i := 0; i < len(msg); i++ {
		if msg[i] != '{' {
			continue
		}
		depth, end := 0, -1
		for j := i; j < len(msg) && end < 0; j++ {
			switch msg[j] {
			case '{':
				depth++
			case '}':
				if depth--; depth == 0 {
					end = j
				}
			}
		}
		if end < 0 {
			return out
		}
		name, rest, plural := strings.Cut(msg[i+1:end], ",")
		out = append(out, strings.TrimSpace(name))
		if plural {
			_, branches, _ := strings.Cut(rest, ",")
			out = append(out, sentencePlaceholders(branchTexts(branches))...)
		}
		i = end
	}
	return out
}

// branchTexts is a plural's branches' texts, each wrapped in braces removed:
// "one {# in {folder}} other {…}" gives "# in {folder} …".
func branchTexts(branches string) string {
	var b strings.Builder
	depth := 0
	for _, r := range branches {
		switch {
		case r == '{':
			if depth > 0 {
				b.WriteRune(r)
			}
			depth++
		case r == '}':
			depth--
			if depth > 0 {
				b.WriteRune(r)
			}
			b.WriteRune(' ')
		case depth > 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestASentencesPlaceholdersAreReadAsTheyAre — the names the catalogue test
// requires a value for, one seed per shape.
func TestASentencesPlaceholdersAreReadAsTheyAre(t *testing.T) {
	for msg, want := range map[string]string{
		"Not done: {reason}.":                                                    "reason",
		"{n, plural, one {# message} other {# messages}}":                        "n",
		"{n, plural, one {Delete # in {folder}?} other {Delete # in {folder}?}}": "n folder folder",
		"It is set on {link}, where.":                                            "link",
		"No names here.":                                                         "",
	} {
		if got := strings.Join(sentencePlaceholders(msg), " "); got != want {
			t.Errorf("%q names %q, want %q", msg, got, want)
		}
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

// TestABookingPageIsInTheGuestsLanguage — its words, its month and the
// heads of its days.
func TestABookingPageIsInTheGuestsLanguage(t *testing.T) {
	a := calendarApp(t)
	danaAllDay(t, a)
	h := bookingRouter(a)
	req := httptest.NewRequest(http.MethodGet, "/book/dana/intro?tz=UTC", nil)
	req.Header.Set("Accept-Language", "ja-JP,ja;q=0.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	month := i18n.Date("ja", time.Now().UTC(), i18n.MonthYear)
	for _, want := range []string{`<html lang="ja">`, "あなたの時間帯：UTC", ">" + month + "<", `aria-hidden="true">月</span>`, "確認用のリンクをメールでお送りします。"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Japanese page has no %q", want)
		}
	}
}
