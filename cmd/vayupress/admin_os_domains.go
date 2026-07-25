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
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
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

	csrfTokenFor(w, r)

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

	// The list is a premium, animated card grid; per-domain branding/content
	// editing moved to each site's own manager (/os/domains/{id}), surfaced from
	// the Optimize hub, so this page stays a clean add / list / remove surface.
	body := domainsHeader(len(domains), viewingHost) +
		domainsCards(domains, counts, mailCounts, memberCounts, mailOn) +
		addForm +
		domainsScript(nonce) +
		torSitesScript(nonce, onion, pending)

	writeOSHTML(w, r, adminOSLayout(nonce, "Domains", "domains", cfg, htmpl.HTML(body)))
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

// domainsCards renders the registry as a premium, animated card grid (replacing
// the Stage-1 table): each hostname is a card carrying its identity, live
// content/member/mail counts, sync/TLS/status pills and lifecycle actions. Each
// secondary card links to its own per-site manager (/os/domains/{id}), which is
// also surfaced from the Optimize hub, so the operator controls every part of a
// site from one place. Shared by both worlds — in the Tor world the hosts are
// .onion addresses and the same cards render unchanged.
func domainsCards(domains []domain.Domain, counts, mailCounts, memberCounts map[string]int, mailOn bool) string {
	if len(domains) == 0 {
		return `<div class="card empty"><div class="empty-title">No domains registered yet</div>
<div class="empty-sub">The primary domain is seeded automatically once DOMAIN is configured. Add a secondary domain below.</div></div>`
	}
	var cards strings.Builder
	held := 0 // secondary domains parked on manual hold (for the bulk action)
	for _, d := range domains {
		if !d.IsPrimary && !d.IsSyncApproved() {
			held++
		}
		// Content/member counts are keyed by domain id; the primary owns "".
		key := d.ID
		if d.IsPrimary {
			key = ""
		}

		cardCls := "domain-card"
		badge := ""
		if d.IsPrimary {
			cardCls += " domain-card--primary"
			badge = ` <span class="pill pill--accent">Primary</span>`
		}

		// A just-added Tor site has a placeholder host until the parent mints its
		// .onion; show that plainly rather than the internal placeholder hostname.
		hostText := `<span class="domain-card__host">` + html.EscapeString(d.Host) + badge + `</span>`
		if !d.IsPrimary && isPendingTorSite(d.Host) {
			hostText = `<span class="domain-card__host"><span class="pill pill--muted">Minting .onion…</span></span>`
		}

		statusPill := `<span class="pill pill--ok">Active</span>`
		if d.Status != domain.StatusActive {
			statusPill = `<span class="pill pill--muted">Disabled</span>`
		}
		tlsPill := `<span class="pill pill--muted">` + html.EscapeString(tlsLabel(d.TLSState)) + `</span>`
		// Sync (P5 manual gate): the primary is provisioned outside the registry.
		syncPill := ""
		if !d.IsPrimary {
			if d.IsSyncApproved() {
				syncPill = `<span class="pill pill--ok">Synced</span>`
			} else {
				syncPill = `<span class="pill pill--muted">Manual hold</span>`
			}
		}

		// Stat chips: content, members and mail (all derived read-only).
		stats := `<span class="domain-stat"><b>` + strconv.Itoa(counts[key]) + `</b> posts</span>` +
			`<span class="domain-stat"><b>` + strconv.Itoa(memberCounts[key]) + `</b> members</span>` +
			domainMailStat(d, mailCounts[strings.ToLower(d.Host)], mailOn)

		// Actions: the primary is managed from Website settings; secondary sites get
		// Manage (per-site editor), Sync, enable/disable and Remove.
		var actions string
		if d.IsPrimary {
			actions = `<a class="btn btn--ghost btn--sm" href="/os/website">Manage in Website</a>`
		} else {
			syncLabel, syncTarget := "Sync now", domain.SyncApproved
			if d.IsSyncApproved() {
				syncLabel, syncTarget = "Pause sync", domain.SyncHold
			}
			actions = `<a class="btn btn--primary btn--sm" href="/os/domains/` + html.EscapeString(d.ID) + `">Manage</a>` +
				`<button type="button" class="btn btn--ghost btn--sm" data-dom-sync data-id="` + html.EscapeString(d.ID) + `" data-sync="` + syncTarget + `">` + syncLabel + `</button>` +
				`<button type="button" class="btn btn--ghost btn--sm" data-dom-toggle data-id="` + html.EscapeString(d.ID) + `" data-status="` + toggleStatusFor(d) + `">` + toggleLabelFor(d) + `</button>` +
				`<button type="button" class="btn btn--danger btn--sm" data-dom-delete data-id="` + html.EscapeString(d.ID) + `" data-host="` + html.EscapeString(d.Host) + `">Remove</button>`
		}

		cards.WriteString(`<div class="` + cardCls + `" data-dom-row>
  <div class="domain-card__head">
    <span class="domain-card__icon">` + iconDomains + `</span>
    <span class="domain-card__id">` + hostText + `
      <span class="domain-card__serves">` + html.EscapeString(siteTypeLabel(d.EffectiveSiteType())) + `</span>
    </span>
    ` + statusPill + `
  </div>
  <div class="domain-card__stats">` + stats + `</div>
  <div class="domain-card__meta">` + syncPill + tlsPill + `</div>
  <div class="domain-card__actions">` + actions + `</div>
</div>`)
	}
	// Bulk action: when one or more secondaries sit on manual hold, offer a single
	// "Sync all pending" that approves them together — the batch counterpart to
	// each card's "Sync now" (the helper still provisions out-of-process).
	bulk := ""
	if held > 0 {
		unit := "domains"
		if held == 1 {
			unit = "domain"
		}
		bulk = `<div class="vm-row mb-6">
  <button type="button" class="btn btn--primary btn--sm" data-dom-sync-all>Sync all pending (` + strconv.Itoa(held) + ` ` + unit + `)</button>
  <span id="dom-sync-all-status" class="text-sm muted" role="status" aria-live="polite"></span>
</div>`
	}
	return bulk + `<div class="domain-grid">` + cards.String() + `</div>`
}

