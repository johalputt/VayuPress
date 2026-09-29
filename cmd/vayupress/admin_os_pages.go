// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_pages.go — VayuOS "Pages" surface (Tumblr-style "Add a page").
//
// A custom page is a standalone article flagged is_page=1: it renders through
// the same article pipeline but without post chrome (date / tags / related /
// comments / author box), so it is ideal for About, Contact, Privacy, etc.
// Pages are managed here, separate from the blog feed (which excludes is_page
// rows). Creating a page seeds an empty draft and drops the operator straight
// into the editor; the "Show in navigation" toggle adds or removes the page's
// link in the public menu (settings key nav.items) entirely client-side via the
// shared /os/api/settings endpoint.
//
// CSP posture matches the rest of VayuOS: no inline styles, the only inline
// <script> carries the per-request nonce, and every dynamic string is escaped.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// handleOSPages lists every custom page (articles flagged is_page=1) as the
// List kind: the pages as a table, and the selected one's inspector, where it is
// put in the site's menu or footer and deleted. There are few pages, so every
// inspector is on the page and a row only shows its own. The current nav.items
// and footer JSON are embedded so those controls save through the shared
// settings endpoint.
func (a *App) handleOSPages(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// CSRF token cookie so the create sheet and the inspector's controls can POST.
	csrfTokenFor(w, r)

	navJSON, footerJSON := "", ""
	if a.siteSettings != nil {
		navJSON = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyNavItems)
		footerJSON = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyFooterConfig)
	}
	var footerCfg render.FooterConfig
	if strings.TrimSpace(footerJSON) != "" {
		_ = json.Unmarshal([]byte(footerJSON), &footerCfg)
	}

	type pageRow struct {
		Title, Slug, Status string
		Updated             time.Time
	}
	var pages []pageRow
	if dbpkg.DB != nil {
		// is_page is NOT NULL DEFAULT 0 (migration 045): `is_page=1` uses
		// idx_articles_is_page and reads only the (few) page rows. The previous
		// `COALESCE(is_page,0)=1` with no LIMIT scanned the whole catalog on the
		// writer connection — a 502-class stall once the catalog is large. Read
		// pool + an explicit cap keep this O(pages), not O(catalog).
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT title,slug,status,updated_at FROM articles WHERE is_page=1 ORDER BY updated_at DESC LIMIT 1000`); err == nil {
			defer rows.Close() //nolint:errcheck
			for rows.Next() {
				var p pageRow
				if rows.Scan(&p.Title, &p.Slug, &p.Status, &p.Updated) == nil {
					pages = append(pages, p)
				}
			}
			_ = rows.Err()
		}
	}

	// A new page rises in a sheet from its button (rule 5). Enter in the title
	// or the button creates it, and the editor opens on it.
	create := ui.Sheet("page-new", "New page", ui.HTML(`<form data-page-create>
  <div class="field"><label class="field-label" for="page-compose-input">Title</label><input id="page-compose-input" class="input" type="text" placeholder="About" autocomplete="off" required></div>
  <div class="field"><label class="field-label" for="page-compose-template">Start from</label><select id="page-compose-template" class="select">
    <option value="blank">A blank page</option>
    <option value="about">About</option>
    <option value="contact">Contact, with the contact form</option>
    <option value="faq">Questions and answers</option>
  </select></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Create and edit</button><span id="page-compose-status" class="text-sm muted" role="status" aria-live="polite"></span></div>
</form>`))

	newBtn := ui.HTML(`<button type="button" class="btn btn--primary" data-sheet="page-new">` + saIcon("plus") + ` New page</button>`)
	sub := ui.HTML(`Standalone pages like About, Contact or Privacy: no date, tags or comments. The contact form's address is in <a href="/os/settings/writing">Settings › Writing</a>.`)
	var body string
	if len(pages) == 0 {
		body = string(ui.List(ui.ListPage{Title: "Pages", Actions: newBtn, Sub: sub},
			ui.Empty("doc", "No pages yet", "An About or Contact page renders cleanly, without a post's date and comments.",
				ui.HTML(`<button type="button" class="btn btn--primary" data-sheet="page-new">Create a page</button>`)), ""))
	} else {
		var rows, panels strings.Builder
		for i, p := range pages {
			esc := html.EscapeString(p.Slug)
			href := "/" + p.Slug
			state := ui.State("ok", "Published")
			if p.Status == "draft" {
				state = ui.State("neutral", "Draft")
			}
			sel, hidden := "false", " hidden"
			if i == 0 {
				sel, hidden = "true", ""
			}
			rows.WriteString(`<tr class="post-row" data-list-row data-list-panel="` + esc + `" tabindex="0" aria-selected="` + sel + `">` +
				`<td class="post-row__name">` + html.EscapeString(p.Title) + `</td>` +
				`<td>` + string(state) + `</td>` +
				`<td class="post-row__date">` + config.FormatSite(p.Updated, "2 Jan") + `</td></tr>`)
			view := ""
			if p.Status != "draft" {
				view = `<a class="btn btn--sm" href="/` + esc + `" target="_blank" rel="noopener">` + saIcon("eye") + ` View</a>`
			}
			panels.WriteString(`<div data-list-panel-id="` + esc + `"` + hidden + `>` +
				`<div class="sa-insp__title">` + html.EscapeString(p.Title) + `</div><div class="sa-insp__meta">/` + esc + `</div>` +
				`<div class="sa-insp__actions"><a class="btn btn--sm" href="/os/editor/` + esc + `">` + saIcon("pencil") + ` Edit</a>` + view + `</div>` +
				`<dl class="sa-insp__facts"><dt>State</dt><dd>` + string(state) + ` <span class="muted">` + config.FormatSite(p.Updated, "2 Jan, 15:04") + `</span></dd>` +
				`<dt>Menu</dt><dd><label class="cz-check"><input type="checkbox" class="toggle" role="switch" data-page-nav data-href="` + html.EscapeString(href) + `" data-label="` + html.EscapeString(p.Title) + `"> In the site's menu</label></dd>` +
				`<dt>Footer</dt><dd>` + pageFooterSelect(href, p.Title, footerCfg) + `</dd></dl>` +
				`<div class="sa-insp__actions"><button type="button" class="btn btn--sm btn--danger" data-page-delete data-slug="` + esc + `" data-title="` + html.EscapeString(p.Title) + `">Delete</button></div></div>`)
		}
		list := `<div class="table-wrap"><table class="table post-table"><thead><tr><th>Title</th><th class="sa-col--state">State</th><th class="sa-col--date">Updated</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div>`
		body = string(ui.List(ui.ListPage{Title: "Pages", Count: intToStr(len(pages)), Actions: newBtn, Sub: sub},
			ui.HTML(list), ui.HTML(panels.String()+`<p id="page-nav-status" class="text-sm muted" role="status" aria-live="polite"></p>`)))
	}

	body += string(create) + `<script nonce="` + nonce + `" src="/os/static/js/admin-os-pages.js?v=` + assetVer("js/admin-os-pages.js") + `"></script>
<span hidden id="page-nav-seed" data-nav="` + html.EscapeString(navJSON) + `" data-footer="` + html.EscapeString(footerJSON) + `"></span>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Pages", "pages", cfg, htmpl.HTML(body)))
}

// pageFooterSelect renders the per-page "Footer group" <select>: the page can be
// left out of the footer, placed in the bottom-bar legal links, or filed under
// any existing footer column — plus a default "Pages" group for first use. The
// option matching the page's current placement is pre-selected (server-side) so
// the control reflects live state without waiting on JS.
func pageFooterSelect(href, title string, cfg render.FooterConfig) string {
	// Current placement.
	current := ""
	for _, l := range cfg.Legal {
		if l.Href == href {
			current = "legal"
		}
	}
	for _, col := range cfg.Columns {
		for _, l := range col.Links {
			if l.Href == href {
				current = "col:" + col.Title
			}
		}
	}
	sel := func(v string) string {
		if v == current {
			return " selected"
		}
		return ""
	}
	opts := `<option value=""` + sel("") + `>Not in footer</option>`
	opts += `<option value="legal"` + sel("legal") + `>Bottom bar</option>`
	hasPages := false
	for _, col := range cfg.Columns {
		t := strings.TrimSpace(col.Title)
		if t == "" {
			continue
		}
		if t == "Pages" {
			hasPages = true
		}
		v := "col:" + t
		opts += `<option value="` + html.EscapeString(v) + `"` + sel(v) + `>` + html.EscapeString(t) + ` (column)</option>`
	}
	if !hasPages {
		opts += `<option value="col:Pages"` + sel("col:Pages") + `>Pages (new column)</option>`
	}
	return `<select class="input" data-page-footer data-href="` + html.EscapeString(href) +
		`" data-label="` + html.EscapeString(title) + `" aria-label="Footer placement for ` + html.EscapeString(title) + `">` + opts + `</select>`
}

