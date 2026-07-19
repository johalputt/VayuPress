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
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
)

// isPendingTorSite reports whether a host is a just-added Tor site still waiting
// for the parent to mint and assign its .onion (ADR-0141). Such rows are shown as
// "Minting .onion…" and drive the page's auto-refresh-while-pending.
func isPendingTorSite(host string) bool {
	return strings.HasPrefix(host, torSitePending) && strings.HasSuffix(host, ".local")
}

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

// toggleLabelFor / toggleStatusFor describe the enable/disable action for a
// secondary domain row: an active row offers Disable, a disabled row Enable.
func toggleLabelFor(d domain.Domain) string {
	if d.Status != domain.StatusActive {
		return "Enable"
	}
	return "Disable"
}

func toggleStatusFor(d domain.Domain) string {
	if d.Status != domain.StatusActive {
		return domain.StatusActive
	}
	return domain.StatusDisabled
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

	// Per-domain member counts (VayuDomains Stage 4 — member attribution). Keyed by
	// the registry domain id ("" = primary), like the article counts.
	memberCounts := map[string]int{}
	if a.members != nil {
		if c, err := a.members.CountsByDomain(r.Context()); err == nil {
			memberCounts = c
		}
	}

	// The host the operator is currently browsing from — surfaced so it is
	// obvious which registered domain served this very page.
	viewingHost := ""
	if d, ok := activeDomain(r); ok {
		viewingHost = d.Host
	}

	// In the Tor world (OnionMode) a domain is a ".onion" the operator can't type —
	// it is minted for them. So swap the clearnet "Add a domain" host form for the
	// one-click "Add Tor site" picker, and auto-refresh while any site is still
	// waiting for its onion to land.
	onion := config.Cfg.OnionMode
	addForm := domainsAddForm()
	pending := false
	if onion {
		addForm = torSitesAddForm()
		for _, d := range domains {
			if !d.IsPrimary && isPendingTorSite(d.Host) {
				pending = true
				break
			}
		}
	}

	body := domainsHeader(len(domains), viewingHost) +
		domainsTable(domains, counts, mailCounts, memberCounts, mailOn) +
		domainsBrandForm(domains, domainsBrandJSON(domains)) +
		domainsAssignForm(domains) +
		addForm +
		domainsScript(nonce) +
		torSitesScript(nonce, onion, pending)

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
<div class="card card--info"><p class="text-sm">VayuDomains is rolling out in stages. The registry drives <strong>host resolution</strong>, and <strong>per-domain content</strong> (homepage, articles, tags, feeds, sitemap and search) is live — each domain serves only its own posts. <strong>Per-domain mail</strong> is being staged: this page now shows each domain's mail status and mailbox count, with isolated per-domain delivery and branded mail arriving next. Adding a domain only <strong>registers</strong> it — nothing is provisioned automatically. When its DNS points here, press <strong>Sync now</strong> to approve it; the provisioning helper (run by deploy/update, or <code>sudo bash scripts/setup-vayudomain.sh</code>) then obtains its TLS certificate and nginx vhost. Domains on manual hold are never touched.</p></div>`
}

