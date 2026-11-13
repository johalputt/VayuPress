// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
)

// i18nMoved is every page whose words a person reads have all moved into
// i18n.T: a whole file (no functions named) or the named functions of one.
// A page is added here when its sentences move, and from then on an English
// sentence written beside T fails TestAMovedPageHasNoEnglishOutsideT.
var i18nMoved = map[string][]string{
	"handlers_mail_recovery_pages.go": nil,
	"admin_os_ui.go":                  {"osLoginPage"},
}

// i18nExempt are the calls whose words are for the operator or the code, not
// the person reading the page: logs, the audit record, errors made for code.
var i18nExempt = map[string]bool{
	"LogError": true, "LogInfo": true, "LogJSON": true, "AuditLog": true,
	"Errorf": true, "New": true, "Sprintf": true, "T": true,
}

// i18nNames are the product's own names, which every language writes as they
// are.
var i18nNames = []string{"VayuPress", "VayuMail", "VayuOS"}

var (
	i18nTag      = regexp.MustCompile(`<[^>]*>|&[a-z]+;|&#[0-9]+;`)
	i18nWord     = regexp.MustCompile(`\p{L}{2,}`)
	i18nSentence = regexp.MustCompile(`^\p{Lu}\p{Ll}*(\s+\S+){1,}[.?!:]?$`)
)

// i18nEnglishIn is the English a person would read in a string literal, or
// "": the text between tags of an HTML fragment, or a literal that is a
// sentence on its own.
func i18nEnglishIn(lit string) string {
	if strings.ContainsAny(lit, "<>") {
		// A literal is often a piece of a tag: it may begin inside one
		// (` required>`) or end inside one (`<input value="`), and that part
		// is attributes, not words.
		if i, j := strings.Index(lit, ">"), strings.Index(lit, "<"); i >= 0 && (j < 0 || i < j) {
			lit = lit[i+1:]
		}
		if i, j := strings.LastIndex(lit, "<"), strings.LastIndex(lit, ">"); i >= 0 && i > j {
			lit = lit[:i]
		}
		text := strings.TrimSpace(i18nTag.ReplaceAllString(lit, " "))
		for _, name := range i18nNames {
			text = strings.ReplaceAll(text, name, "")
		}
		if i18nWord.MatchString(text) {
			return text
		}
		return ""
	}
	s := strings.TrimSpace(lit)
	if i18nSentence.MatchString(s) && !strings.ContainsAny(s, "=/{}()") {
		return s
	}
	return ""
}

// TestAMovedPageHasNoEnglishOutsideT — a page moved into i18n.T stays moved:
// an English sentence written beside T would be shown in English in every
// language, and nothing else would notice.
func TestAMovedPageHasNoEnglishOutsideT(t *testing.T) {
	fset := token.NewFileSet()
	for file, funcs := range i18nMoved {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		only := map[string]bool{}
		for _, fn := range funcs {
			only[fn] = true
		}
		found := map[string]bool{}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if len(only) > 0 && !only[fd.Name.Name] {
				continue
			}
			found[fd.Name.Name] = true
			ast.Inspect(fd, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch fun := call.Fun.(type) {
					case *ast.SelectorExpr:
						if i18nExempt[fun.Sel.Name] {
							return false
						}
					case *ast.Ident:
						if i18nExempt[fun.Name] {
							return false
						}
					}
				}
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if eng := i18nEnglishIn(s); eng != "" {
					t.Errorf("%s: English outside i18n.T: %q", fset.Position(lit.Pos()), eng)
				}
				return true
			})
		}
		for fn := range only {
			if !found[fn] {
				t.Errorf("%s has no function %s: the moved list names one that is gone", file, fn)
			}
		}
	}
}

// TestTheEnglishLintSeesEnglish — the lint's reading, checked on its own
// seeds: one per rule, and the strings a page holds that are not English.
func TestTheEnglishLintSeesEnglish(t *testing.T) {
	for _, c := range []struct {
		lit  string
		want bool
	}{
		{`<h1 class="login-title">Welcome back</h1>`, true},
		{`Those passwords do not match.`, true},
		{`<input class="input" type="email" name="email">`, false},
		{`</label><input id="x">`, false},
		{`email`, false},
		{`/mail/recover`, false},
		{` open`, false},
		{`login-error`, false},
		{`<input class="input" value="`, false},
		{`" autocomplete="current-password" required>`, false},
		{`<span>VayuPress</span>`, false},
		{`" required><span>Sign in</span>`, true},
	} {
		if got := i18nEnglishIn(c.lit) != ""; got != c.want {
			t.Errorf("%q read as English: %v, want %v", c.lit, got, c.want)
		}
	}
}

// TestChangingLanguageKeepsWhatThePageWasOpenedWith — a reset link's token is
// in its address; the language menu sends it with the new language, and does
// not send the old language back with it.
func TestChangingLanguageKeepsWhatThePageWasOpenedWith(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/mail/recover/reset?token=abc123&lang=fr", nil)
	menu := languageMenu("fr", r)
	if !strings.Contains(menu, `<input type="hidden" name="token" value="abc123">`) {
		t.Errorf("the menu drops the reset link's token:\n%s", menu)
	}
	if strings.Contains(menu, `type="hidden" name="lang"`) {
		t.Errorf("the menu sends the old language back beside the new:\n%s", menu)
	}
}

// TestSignInIsInThePersonsLanguage — the sign-in page in German, its 2FA
// field opened by the handler's word when the code was what failed (a German
// message holds none of the English words the field used to be opened by).
func TestSignInIsInThePersonsLanguage(t *testing.T) {
	page := osLoginPage("de", "", i18n.T("de", "Enter the 6-digit code from your authenticator app, then re-enter your password."), "", true)
	for _, want := range []string{`<html lang="de"`, "Willkommen zurück", "Anmelden", `<details class="login-totp" open>`} {
		if !strings.Contains(page, want) {
			t.Errorf("the German sign-in page has no %q", want)
		}
	}
	if strings.Contains(page, "Welcome back") {
		t.Error("the German sign-in page says Welcome back")
	}
	if closed := osLoginPage("de", "", i18n.T("de", "Invalid email or password."), "", false); strings.Contains(closed, `login-totp" open`) {
		t.Error("the 2FA field opened for a wrong password")
	}
}
