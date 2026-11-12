// SPDX-License-Identifier: Apache-2.0

package main

// guest_language.go — the language a page for someone with no account is
// shown in (internal/i18n/console.go): the one they chose from the page's
// menu, kept in a cookie, else the best of what their browser asks for,
// else English. The menu is a plain form that asks for the same page again
// with ?lang=, so it needs no script.

import (
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
)

// langCookie keeps a visitor's chosen language.
const langCookie = "vp_lang"

// requestLang is the language to answer r in. A ?lang= naming one of the
// console's languages is the visitor's choice, and is kept.
func requestLang(w http.ResponseWriter, r *http.Request) string {
	if want := r.URL.Query().Get("lang"); want != "" {
		if got := i18n.Choose(want, ""); got == want {
			http.SetCookie(w, &http.Cookie{Name: langCookie, Value: got, Path: "/", MaxAge: int((365 * 24 * time.Hour).Seconds()),
				HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
			return got
		}
	}
	chosen := ""
	if c, err := r.Cookie(langCookie); err == nil {
		chosen = c.Value
	}
	return i18n.Choose(chosen, r.Header.Get("Accept-Language"))
}

// languageMenu is the form that shows the page in another language.
func languageMenu(lang string) string {
	var b strings.Builder
	b.WriteString(`<form class="login-form" method="GET"><label class="field"><span class="field-label">` + esc(i18n.T(lang, "Language")) + `</span><select class="input" name="lang">`)
	for _, l := range i18n.Languages {
		sel := ""
		if l.Tag == lang {
			sel = " selected"
		}
		b.WriteString(`<option value="` + esc(l.Tag) + `" lang="` + esc(l.Tag) + `"` + sel + `>` + esc(l.Name) + `</option>`)
	}
	b.WriteString(`</select></label><button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Change")) + `</button></form>`)
	return b.String()
}
