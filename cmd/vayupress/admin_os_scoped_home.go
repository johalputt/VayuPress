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
	"time"

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
	Key   string // chip lookup, and the last segment of Path
	Path  string
	Icon  string
	Title string
	Desc  string
	Live  bool
	Soon  string
}

// scopedTools is the per-site surface.
//
// The descriptions are one short line each, deliberately. They are subtitles in
// the same grammar as the accordions below ("Who can sign in and see only this
// site"), not paragraphs: a row whose subtitle wraps to three lines does not
// read as the same component as one whose subtitle is six words, and that was
// half of why this band looked foreign on its own page.
var scopedTools = []scopedTool{
	{Key: "content", Path: "/os/d/%s/content", Icon: "📝", Title: "Posts & pages", Live: true,
		Desc: "This site's own writing, listed and published"},
	{Key: "website", Path: "/os/d/%s/website", Icon: "🌐", Title: "Website", Live: true,
		Desc: "Serve this domain as a blog or as a website"},
	{Key: "settings", Path: "/os/d/%s/settings", Icon: "⚙️", Title: "Site settings", Live: true,
		Desc: "Name, tagline and description for this site"},
	{Key: "theme", Path: "/os/d/%s/theme", Icon: "🎨", Title: "Theme Studio", Live: true,
		Desc: "Colours, typography and custom CSS — this site's own"},
	{Key: "seo", Path: "/os/d/%s/seo", Icon: "🔍", Title: "SEO", Live: true,
		Desc: "Head directives, tokens, sitemap and robots"},
	{Key: "analytics", Path: "/os/d/%s/analytics", Icon: "📈", Title: "Visitors", Live: true,
		Desc: "This site's own traffic, counted server-side"},
}

