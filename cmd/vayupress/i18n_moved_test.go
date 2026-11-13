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
	"vayuos.go":                       {"mailFolderNav", "mailFolderLabel", "mailViewLabel"},
	"vayuos_mail_blocked.go":          nil,
	"vayuos_mail_folders.go":          nil,
	"vayuos_mail_keys.go":             nil,
	"vayuos_mail_labels.go":           nil,
	"vayuos_mail_searches.go":         nil,
}

// i18nExempt are the calls whose words are for the operator or the code, not
// the person reading the page: logs, the audit record, errors made for code.
var i18nExempt = map[string]bool{
	"LogError": true, "LogInfo": true, "LogJSON": true, "AuditLog": true,
	"Errorf": true, "New": true, "Sprintf": true, "T": true,
}

// i18nNames are what every language writes as it is: the product's own names,
// and the armour line OpenPGP fixes, which a key pasted in any language begins
// with.
var i18nNames = []string{"VayuPress", "VayuMail", "VayuOS", "-----BEGIN PGP PUBLIC KEY BLOCK-----"}

// i18nWithoutNames is text with the names taken out.
func i18nWithoutNames(text string) string {
	for _, name := range i18nNames {
		text = strings.ReplaceAll(text, name, "")
	}
	return text
}

var (
	i18nReadAttr = regexp.MustCompile(`(?:title|placeholder|aria-label|alt|hx-confirm|data-confirm)="([^"]*)`)
	// i18nPhrase is words a person reads: two of them at least, apart.
	i18nPhrase   = regexp.MustCompile(`\p{L}{2,}\s+\p{L}{2,}`)
	i18nTag      = regexp.MustCompile(`<[^>]*>|&[a-z]+;|&#[0-9]+;`)
	i18nWord     = regexp.MustCompile(`\p{L}{2,}`)
	i18nSentence = regexp.MustCompile(`^\p{Lu}\p{Ll}*(\s+\S+){1,}[.?!:]?$`)
)

// i18nEnglishIn is the English a person would read in a string literal, or
// "": the text between tags of an HTML fragment, or a literal that is a
// sentence on its own.
func i18nEnglishIn(lit string) string {
	// What a person reads inside a tag: a confirmation, a placeholder, a
	// tooltip, a label for a screen reader. A value may run past the end of
	// the literal, cut off by a value spliced in.
	for _, m := range i18nReadAttr.FindAllStringSubmatch(lit, -1) {
		if i18nPhrase.MatchString(i18nWithoutNames(m[1])) {
			return m[1]
		}
	}
	// A literal that begins inside such a value, after a value spliced in:
	// `? Mail to them is then sent readable.">`.
	if i := strings.Index(lit, `"`); i > 0 && strings.Contains(lit[i:], ">") && !strings.ContainsAny(lit[:i], "<>=") && i18nPhrase.MatchString(lit[:i]) {
		return lit[:i]
	}
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
		text := i18nWithoutNames(strings.TrimSpace(i18nTag.ReplaceAllString(lit, " ")))
		if i18nWord.MatchString(text) {
			return text
		}
		return ""
	}
	// A sentence spliced after a value begins with the punctuation that ends
	// it: ". Their mail will be refused."
	s := strings.TrimLeft(strings.TrimSpace(lit), ".,;:!? ")
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
		{`hx-confirm="Remove the key for `, true},
		{`? Mail to them is then sent readable, unless they publish a key.">`, true},
		{`<input placeholder="you@example.com" aria-label="`, false},
		{`<input placeholder="Your password">`, true},
		{`<textarea placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----" required>`, false},
		{`<textarea placeholder="Paste your VayuMail key">`, true},
		{`width:12px">`, false},
		{`. Their mail will be refused, or put straight in Trash.`, true},
		{`.`, false},
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
