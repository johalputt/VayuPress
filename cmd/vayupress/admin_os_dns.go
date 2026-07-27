// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"html"
	htmpl "html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// admin_os_dns.go — "Domains & DNS": every record an install needs, and whether
// it is actually pointed.
//
// WHY THIS PAGE EXISTS
// Each optional product hangs off its own subdomain, and the guidance for them
// was spread across the installation guide, the troubleshooting doc and three
// separate panels. Worse, every one of these fails QUIETLY when its record is
// missing: mail does not arrive, key discovery silently stops and correspondents
// fall back to cleartext, and an MCP client gets a challenge page it cannot read.
// Nothing surfaced, so the only way to learn an install was half-configured was
// to notice a symptom weeks later and go looking.
//
// This turns that into one page that answers three questions per record: does it
// need pointing, is it pointed, and does it point HERE.

// dnsRecord is one row.
type dnsRecord struct {
	Host     string // full hostname
	Label    string // what it unlocks
	Why      string // one line on what breaks without it
	Required bool
	ProxyOff bool // CDN proxy must be off
}

// dnsCheck is a record plus its live resolution result.
type dnsCheck struct {
	dnsRecord
	Resolved  bool
	SameAsAPX bool     // resolves to at least one address the apex resolves to
	Addrs     []string // what it resolves to
}

// dnsLookupTimeout bounds the whole page's resolution work. These are ordinary
// DNS lookups against the operator's own names, but a panel that can hang on a
// slow resolver is a panel that makes the console feel broken.
const dnsLookupTimeout = 5 * time.Second

// subdomainRecords returns every record this install cares about for a domain.
func subdomainRecords(domain string) []dnsRecord {
	d := strings.TrimSpace(strings.ToLower(domain))
	return []dnsRecord{
		{Host: d, Label: "Website & blog", Why: "The site itself.", Required: true},
		{Host: "www." + d, Label: "www redirect", Why: "Visitors typing www reach the site.", Required: true},
		{Host: "mail." + d, Label: "VayuMail", Why: "Without it mail does not arrive at all — a proxy cannot carry SMTP or IMAP.", ProxyOff: true},
		{Host: "openpgpkey." + d, Label: "VayuPGP key discovery", Why: "Without it key lookup fails SILENTLY and correspondents fall back to unencrypted mail.", ProxyOff: true},
		{Host: "talk." + d, Label: "VayuTalk", Why: "Without it the live message stream is buffered or challenged, so the app sends but never receives.", ProxyOff: true},
		{Host: "mcp." + d, Label: "VayuMCP", Why: "Without it an MCP client receives a bot-challenge page it has no way to answer.", ProxyOff: true},
		{Host: "api." + d, Label: "VayuAPI", Why: "Without it scripts, CI jobs and agents hit a challenge page instead of JSON.", ProxyOff: true},
	}
}

// resolveRecords looks every record up concurrently and compares each against
// the apex, since "resolves" and "resolves HERE" are different questions and only
// the second one means the subdomain will work.
func resolveRecords(ctx context.Context, recs []dnsRecord, apex string) []dnsCheck {
	ctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()

	var resolver net.Resolver
	lookup := func(host string) []string {
		addrs, err := resolver.LookupHost(ctx, host)
		if err != nil {
			return nil
		}
		return addrs
	}

	apexAddrs := map[string]bool{}
	for _, a := range lookup(apex) {
		apexAddrs[a] = true
	}

	out := make([]dnsCheck, len(recs))
	var wg sync.WaitGroup
	for i, rec := range recs {
		wg.Add(1)
		go func(i int, rec dnsRecord) {
			defer wg.Done()
			addrs := lookup(rec.Host)
			c := dnsCheck{dnsRecord: rec, Resolved: len(addrs) > 0, Addrs: addrs}
			for _, a := range addrs {
				if apexAddrs[a] {
					c.SameAsAPX = true
					break
				}
			}
			out[i] = c
		}(i, rec)
	}
	wg.Wait()
	return out
}

