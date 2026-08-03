// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_home.go — the console for one hosted site (ADR-0154).
//
// /os/d/{id} is the ONE address where a site is operated. Every tool reached
// from here carries the domain in its own URL, so the page you are on is the
// site you are editing, and that is true of the address bar rather than of a
// mode you have to remember being in.
//
// ADR-0154 D2, which this file exists to hold: no install-wide link ever appears
// here. Not demoted, not caveated — absent. The page this console replaced
// carried four buttons labelled Theme Studio / Website settings / Analytics /
// SEO that opened the OPERATOR'S tools, under one line of grey type saying so,
// and that is the entire reported bug. A caveat does not survive contact with a
// button that looks like the thing you came for.

import (
	htmpl "html/template"

	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/users"
)

// scopedTool is one per-site surface reachable from the console.
//
// Live says whether the tool is scoped YET. A link to a page that silently
// edited the primary would be a worse version of the defect this ADR exists to
// fix — so an unscoped tool is listed and disabled with the reason, not linked
// and hoped for.
type scopedTool struct {
	Path  string
	Icon  string
	Title string
	Desc  string
	Live  bool
	Soon  string
}

// scopedTools is the per-site surface.
var scopedTools = []scopedTool{
	{Path: "/os/d/%s/content", Icon: "📝", Title: "Posts & pages", Live: true,
		Desc: "This site's own writing — list it, publish to it, move a post in or out."},
	{Path: "/os/d/%s/settings", Icon: "⚙️", Title: "Site settings", Live: true,
		Desc: "Name, tagline, description and the basics this site introduces itself with."},
	{Path: "/os/d/%s/theme", Icon: "🎨", Title: "Theme Studio", Live: true,
		Desc: "Colours, typography and custom CSS — this site's own, not the primary's."},
	{Path: "/os/d/%s/seo", Icon: "🔍", Title: "SEO", Live: true,
		Desc: "This site's head directives and verification tokens, and its own live sitemap and robots."},
	{Path: "/os/d/%s/analytics", Icon: "📈", Title: "Visitors", Live: true,
		Desc: "This site's own traffic, attributed server-side from the host that served it."},
}

// sharedTools are install-wide and are NOT linked from here, by ADR-0154 D2.
// Naming them is the honest half: an operator needs to know what a hosted site
// does not yet get its own copy of, and finding out by clicking into the
// operator's own newsletter would be the reported bug again.
var sharedTools = []string{"Media library", "Comments", "Newsletter", "Monetization", "Integrations"}