// domainMailStat renders the mail chip for a domain card — the compact,
// card-friendly counterpart to mailCell. The primary carries the install's mail
// when the engine is on; a secondary opts in via mail_enabled; otherwise the
// chip reads "no mail". The mailbox count is derived read-only from the account
// store (VayuDomains Stage 3a).
func domainMailStat(d domain.Domain, n int, mailOn bool) string {
	switch {
	case d.IsPrimary:
		if !mailOn {
			return `<span class="domain-stat">no mail</span>`
		}
		return `<span class="domain-stat"><b>` + strconv.Itoa(n) + `</b> mailboxes</span>`
	case d.MailEnabled:
		return `<span class="domain-stat"><b>` + strconv.Itoa(n) + `</b> mailboxes</span>`
	default:
		return `<span class="domain-stat">no mail</span>`
	}
}

// handleOSDomainManage renders the per-site manager for one secondary domain —
// the "control every part of this site" surface reached from its card and from
// the Optimize hub's "Your websites" row. It carries the site's identity and
// live counts, a scoped branding editor (its own name / tagline / colours), a
// post-assignment box, lifecycle controls (sync / enable / remove) and shortcuts
// into Theme Studio and Website. Shared by both worlds. The primary site's
// identity is the global Website settings, so it is redirected there; an unknown
// id falls back to the registry list.
func (a *App) handleOSDomainManage(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	id := chi.URLParam(r, "id")

	csrfTokenFor(w, r)

	var found *domain.Domain
	if a.domains != nil {
		if list, err := a.domains.List(r.Context()); err == nil {
			for i := range list {
				if list[i].ID == id {
					d := list[i]
					found = &d
					break
				}
			}
		}
	}
	if found == nil {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	if found.IsPrimary {
		http.Redirect(w, r, "/os/website", http.StatusSeeOther)
		return
	}

	// Per-domain counts — the same read-only sources the registry list uses.
	posts, members, mailboxes := 0, 0, 0
	if a.articles != nil {
		if c, err := a.articles.CountsByDomain(r.Context()); err == nil {
			posts = c[found.ID]
		}
	}
	if a.members != nil {
		if c, err := a.members.CountsByDomain(r.Context()); err == nil {
			members = c[found.ID]
		}
	}
	mailOn := false
	if a.vayuMail != nil {
		mailOn = a.vayuMail.Config().Enabled
		if a.vayuMail.Accounts() != nil {
			if c, err := a.vayuMail.Accounts().CountsByHost(r.Context()); err == nil {
				mailboxes = c[strings.ToLower(found.Host)]
			}
		}
	}

	body := domainManagePage(*found, posts, members, mailboxes, mailOn) + domainManageScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, "Manage · "+found.Host, "optimize", cfg, htmpl.HTML(body)))
}

