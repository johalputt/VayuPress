package main

// admin_os_domains.go — VayuOS "Domains" surface: the VayuDomains registry
// (Stage 1). It lists every hostname this binary serves, lets the operator add
// or remove secondary domains and choose what each one serves, and records a
// per-domain TLS state that later stages will provision automatically.
//
// Stage 1 scope is deliberately narrow: the registry is authoritative for host
// resolution, but content/mail/member scoping per domain ships in later stages.
// The page says so plainly so an operator is never surprised by what a
// newly-added domain does (and does not yet) serve.
//
// CSP posture matches the rest of VayuOS: no inline styles, the single inline
// <script> carries the per-request nonce, every dynamic string is escaped.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
)

// siteTypeOptions is the operator-facing catalogue of what "/" can serve for a
// domain, with a short description of the current support level.
var siteTypeOptions = []struct{ Value, Label, Note string }{
	{domain.SiteBlog, "Blog", "Serves the blog at / (the classic VayuPress site)."},
	{domain.SiteBusiness, "Business site", "Business site at /, blog at blog.<host>."},
	{domain.SiteBusinessSubpath, "Business + /blog", "Business site at /, blog at /blog."},
	{domain.SiteStatic, "Static bundle", "Reserved — served in a later stage."},
	{domain.SiteMailOnly, "Mail only", "No public site; branded mail only (later stage)."},
}

func siteTypeLabel(v string) string {
	for _, o := range siteTypeOptions {
		if o.Value == v {
			return o.Label
		}
	}
	return v
}

// handleOSDomains renders the domain registry management page.
func (a *App) handleOSDomains(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	var domains []domain.Domain
	if a.domains != nil {
		if list, err := a.domains.List(r.Context()); err == nil {
			domains = list
		}
	}

	// Per-domain article counts (VayuDomains Stage 2 — content ownership).
	counts := map[string]int{}
	if a.articles != nil {
		if c, err := a.articles.CountsByDomain(r.Context()); err == nil {
			counts = c
		}
	}

	// Per-domain mailbox counts (VayuDomains Stage 3a — mail-domain foundation).
	// Read-only reporting: mailboxes are keyed by full address, so the host is
	// derived. Delivery/auth stays untouched until Stage 3b.
	mailCounts := map[string]int{}
	mailOn := false
	if a.vayuMail != nil {
		mailOn = a.vayuMail.Config().Enabled
		if a.vayuMail.Accounts() != nil {
			if c, err := a.vayuMail.Accounts().CountsByHost(r.Context()); err == nil {
				mailCounts = c
			}
		}
	}

	// The host the operator is currently browsing from — surfaced so it is
	// obvious which registered domain served this very page.
	viewingHost := ""
	if d, ok := activeDomain(r); ok {
		viewingHost = d.Host
	}

	body := domainsHeader(len(domains), viewingHost) +
		domainsTable(domains, counts, mailCounts, mailOn) +
		domainsAssignForm(domains) +
		domainsAddForm() +
		domainsScript(nonce)

	writeOSHTML(w, adminOSLayout(nonce, "Domains", "domains", cfg, htmpl.HTML(body)))
}

func domainsHeader(n int, viewingHost string) string {
	sub := "One binary, one registry — every hostname this install answers on."
	if viewingHost != "" {
		sub += ` You are viewing from <strong>` + html.EscapeString(viewingHost) + `</strong>.`
	}
	count := "domain"
	if n != 1 {
		count = "domains"
	}
	return `<div class="page-head">
  <div>
    <h1 class="page-title">Domains</h1>
    <p class="page-sub">` + sub + `</p>
  </div>
  <div class="page-head__meta"><span class="pill">` + strconv.Itoa(n) + ` ` + count + `</span></div>
</div>
<div class="card card--info"><p class="text-sm">VayuDomains is rolling out in stages. The registry drives <strong>host resolution</strong>, and <strong>per-domain content</strong> (homepage, articles, tags, feeds, sitemap and search) is live — each domain serves only its own posts. <strong>Per-domain mail</strong> is being staged: this page now shows each domain's mail status and mailbox count, with isolated per-domain delivery and branded mail arriving next. Adding a domain registers it — point its DNS at this server and provision TLS before it serves traffic.</p></div>`
}