// handleOSScopedHome renders the console for one hosted site.
func (a *App) handleOSScopedHome(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	csrfTokenFor(w, r)

	posts, members, mailboxes := 0, 0, 0
	if a.articles != nil {
		if c, err := a.articles.CountsByDomain(r.Context()); err == nil {
			posts = c[d.ID]
		}
	}
	if a.members != nil {
		if c, err := a.members.CountsByDomain(r.Context()); err == nil {
			members = c[d.ID]
		}
	}
	mailOn := false
	if a.vayuMail != nil {
		mailOn = a.vayuMail.Config().Enabled
		if a.vayuMail.Accounts() != nil {
			if c, err := a.vayuMail.Accounts().CountsByHost(r.Context()); err == nil {
				mailboxes = c[strings.ToLower(d.Host)]
			}
		}
	}
	var clients []users.User
	if a.userStore != nil {
		clients, _ = a.userStore.ClientsForDomain(r.Context(), d.ID)
	}

	body := scopedConsolePage(d, posts, members, mailboxes, mailOn, clients) + domainManageScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// scopedConsolePage builds the console body in the Monetization house style:
// header, four tiles answering "what is the state of this site", the site's own
// tools, then administration folded into accordions so the page is scannable
// rather than a wall of cards.
func scopedConsolePage(d domain.Domain, posts, members, mailboxes int, mailOn bool, clients []users.User) string {
	esc := html.EscapeString
	pending := isPendingTorSite(d.Host)
	var b strings.Builder

	// data-id is what domainManageScript binds every control to.
	b.WriteString(`<div id="dom-manage" data-id="` + esc(d.ID) + `" hidden></div>`)

	hostShown := esc(d.Host)
	view := `<a class="btn btn--ghost btn--sm" href="` + esc(seo.Origin(d.Host)) +
		`" target="_blank" rel="noopener noreferrer">View site ↗</a>`
	if pending {
		hostShown = "Minting .onion…"
		view = ""
	}
	b.WriteString(`<div class="page-header"><h1>` + hostShown + `</h1>` +
		`<div class="page-actions">` + view +
		`<a class="btn btn--ghost btn--sm" href="/os/domains">All sites</a>` +
		`<span id="dom-manage-status" class="text-sm muted" role="status" aria-live="polite"></span>` +
		`</div></div>`)
	b.WriteString(`<p class="page-sub">Everything on this page applies to <b>` + esc(d.Host) +
		`</b> and to nothing else on this install. Your own site's tools are in the left-hand ` +
		`navigation; nothing here will open them.</p>`)

	// ── Four tiles ────────────────────────────────────────────────────────────
	b.WriteString(`<div class="vm-stats">`)
	b.WriteString(vmStatTile(strconv.Itoa(posts), "Posts & pages", ""))
	b.WriteString(vmStatTile(strconv.Itoa(members), "Members", ""))
	if mailOn && d.MailEnabled {
		b.WriteString(vmStatTile(strconv.Itoa(mailboxes), "Mailboxes", ""))
	} else {
		b.WriteString(vmStatTile("—", "Mailboxes", ""))
	}
	certLabel, certTone := scopedCertTile(d)
	b.WriteString(vmStatTile(certLabel, "Certificate", certTone))
	b.WriteString(`</div>`)

	// A pending certificate is the state that stops a site serving, and the tile
	// alone was a dead end: amber, correct, and offering nothing to do about it.
	// The control that fixes it lived on another page this console did not even
	// link. Surfacing a problem without the action that resolves it is the same
	// defect as not surfacing it — the operator learns the tile means "wait".
	if !d.IsPrimary && d.IsSyncApproved() &&
		d.TLSState != domain.TLSActive && d.TLSState != domain.TLSPrimary {
		b.WriteString(scopedCertificateBody(d))
	}

	// A site nobody can reach is the one fact that outranks everything below it.
	if !d.IsPrimary && !d.IsSyncApproved() {
		b.WriteString(`<div class="card"><p class="text-sm"><span class="badge badge--warn">on hold</span> ` +
			`<strong>This site is on manual hold.</strong> No certificate or vhost is issued for it, so it ` +
			`does not serve. Approve it under <b>Lifecycle</b> below, then run <b>Provision subdomains</b> on ` +
			`<a href="/os/dns">Domains &amp; DNS</a>.</p></div>`)
	}

	// ── This site's own tools ─────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">This site's tools</span>` +
		`<span class="section-head__hint">Every link carries this site in its address</span></div>`)
	b.WriteString(`<div class="mon-stack">`)
	for _, t := range scopedTools {
		href := "/os/d/" + esc(d.ID) + esc(t.Path[len("/os/d/%s"):])
		if t.Live {
			b.WriteString(`<a class="card scoped-tool" href="` + href + `">` +
				`<span class="scoped-tool__icon" aria-hidden="true">` + t.Icon + `</span>` +
				`<span class="scoped-tool__body"><span class="settings-block-title">` + esc(t.Title) + `</span>` +
				`<span class="text-sm muted">` + esc(t.Desc) + `</span></span></a>`)
			continue
		}
		b.WriteString(`<div class="card scoped-tool scoped-tool--soon">` +
			`<span class="scoped-tool__icon" aria-hidden="true">` + t.Icon + `</span>` +
			`<span class="scoped-tool__body"><span class="settings-block-title">` + esc(t.Title) +
			` <span class="pill pill--muted">` + esc(t.Soon) + `</span></span>` +
			`<span class="text-sm muted">` + esc(t.Desc) + `</span>` +
			`<span class="text-sm muted">Not scoped yet — until it is, it would edit the primary ` +
			`site, so it is not linked from here.</span></span></div>`)
	}
	b.WriteString(`</div>`)

	// ── Administration ────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Site administration</span>` +
		`<span class="section-head__hint">Access, allowances and lifecycle for this site</span></div>`)
	b.WriteString(`<div class="mon-stack">`)
	b.WriteString(monAcc("👤", "Client access", "Who can sign in and see only this site",
		chipFor(len(clients) > 0, strconv.Itoa(len(clients))+" login(s)", "no logins"),
		len(clients) == 0, domainClientAccessCard(d, clients)))
	// Always rendered, including when mail is off install-wide. Hiding it then
	// would leave the "—" on the mailbox tile unexplained; the card itself says
	// "Mail is switched off", which is the different situation an operator needs
	// distinguished from an allowance of zero.
	b.WriteString(monAcc("✉️", "Mailbox allowance", "How many mailboxes this site may create",
		chipFor(mailOn && d.MailEnabled, strconv.Itoa(mailboxes)+" in use", "mail off"),
		false, domainAllowanceCard(d, mailboxes, mailOn)))
	b.WriteString(monAcc("🔧", "Lifecycle", "Provisioning, availability and removal",
		chipFor(d.Status == domain.StatusActive, "active", "disabled"), false, scopedLifecycleBody(d)))
	b.WriteString(monAcc("🏛", "What is shared, and always will be",
		"One binary, one machine — some things cannot be per-site",
		`<span class="mon-chip mon-chip--off">by construction</span>`, false, scopedSharedBody()))
	b.WriteString(`</div>`)
	return b.String()
}

