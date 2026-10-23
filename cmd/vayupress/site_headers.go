// SPDX-License-Identifier: Apache-2.0

package main

// site_headers.go — a hosted site's own response headers.
//
// Each is a choice from a fixed list, never free text: a header value an
// operator types is a header an operator can break, and one that would let a
// page be framed by anyone, or send full URLs to every other site, is not on
// any list here. The built-in headers stay the default (an empty setting),
// and a choice reaches the site's public pages only: never a strict path
// (the console, the API, sign-in, a page that carries a session), so no
// setting can loosen the console, and never an onion, which keeps its own
// stricter set. The CSP stays the code's; framing is the one directive a
// choice here may change, from 'none' to 'self'.

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// headerChoice is one value a header setting may take.
type headerChoice struct{ Value, Label string }

var (
	hstsChoices = []headerChoice{
		{"", "Built-in: two years, subdomains, preloaded"},
		{"2y-sub", "Two years, with subdomains, not for the preload list"},
		{"2y", "Two years, this host only"},
		{"6m", "Six months, this host only"},
		{"off", "Off: browsers that visit forget it (max-age=0)"},
	}
	hstsValues = map[string]string{
		"2y-sub": "max-age=63072000; includeSubDomains",
		"2y":     "max-age=63072000",
		"6m":     "max-age=15552000",
		"off":    "max-age=0",
	}
	// Only the policies that never send a full address to another site.
	referrerChoices = []headerChoice{
		{"", "Built-in: the origin to other sites, the full address within this one"},
		{"strict-origin", "The origin only, everywhere"},
		{"same-origin", "Nothing to other sites, the full address within this one"},
		{"no-referrer", "Nothing, ever"},
	}
	frameChoices = []headerChoice{
		{"", "Built-in: no page may frame this site's pages"},
		{"sameorigin", "This site's own pages may frame each other"},
	}
	allowChoices = []headerChoice{
		{"", "Blocked (built-in)"},
		{"self", "Allowed for this site's own pages"},
	}
)

// permissionKeys are the features the built-in Permissions-Policy blocks,
// with the key that may allow each for a site's own pages.
var permissionKeys = []struct{ feature, key string }{
	{"camera", settings.KeySiteAllowCamera},
	{"microphone", settings.KeySiteAllowMic},
	{"geolocation", settings.KeySiteAllowGeo},
	{"payment", settings.KeySiteAllowPayment},
}

// hstsConsequence is said beside the choice, because HSTS is the one header
// whose change outlives the setting.
const hstsConsequence = "A browser keeps what it was last told for as long as that said: shortening this or turning it off reaches only browsers that visit again, and a host on the preload list stays on it, with every subdomain, until the list's own removal is done, which takes months."

// applySiteHeaders sets d's chosen headers on a public page of d. It returns
// the writer to use: wrapped when framing is opened, so the CSP the handler
// builds is adjusted as it goes out.
func (a *App) applySiteHeaders(w http.ResponseWriter, r *http.Request, d domain.Domain) http.ResponseWriter {
	if a.siteSettings == nil || d.IsPrimary || strictPath(r.URL.Path) {
		return w
	}
	h := w.Header()
	// securityHeadersMiddleware omits HSTS exactly for an onion, before the
	// onion middleware rewrites the Host; its absence is the only record of
	// that left by now. An onion keeps its own stricter headers.
	if h.Get("Strict-Transport-Security") == "" {
		return w
	}
	sc := settings.ForDomain(d.ID)
	get := func(k string) string { return strings.TrimSpace(a.siteSettings.Get(r.Context(), sc, k)) }
	if v, ok := hstsValues[get(settings.KeySiteHSTS)]; ok {
		h.Set("Strict-Transport-Security", v)
	}
	if v := get(settings.KeySiteReferrer); v != "" && isChoice(referrerChoices, v) {
		h.Set("Referrer-Policy", v)
	}
	parts := make([]string, 0, len(permissionKeys))
	allowed := false
	for _, p := range permissionKeys {
		if get(p.key) == "self" {
			parts = append(parts, p.feature+"=(self)")
			allowed = true
		} else {
			parts = append(parts, p.feature+"=()")
		}
	}
	if allowed {
		h.Set("Permissions-Policy", strings.Join(parts, ", "))
	}
	if get(settings.KeySiteFrame) == "sameorigin" {
		h.Set("X-Frame-Options", "SAMEORIGIN")
		return &frameWriter{ResponseWriter: w}
	}
	return w
}

func isChoice(list []headerChoice, v string) bool {
	for _, c := range list {
		if c.Value == v {
			return true
		}
	}
	return false
}

// frameWriter opens the CSP's frame-ancestors to 'self' as the headers go
// out, for the CSP whichever handler built. It does so only while the
// response still says SAMEORIGIN: a handler that set DENY for its own page
// keeps its refusal.
type frameWriter struct {
	http.ResponseWriter
	done bool
}

func (w *frameWriter) apply() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	if h.Get("X-Frame-Options") != "SAMEORIGIN" {
		return
	}
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if v := h.Get(name); v != "" {
			h.Set(name, strings.Replace(v, "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
		}
	}
}

func (w *frameWriter) WriteHeader(code int) {
	w.apply()
	w.ResponseWriter.WriteHeader(code)
}

func (w *frameWriter) Write(b []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection's own writer.
func (w *frameWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps streamed responses streaming through the wrapper.
func (w *frameWriter) Flush() {
	w.apply()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