func domainsTable(domains []domain.Domain, counts, mailCounts map[string]int, mailOn bool) string {
	if len(domains) == 0 {
		return `<div class="card empty"><div class="empty-title">No domains registered yet</div>
<div class="empty-sub">The primary domain is seeded automatically once DOMAIN is configured. Add a secondary domain below.</div></div>`
	}
	var rows strings.Builder
	for _, d := range domains {
		badge := ""
		if d.IsPrimary {
			badge = ` <span class="pill pill--accent">Primary</span>`
		}
		statusPill := `<span class="pill pill--ok">Active</span>`
		if d.Status != domain.StatusActive {
			statusPill = `<span class="pill pill--muted">Disabled</span>`
		}
		tls := `<span class="pill pill--muted">` + html.EscapeString(tlsLabel(d.TLSState)) + `</span>`
		// Mail (VayuDomains Stage 3a): show whether the domain carries mail and how
		// many mailboxes it holds. The primary always carries mail when configured;
		// a secondary opts in via mail_enabled. Counts are derived read-only from the
		// account store — per-domain delivery/read isolation ships in Stage 3b.
		mail := mailCell(d, mailCounts[strings.ToLower(d.Host)], mailOn)
		// Content ownership (Stage 2): the primary owns the unassigned bucket ("").
		key := d.ID
		if d.IsPrimary {
			key = ""
		}
		content := strconv.Itoa(counts[key]) + " posts"

		// Actions: the primary row is read-only here (managed from Website
		// settings); secondary rows can be toggled and removed.
		actions := `<span class="text-xs muted">Managed in Website</span>`
		if !d.IsPrimary {
			toggleLabel := "Disable"
			toggleStatus := domain.StatusDisabled
			if d.Status != domain.StatusActive {
				toggleLabel = "Enable"
				toggleStatus = domain.StatusActive
			}
			actions = `<button type="button" class="btn btn--ghost btn--sm" data-dom-toggle data-id="` + html.EscapeString(d.ID) + `" data-status="` + toggleStatus + `">` + toggleLabel + `</button>
<button type="button" class="btn btn--ghost btn--sm" data-dom-delete data-id="` + html.EscapeString(d.ID) + `" data-host="` + html.EscapeString(d.Host) + `">Remove</button>`
		}

		rows.WriteString(`<tr data-dom-row>
  <td><strong>` + html.EscapeString(d.Host) + `</strong>` + badge + `</td>
  <td>` + html.EscapeString(siteTypeLabel(d.EffectiveSiteType())) + `</td>
  <td class="text-xs muted">` + content + `</td>
  <td>` + mail + `</td>
  <td>` + tls + `</td>
  <td>` + statusPill + `</td>
  <td class="text-right">` + actions + `</td>
</tr>`)
	}
	return `<div class="card"><div class="table-wrap"><table class="table">
  <thead><tr><th>Host</th><th>Serves</th><th>Content</th><th>Mail</th><th>TLS</th><th>Status</th><th></th></tr></thead>
  <tbody>` + rows.String() + `</tbody>
</table></div></div>`
}