func domainsTable(domains []domain.Domain, counts, mailCounts, memberCounts map[string]int, mailOn bool) string {
	if len(domains) == 0 {
		return `<div class="card empty"><div class="empty-title">No domains registered yet</div>
<div class="empty-sub">The primary domain is seeded automatically once DOMAIN is configured. Add a secondary domain below.</div></div>`
	}
	var rows strings.Builder
	held := 0 // secondary domains parked on manual hold (for the bulk action)
	for _, d := range domains {
		if !d.IsPrimary && !d.IsSyncApproved() {
			held++
		}
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
		// Members attributed to this domain (VayuDomains Stage 4). Keyed the same
		// way as content: the primary owns the "" bucket.
		members := strconv.Itoa(memberCounts[key]) + " members"

		// Sync (P5 manual gate): the primary is provisioned outside the registry;
		// a secondary is either approved (helper provisions + maintains it) or on
		// manual hold (helper skips it until the operator presses Sync now).
		syncCell := `<span class="text-xs muted">—</span>`
		if !d.IsPrimary {
			if d.IsSyncApproved() {
				syncCell = `<span class="pill pill--ok">Synced</span>`
			} else {
				syncCell = `<span class="pill pill--muted">Manual hold</span>`
			}
		}

		// Actions: the primary row is read-only here (managed from Website
		// settings); secondary rows can be synced/held, toggled and removed.
		actions := `<span class="text-xs muted">Managed in Website</span>`
		if !d.IsPrimary {
			syncLabel, syncTarget := "Sync now", domain.SyncApproved
			if d.IsSyncApproved() {
				syncLabel, syncTarget = "Pause sync", domain.SyncHold
			}
			actions = `<button type="button" class="btn btn--ghost btn--sm" data-dom-sync data-id="` + html.EscapeString(d.ID) + `" data-sync="` + syncTarget + `">` + syncLabel + `</button>
<button type="button" class="btn btn--ghost btn--sm" data-dom-toggle data-id="` + html.EscapeString(d.ID) + `" data-status="` + toggleStatusFor(d) + `">` + toggleLabelFor(d) + `</button>
<button type="button" class="btn btn--ghost btn--sm" data-dom-delete data-id="` + html.EscapeString(d.ID) + `" data-host="` + html.EscapeString(d.Host) + `">Remove</button>`
		}

		// A just-added Tor site has a placeholder host until the parent mints its
		// .onion; show that plainly rather than the internal placeholder hostname.
		hostCell := `<strong>` + html.EscapeString(d.Host) + `</strong>`
		if !d.IsPrimary && isPendingTorSite(d.Host) {
			hostCell = `<span class="pill pill--muted">Minting .onion…</span>`
		}

		rows.WriteString(`<tr data-dom-row>
  <td>` + hostCell + badge + `</td>
  <td>` + html.EscapeString(siteTypeLabel(d.EffectiveSiteType())) + `</td>
  <td class="text-xs muted">` + content + `</td>
  <td class="text-xs muted">` + members + `</td>
  <td>` + mail + `</td>
  <td>` + syncCell + `</td>
  <td>` + tls + `</td>
  <td>` + statusPill + `</td>
  <td class="text-right">` + actions + `</td>
</tr>`)
	}
	// Bulk action: when one or more secondaries sit on manual hold, offer a
	// single "Sync all pending" that approves them together — the batch
	// counterpart to each row's "Sync now" (the helper still provisions
	// out-of-process; approving only adds them to its work list).
	bulk := ""
	if held > 0 {
		unit := "domains"
		if held == 1 {
			unit = "domain"
		}
		bulk = `<div class="vm-row" style="gap:.5rem;align-items:center;margin-top:.75rem">
  <button type="button" class="btn btn--primary btn--sm" data-dom-sync-all>Sync all pending (` + strconv.Itoa(held) + ` ` + unit + `)</button>
  <span id="dom-sync-all-status" class="text-sm muted" role="status" aria-live="polite"></span>
</div>`
	}
	return `<div class="card"><div class="table-wrap"><table class="table">
  <thead><tr><th>Host</th><th>Serves</th><th>Content</th><th>Members</th><th>Mail</th><th>Sync</th><th>TLS</th><th>Status</th><th></th></tr></thead>
  <tbody>` + rows.String() + `</tbody>
</table></div>` + bulk + `</div>`
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

// domBrandsCarrier renders the hidden element that carries the per-domain brand
// map to the page script. It uses html/template so the JSON payload is quoted by
// the template engine's context-aware auto-escaper — not by manual string
// concatenation — which is the safe, recognised way to embed a value in an HTML
// attribute (CWE-116): no brand value can break out of the attribute's quoting.
var domBrandsCarrier = htmpl.Must(htmpl.New("dom-brands").Parse(
	`<div id="dom-brands" data-brands="{{.}}" hidden></div>`))

// domainsBrandForm lets the operator give each secondary domain its own public
// identity — site name, tagline, description, accent colours and browser
// theme-colour — so it presents as its own site. Every field is optional: a
// blank field inherits the primary site's value, so a domain can re-brand just
// its name and keep the rest of the operator's design. The primary domain's
// identity is the global Website settings and is intentionally not editable here.
// The card is only rendered when a secondary domain exists (nothing to brand on a
// single-domain install).
func domainsBrandForm(domains []domain.Domain, brandJSON string) string {
	var opts strings.Builder
	secondaries := 0
	for _, d := range domains {
		if d.IsPrimary {
			continue
		}
		opts.WriteString(`<option value="` + html.EscapeString(d.ID) + `">` + html.EscapeString(d.Host) + `</option>`)
		secondaries++
	}
	if secondaries == 0 {
		return ""
	}
	// The per-domain brand map rides in an HTML data attribute and is JSON.parsed by
	// the page script, rather than being interpolated straight into the inline
	// <script>. html/template (domBrandsCarrier) owns the attribute quoting, so no
	// value can break out of it (CWE-116).
	var carrier strings.Builder
	_ = domBrandsCarrier.Execute(&carrier, brandJSON)
	return carrier.String() + `
<div class="card">
  <h2 class="card-title">Brand a domain</h2>
  <p class="text-sm muted">Give a secondary domain its own public identity so it presents as its own site. Every field is optional — leave one blank to inherit the primary site's value. Changes apply to that domain's homepage, articles and theme within a few seconds.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">Domain</span>
      <select id="dom-brand-domain" class="input">` + opts.String() + `</select></label>
    <label class="field"><span class="field-label">Site name</span>
      <input type="text" id="dom-brand-name" class="input" placeholder="Inherit primary" autocomplete="off"></label>
    <label class="field"><span class="field-label">Tagline</span>
      <input type="text" id="dom-brand-tagline" class="input" placeholder="Inherit primary" autocomplete="off"></label>
    <label class="field"><span class="field-label">Meta description</span>
      <input type="text" id="dom-brand-desc" class="input" placeholder="Inherit primary" autocomplete="off"></label>
    <label class="field"><span class="field-label">Accent · light (hex)</span>
      <input type="text" id="dom-brand-accent-light" class="input" placeholder="#2563eb" autocomplete="off" spellcheck="false"></label>
    <label class="field"><span class="field-label">Accent · dark (hex)</span>
      <input type="text" id="dom-brand-accent-dark" class="input" placeholder="#60a5fa" autocomplete="off" spellcheck="false"></label>
    <label class="field"><span class="field-label">Theme colour (hex)</span>
      <input type="text" id="dom-brand-theme" class="input" placeholder="#0f172a" autocomplete="off" spellcheck="false"></label>
  </div>
  <div class="vm-row" style="gap:.5rem;align-items:center">
    <button type="button" class="btn btn--primary" data-dom-brand-save>Save branding</button>
    <button type="button" class="btn btn--ghost" data-dom-brand-clear>Reset to primary</button>
    <span id="dom-brand-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

// domainsBrandJSON encodes each secondary domain's current brand as a JSON map
// (id → brand) so the page script can populate the branding form when a domain
// is selected. Primary domains carry no brand and are omitted.
func domainsBrandJSON(domains []domain.Domain) string {
	m := map[string]domain.Brand{}
	for _, d := range domains {
		if d.IsPrimary {
			continue
		}
		if b, ok := d.Brand(); ok {
			m[d.ID] = b
		} else {
			m[d.ID] = domain.Brand{}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(out)
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
  <p class="text-sm muted">Register another hostname this install should answer on. New domains start on <strong>manual hold</strong>: nothing is provisioned until you point DNS here and press <strong>Sync now</strong> on the domain's row.</p>
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

// torSitesAddForm is the one-click "Add Tor site" card, shown only in the Tor
// world (ADR-0141). There is no host to type — the operator picks what the site
// serves and the parent mints a fresh dedicated .onion for it. Anonymous mail
// (VayuMail·Tor) can be switched on so the new site also carries mailboxes.
func torSitesAddForm() string {
	var opts strings.Builder
	for _, o := range siteTypeOptions {
		opts.WriteString(`<option value="` + o.Value + `">` + html.EscapeString(o.Label) + `</option>`)
	}
	return `<div class="card">
  <h2 class="card-title">Add a Tor site</h2>
  <p class="text-sm muted">Spin up another anonymous site in one click. You don't pick a name — VayuPress mints a fresh <code>.onion</code> for it automatically. Choose what it serves, optionally turn on anonymous mail, and its <code>.onion</code> address appears in the table above within about a minute.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">Serves</span>
      <select id="tor-site-type" class="input">` + opts.String() + `</select></label>
    <label class="field field--check"><input type="checkbox" id="tor-site-mail"> <span class="field-label">Enable anonymous mail (VayuMail·Tor)</span></label>
  </div>
  <div class="vm-row" style="gap:.5rem;align-items:center">
    <button type="button" class="btn btn--primary" data-tor-site-add>Add Tor site</button>
    <span id="tor-site-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

// torSitesScript wires the "Add Tor site" button and, while any site is still
// waiting on its onion, auto-refreshes so the freshly-assigned .onion appears
// without a manual reload. Emitted only in the Tor world; the empty string
// otherwise keeps the clearnet console byte-identical.
func torSitesScript(nonce string, onion, pending bool) string {
	if !onion {
		return ""
	}
	// A pending site is waiting on the parent's tor engine (it reconciles about
	// once a minute); re-check periodically until every onion has landed.
	poll := ""
	if pending {
		poll = `setTimeout(function(){location.reload();},15000);`
	}
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('tor-site-status');
function show(t){if(st)st.textContent=t;}
var b=document.querySelector('[data-tor-site-add]');
if(b)b.addEventListener('click',function(){
  var typeEl=document.getElementById('tor-site-type');
  var mailEl=document.getElementById('tor-site-mail');
  var type=typeEl?typeEl.value:'blog';
  var mail=mailEl?mailEl.checked:false;
  b.disabled=true;show('Creating your Tor site…');
  fetch('/os/api/torworld/add-site',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({site_type:type,mail_enabled:mail})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};}).catch(function(){return {ok:r.ok,j:null};});})
    .then(function(res){if(res.ok){show('Site created — minting its .onion…');setTimeout(function(){location.reload();},900);}else{b.disabled=false;show((res.j&&res.j.error&&res.j.error.message)||'Could not add site');}})
    .catch(function(e){b.disabled=false;show('Error: '+e);});
});
` + poll + `
})();
</script>`
}

func domainsScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('dom-status');
function show(t){if(st)st.textContent=t;}
var BRANDS={};var _be=document.getElementById('dom-brands');
if(_be){try{BRANDS=JSON.parse(_be.getAttribute('data-brands')||'{}')||{};}catch(e){BRANDS={};}}
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
document.querySelectorAll('[data-dom-sync]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;show('Saving…');
    fetch('/os/api/domains/'+encodeURIComponent(b.getAttribute('data-id'))+'/sync',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({sync_state:b.getAttribute('data-sync')})})
      .then(function(r){if(r.ok){location.reload();}else{b.disabled=false;show('Could not update sync state');}})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
  });
});
var syncAllBtn=document.querySelector('[data-dom-sync-all]');
if(syncAllBtn)syncAllBtn.addEventListener('click',function(){
  var s=document.getElementById('dom-sync-all-status');
  syncAllBtn.disabled=true;if(s)s.textContent='Approving…';
  fetch('/os/api/domains/sync-all',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({sync_state:'approved'})})
    .then(function(r){if(r.ok){location.reload();}else{syncAllBtn.disabled=false;if(s)s.textContent='Could not approve pending domains';}})
    .catch(function(e){syncAllBtn.disabled=false;if(s)s.textContent='Error: '+e;});
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
// ── Per-domain branding ─────────────────────────────────────────────────────
var bSel=document.getElementById('dom-brand-domain');
var bName=document.getElementById('dom-brand-name'),bTag=document.getElementById('dom-brand-tagline'),
    bDesc=document.getElementById('dom-brand-desc'),bAL=document.getElementById('dom-brand-accent-light'),
    bAD=document.getElementById('dom-brand-accent-dark'),bTheme=document.getElementById('dom-brand-theme'),
    bSt=document.getElementById('dom-brand-status');
function bShow(t){if(bSt)bSt.textContent=t;}
function bFill(){if(!bSel)return;var b=BRANDS[bSel.value]||{};
  bName.value=b.site_name||'';bTag.value=b.tagline||'';bDesc.value=b.description||'';
  bAL.value=b.accent_light||'';bAD.value=b.accent_dark||'';bTheme.value=b.theme_color||'';bShow('');}
if(bSel){bSel.addEventListener('change',bFill);bFill();}
var bSave=document.querySelector('[data-dom-brand-save]');
if(bSave)bSave.addEventListener('click',function(){
  if(!bSel||!bSel.value){bShow('Select a domain.');return;}
  var payload={site_name:bName.value.trim(),tagline:bTag.value.trim(),description:bDesc.value.trim(),
    accent_light:bAL.value.trim(),accent_dark:bAD.value.trim(),theme_color:bTheme.value.trim()};
  bSave.disabled=true;bShow('Saving…');
  fetch('/os/api/domains/'+encodeURIComponent(bSel.value)+'/brand',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload)})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){bSave.disabled=false;if(res.ok){BRANDS[bSel.value]=res.j&&res.j.brand?res.j.brand:payload;bShow('Saved ✓');}else{bShow((res.j&&res.j.message)||'Could not save branding');}})
    .catch(function(e){bSave.disabled=false;bShow('Error: '+e);});
});
var bClear=document.querySelector('[data-dom-brand-clear]');
if(bClear)bClear.addEventListener('click',function(){
  if(!bSel||!bSel.value){bShow('Select a domain.');return;}
  if(!window.confirm('Reset this domain to inherit the primary site branding?'))return;
  bClear.disabled=true;bShow('Resetting…');
  fetch('/os/api/domains/'+encodeURIComponent(bSel.value)+'/brand',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){bClear.disabled=false;if(res.ok){BRANDS[bSel.value]={};bFill();bShow('Reset ✓');}else{bShow((res.j&&res.j.message)||'Could not reset');}})
    .catch(function(e){bClear.disabled=false;bShow('Error: '+e);});
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

// handleOSDomainSync approves or holds a secondary domain for out-of-process
// TLS+nginx provisioning (P5 manual sync gate). Approving does not provision
// anything by itself — it only adds the domain to the work list the privileged
// helper (scripts/setup-vayudomain.sh) reads on its next run; the page copy
// says so, keeping the surface truthful.
func (a *App) handleOSDomainSync(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		SyncState string `json:"sync_state"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if err := a.domains.SetSyncState(r.Context(), id, strings.TrimSpace(body.SyncState)); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "sync-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSDomainSyncAll flips every secondary domain's sync state in one call —
// the bulk counterpart to handleOSDomainSync behind the "Sync all pending"
// button. Like the per-row action it only records approval; provisioning still
// happens out-of-process on the helper's next run. Returns how many rows
// changed so the UI can report the batch result.
func (a *App) handleOSDomainSyncAll(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	var body struct {
		SyncState string `json:"sync_state"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	n, err := a.domains.SetAllSyncState(r.Context(), strings.TrimSpace(body.SyncState))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "sync-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "changed": n})
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

// handleOSDomainBrand stores a secondary domain's public branding overrides
// (VayuDomains per-domain branding). Colour fields are hex-validated before they
// can reach the domain's /theme.css or its <meta theme-color>, so no CSS or
// attribute injection is possible through the accent variables; text fields are
// length-capped. An empty payload clears the brand back to inheriting the
// primary site. Only secondary domains are brandable — the registry refuses the
// primary, whose identity is the global Website settings.
func (a *App) handleOSDomainBrand(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		SiteName    string `json:"site_name"`
		Tagline     string `json:"tagline"`
		Description string `json:"description"`
		AccentLight string `json:"accent_light"`
		AccentDark  string `json:"accent_dark"`
		ThemeColor  string `json:"theme_color"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if len(s) > n {
			s = strings.TrimSpace(s[:n])
		}
		return s
	}
	brand := domain.Brand{
		SiteName:    clip(body.SiteName, 120),
		Tagline:     clip(body.Tagline, 200),
		Description: clip(body.Description, 320),
		AccentLight: strings.TrimSpace(body.AccentLight),
		AccentDark:  strings.TrimSpace(body.AccentDark),
		ThemeColor:  strings.TrimSpace(body.ThemeColor),
	}
	// Colour fields are injected verbatim into the domain's /theme.css and its
	// <meta theme-color>, so a non-empty value MUST be a plain hex colour — this
	// closes any CSS/attribute injection through the accent variables.
	for _, c := range []struct{ name, val string }{
		{"Accent (light)", brand.AccentLight},
		{"Accent (dark)", brand.AccentDark},
		{"Theme colour", brand.ThemeColor},
	} {
		if c.val != "" && !hexColorRe.MatchString(c.val) {
			writeAPIError(w, r, http.StatusBadRequest, "bad-color", c.name+" must be a hex colour like #2563eb", "")
			return
		}
	}
	if err := a.domains.SetBrand(r.Context(), id, brand); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "brand-failed", err.Error(), "")
		return
	}
	// The domain's public identity changed: purge the public HTML caches so its
	// homepage and articles re-render with the new brand on the next request (the
	// same lazy purge the assign path uses). /theme.css is served live per request,
	// so its accent update needs no purge and takes effect immediately.
	render.CachePurgeAll()
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "brand": brand})
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
