// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_seo.go — one hosted domain's search presence (ADR-0153 Phase 5).
//
// This page exists instead of mounting /os/seo under the domain route, and the
// reason is worth stating because the shortcut was tempting.
//
// /os/seo reports on cached artefacts — sitemap.xml, feed.xml, robots.txt in
// config.Cfg.CacheDir — and on config.Cfg.Domain. Those are the PRIMARY's files
// and the primary's hostname. Mounting that page under /os/d/{id} would have
// shown the operator their own site's sitemap freshness under a client's URL,
// with a heading naming the client. That is the same class of defect as the card
// that claimed the shared tools were per-domain: right mechanism, wrong subject,
// and no way for the reader to tell.
//
// So this page shows what genuinely IS this domain's — its own head directives
// and verification tokens, read from its own scope, and its own live SEO URLs —
// and says plainly which parts of search reporting remain install-level.

import (
	"html"
	htmpl "html/template"
	"net/http"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/settings"
)

// scopedSEOFields are the head directives that belong to one site. Verification
// tokens are the clearest case: a search-console token identifies ONE property,
// so a token shared across every hosted domain is correct for exactly one of
// them and wrong for the rest.
var scopedSEOFields = []struct{ Key, Label, Hint string }{
	{settings.KeyHeadRobots, "Robots directive",
		"What search engines are told to do with this site's pages."},
	{settings.KeyHeadKeywords, "Keywords",
		"Rarely used by search engines today; harmless and occasionally read by other tools."},
	{settings.KeyHeadVerifyGoogle, "Search Console verification",
		"Identifies THIS property. A token from another site verifies nothing here."},
	{settings.KeyHeadVerifyBing, "Bing verification",
		"As above — one token, one property."},
}

// handleOSScopedSEO renders one domain's search presence.
func (a *App) handleOSScopedSEO(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	sc := osScope(r)
	esc := html.EscapeString
	origin := seo.Origin(d.Host)

	body := `<div class="page-head"><div><h1 class="page-title">SEO</h1>` +
		`<p class="page-sub"><a href="/os/d/` + esc(d.ID) + `">← ` + esc(d.Host) + `</a></p></div></div>` +
		`<p class="page-sub">What search engines are told about <b>` + esc(d.Host) + `</b>.</p>`

	// What this domain currently declares, read from its own scope.
	body += `<div class="section-head"><div class="section-head__title">This site's head directives</div>` +
		`<div class="section-head__hint">Edited in this site's Theme Studio</div></div><div class="card">`
	for _, f := range scopedSEOFields {
		v := a.siteSettings.Get(r.Context(), sc, f.Key)
		shown := `<span class="muted">not set — using the product default</span>`
		if v != "" {
			shown = `<code class="mono">` + esc(v) + `</code>`
		}
		body += `<p class="text-sm"><b>` + esc(f.Label) + `</b> — ` + shown +
			`<br><span class="text-sm muted">` + esc(f.Hint) + `</span></p>`
	}
	body += `<div class="vm-row"><a class="btn btn--primary btn--sm" href="/os/d/` + esc(d.ID) +
		`/theme">Edit in Theme Studio</a></div></div>`

	// This domain's own live artefacts, linked so they can be checked rather
	// than asserted.
	body += `<div class="section-head"><div class="section-head__title">This site's live files</div>` +
		`<div class="section-head__hint">Served per host — open them to see what a crawler sees</div></div>` +
		`<div class="card"><div class="vm-row">` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(origin) + `/sitemap.xml" target="_blank" rel="noopener noreferrer">sitemap.xml ↗</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(origin) + `/robots.txt" target="_blank" rel="noopener noreferrer">robots.txt ↗</a>` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(origin) + `/feed.xml" target="_blank" rel="noopener noreferrer">feed.xml ↗</a>` +
		`</div><p class="text-sm muted">These are generated for this hostname from this domain's own posts.</p></div>`

	// The honest limit, on the page rather than in a commit message.
	body += `<div class="section-head"><div class="section-head__title">What is still install-level</div>` +
		`<div class="section-head__hint">Not per-domain, and not claimed to be</div></div>` +
		`<div class="card"><p class="text-sm muted">The <a href="/os/seo">SEO health report</a> checks cached ` +
		`artefacts and the install's canonical hostname, so it describes the primary site. It is not shown ` +
		`here rather than shown here mislabelled: a freshness check reported under this domain's name, ` +
		`measuring another site's files, would be worse than no check at all.</p></div>`

	writeOSHTML(w, r, adminOSLayout(nonce, "SEO · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}
