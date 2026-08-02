// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_mysite.go — "My site", the one console page an agency CLIENT owns
// (ADR-0152 Phase 2).
//
// A client is a paying customer of a studio that builds and hosts their site.
// They are not staff, they are not technical, and the promise sold to them is
// small and specific: your website, your mail, no maintenance. This page is the
// website half — their brand, and the plain facts about what is live.
//
// What it deliberately is NOT: an editor. A client cannot write posts, upload
// media, or reach the content model at all. `handleOSPostDelete` and
// `handleOSEditorSave` are install-wide destructive primitives with no
// per-record ownership check, so exposing authoring to an untrusted principal is
// gated behind work that has not been done (ADR-0152 open decision 5). Selling
// "edit your own pages" before that check exists would be selling a control the
// code cannot honour.
//
// Traffic is absent for the same reason and it is worth being explicit: the
// analytics table is keyed (day, path) with no domain dimension, so two client
// domains sharing /about have MERGED view counts. Showing a client that figure
// would be showing them another client's traffic and calling it theirs. The
// panel says so in those words rather than displaying a number with a footnote.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
)

// mySiteDomain resolves the domain the current session may administer through
// this page, or reports that there is none.
//
// A client is bound to exactly one domain and may only ever see that one. An
// operator has no binding — they manage every domain from /os/domains — so they
// are sent there rather than shown a page that would have to guess which site
// they meant.
func (a *App) mySiteDomain(r *http.Request) (domain.Domain, bool) {
	u := currentUser(r)
	if u == nil || a.domains == nil {
		return domain.Domain{}, false
	}
	scope, ok := clientScopeFor(u.Role, u.ClientDomainID)
	if !ok {
		return domain.Domain{}, false
	}
	d, err := a.domains.ByID(r.Context(), scope)
	if err != nil {
		return domain.Domain{}, false
	}
	// A disabled domain is a soft-deleted one. Its client keeps their login but
	// has nothing to administer, and saying so beats rendering a live-looking
	// page for a site that is not being served.
	if d.Status != domain.StatusActive || d.IsPrimary {
		return domain.Domain{}, false
	}
	return d, true
}