// chipFor renders a monAcc summary chip in the on/off styles.
func chipFor(on bool, onText, offText string) string {
	if on {
		return `<span class="mon-chip mon-chip--on">` + html.EscapeString(onText) + `</span>`
	}
	return `<span class="mon-chip mon-chip--off">` + html.EscapeString(offText) + `</span>`
}

// scopedCertTile renders the certificate tile. A site with no certificate serves
// a browser security warning, so it is toned rather than stated flatly.
func scopedCertTile(d domain.Domain) (label, tone string) {
	switch d.TLSState {
	case domain.TLSActive, domain.TLSPrimary:
		return "Live", ""
	case domain.TLSFailed:
		return "Failed", "warn"
	default:
		return "Pending", "warn"
	}
}

// scopedCertificateBody explains why a certificate is not automatic and offers
// the control that makes it happen now.
//
// "Why is the certificate not installed automatically?" is the question this
// answers, and the honest answer is that it IS automatic on a daily cadence —
// it is just not instant, because the thing that has to happen needs root and
// this service deliberately cannot become root. Stating only "pending" left an
// operator to conclude the feature was broken.
func scopedCertificateBody(d domain.Domain) string {
	esc := html.EscapeString
	headline := `no certificate has been issued for <b>` + esc(d.Host) + `</b> yet`
	if d.TLSState == domain.TLSFailed {
		headline = `the last attempt to issue a certificate for <b>` + esc(d.Host) + `</b> <b>failed</b>`
	}
	return `<div class="card">
  <div class="settings-block-title">Certificate pending</div>
  <p class="text-sm"><span class="badge badge--warn">no certificate</span> ` + headline + `, so a visitor is
    served the primary domain's certificate and the browser refuses the page
    (<code>ERR_CERT_COMMON_NAME_INVALID</code>).</p>
  <p class="text-sm muted">This is not instant by design. Obtaining a certificate and reloading nginx needs
    <b>root</b>, and this service runs unprivileged and deliberately cannot become root — that is why a bug in
    it cannot take over the machine. A small root-side helper does that step instead. It runs <b>once a day on
    its own</b>, and immediately whenever you ask for it, so a DNS record you point today is picked up without
    you touching a terminal.</p>
  <p class="text-sm muted">Two things it needs before it can succeed: this site's DNS pointing at this server
    (check on <a href="/os/dns">Domains &amp; DNS</a>), and the helper installed — if it never has been, that
    page shows the one command that installs it.</p>
  <div class="vm-row">
    <button type="button" class="btn btn--primary btn--sm" data-site-provision>Provision now</button>
    <span id="site-cert-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
  <p class="text-xs muted">Asking for a run creates an empty flag file a root-side service watches. No argument
    is passed and its contents are never read, so this console can request provisioning and cannot influence
    what the privileged step does.</p>
</div>`
}

func scopedLifecycleBody(d domain.Domain) string {
	esc := html.EscapeString
	syncLabel, syncTarget := "Approve for provisioning", domain.SyncApproved
	if d.IsSyncApproved() {
		syncLabel, syncTarget = "Pause provisioning", domain.SyncHold
	}
	return `<div class="card">
  <div class="settings-block-title">Provisioning &amp; availability</div>
  <p class="text-sm muted">Approving lets the root-side helper obtain this site's own certificate and write its
    vhost. Pausing leaves the site registered and stops it being touched. Removing it deletes the registry entry —
    its posts keep the old domain id and stop being served.</p>
  <div class="vm-row">
    <button type="button" class="btn btn--ghost btn--sm" data-site-sync data-sync="` + esc(syncTarget) + `">` + esc(syncLabel) + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-site-toggle data-status="` + esc(toggleStatusFor(d)) + `">` + esc(toggleLabelFor(d)) + `</button>
    <button type="button" class="btn btn--danger btn--sm" data-site-delete data-host="` + esc(d.Host) + `">Remove site</button>
    <span id="site-life-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

// scopedSharedBody states what independence means here, and what it does not. An
// operator selling this needs the ceiling in the same view as the capability, or
// they will discover it in front of a client.
func scopedSharedBody() string {
	var b strings.Builder
	b.WriteString(`<div class="card"><p class="text-sm muted">This site's settings, content and traffic are ` +
		`its own. These are not, by construction: one process (a bug in it reaches every site at once — row ` +
		`scoping is not a sandbox), one machine and one database (they fail and recover together), one mail ` +
		`signing key, and one bot shield seeing one network stack. Backups and updates are the install's, not ` +
		`the site's.</p>`)
	b.WriteString(`<p class="text-sm muted">These tools are still install-wide, so a hosted site has no ` +
		`separate copy of them yet: <b>` + html.EscapeString(strings.Join(sharedTools, "</b>, <b>")) + `</b>. ` +
		`They are named here rather than linked — a link from this page to a tool that edits the primary site ` +
		`is the defect this console was rebuilt to remove.</p></div>`)
	return b.String()
}
