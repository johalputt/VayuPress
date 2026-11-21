// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/users"
)

// consoleLangOf is the language consoleLanguage gives a request to path from
// a browser asking for German.
func consoleLangOf(path string) (string, http.Header) {
	var got string
	h := consoleLanguage(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = i18n.Lang(r.Context()) }))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return got, rec.Header()
}

// A section of the console that speaks the person's language follows their
// browser; every other section stays English, so a page half moved into
// i18n.T is never shown half in German.
func TestTheConsoleSpeaksTheLanguageOnlyWhereASectionHasMoved(t *testing.T) {
	old := consoleSpeaks
	t.Cleanup(func() { consoleSpeaks = old })
	consoleSpeaks = []string{"/os/vayumail"}

	if lang, h := consoleLangOf("/os/vayumail/inbox"); lang != "de" || h.Get("Content-Language") != "de" {
		t.Errorf("a speaking section: %q, Content-Language %q", lang, h.Get("Content-Language"))
	}
	if lang, h := consoleLangOf("/os/vayumail"); lang != "de" || !strings.Contains(h.Get("Vary"), "Accept-Language") {
		t.Errorf("a speaking section's own path: %q, Vary %q", lang, h.Get("Vary"))
	}
	for _, path := range []string{"/os/posts", "/os/vayumailbox", "/os/"} {
		if lang, h := consoleLangOf(path); lang != "en" || h.Get("Content-Language") != "en" {
			t.Errorf("%s, a section still in English, was given %q", path, lang)
		}
	}
}

// The shell writes the page's language into <html lang>, which is what a
// screen reader reads it in.
func TestTheConsoleShellSaysThePagesLanguage(t *testing.T) {
	if head := stillAirShellHead("n", "Mail", "mail", &osSettings{Lang: "ja"}); !strings.Contains(head, `<html lang="ja">`) {
		t.Errorf("a Japanese page's shell says another language")
	}
	if head := stillAirShellHead("n", "Posts", "posts", &osSettings{}); !strings.Contains(head, `<html lang="en">`) {
		t.Errorf("a page with no language set is not English")
	}
}

// The settings every console page is drawn with carry its request's language
// to the shell.
func TestTheConsoleSettingsCarryThePagesLanguage(t *testing.T) {
	if got := (&App{}).getOSSettings(i18n.WithLang(context.Background(), "ko")).Lang; got != "ko" {
		t.Errorf("the settings' language is %q, not the request's", got)
	}
}

// Mail is the section that speaks the person's language as shipped, not only
// when a test sets the list.
func TestMailSpeaksThePersonsLanguage(t *testing.T) {
	if lang, _ := consoleLangOf("/os/vayumail/inbox"); lang != "de" {
		t.Errorf("Mail answered a German browser in %q", lang)
	}
	if lang, _ := consoleLangOf("/os/posts"); lang != "en" {
		t.Errorf("Posts, still English, answered in %q", lang)
	}
}

// The shell of a German page is German: its own sentences, the app labels and
// the account menu's language choice, with German chosen. Drawing it in German
// leaves the registry, which every href and test is written against, English.
func TestTheShellIsDrawnInThePagesLanguage(t *testing.T) {
	head := stillAirShellHead("n", "Mail", "mail", &osSettings{Lang: "de", AccessLevel: accessAdmin})
	for _, want := range []string{
		">Zum Hauptinhalt springen<", ">Suchen oder Befehl ausführen<", ">Inhalte<", ">Inhalte › Beiträge<",
		`action="/os/profile/language"`, `<option value="de" lang="de" selected>Deutsch</option>`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("the German shell lacks %q", want)
		}
	}
	for _, app := range saClearnetApps {
		if app.Label == "Beiträge" || app.Label == "Analysen" {
			t.Fatalf("drawing in German changed the registry: %q", app.Label)
		}
	}
}

// The account menu's language choice goes back to a console page on this
// host and nowhere else, without the ?lang= that would win over it.
func TestTheLanguageChoiceComesBackOnlyToTheConsole(t *testing.T) {
	for back, want := range map[string]string{
		"/os/vayumail/inbox?lang=fr&folder=2": "/os/vayumail/inbox?folder=2",
		"/os":                                 "/os",
		"":                                    "/os",
		"//elsewhere.example/os/x":            "/os",
		"https://elsewhere.example/os/x":      "/os",
		"/\\elsewhere.example/os/x":           "/os",
		"javascript:/os/x":                    "/os",
		"/os/../evil.example":                 "/os",
		"/os/..//evil.example/x":              "/os",
		"/os//elsewhere.example/x":            "/os/elsewhere.example/x",
		"/os/%2F%2Felsewhere.example":         "/os/elsewhere.example",
		"/posts":                              "/os",
		"/osx":                                "/os",
	} {
		if got := consoleBack(back); got != want {
			t.Errorf("back %q: %q, want %q", back, got, want)
		}
	}
}

