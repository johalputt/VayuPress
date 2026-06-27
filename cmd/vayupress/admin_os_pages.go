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
	"context"
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleOSPages lists every custom page (articles flagged is_page=1) with a
// quick-create box, live-URL link, edit link, publish state and a "Show in nav"
// toggle. The current nav.items JSON is embedded so the toggle can add/remove
// the page link without a server round-trip beyond the shared settings save.
func (a *App) handleOSPages(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// CSRF token cookie so the inline create/nav controls can POST.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	navJSON, footerJSON, contactEmail := "", "", ""
	autoReply := true
	if a.siteSettings != nil {
		navJSON = a.siteSettings.Get(r.Context(), settings.KeyNavItems)
		footerJSON = a.siteSettings.Get(r.Context(), settings.KeyFooterConfig)
		contactEmail = a.siteSettings.Get(r.Context(), settings.KeyContactEmail)
		autoReply = a.siteSettings.Get(r.Context(), settings.KeyContactAutoReply) != "off"
	}
	autoReplyChecked := ""
	if autoReply {
		autoReplyChecked = " checked"
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
		if rows, err := dbpkg.DB.QueryContext(r.Context(),
			`SELECT title,slug,COALESCE(status,'published'),updated_at FROM articles WHERE is_page=1 ORDER BY updated_at DESC`); err == nil {
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

	create := `<div class="quick-compose" role="search">
  <span class="quick-compose-icon" aria-hidden="true">📄</span>
  <input id="page-compose-input" class="quick-compose-input" type="text"
    placeholder="Add a page… type a title and press Enter" autocomplete="off"
    aria-label="Add a page: type a title and press Enter">
  <select id="page-compose-template" class="input" aria-label="Page template" title="Start from a template">
    <option value="blank">Blank page</option>
    <option value="about">About</option>
    <option value="contact">Contact</option>
    <option value="faq">FAQ</option>
  </select>
</div>
<div id="page-compose-status" class="text-sm muted" role="status" aria-live="polite">Pick a template, type a title, press Enter.</div>
<div class="card mt-3">
  <div class="theme-field theme-field--text">
    <span class="theme-field__label">Contact form recipient</span>
    <div class="vm-row">
      <input type="email" id="contact-email" class="input" style="flex:1" value="` + html.EscapeString(contactEmail) + `" placeholder="you@example.com" aria-label="Contact form recipient email">
      <button type="button" class="btn btn--primary btn--sm" id="contact-email-save">Save</button>
      <span id="contact-email-status" class="text-xs muted" role="status" aria-live="polite"></span>
    </div>
    <span class="theme-field__hint">Messages from any page containing the contact form are emailed here via VayuMail. Add the form to a page with the Contact template (or type <code>[[contact-form]]</code> in the page body). For a custom confirmation on a specific page, use <code>[[contact-form: your thank-you message]]</code>.</span>
    <div class="vm-row mt-2">
      <label class="cz-check"><input type="checkbox" id="contact-autoreply"` + autoReplyChecked + `> Send visitors an auto-reply confirmation</label>
      <span id="contact-autoreply-status" class="text-xs muted" role="status" aria-live="polite"></span>
    </div>
  </div>
</div>`

	var body string
	if len(pages) == 0 {
		body = `<div class="page-header"><h1>Pages</h1>
  <p class="text-sm muted">Standalone pages like About, Contact or Privacy — no date, tags or comments.</p></div>` +
			create + `
<div class="card empty-state">
  <div class="empty-icon">📄</div>
  <div class="empty-title">No pages yet</div>
  <div class="empty-sub">Create an About or Contact page above. Pages render cleanly, without the blog post furniture.</div>
</div>`
	} else {
		rows := ""
		for _, p := range pages {
			esc := html.EscapeString(p.Slug)
			statusPill := `<span class="status-pill status-pill--live">● Published</span>`
			viewBtn := `<a class="btn btn--ghost btn--sm" href="/` + esc + `" target="_blank" rel="noopener">View ↗</a>`
			if p.Status == "draft" {
				statusPill = `<span class="status-pill status-pill--draft">● Draft</span>`
				viewBtn = ""
			}
			href := "/" + esc
			rows += `<tr>
  <td class="row-title"><a href="/os/editor/` + esc + `">` + html.EscapeString(p.Title) + `</a>
    <div class="row-meta">/` + esc + `</div></td>
  <td>` + statusPill + `</td>
  <td><label class="cz-check"><input type="checkbox" data-page-nav data-href="` + html.EscapeString(href) + `" data-label="` + html.EscapeString(p.Title) + `"> In menu</label>
    <label class="theme-field theme-field--text mt-2"><span class="theme-field__label text-xs">Footer group</span>
      ` + pageFooterSelect(href, p.Title, footerCfg) + `</label></td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/editor/` + esc + `">Edit</a>
    ` + viewBtn + `
    <button type="button" class="btn btn--ghost btn--sm" data-page-delete data-slug="` + esc + `" data-title="` + html.EscapeString(p.Title) + `">Delete</button>
  </td>
</tr>`
		}
		body = `<div class="page-header"><h1>Pages <span class="count-pill">` + intToStr(len(pages)) + `</span></h1>
  <p class="text-sm muted">Standalone pages like About, Contact or Privacy — no date, tags or comments.</p></div>` +
			create + `
<div class="card">
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Title</th><th>Status</th><th>Show in</th><th></th></tr></thead>
    <tbody>` + rows + `</tbody>
  </table></div>
</div>
<div id="page-nav-status" class="text-sm muted" role="status" aria-live="polite"></div>`
	}

	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-pages.js"></script>
<span hidden id="page-nav-seed" data-nav="` + html.EscapeString(navJSON) + `" data-footer="` + html.EscapeString(footerJSON) + `"></span>`

	writeOSHTML(w, adminOSLayout(nonce, "Pages", "pages", cfg, htmpl.HTML(body)))
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
	if _, err := a.articles.Create(r.Context(), title, slug, pageTemplateSeed(body.Template), nil); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
		return
	}
	if err := setArticleIsPage(r.Context(), slug, true); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"slug": slug})
}

// setArticleIsPage flips the is_page flag for a slug. A targeted single-column
// UPDATE so it never races the queued content/title writer or clobbers the
// other publishing-options columns.
func setArticleIsPage(ctx context.Context, slug string, isPage bool) error {
	v := 0
	if isPage {
		v = 1
	}
	_, err := dbpkg.WDB.ExecContext(ctx, `UPDATE articles SET is_page=? WHERE slug=?`, v, slug)
	return err
}