// domainManagePage builds the per-site manager body for a secondary domain: a
// hero with identity + live-view link, stat chips, lifecycle controls, a scoped
// branding editor prefilled from the domain's current brand, a post-assignment
// box and shortcuts into Theme Studio / Website / Analytics.
func domainManagePage(d domain.Domain, posts, members, mailboxes int, mailOn bool) string {
	esc := html.EscapeString
	b, _ := d.Brand()
	pending := isPendingTorSite(d.Host)

	statusPill := `<span class="pill pill--ok">Active</span>`
	if d.Status != domain.StatusActive {
		statusPill = `<span class="pill pill--muted">Disabled</span>`
	}
	syncPill := `<span class="pill pill--ok">Synced</span>`
	if !d.IsSyncApproved() {
		syncPill = `<span class="pill pill--muted">Manual hold</span>`
	}
	tlsPill := `<span class="pill pill--muted">` + esc(tlsLabel(d.TLSState)) + `</span>`

	hostShown := esc(d.Host)
	viewLink := `<a class="btn btn--ghost btn--sm" href="` + esc(seo.Origin(d.Host)) + `" target="_blank" rel="noopener noreferrer">View site ↗</a>`
	if pending {
		hostShown = "Minting .onion…"
		viewLink = ""
	}

	stats := `<span class="domain-stat"><b>` + strconv.Itoa(posts) + `</b> posts</span>` +
		`<span class="domain-stat"><b>` + strconv.Itoa(members) + `</b> members</span>` +
		domainMailStat(d, mailboxes, mailOn)

	syncLabel, syncTarget := "Sync now", domain.SyncApproved
	if d.IsSyncApproved() {
		syncLabel, syncTarget = "Pause sync", domain.SyncHold
	}

	// A prefilled text input for the branding editor.
	input := func(id, label, ph, val string) string {
		return `<label class="field"><span class="field-label">` + label + `</span>
      <input type="text" id="` + id + `" class="input" placeholder="` + ph + `" value="` + esc(val) + `" autocomplete="off"></label>`
	}

	branding := `<div class="card">
  <h2 class="card-title">Branding</h2>
  <p class="text-sm muted">Give this site its own public identity. Every field is optional — leave one blank to inherit the primary site's value. Changes apply to this domain's homepage, articles and theme within a few seconds.</p>
  <div class="form-grid">
    ` + input("site-name", "Site name", "Inherit primary", b.SiteName) + `
    ` + input("site-tagline", "Tagline", "Inherit primary", b.Tagline) + `
    ` + input("site-desc", "Meta description", "Inherit primary", b.Description) + `
    ` + input("site-accent-light", "Accent · light (hex)", "#2563eb", b.AccentLight) + `
    ` + input("site-accent-dark", "Accent · dark (hex)", "#60a5fa", b.AccentDark) + `
    ` + input("site-theme", "Theme colour (hex)", "#0f172a", b.ThemeColor) + `
  </div>
  <div class="vm-row">
    <button type="button" class="btn btn--primary" data-site-brand-save>Save branding</button>
    <button type="button" class="btn btn--ghost" data-site-brand-clear>Reset to primary</button>
    <span id="site-brand-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`

	assign := `<div class="card">
  <h2 class="card-title">Content</h2>
  <p class="text-sm muted">Move a published post to this site by its slug. Existing posts stay on their current domain until reassigned.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">Post slug</span>
      <input type="text" id="site-assign-slug" class="input" placeholder="my-post-slug" autocomplete="off" spellcheck="false"></label>
  </div>
  <div class="vm-row">
    <button type="button" class="btn btn--primary" data-site-assign>Assign to this site</button>
    <span id="site-assign-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`

	shortcuts := `<div class="card">
  <h2 class="card-title">Design &amp; more</h2>
  <p class="text-sm muted">Deeper editing lives in the shared tools — they apply per domain once this site is selected.</p>
  <div class="vm-row">
    <a class="btn btn--ghost btn--sm" href="/os/theme">Theme Studio</a>
    <a class="btn btn--ghost btn--sm" href="/os/website">Website settings</a>
    <a class="btn btn--ghost btn--sm" href="/os/analytics">Analytics</a>
    <a class="btn btn--ghost btn--sm" href="/os/seo">SEO</a>
  </div>
</div>`

	lifecycle := `<div class="card">
  <h2 class="card-title">Lifecycle</h2>
  <p class="text-sm muted">Approve this site for TLS + nginx provisioning, take it offline, or remove it from the registry.</p>
  <div class="vm-row">
    <button type="button" class="btn btn--ghost btn--sm" data-site-sync data-sync="` + syncTarget + `">` + syncLabel + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-site-toggle data-status="` + toggleStatusFor(d) + `">` + toggleLabelFor(d) + `</button>
    <button type="button" class="btn btn--danger btn--sm" data-site-delete data-host="` + esc(d.Host) + `">Remove site</button>
    <span id="site-life-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`

	return `<div id="dom-manage" data-id="` + esc(d.ID) + `" hidden></div>
<div class="page-head">
  <div>
    <h1 class="page-title">Manage site</h1>
    <p class="page-sub"><a href="/os/domains">← All domains</a></p>
  </div>
</div>
<div class="site-hero">
  <span class="site-hero__icon">` + iconDomains + `</span>
  <span class="site-hero__body">
    <span class="site-hero__host">` + hostShown + `</span>
    <span class="site-hero__sub">` + esc(siteTypeLabel(d.EffectiveSiteType())) + statusPill + syncPill + tlsPill + `</span>
  </span>
  <span class="site-hero__actions">` + viewLink + `</span>
</div>
<div class="domain-card__stats mb-6">` + stats + `</div>
` + branding + assign + shortcuts + lifecycle
}