// scopedToolChip is one navigation row's state, in the grammar every other row
// on this page already used.
//
// These six rows carried no chip at all while the four administration rows below
// them each reported their state while shut. That is what made the band read as
// a different component: same page, same stack, one half labelled and the other
// half not. A chip here is not decoration — it is the difference between "open
// Site settings to find out whether anything is set" and knowing at a glance.
type scopedToolChip struct {
	On   bool
	Text string
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

	// The diagnostic runs only when there is something to diagnose — it does a
	// DNS lookup and reads a file, and neither belongs on a healthy page.
	var checks []diagCheck
	var logLines []string
	if scopedNeedsCertificate(d) {
		logLines = provisionLogTail(provisionLogLines)
		checks = a.diagnoseCertificate(r.Context(), d, logLines)
	}

	body := scopedConsolePage(d, posts, members, mailboxes, mailOn, clients, checks, logLines,
		a.scopedToolChips(r, d, posts)) + domainManageScript(nonce) + domainServesScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// scopedConsolePage builds the console body in the Monetization house style:
// header, four tiles answering "what is the state of this site", the site's own
// tools, then administration folded into accordions so the page is scannable
// rather than a wall of cards.
func scopedConsolePage(d domain.Domain, posts, members, mailboxes int, mailOn bool, clients []users.User, checks []diagCheck, logLines []string, tools map[string]scopedToolChip) string {
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
	b.WriteString(`<div class="stat-grid">`)
	b.WriteString(osStatTile("Posts & pages", strconv.Itoa(posts), ""))
	b.WriteString(osStatTile("Members", strconv.Itoa(members), ""))
	// A site with mail switched on and an allowance of nothing cannot create a
	// single mailbox, and that read as "0 in use" — which looks like a new site
	// nobody has set up rather than one that will refuse its owner. The allowance
	// defaults to 0 deliberately, so every new customer domain passes through
	// this state and it has to be visible at a glance.
	mailReady := mailOn && d.MailEnabled
	noAllowance := mailReady && d.Limits().Mailboxes == 0
	switch {
	case noAllowance:
		b.WriteString(osStatTile("Mailboxes", "0 granted", "warn"))
	case mailReady:
		b.WriteString(osStatTile("Mailboxes", strconv.Itoa(mailboxes), ""))
	default:
		b.WriteString(osStatTile("Mailboxes", "—", ""))
	}
	certLabel, certTone := scopedCertTile(d)
	b.WriteString(osStatTile("Certificate", certLabel, certTone))
	b.WriteString(`</div>`)

	// A pending certificate is the state that stops a site serving, and the tile
	// alone was a dead end: amber, correct, and offering nothing to do about it.
	// The control that fixes it lived on another page this console did not even
	// link. Surfacing a problem without the action that resolves it is the same
	// defect as not surfacing it — the operator learns the tile means "wait".
	if scopedNeedsCertificate(d) {
		b.WriteString(scopedCertificateSection(d, checks, logLines))
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
		// The inner grammar is the accordion summary's, class for class, so the two
		// bands on this page cannot drift apart again. Only the frame and the
		// affordance differ: a link leans, a disclosure rotates.
		head := `<span class="mon-acc__ic" aria-hidden="true">` + t.Icon + `</span>` +
			`<span class="mon-acc__head"><span class="mon-acc__title">` + esc(t.Title) + `</span>` +
			`<span class="mon-acc__sub">` + esc(t.Desc) + `</span></span>`
		if t.Live {
			b.WriteString(`<a class="scoped-tool" href="` + href + `">` + head +
				scopedToolChipHTML(tools[t.Key]) + `</a>`)
			continue
		}
		// The reason stays on the row. A tool listed without one reads as broken
		// rather than as deliberately not linked yet.
		b.WriteString(`<div class="scoped-tool scoped-tool--soon">` +
			`<span class="mon-acc__ic" aria-hidden="true">` + t.Icon + `</span>` +
			`<span class="mon-acc__head"><span class="mon-acc__title">` + esc(t.Title) + `</span>` +
			`<span class="mon-acc__sub">Not scoped yet — it would edit the primary site, so it is ` +
			`not linked from here</span></span>` +
			`<span class="mon-chip mon-chip--off">` + esc(t.Soon) + `</span></div>`)
	}
	b.WriteString(`</div>`)

	// ── Administration ────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Site administration</span>` +
		`<span class="section-head__hint">Access, allowances and lifecycle for this site</span></div>`)
	b.WriteString(`<div class="mon-stack">`)
	// FIRST in Site administration: what a domain serves is the question every
	// other row on this page assumes an answer to. It was settable only on the
	// "Add a domain" form and then frozen forever, because Registry.Update
	// existed and nothing called it (ADR-0159).
	servesChip := `<span class="mon-chip mon-chip--on">` +
		html.EscapeString(siteTypeLabel(d.EffectiveSiteType())) + `</span>`
	if d.MailEnabled {
		servesChip = `<span class="mon-chip mon-chip--on">` +
			html.EscapeString(siteTypeLabel(d.EffectiveSiteType())) + ` + mail</span>`
	}
	b.WriteString(monAcc("🌐", "What this domain serves", "Blog, website, or both — and whether it carries mail",
		servesChip, false, domainServesCard(d, mailOn)))
	b.WriteString(monAcc("👤", "Client access", "Who can sign in and see only this site",
		chipFor(len(clients) > 0, strconv.Itoa(len(clients))+" login(s)", "no logins"),
		len(clients) == 0, domainClientAccessCard(d, clients)))
	// Always rendered, including when mail is off install-wide. Hiding it then
	// would leave the "—" on the mailbox tile unexplained; the card itself says
	// "Mail is switched off", which is the different situation an operator needs
	// distinguished from an allowance of zero.
	// Opened, and chipped as a problem, in the one state where the card's advice
	// is the difference between a working customer and a confused one.
	allowanceChip := chipFor(mailReady, strconv.Itoa(mailboxes)+" in use", "mail off")
	if noAllowance {
		allowanceChip = `<span class="mon-chip mon-chip--off">none granted</span>`
	}
	b.WriteString(monAcc("✉️", "Mailbox allowance", "How many mailboxes this site may create",
		allowanceChip, noAllowance, domainAllowanceCard(d, mailboxes, mailOn)))
	b.WriteString(monAcc("🔧", "Lifecycle", "Provisioning, availability and removal",
		chipFor(d.Status == domain.StatusActive, "active", "disabled"), false, scopedLifecycleBody(d)))
	b.WriteString(monAcc("🏛", "What is shared, and always will be",
		"One binary, one machine — some things cannot be per-site",
		`<span class="mon-chip mon-chip--off">by construction</span>`, false, scopedSharedBody()))
	b.WriteString(`</div>`)
	return b.String()
}