// domainsAssignForm lets the operator move a post to a domain by slug. This is
// the write half of Stage 2 content ownership; the public site begins serving
// per-domain content once Stage 2b keys the render cache by domain.
func domainsAssignForm(domains []domain.Domain) string {
	var opts strings.Builder
	opts.WriteString(`<option value="">Primary domain (default)</option>`)
	for _, d := range domains {
		if d.IsPrimary {
			continue
		}
		opts.WriteString(`<option value="` + html.EscapeString(d.ID) + `">` + html.EscapeString(d.Host) + `</option>`)
	}
	return `<div class="card">
  <h2 class="card-title">Assign a post to a domain</h2>
  <p class="text-sm muted">Move a published post to a domain by its slug. Existing posts stay on the primary domain until reassigned. The public per-domain site turns on in the next stage; assignments made now take effect then.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">Post slug</span>
      <input type="text" id="dom-assign-slug" class="input" placeholder="my-post-slug" autocomplete="off" spellcheck="false"></label>
    <label class="field"><span class="field-label">Owner domain</span>
      <select id="dom-assign-domain" class="input">` + opts.String() + `</select></label>
  </div>
  <div class="vm-row" style="gap:.5rem;align-items:center">
    <button type="button" class="btn btn--primary" data-dom-assign>Assign post</button>
    <span id="dom-assign-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

// mailCell renders a domain's Mail column (VayuDomains Stage 3a). The primary
// carries the install's mail when the engine is enabled; a secondary opts in via
// mail_enabled, with per-domain delivery/read isolation arriving in Stage 3b. The
// mailbox count is derived read-only from the account store.
func mailCell(d domain.Domain, n int, mailOn bool) string {
	unit := "mailboxes"
	if n == 1 {
		unit = "mailbox"
	}
	count := ` <span class="text-xs muted">` + strconv.Itoa(n) + ` ` + unit + `</span>`
	switch {
	case d.IsPrimary:
		if !mailOn {
			return `<span class="text-xs muted">Not configured</span>`
		}
		return `<span class="pill pill--ok">Primary mail</span>` + count
	case d.MailEnabled:
		return `<span class="pill pill--muted">Enabled · Stage 3b</span>` + count
	default:
		return `<span class="text-xs muted">—</span>`
	}
}

func tlsLabel(state string) string {
	switch state {
	case domain.TLSPrimary:
		return "Primary cert"
	case domain.TLSActive:
		return "Active"
	case domain.TLSFailed:
		return "Failed"
	default:
		return "Pending"
	}
}

func domainsAddForm() string {
	var opts strings.Builder
	for _, o := range siteTypeOptions {
		opts.WriteString(`<option value="` + o.Value + `">` + html.EscapeString(o.Label) + `</option>`)
	}
	return `<div class="card">
  <h2 class="card-title">Add a domain</h2>
  <p class="text-sm muted">Register another hostname this install should answer on. It is added in a pending state until you point DNS here and provision a certificate.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">Host</span>
      <input type="text" id="dom-host" class="input" placeholder="example.com" autocomplete="off" spellcheck="false"></label>
    <label class="field"><span class="field-label">Serves</span>
      <select id="dom-type" class="input">` + opts.String() + `</select></label>
    <label class="field field--check"><input type="checkbox" id="dom-mail"> <span class="field-label">Enable branded mail (later stage)</span></label>
  </div>
  <div class="vm-row" style="gap:.5rem;align-items:center">
    <button type="button" class="btn btn--primary" data-dom-add>Add domain</button>
    <span id="dom-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

func domainsScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('dom-status');
function show(t){if(st)st.textContent=t;}
var addBtn=document.querySelector('[data-dom-add]');
if(addBtn)addBtn.addEventListener('click',function(){
  var host=(document.getElementById('dom-host').value||'').trim();
  if(!host){show('Enter a host.');return;}
  var type=document.getElementById('dom-type').value;
  var mail=document.getElementById('dom-mail').checked;
  addBtn.disabled=true;show('Adding…');
  fetch('/os/api/domains',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({host:host,site_type:type,mail_enabled:mail})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){location.reload();}else{addBtn.disabled=false;show((res.j&&res.j.message)||'Could not add domain');}})
    .catch(function(e){addBtn.disabled=false;show('Error: '+e);});
});
document.querySelectorAll('[data-dom-toggle]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;show('Saving…');
    fetch('/os/api/domains/'+encodeURIComponent(b.getAttribute('data-id'))+'/status',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({status:b.getAttribute('data-status')})})
      .then(function(r){if(r.ok){location.reload();}else{b.disabled=false;show('Could not update');}})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
  });
});
document.querySelectorAll('[data-dom-delete]').forEach(function(b){
  b.addEventListener('click',function(){
    if(!window.confirm('Remove '+b.getAttribute('data-host')+' from the registry? This cannot be undone.'))return;
    b.disabled=true;show('Removing…');
    fetch('/os/api/domains/'+encodeURIComponent(b.getAttribute('data-id')),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){if(r.ok){location.reload();}else{b.disabled=false;show('Could not remove');}})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
  });
});
var ast=document.getElementById('dom-assign-status');
var assignBtn=document.querySelector('[data-dom-assign]');
if(assignBtn)assignBtn.addEventListener('click',function(){
  var slug=(document.getElementById('dom-assign-slug').value||'').trim();
  if(!slug){if(ast)ast.textContent='Enter a post slug.';return;}
  var dom=document.getElementById('dom-assign-domain').value;
  assignBtn.disabled=true;if(ast)ast.textContent='Assigning…';
  fetch('/os/api/domains/assign',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({slug:slug,domain_id:dom})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){assignBtn.disabled=false;if(res.ok){if(ast)ast.textContent='Assigned ✓';setTimeout(function(){location.reload();},700);}else{if(ast)ast.textContent=(res.j&&res.j.message)||'Could not assign';}})
    .catch(function(e){assignBtn.disabled=false;if(ast)ast.textContent='Error: '+e;});
});
})();
</script>`
}

// handleOSDomainAssign moves a post to a domain (Stage 2 content ownership).
func (a *App) handleOSDomainAssign(w http.ResponseWriter, r *http.Request) {
	if a.articles == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "article service not initialised", "")
		return
	}
	var body struct {
		Slug     string `json:"slug"`
		DomainID string `json:"domain_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	body.Slug = strings.TrimSpace(body.Slug)
	body.DomainID = strings.TrimSpace(body.DomainID)
	// A non-empty target must be a real, registered secondary domain.
	if body.DomainID != "" && a.domains != nil {
		found := false
		if list, err := a.domains.List(r.Context()); err == nil {
			for _, d := range list {
				if d.ID == body.DomainID && !d.IsPrimary {
					found = true
					break
				}
			}
		}
		if !found {
			writeAPIError(w, r, http.StatusBadRequest, "unknown-domain", "target is not a registered secondary domain", "")
			return
		}
	}
	if err := a.articles.SetDomain(r.Context(), body.Slug, body.DomainID); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "assign-failed", err.Error(), "")
		return
	}
	// The post just moved between domains — lazily invalidate the public caches
	// so every domain's homepage re-renders on next request (Stage 2b). Reassigning
	// touches no search-indexed field, so the engine snapshot version is unchanged;
	// clear the per-domain client-index memo explicitly so search re-scopes too
	// (Stage 2c). The per-domain sitemap/feed self-heal within their freshness
	// window, so they need no explicit purge.
	render.CachePurgeAll()
	purgeDomainSearchIndex()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Session-friendly write APIs (CSRF-protected; operators hold a cookie) ---

// handleOSDomainCreate registers a new secondary domain.
func (a *App) handleOSDomainCreate(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	var body struct {
		Host        string `json:"host"`
		SiteType    string `json:"site_type"`
		MailEnabled bool   `json:"mail_enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	body.Host = strings.TrimSpace(body.Host)
	if body.SiteType == "" {
		body.SiteType = domain.SiteBlog
	}
	d, err := a.domains.Create(r.Context(), body.Host, body.SiteType, body.MailEnabled)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, d)
}

// handleOSDomainStatus enables or disables a secondary domain.
func (a *App) handleOSDomainStatus(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if err := a.domains.SetStatus(r.Context(), id, strings.TrimSpace(body.Status)); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "status-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSDomainDelete removes a secondary domain from the registry.
func (a *App) handleOSDomainDelete(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.domains.Delete(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "delete-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}