// domainManageScript wires the per-site manager: a scoped branding save/reset, a
// post-assignment box and the lifecycle controls. It reads the domain id from a
// hidden data node (CSP-safe) and mirrors the vp_csrf cookie into X-CSRF-Token,
// exactly like the registry page's script.
func domainManageScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var node=document.getElementById('dom-manage');
var ID=node?node.getAttribute('data-id'):'';
if(!ID)return;
function val(id){var e=document.getElementById(id);return e?e.value.trim():'';}
function set(id,t){var e=document.getElementById(id);if(e)e.textContent=t;}
// Branding save
var bSave=document.querySelector('[data-site-brand-save]');
if(bSave)bSave.addEventListener('click',function(){
  var payload={site_name:val('site-name'),tagline:val('site-tagline'),description:val('site-desc'),
    accent_light:val('site-accent-light'),accent_dark:val('site-accent-dark'),theme_color:val('site-theme')};
  bSave.disabled=true;set('site-brand-status','Saving…');
  fetch('/os/api/domains/'+encodeURIComponent(ID)+'/brand',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload)})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){bSave.disabled=false;set('site-brand-status',res.ok?'Saved ✓':((res.j&&res.j.message)||'Could not save branding'));})
    .catch(function(e){bSave.disabled=false;set('site-brand-status','Error: '+e);});
});
// Branding reset
var bClear=document.querySelector('[data-site-brand-clear]');
if(bClear)bClear.addEventListener('click',function(){
  if(!window.confirm('Reset this site to inherit the primary branding?'))return;
  bClear.disabled=true;set('site-brand-status','Resetting…');
  fetch('/os/api/domains/'+encodeURIComponent(ID)+'/brand',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({})})
    .then(function(r){if(r.ok){location.reload();}else{bClear.disabled=false;set('site-brand-status','Could not reset');}})
    .catch(function(e){bClear.disabled=false;set('site-brand-status','Error: '+e);});
});
// Assign a post to this site
var aBtn=document.querySelector('[data-site-assign]');
if(aBtn)aBtn.addEventListener('click',function(){
  var slug=val('site-assign-slug');
  if(!slug){set('site-assign-status','Enter a post slug.');return;}
  aBtn.disabled=true;set('site-assign-status','Assigning…');
  fetch('/os/api/domains/assign',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({slug:slug,domain_id:ID})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){aBtn.disabled=false;set('site-assign-status',res.ok?'Assigned ✓':((res.j&&res.j.message)||'Could not assign'));})
    .catch(function(e){aBtn.disabled=false;set('site-assign-status','Error: '+e);});
});
// Lifecycle: sync
var sBtn=document.querySelector('[data-site-sync]');
if(sBtn)sBtn.addEventListener('click',function(){
  sBtn.disabled=true;set('site-life-status','Saving…');
  fetch('/os/api/domains/'+encodeURIComponent(ID)+'/sync',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({sync_state:sBtn.getAttribute('data-sync')})})
    .then(function(r){if(r.ok){location.reload();}else{sBtn.disabled=false;set('site-life-status','Could not update');}})
    .catch(function(e){sBtn.disabled=false;set('site-life-status','Error: '+e);});
});
// Lifecycle: enable/disable
var tBtn=document.querySelector('[data-site-toggle]');
if(tBtn)tBtn.addEventListener('click',function(){
  tBtn.disabled=true;set('site-life-status','Saving…');
  fetch('/os/api/domains/'+encodeURIComponent(ID)+'/status',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({status:tBtn.getAttribute('data-status')})})
    .then(function(r){if(r.ok){location.reload();}else{tBtn.disabled=false;set('site-life-status','Could not update');}})
    .catch(function(e){tBtn.disabled=false;set('site-life-status','Error: '+e);});
});
// Lifecycle: remove → back to the registry
var dBtn=document.querySelector('[data-site-delete]');
if(dBtn)dBtn.addEventListener('click',function(){
  if(!window.confirm('Remove '+dBtn.getAttribute('data-host')+' from the registry? This cannot be undone.'))return;
  dBtn.disabled=true;set('site-life-status','Removing…');
  fetch('/os/api/domains/'+encodeURIComponent(ID),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){window.location.href='/os/domains';}else{dBtn.disabled=false;set('site-life-status','Could not remove');}})
    .catch(function(e){dBtn.disabled=false;set('site-life-status','Error: '+e);});
});
})();
</script>`
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

// domainsScript wires the registry list page: add a domain, per-card sync /
// enable / remove, and the bulk "Sync all pending" action. Per-domain branding
// and post assignment moved to each site's manager (domainManageScript), so this
// script is now just the list-page CRUD. Every handler is null-guarded so a page
// without a given control (e.g. no pending domains → no bulk button) is safe.
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