// handleOSMySite renders the client's own site page.
func (a *App) handleOSMySite(w http.ResponseWriter, r *http.Request) {
	d, ok := a.mySiteDomain(r)
	if !ok {
		// Operators manage all domains from the registry page; a client with no
		// usable binding has already been refused upstream in serveWithAccess.
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	cfg := a.getOSSettings(r.Context())
	nonce := render.CSPNonce(r)
	b, _ := d.Brand()

	used, granted := a.mailboxAllowanceUsage(r.Context(), d.Host)

	body := `<div class="page-header"><h1>My site</h1>` +
		`<div class="page-actions"><span id="mysite-status" role="status" aria-live="polite" class="text-sm muted"></span></div></div>` +
		`<p class="page-sub">Your website's name, description and colours. Changes go live immediately.</p>` +
		mySiteFactsGrid(d) +
		`<div class="section-head"><div class="section-head__title">Your branding</div>` +
		`<div class="section-head__hint">Leave a field empty to use the default</div></div>` +
		mySiteBrandCard(d, b) +
		mySiteMailboxCard(d, used, granted) +
		mySiteWhatsNotHere()

	full := adminOSShellHead(nonce, "My site", "mysite", cfg) + body +
		adminOSShellFoot(nonce, mySiteScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// mySiteFactsGrid answers "what is the state of my site?" at a glance, in words
// a non-technical owner reads without a glossary.
func mySiteFactsGrid(d domain.Domain) string {
	tls := "Not yet secured"
	tlsClass := " stat-card--warn"
	if d.TLSState == domain.TLSActive || d.TLSState == domain.TLSPrimary {
		tls = "Secured"
		tlsClass = ""
	}
	mail := "Not set up"
	if d.MailEnabled {
		mail = "Active"
	}
	return `<div class="stat-grid">` +
		`<div class="stat-card"><div class="stat-card__label">Your address</div>` +
		`<div class="stat-card__value">` + html.EscapeString(d.Host) + `</div></div>` +
		`<div class="stat-card` + tlsClass + `"><div class="stat-card__label">Secure connection</div>` +
		`<div class="stat-card__value">` + tls + `</div></div>` +
		`<div class="stat-card"><div class="stat-card__label">Email on your domain</div>` +
		`<div class="stat-card__value">` + mail + `</div></div>` +
		`<div class="stat-card"><div class="stat-card__label">Website</div>` +
		`<div class="stat-card__value">` + html.EscapeString(mySiteModeLabel(d)) + `</div></div>` +
		`</div>`
}

// mySiteModeLabel describes what the domain's root serves, without the internal
// mode vocabulary a client has no reason to learn.
func mySiteModeLabel(d domain.Domain) string {
	s, ok := d.Site()
	if !ok {
		return "Blog"
	}
	switch s.Mode {
	case "custom":
		return "Custom design"
	case "business", "business_subpath":
		return "Business site"
	default:
		return "Blog"
	}
}

// mySiteBrandCard is the only thing on this page a client can change.
func mySiteBrandCard(d domain.Domain, b domain.Brand) string {
	f := func(label, name, val, hint string) string {
		out := `<div class="field"><label class="field-label" for="mysite-` + name + `">` +
			html.EscapeString(label) + `</label>` +
			`<input class="field-input" id="mysite-` + name + `" name="` + name + `" type="text" value="` +
			html.EscapeString(val) + `">`
		if hint != "" {
			out += `<div class="field-hint">` + html.EscapeString(hint) + `</div>`
		}
		return out + `</div>`
	}
	return `<div class="card">` +
		`<div class="settings-block-title">How your site introduces itself</div>` +
		`<p class="text-sm muted">This is the name and description search engines and social ` +
		`previews show for ` + html.EscapeString(d.Host) + `.</p>` +
		f("Site name", "site_name", b.SiteName, "") +
		f("Tagline", "tagline", b.Tagline, "One short line under the name") +
		f("Description", "description", b.Description, "Up to about 160 characters") +
		f("Accent colour (light)", "accent_light", b.AccentLight, "A hex colour such as #2563eb") +
		f("Accent colour (dark)", "accent_dark", b.AccentDark, "") +
		`<button type="button" class="btn btn--primary btn--sm" id="mysite-save">Save changes</button>` +
		`</div>`
}

// mySiteMailboxCard shows the client how many branded mailboxes they have and
// how many they were given, and tells them how to get another.
//
// There is no "create" button and no request FORM. A form would be a new
// unauthenticated-ish write surface on the one page an untrusted principal can
// reach, to save an email nobody minds sending. The studio creates mailboxes;
// this says so and shows the number, which is what the client actually wants to
// know before they ask.
func mySiteMailboxCard(d domain.Domain, used, granted int) string {
	if !d.MailEnabled {
		return `<div class="section-head"><div class="section-head__title">Email</div></div>` +
			`<div class="card"><p class="text-sm muted">Email on ` + html.EscapeString(d.Host) +
			` is not set up yet. Ask us if you would like branded addresses.</p></div>`
	}
	state := fmt.Sprintf("%d of %d in use", used, granted)
	cls := ""
	if granted <= 0 {
		state = "None allocated yet"
		cls = " stat-card--warn"
	} else if used >= granted {
		state = fmt.Sprintf("All %d in use", granted)
		cls = " stat-card--warn"
	}
	return `<div class="section-head"><div class="section-head__title">Your mailboxes</div>` +
		`<div class="section-head__hint">Created for you on request</div></div>` +
		`<div class="stat-grid"><div class="stat-card` + cls + `">` +
		`<div class="stat-card__label">Branded addresses</div>` +
		`<div class="stat-card__value">` + html.EscapeString(state) + `</div></div></div>` +
		`<div class="card"><p class="text-sm muted">Mailboxes on ` + html.EscapeString(d.Host) +
		` are created by us — just ask and we will add one. You can change your own ` +
		`password and turn on a login code from your mailbox settings at any time.</p></div>`
}

// mySiteWhatsNotHere states plainly what this page does not do.
//
// A client who cannot find a control assumes it is hidden, asks the studio, and
// costs it the support call that the whole product is priced to avoid. Saying
// where the boundary is, and why, is cheaper than answering it thirty times.
func mySiteWhatsNotHere() string {
	return `<div class="section-head"><div class="section-head__title">Not on this page</div></div>` +
		`<div class="card"><p class="text-sm muted">` +
		`<strong>Website content and design</strong> are looked after for you — ask and it gets changed.<br>` +
		`<strong>Visitor numbers</strong> are on the <a href="/os/mysite/traffic">Visitors</a> page.<br>` +
		`<strong>New mailboxes</strong> are created on request.` +
		`</p></div>`
}

// mySiteScript posts the brand form. One inline script under the page nonce, no
// inline style attributes — the console's CSP admits neither otherwise.
const mySiteScript = `
(function(){
  var btn = document.getElementById('mysite-save');
  if (!btn) return;
  var status = document.getElementById('mysite-status');
  btn.addEventListener('click', function(){
    var ids = ['site_name','tagline','description','accent_light','accent_dark'];
    var body = {};
    ids.forEach(function(k){
      var el = document.getElementById('mysite-' + k);
      if (el) body[k] = el.value;
    });
    status.textContent = 'Saving…';
    fetch('/os/api/mysite/brand', {
      method: 'POST',
      headers: {'Content-Type':'application/json','X-CSRF-Token': (window.__vpCSRF || '')},
      body: JSON.stringify(body)
    }).then(function(res){
      status.textContent = res.ok ? 'Saved' : 'Could not save — please try again';
    }).catch(function(){ status.textContent = 'Could not save — please try again'; });
  });
})();
`

// handleOSMySiteBrand saves the client's branding.
//
// The domain is taken from the SESSION, never from the request body. A body that
// names a domain is refused rather than silently redirected to the caller's own
// scope: silent substitution turns an attempt to write someone else's site into
// a success message, which hides both the bug and the attempt.
func (a *App) handleOSMySiteBrand(w http.ResponseWriter, r *http.Request) {
	d, ok := a.mySiteDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "no-site", "This account does not administer a site.", "")
		return
	}
	var in struct {
		DomainID    string `json:"domain_id"`
		SiteName    string `json:"site_name"`
		Tagline     string `json:"tagline"`
		Description string `json:"description"`
		AccentLight string `json:"accent_light"`
		AccentDark  string `json:"accent_dark"`
		ThemeColor  string `json:"theme_color"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", err.Error(), "")
		return
	}
	if id := strings.TrimSpace(in.DomainID); id != "" && id != d.ID {
		writeAPIError(w, r, http.StatusForbidden, "wrong-domain",
			"That site is not yours to change.", "")
		return
	}
	b := domain.Brand{
		SiteName:    strings.TrimSpace(in.SiteName),
		Tagline:     strings.TrimSpace(in.Tagline),
		Description: strings.TrimSpace(in.Description),
		AccentLight: strings.TrimSpace(in.AccentLight),
		AccentDark:  strings.TrimSpace(in.AccentDark),
		ThemeColor:  strings.TrimSpace(in.ThemeColor),
	}
	// SetBrand merges into the existing config_json, so the domain's website
	// override (and anything else an operator has set beside it) survives a brand
	// save. That is not incidental: the whole-blob writer this replaced would have
	// let a client take their own site offline by editing their colours.
	if err := a.domains.SetBrand(r.Context(), d.ID, b); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "save-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "saved"})
}

// mailboxAllowanceUsage reports how many mailboxes exist on a hosted domain and
// how many the operator has granted it.
//
// granted of 0 means NONE have been granted, not "unlimited". A per-client
// allowance that defaults to unlimited is one the operator never chose, and the
// first client to notice would be the one filling the disk the other thirty
// share.
func (a *App) mailboxAllowanceUsage(ctx context.Context, host string) (used, granted int) {
	if a.domains == nil {
		return 0, 0
	}
	d, err := a.domains.Resolve(ctx, host)
	if err != nil || d.IsPrimary {
		return 0, 0
	}
	granted = d.Limits().Mailboxes
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if n, err := a.vayuMail.Accounts().CountForDomain(ctx, host); err == nil {
			used = n
		}
	}
	return used, granted
}

// mailboxAllowanceExceeded reports whether another mailbox on host would exceed
// the operator's grant, with the message the panel shows.
//
// It fails CLOSED on an unknown domain: a host the registry cannot resolve has
// no allowance, so it gets none. The alternative — treating an unresolvable
// domain as unmetered — would make the check depend on the registry being
// reachable, which is the wrong thing for a limit to depend on.
func (a *App) mailboxAllowanceExceeded(ctx context.Context, host string) (bool, string) {
	used, granted := a.mailboxAllowanceUsage(ctx, host)
	if granted <= 0 {
		return true, "No mailboxes have been allocated to " + host +
			" yet. Set an allowance for it under Domains first."
	}
	if used >= granted {
		return true, fmt.Sprintf("%s has used all %d of its allocated mailboxes. Raise its allowance under Domains to add another.", host, granted)
	}
	return false, ""
}

// handleOSMySiteTraffic renders a client's own visitor numbers.
//
// The figures come from analytics.ViewsForScope, which filters by domain in SQL
// rather than fetching everything and narrowing afterwards. That is not a
// performance choice: a filter applied after the fact is a filter somebody can
// forget, and the thing forgotten would be another client's traffic appearing in
// this one's report.
func (a *App) handleOSMySiteTraffic(w http.ResponseWriter, r *http.Request) {
	d, ok := a.mySiteDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	cfg := a.getOSSettings(r.Context())
	nonce := render.CSPNonce(r)

	var total int64
	var top []analytics.PathCount
	if a.analytics != nil {
		total, top, _ = a.analytics.ViewsForScope(r.Context(), d.ID, 30, 10)
	}

	body := `<div class="page-header"><h1>Visitors</h1></div>` +
		`<p class="page-sub">Page views on ` + html.EscapeString(d.Host) +
		` over the last 30 days. Counted without cookies, so nobody is tracked between visits.</p>` +
		`<div class="stat-grid"><div class="stat-card">` +
		`<div class="stat-card__label">Page views, last 30 days</div>` +
		`<div class="stat-card__value">` + strconv.FormatInt(total, 10) + `</div></div></div>` +
		mySiteTopPages(top) +
		`<div class="card"><p class="text-sm muted">` +
		`These are views of your pages only. Counting started when your site was added, ` +
		`so a site added recently will show a short history.` +
		`</p></div>`

	full := adminOSShellHead(nonce, "Visitors", "mysite", cfg) + body +
		adminOSShellFoot(nonce, "", pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// mySiteTopPages lists the busiest pages, or says plainly that there is nothing
// yet rather than rendering an empty table that reads like a fault.
func mySiteTopPages(top []analytics.PathCount) string {
	if len(top) == 0 {
		return `<div class="card"><p class="text-sm muted">No visits recorded yet.</p></div>`
	}
	out := `<div class="section-head"><div class="section-head__title">Most visited pages</div></div>` +
		`<div class="card"><table class="table"><thead><tr><th>Page</th><th>Views</th></tr></thead><tbody>`
	for _, p := range top {
		out += `<tr><td class="mono text-sm">` + html.EscapeString(p.Path) + `</td>` +
			`<td>` + strconv.FormatInt(p.Views, 10) + `</td></tr>`
	}
	return out + `</tbody></table></div>`
}
