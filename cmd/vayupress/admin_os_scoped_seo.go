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
	"strconv"
	"strings"

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

	body := `<div class="page-header"><h1>SEO</h1></div>` +
		`<p class="page-sub"><a href="/os/d/` + esc(d.ID) + `">← ` + esc(d.Host) + `</a></p>` +
		`<p class="page-sub">What search engines are told about <b>` + esc(d.Host) + `</b>.</p>`

	// Read this domain's own declared values first, so both the tiles and the
	// bands describe the same state rather than one being computed twice.
	declared := map[string]string{}
	for _, f := range scopedSEOFields {
		declared[f.Key] = a.siteSettings.Get(r.Context(), sc, f.Key)
	}
	body += scopedSEOBody(d.ID, origin, declared)

	writeOSHTML(w, r, adminOSLayout(nonce, "SEO · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// scopedSEOBody renders one domain's SEO state.
//
// Extracted from the handler, which built its markup inline against a request
// and a settings store — so the page could not be rendered in a test, and its
// restyling could not be checked. Same reason as the traffic page.
func scopedSEOBody(domainID, origin string, declared map[string]string) string {
	esc := html.EscapeString
	var b strings.Builder

	// ── Four tiles ────────────────────────────────────────────────────────────
	//
	// The question this page answers at a glance is "how much of this site's SEO
	// is actually set, and how much is still the product default?" — a count, not
	// a list, because the list is one band down.
	set := 0
	for _, f := range scopedSEOFields {
		if strings.TrimSpace(declared[f.Key]) != "" {
			set++
		}
	}
	total := len(scopedSEOFields)
	setTone := ""
	if set == 0 {
		setTone = "warn"
	}
	b.WriteString(`<div class="stat-grid">` +
		osStatTile("Directives set", strconv.Itoa(set)+" of "+strconv.Itoa(total), setTone) +
		osStatTile("Using defaults", strconv.Itoa(total-set), "") +
		osStatTile("Origin", strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"), "") +
		osStatTile("Scheme", schemeOf(origin), "") +
		`</div>`)

	// ── The bands, as collapsible details (house style §11) ──────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">What crawlers are told</span>` +
		`<span class="section-head__hint">This domain only</span></div>`)
	b.WriteString(`<div class="mon-stack">`)

	var head strings.Builder
	head.WriteString(`<div class="card">`)
	for _, f := range scopedSEOFields {
		shown := `<span class="muted">not set — using the product default</span>`
		if v := declared[f.Key]; v != "" {
			shown = `<code class="mono">` + esc(v) + `</code>`
		}
		head.WriteString(`<p class="text-sm"><b>` + esc(f.Label) + `</b> — ` + shown +
			`<br><span class="text-sm muted">` + esc(f.Hint) + `</span></p>`)
	}
	head.WriteString(`<div class="vm-row"><a class="btn btn--primary btn--sm" href="/os/d/` + esc(domainID) +
		`/theme">Edit in Theme Studio</a></div></div>`)
	headChip := `<span class="mon-chip mon-chip--off">all default</span>`
	if set > 0 {
		headChip = `<span class="mon-chip mon-chip--on">` + strconv.Itoa(set) + ` set</span>`
	}
	b.WriteString(monAcc("🏷", "This site's head directives", "Edited in this site's Theme Studio",
		headChip, true, head.String()))

	// This domain's own live artefacts, linked so they can be checked rather
	// than asserted.
	b.WriteString(monAcc("📄", "This site's live files",
		"Served per host — open them to see what a crawler sees",
		`<span class="mon-chip mon-chip--on">3 files</span>`, false,
		`<div class="card"><div class="vm-row">`+
			`<a class="btn btn--ghost btn--sm" href="`+esc(origin)+`/sitemap.xml" target="_blank" rel="noopener noreferrer">sitemap.xml ↗</a>`+
			`<a class="btn btn--ghost btn--sm" href="`+esc(origin)+`/robots.txt" target="_blank" rel="noopener noreferrer">robots.txt ↗</a>`+
			`<a class="btn btn--ghost btn--sm" href="`+esc(origin)+`/feed.xml" target="_blank" rel="noopener noreferrer">feed.xml ↗</a>`+
			`</div><p class="text-sm muted">These are generated for this hostname from this domain's own posts.</p></div>`))

	// The honest limit, on the page rather than in a commit message.
	b.WriteString(monAcc("🏛", "What is still install-level", "Not per-domain, and not claimed to be",
		`<span class="mon-chip mon-chip--off">by construction</span>`, false,
		`<div class="card"><p class="text-sm muted">The install-wide SEO health report checks cached `+
			`artefacts and the install's canonical hostname, so it describes the primary site. It is not shown `+
			`here rather than shown here mislabelled: a freshness check reported under this domain's name, `+
			`measuring another site's files, would be worse than no check at all.</p></div>`))

	b.WriteString(`</div>`) // mon-stack
	return b.String()
}

// schemeOf names the scheme an origin uses, so the tile states it rather than
// leaving an operator to read it out of a URL. An onion is served over http by
// design and that is not a fault to be toned as one.
func schemeOf(origin string) string {
	if strings.HasPrefix(origin, "http://") {
		return "http"
	}
	return "https"
}