// scopedToolChips reads the state behind each navigation row.
//
// Two extra reads for six chips: one settings fetch covering identity, theme and
// SEO, and one traffic overview. Both are guarded, and a store that is absent or
// failing yields NO chip for the rows it feeds rather than a confident one — the
// zero scopedToolChip renders as "—".
func (a *App) scopedToolChips(r *http.Request, d domain.Domain, posts int) map[string]scopedToolChip {
	ctx := r.Context()
	sc := osScope(r)
	c := map[string]scopedToolChip{}

	if posts > 0 {
		c["content"] = scopedToolChip{On: true, Text: strconv.Itoa(posts) + " items"}
	} else {
		c["content"] = scopedToolChip{Text: "nothing yet"}
	}

	// What this domain serves, straight off the config already decoded in memory.
	// An empty mode means "inherit", which serves the blog — so it reports blog
	// rather than "not set up", because "not set up" would be untrue of a domain
	// that is serving perfectly well.
	switch s, ok := d.Site(); {
	case ok && s.Mode == "custom":
		c["website"] = scopedToolChip{On: true, Text: "uploaded site"}
	case ok && strings.HasPrefix(s.Mode, "business"):
		c["website"] = scopedToolChip{On: true, Text: "website"}
	default:
		c["website"] = scopedToolChip{Text: "blog"}
	}

	if a.siteSettings != nil && sc.Valid() {
		if all, err := a.siteSettings.GetAll(ctx, sc); err == nil {
			set := 0
			for _, f := range scopedSettingKeys {
				if strings.TrimSpace(all[f.Key]) != "" {
					set++
				}
			}
			if set > 0 {
				c["settings"] = scopedToolChip{On: true,
					Text: strconv.Itoa(set) + " of " + strconv.Itoa(len(scopedSettingKeys))}
			} else {
				c["settings"] = scopedToolChip{Text: "nothing set"}
			}

			themed := false
			for _, k := range copyableFromPrimary {
				if strings.TrimSpace(all[k]) != "" {
					themed = true
					break
				}
			}
			c["theme"] = scopedToolChip{On: themed, Text: map[bool]string{true: "custom", false: "default"}[themed]}

			seo := 0
			for _, f := range scopedSEOFields {
				if strings.TrimSpace(all[f.Key]) != "" {
					seo++
				}
			}
			if seo > 0 {
				c["seo"] = scopedToolChip{On: true, Text: strconv.Itoa(seo) + " set"}
			} else {
				c["seo"] = scopedToolChip{Text: "all default"}
			}
		}
	}

	if a.analytics != nil {
		if ov, err := a.analytics.OverviewSinceScoped(ctx, d.ID, 30); err == nil {
			if ov.TotalPageviews > 0 {
				c["analytics"] = scopedToolChip{On: true, Text: strconv.Itoa(ov.TotalPageviews) + " views"}
			} else {
				c["analytics"] = scopedToolChip{Text: "no visits yet"}
			}
		}
	}
	return c
}

// scopedToolChipHTML renders a tool row's state chip.
//
// A row whose state could not be read gets "—" rather than a cheerful default.
// The Settings page learned this the expensive way: collapsing a failed read
// into the "nothing set" branch tells an operator something definite about a
// site nobody managed to look at.
func scopedToolChipHTML(c scopedToolChip) string {
	if strings.TrimSpace(c.Text) == "" {
		return `<span class="mon-chip mon-chip--off">—</span>`
	}
	cls := "mon-chip mon-chip--off"
	if c.On {
		cls = "mon-chip mon-chip--on"
	}
	return `<span class="` + cls + `">` + html.EscapeString(c.Text) + `</span>`
}

// chipFor renders a monAcc summary chip in the on/off styles.
func chipFor(on bool, onText, offText string) string {
	if on {
		return `<span class="mon-chip mon-chip--on">` + html.EscapeString(onText) + `</span>`
	}
	return `<span class="mon-chip mon-chip--off">` + html.EscapeString(offText) + `</span>`
}