// pageTemplateSeed returns starter HTML for a new page based on the chosen
// template. The markup uses only tags the UGC sanitiser keeps (headings,
// paragraphs, lists, links, emphasis), so it survives rendering and re-hydrates
// cleanly in the editor. "blank" (or anything unknown) seeds an empty document —
// a single space, which article validation requires and which renders to
// nothing. The operator edits everything afterward; these are just scaffolds.
func pageTemplateSeed(template string) string {
	switch strings.ToLower(strings.TrimSpace(template)) {
	case "about":
		return `<h2>About us</h2>
<p>Welcome! Tell your readers who you are, what you write about, and why it matters. A couple of short paragraphs is plenty to start.</p>
<p>You can mention your background, what readers can expect, and how often you publish.</p>
<h2>What we cover</h2>
<ul><li>Topic one</li><li>Topic two</li><li>Topic three</li></ul>`
	case "contact":
		return `<h2>Get in touch</h2>
<p>We'd love to hear from you. Fill in the form below and we'll get back to you soon.</p>
[[contact-form: Thanks for reaching out — we've received your message and usually reply within one business day.]]
<p>Prefer email? Reach us at <a href="mailto:hello@example.com">hello@example.com</a>.</p>`
	case "faq":
		return `<h2>Frequently asked questions</h2>
<h3>What is this site about?</h3>
<p>Answer the question here in a sentence or two.</p>
<h3>How often do you publish?</h3>
<p>Let readers know your cadence.</p>
<h3>How can I get updates?</h3>
<p>Point readers to your newsletter, feed, or social profiles.</p>`
	default:
		return " "
	}
}

// handleOSQuickCreatePage creates an empty draft page (article flagged is_page)
// from the Pages quick-create box and returns its slug so the client can open
// the editor. Mirrors handleOSQuickCreatePost, then sets is_page=1.
func (a *App) handleOSQuickCreatePage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    string `json:"title"`
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeAPIError(w, r, http.StatusBadRequest, "empty-title", "Title is required", "")
		return
	}
	slug := a.uniqueArticleSlug(r.Context(), title)
	// CreatePage sets is_page atomically through the write pipeline — no
	// follow-up UPDATE that could race the queued insert (the old pattern).
	if _, err := a.articles.CreatePage(r.Context(), title, slug, pageTemplateSeed(body.Template), nil); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"slug": slug})
}
