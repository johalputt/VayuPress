// SPDX-License-Identifier: Apache-2.0

package main

// guest_language.go — the language a page for someone with no account is
// shown in (internal/i18n/console.go): the one they chose from the page's
// menu, kept in a cookie, else the best of what their browser asks for,
// else English. The menu is a plain form that asks for the same page again
// with ?lang=, so it needs no script.

import (
	"net/http"
	"net/url"
	"path"
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
			setLangCookie(w, r, got)
			return got
		}
	}
	chosen := ""
	if c, err := r.Cookie(langCookie); err == nil {
		chosen = c.Value
	}
	return i18n.Choose(chosen, r.Header.Get("Accept-Language"))
}

// setLangCookie keeps lang as this browser's language for a year.
func setLangCookie(w http.ResponseWriter, r *http.Request, lang string) {
	http.SetCookie(w, &http.Cookie{Name: langCookie, Value: lang, Path: "/", MaxAge: int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
}

// saLanguageRow is the account menu's language choice. It posts to
// /os/profile/language (handleOSProfileLanguage), which keeps it on the
// account and in this browser, and comes back to the page it was chosen on;
// admin-os.js fills the token and the page to come back to as it is sent.
func saLanguageRow(lang string) string {
	var b strings.Builder
	b.WriteString(`<form class="sa-menu__row sa-menu__lang" method="POST" action="/os/profile/language" data-lang-form>` +
		`<input type="hidden" name="csrf_token" value=""><input type="hidden" name="back" value="">` +
		`<label><span>` + esc(i18n.T(lang, "Language")) + `</span><select name="lang">`)
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

// handleOSProfileLanguage keeps the language chosen in the account menu: on
// the account, when the session has one (a mailbox signed in on its own has
// none, and keeps it in this browser alone), and in this browser's cookie.
// It answers with the page it was chosen on, by a local path only, with any
// ?lang= taken off so the old choice in the address does not win over it.
func (a *App) handleOSProfileLanguage(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	if got := i18n.Choose(lang, ""); got != lang {
		http.Error(w, "unknown language", http.StatusBadRequest)
		return
	}
	if u := currentUser(r); u != nil && a.userStore != nil && !strings.HasPrefix(u.ID, "vmail:") {
		if err := a.userStore.SetConsoleLang(r.Context(), u.ID, lang); err != nil {
			http.Error(w, "could not keep the language", http.StatusInternalServerError)
			return
		}
	}
	setLangCookie(w, r, lang)
	http.Redirect(w, r, consoleBack(r.FormValue("back")), http.StatusSeeOther)
}

// consoleBack is back when it is a console page on this host, without its
// lang parameter; /os otherwise. The answer is built from the constant "/os/"
// and the path cleaned of ".." and repeated slashes, never from back itself,
// so it cannot name another host whatever back holds: "//host/os" parses with
// a host and "javascript:/os" with a scheme, and both are refused; "/\\host",
// which a browser reads as another host, is not under /os/.
func consoleBack(back string) string {
	u, err := url.Parse(back)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "/os"
	}
	p := path.Clean(u.Path)
	if p != "/os" && !strings.HasPrefix(p, "/os/") {
		return "/os"
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(p, "/os"), "/")
	q := u.Query()
	q.Del("lang")
	out := "/os/" + (&url.URL{Path: rest}).EscapedPath()
	if p == "/os" {
		out = "/os"
	}
	if enc := q.Encode(); enc != "" {
		out += "?" + enc
	}
	return out
}

// languageMenu is the form that shows the page in another language.
//
// The form sends the page's own query values with it: a reset link's token,
// a booking's time zone and a sign-in's next page outlive the change, which a
// form of lang alone would drop.
func languageMenu(lang string, r *http.Request) string {
	var b strings.Builder
	b.WriteString(`<form class="login-form" method="GET">`)
	for k, vs := range r.URL.Query() {
		if k == "lang" {
			continue
		}
		for _, v := range vs {
			b.WriteString(`<input type="hidden" name="` + esc(k) + `" value="` + esc(v) + `">`)
		}
	}
	b.WriteString(`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Language")) + `</span><select class="input" name="lang">`)
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

// pageLangHeader carries the language of the console page a script's call
// comes from.
const pageLangHeader = "X-VP-Lang"

// consoleSpeaks is the console's paths that are in the person's language;
// every other page of it is English. A section joins when the last of its
// sentences has moved into i18n.T, so no release shows a page half in one
// language and half in another while a section is being moved.
var consoleSpeaks = []string{"/os/vayumail", "/os/settings", "/os/vayukeep", "/os/apikeys", "/os/newsletter"}

// consoleLanguage puts the language a console page is written in on its
// request: the person's (their choice from ?lang=, kept, then their
// browser's) on a section that speaks it, English on every other. A call to
// /os/api/ answers in the language of the page that made it, which the
// console's scripts send as pageLangHeader (vayuos.js): the API serves every
// section, so its path cannot say, and a Tor world sends no Referer.
func consoleLanguage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := "en"
		for _, p := range consoleSpeaks {
			if r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") {
				lang = requestLang(w, r)
				w.Header().Add("Vary", "Accept-Language, Cookie")
				break
			}
		}
		if strings.HasPrefix(r.URL.Path, "/os/api/") {
			if want := r.Header.Get(pageLangHeader); want != "" && i18n.Choose(want, "") == want {
				lang = want
			}
			w.Header().Add("Vary", pageLangHeader)
		}
		w.Header().Set("Content-Language", lang)
		next.ServeHTTP(w, r.WithContext(i18n.WithLang(r.Context(), lang)))
	})
}
