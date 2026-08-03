// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_home.go — the per-domain console (ADR-0153 Phase 3).
//
// /os/d/{id} is where one hosted domain is operated. Every tool reached from
// here carries the domain in its own URL, so the page you are on is the site you
// are editing, and that is true of the address bar rather than of a mode you
// have to remember being in.

import (
	htmpl "html/template"

	"html"
	"net/http"
	"strconv"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// scopedTool is one per-domain surface reachable from the console.
//
// Live says whether the tool is scoped YET. ADR-0153 lands them one phase at a
// time, and a link to a page that silently edits the primary would be a worse
// version of the defect this whole ADR exists to fix — so an unscoped tool is
// listed and disabled with the reason, not linked and hoped for.
type scopedTool struct {
	Path  string
	Icon  string
	Title string
	Desc  string
	Live  bool
	Soon  string
}

// scopedTools is the per-domain surface. Phase 3 ships the shell; each later
// phase flips one entry Live as it lands.
var scopedTools = []scopedTool{
	{Path: "/os/d/%s/settings", Icon: "⚙️", Title: "Site settings", Live: true,
		Desc: "Name, tagline, description and the basics this site introduces itself with."},
	{Path: "/os/d/%s/theme", Icon: "🎨", Title: "Theme Studio", Live: true,
		Desc: "Colours, typography and custom CSS — this domain's own, not the primary's."},
	{Path: "/os/d/%s/seo", Icon: "🔍", Title: "SEO", Live: false,
		Soon: "Phase 5",
		Desc: "Meta defaults, social cards and per-property verification tokens."},
	{Path: "/os/d/%s/analytics", Icon: "📈", Title: "Analytics", Live: false,
		Soon: "Phase 6",
		Desc: "This domain's own traffic. The event log needs its domain column first."},
}

// handleOSScopedHome renders the console for one hosted domain.
func (a *App) handleOSScopedHome(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	body := scopedHomePage(d) + scopedIndependenceNote()
	writeOSHTML(w, r, adminOSLayout(nonce, d.Host, "optimize", cfg, htmpl.HTML(body)))
}

func scopedHomePage(d domain.Domain) string {
	esc := html.EscapeString
	var b string
	b += `<div class="page-head"><div><h1 class="page-title">` + esc(d.Host) + `</h1>` +
		`<p class="page-sub"><a href="/os/domains/` + esc(d.ID) + `">← Manage site</a> · ` +
		`<a href="` + esc(seo.Origin(d.Host)) + `" target="_blank" rel="noopener noreferrer">View site ↗</a></p></div></div>`
	b += `<p class="page-sub">Everything below applies to <b>` + esc(d.Host) + `</b> and to nothing else on ` +
		`this install. The address bar says which site you are editing, so it cannot be the wrong one.</p>`

	b += `<div class="mon-stack">`
	for _, t := range scopedTools {
		href := "/os/d/" + esc(d.ID) + esc(t.Path[len("/os/d/%s"):])
		if t.Live {
			b += `<a class="card scoped-tool" href="` + href + `">` +
				`<span class="scoped-tool__icon" aria-hidden="true">` + t.Icon + `</span>` +
				`<span class="scoped-tool__body"><span class="settings-block-title">` + esc(t.Title) + `</span>` +
				`<span class="text-sm muted">` + esc(t.Desc) + `</span></span></a>`
			continue
		}
		b += `<div class="card scoped-tool scoped-tool--soon">` +
			`<span class="scoped-tool__icon" aria-hidden="true">` + t.Icon + `</span>` +
			`<span class="scoped-tool__body"><span class="settings-block-title">` + esc(t.Title) +
			` <span class="pill pill--muted">` + esc(t.Soon) + `</span></span>` +
			`<span class="text-sm muted">` + esc(t.Desc) + `</span>` +
			`<span class="text-sm muted">Not scoped yet — until it is, it would edit the primary ` +
			`site, so it is not linked from here.</span></span></div>`
	}
	b += `</div>`
	return b
}

// scopedIndependenceNote states what independence means here, and what it does
// not. An operator selling this needs the ceiling in the same view as the
// capability, or they will discover it in front of a client.
func scopedIndependenceNote() string {
	return `<div class="section-head"><div class="section-head__title">What is shared, and always will be</div>` +
		`<div class="section-head__hint">One binary, one machine — some things cannot be per-domain</div></div>` +
		`<div class="card"><p class="text-sm muted">This domain's settings, content and traffic are its own. ` +
		`These are not, by construction: one process (a bug in it reaches every site at once — row scoping ` +
		`is not a sandbox), one machine and one database (they fail and recover together), one mail signing ` +
		`key, and one bot shield seeing one network stack. Backups and updates are the install's, not the ` +
		`site's.</p></div>`
}

// scopedToolCount is the number of per-domain tools currently live, used by the
// manage page so the link can say what is behind it rather than "more".
func scopedToolCount() string {
	n := 0
	for _, t := range scopedTools {
		if t.Live {
			n++
		}
	}
	return strconv.Itoa(n)
}