// handleOSDNS renders the Domains & DNS page.
func (a *App) handleOSDNS(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}

	domain := strings.TrimSpace(config.Cfg.Domain)
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Domains &amp; DNS</h1></div>`)
	body.WriteString(`<p class="page-sub">Every record this install needs, and whether it is actually pointed here. Each subdomain unlocks one product and is optional — but when one is missing it fails <strong>quietly</strong>, so this page checks rather than assumes.</p>`)

	if domain == "" || domain == "localhost" {
		body.WriteString(`<div class="empty-state">No domain is configured yet. Set <code>DOMAIN</code> and restart to manage DNS here.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Domains & DNS", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	recs := subdomainRecords(domain)
	checks := resolveRecords(r.Context(), recs, domain)

	pointed, missing, elsewhere := 0, 0, 0
	for _, c := range checks {
		switch {
		case !c.Resolved:
			missing++
		case c.SameAsAPX:
			pointed++
		default:
			elsewhere++
		}
	}

	// Stats strip, matching the Mail accounts / Monetization language.
	body.WriteString(`<div class="vm-stats">`)
	body.WriteString(vmStatTile(strconv.Itoa(pointed), "Pointed here", ""))
	elseTone := ""
	if elsewhere > 0 {
		elseTone = "warn"
	}
	body.WriteString(vmStatTile(strconv.Itoa(elsewhere), "Resolve elsewhere", elseTone))
	body.WriteString(vmStatTile(strconv.Itoa(missing), "Not pointed", ""))
	body.WriteString(vmStatTile(html.EscapeString(domain), "Primary domain", ""))
	body.WriteString(`</div>`)

	body.WriteString(`<div class="section-head"><span class="section-head__title">Records</span><span class="section-head__hint">Point each one at this server; unpointed records are simply skipped</span></div>`)
	body.WriteString(`<div class="card"><div class="table-wrap"><table class="table"><thead><tr>` +
		`<th>Record</th><th>Status</th><th>CDN proxy</th><th>Unlocks</th></tr></thead><tbody>`)

	for _, c := range checks {
		var status string
		switch {
		case c.Resolved && c.SameAsAPX:
			status = `<span class="badge badge--ok">pointed here</span>`
		case c.Resolved:
			// Resolving somewhere else is the dangerous middle state: certificates
			// may issue and the service still not work, which reads as a product
			// fault rather than a DNS one.
			status = `<span class="badge badge--warn">resolves elsewhere</span>`
		case c.Required:
			status = `<span class="badge badge--warn">not pointed</span>`
		default:
			status = `<span class="badge badge--muted">not pointed</span>`
		}

		proxy := `<span class="muted text-sm">either</span>`
		if c.ProxyOff {
			proxy = `<strong>off</strong>`
		}

		addrNote := ""
		if len(c.Addrs) > 0 {
			addrNote = `<div class="text-xs muted mono">` + html.EscapeString(strings.Join(c.Addrs, ", ")) + `</div>`
		}

		body.WriteString(`<tr><td><code class="mono">` + html.EscapeString(c.Host) + `</code>` + addrNote + `</td>` +
			`<td>` + status + `</td><td>` + proxy + `</td>` +
			`<td>` + html.EscapeString(c.Label) + `<div class="text-xs muted">` + html.EscapeString(c.Why) + `</div></td></tr>`)
	}
	body.WriteString(`</tbody></table></div>`)
	body.WriteString(`<p class="text-xs muted">“Pointed here” means the record resolves to an address this site's own domain also resolves to. “Resolves elsewhere” is worth attention: a certificate may still issue while the service never works, which looks like a VayuPress fault rather than a DNS one.</p></div>`)

	// Why the proxy matters, stated once rather than repeated per row.
	body.WriteString(`<div class="section-head"><span class="section-head__title">Why “CDN proxy off”</span><span class="section-head__hint">The field most often set wrong</span></div>`)
	body.WriteString(`<div class="card"><p class="text-sm muted">Every service above is <strong>machine-to-machine</strong>. A mail server, GnuPG, a CI job and an MCP client have no JavaScript engine, so a proxy that presents a bot challenge — a “checking your browser” page a human passes without noticing — stops them dead. In Cloudflare this is the grey cloud, labelled <strong>DNS only</strong>. Your apex and <code>www</code> keep full protection for human traffic; only these direct hosts bypass it, and each vhost is deliberately narrow.</p></div>`)

	// Live WKD URLs, so the operator can see exactly what a PGP client asks for.
	if wkd := vpgp.WKDURL("you@" + domain); wkd != "" {
		body.WriteString(`<div class="section-head"><span class="section-head__title">PGP key discovery</span><span class="section-head__hint">What a correspondent's client actually requests</span></div>`)
		body.WriteString(`<div class="card"><p class="text-sm muted">Once <code>openpgpkey.` + html.EscapeString(domain) + `</code> is pointed and provisioned, clients find every mailbox's key on their own — no key exchange, no attachment. Per-mailbox URLs are listed under <a href="/os/vayumail/pgp">VayuPGP</a>. Verify from any machine with:</p>` +
			`<p class="mono text-xs">gpg --locate-keys you@` + html.EscapeString(domain) + `</p></div>`)
	}

	// The provisioning card — same control as on Update & Backup, because this is
	// the page an operator lands on when a record is wrong, and sending them
	// somewhere else to act on it is how a fix gets postponed.
	body.WriteString(provisionCardHTML())

	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-update.js?v=` + assetVer("js/admin-os-update.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Domains & DNS", "vayuos", cfg, htmpl.HTML(body.String())))
}