// A language chosen in the account menu is kept on the account and in this
// browser, and a new sign-in puts it in that browser's cookie; a language
// the console does not have is refused and nothing is kept. Against the
// shipped schema (migration 135).
func TestTheLanguageChosenIsKeptWithTheAccount(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("DB_PATH", filepath.Join(dir, "l.db"))
	os.Setenv("API_KEY", "test-key")
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); dbpkg.DB.Close() })
	a := &App{userStore: users.New(dbpkg.DB), sessions: auth.NewSessionStore(dbpkg.DB)}
	ctx := context.Background()
	u, err := a.userStore.Create(ctx, "ana@example.com", "Ana", "a long enough passphrase 42", users.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	choose := func(lang string) *httptest.ResponseRecorder {
		form := url.Values{"lang": {lang}, "back": {"/os/vayumail/inbox?lang=en"}}
		r := withUser(httptest.NewRequest(http.MethodPost, "/os/profile/language", strings.NewReader(form.Encode())), u)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		a.handleOSProfileLanguage(rec, r)
		return rec
	}
	langCookieOf := func(rec *httptest.ResponseRecorder) string {
		for _, c := range rec.Result().Cookies() {
			if c.Name == langCookie {
				return c.Value
			}
		}
		return ""
	}

	if rec := choose("xx"); rec.Code != http.StatusBadRequest || langCookieOf(rec) != "" {
		t.Errorf("an unknown language: %d, cookie %q", rec.Code, langCookieOf(rec))
	}
	if got := a.userStore.ConsoleLang(ctx, u.ID); got != "" {
		t.Errorf("an unknown language was kept: %q", got)
	}

	rec := choose("ja")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/os/vayumail/inbox" {
		t.Errorf("choosing: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := langCookieOf(rec); got != "ja" {
		t.Errorf("this browser keeps %q", got)
	}
	if got := a.userStore.ConsoleLang(ctx, u.ID); got != "ja" {
		t.Errorf("the account keeps %q", got)
	}

	signIn := httptest.NewRecorder()
	if _, err := a.startSession(signIn, httptest.NewRequest(http.MethodPost, "/os/login", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	if got := langCookieOf(signIn); got != "ja" {
		t.Errorf("a new sign-in's browser is in %q", got)
	}
	other, err := a.userStore.Create(ctx, "bo@example.com", "Bo", "a long enough passphrase 43", users.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	signIn = httptest.NewRecorder()
	if _, err := a.startSession(signIn, httptest.NewRequest(http.MethodPost, "/os/login", nil), other.ID); err != nil {
		t.Fatal(err)
	}
	if got := langCookieOf(signIn); got != "" {
		t.Errorf("an account with no choice set this browser to %q", got)
	}
}

// A call to the console's API answers in the language of the page that made
// it, which the page's scripts send: a language the console has, on an API
// path only. A page outside a speaking section stays English whatever it is
// sent.
func TestAnAPICallAnswersInItsPagesLanguage(t *testing.T) {
	langOf := func(path, header string) string {
		var got string
		h := consoleLanguage(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = i18n.Lang(r.Context()) }))
		r := httptest.NewRequest(http.MethodPost, path, nil)
		if header != "" {
			r.Header.Set(pageLangHeader, header)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	for _, c := range []struct{ path, header, want string }{
		{"/os/api/vayukeep/setup", "de", "de"},
		{"/os/api/settings", "ja", "ja"},
		{"/os/api/settings", i18n.Pseudo, i18n.Pseudo},
		{"/os/api/settings", "", "en"},
		{"/os/api/settings", "xx", "en"},
		{"/os/api/settings", "de\r\nX: y", "en"},
		{"/os/posts", "de", "en"},
	} {
		if got := langOf(c.path, c.header); got != c.want {
			t.Errorf("%s with %q answers in %q, want %q", c.path, c.header, got, c.want)
		}
	}
}