// scopedNeedsCertificate reports whether a CA certificate is something this site
// is actually waiting for. It is the single predicate behind the tile, the
// certificate section and the diagnostic, which previously repeated the same
// four-clause condition in three places.
//
// The clause all three were missing is the onion one. A Tor site is registered
// sync-approved, active and TLS-pending, and it stays TLS-pending forever
// because an onion is served over http by design — Tor Browser treats a v3
// address as a trustworthy origin and no CA could issue for it in any case. So
// every Tor site's console announced "no certificate has been issued … the
// browser refuses the page", and the diagnosis under it told the operator to
// point DNS at this server for a name the DNS system does not resolve at all.
// Two confident statements, both false, on a page whose whole purpose is to be
// the thing an operator does not have to second-guess.
func scopedNeedsCertificate(d domain.Domain) bool {
	if d.IsPrimary || !d.IsSyncApproved() {
		return false
	}
	if seo.IsOnion(d.Host) || isPendingTorSite(d.Host) {
		return false
	}
	return d.TLSState != domain.TLSActive && d.TLSState != domain.TLSPrimary
}

// scopedCertTile renders the certificate tile. A site with no certificate serves
// a browser security warning, so it is toned rather than stated flatly.
func scopedCertTile(d domain.Domain) (label, tone string) {
	// Not amber, and not "Pending": an onion has no certificate to wait for, and
	// an amber tile on a site that is working exactly as designed is an alarm
	// that trains the operator to ignore the tile.
	if seo.IsOnion(d.Host) || isPendingTorSite(d.Host) {
		return "Onion", ""
	}
	switch d.TLSState {
	case domain.TLSActive, domain.TLSPrimary:
		return "Live", ""
	case domain.TLSFailed:
		return "Failed", "warn"
	default:
		return "Pending", "warn"
	}
}

// scopedCertificateSection folds the certificate explanation and the console's
// own diagnosis into accordions, in the house style.
//
// Both were flat, permanently-expanded cards, and between them they were most of
// the page: a screen of prose, then a seven-row table, then twenty-five lines of
// log — all above the tools the operator actually came for.
//
// Folding them cannot be allowed to HIDE anything, which is the whole design
// constraint here. So each summary carries its verdict as a chip that reads
// while collapsed, and the diagnosis opens BY ITSELF whenever a check is
// blocking. A collapsed panel quietly holding the reason the button will not
// work would be the same defect as the tile that said "pending" and offered
// nothing to do about it.
func scopedCertificateSection(d domain.Domain, checks []diagCheck, logLines []string) string {
	var b strings.Builder
	b.WriteString(`<div class="section-head"><span class="section-head__title">Certificate</span>` +
		`<span class="section-head__hint">Why this site is not served over HTTPS yet, and what this ` +
		`console can determine about it</span></div>`)
	b.WriteString(`<div class="mon-stack">`)

	blocking := 0
	for _, c := range checks {
		if !c.OK && c.Fatal {
			blocking++
		}
	}

	// The subtitle is derived from the checks rather than written once and left
	// there. "One root-side step away" is true of a site waiting on the daily
	// sweep and FALSE of one whose DNS points at somebody else's server — and the
	// console knows which it is looking at, three lines further down the page.
	// Telling an operator to wait for something that can never arrive is the same
	// defect as the tile that said "pending" and offered nothing.
	certChip, certSub := "pending", "Not issued yet — one root-side step away"
	if d.TLSState == domain.TLSFailed {
		certChip = "last attempt failed"
		certSub = "The last attempt was refused; the diagnosis below says why"
	}
	if blocking > 0 {
		certSub = "Blocked — the diagnosis below names what is stopping it, and waiting will not clear it"
	}
	b.WriteString(monAcc("🔒", "Certificate", certSub,
		`<span class="mon-chip mon-chip--off">`+html.EscapeString(certChip)+`</span>`,
		true, scopedCertificateBody(d)))

	if len(checks) > 0 {
		b.WriteString(monAcc("🩺", "What this console checked",
			"Run here, now, against this install — not a description of where to go and look",
			chipFor(blocking == 0, "nothing blocking", strconv.Itoa(blocking)+" blocking"),
			blocking > 0, scopedDiagnosticPanel(d.ID, checks, logLines, d.Host, time.Now())))
	}
	b.WriteString(`</div>`)
	return b.String()
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
    <button type="button" class="btn btn--sm" data-site-repair>Repair the certificate helpers</button>
    <span id="site-cert-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
  <p class="text-xs muted">Use <b>Repair the certificate helpers</b> when the diagnosis below says nginx has
    not reloaded since this site's vhost was written. It installs the current, signature-verified helpers and
    performs that reload. It lives here as well as on VayuShield because an operator reading this diagnosis is
    already on the page that needs it — sending them somewhere else to act on what they are looking at is the
    same defect as reporting a problem with no way to fix it.</p>
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
