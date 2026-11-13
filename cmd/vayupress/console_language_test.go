// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
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
