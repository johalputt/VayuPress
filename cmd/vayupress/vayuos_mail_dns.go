package main

// vayuos_mail_dns.go — the VayuMail "DNS" tab, rebuilt for multi-domain.
//
// The old tab was a stack of flat tables that only ever described the primary
// domain and only listed the MX/SPF/DKIM/DMARC "records to publish" — it never
// showed the A/AAAA record that makes the mail host reachable, never warned that
// those records must be DNS-only (not proxied), and never verified a secondary
// domain. This rebuild presents everything as collapsible sections, adds the
// missing mail-host & networking records, and verifies EVERY mail domain (primary
// + each mail_enabled secondary) live, with an HTMX re-check that refreshes the
// verification in place. It is administrator-only, like the rest of the tab.

import (
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// vayuDNSCollapsible wraps a titled, collapsible section (a <details> card). meta
// is optional trailing HTML (a badge) shown in the header; open controls the
// initial state so the important sections start expanded.
func vayuDNSCollapsible(title, meta string, open bool, inner string) string {
	o := ""
	if open {
		o = " open"
	}
	metaHTML := ""
	if meta != "" {
		metaHTML = `<span class="vm-sec__meta">` + meta + `</span>`
	}
	return `<details class="card vm-sec"` + o + `>
  <summary class="vm-sec__head"><span class="vm-sec__title">` + html.EscapeString(title) + `</span>` + metaHTML + `<span class="vm-sec__chev" aria-hidden="true">▾</span></summary>
  <div class="vm-sec__body">` + inner + `</div>
</details>`
}

// vayuDNSCopyBtn renders a copy-to-clipboard control carrying the record value in
// a data attribute (escaped so the long DKIM value stays attribute-safe).
func vayuDNSCopyBtn(val string) string {
	return `<button type="button" class="vm-copy" data-copy="` + html.EscapeString(val) + `" aria-label="Copy value">Copy</button>`
}

// vayuDNSRecordsSection renders one domain's "records to publish" collapsible: the
// MX/SPF/DKIM/DMARC table with a copy button on each value.
func vayuDNSRecordsSection(title, subtitle string, recs []mail.DNSRecord, open bool) string {
	var rows strings.Builder
	for _, rec := range recs {
		rows.WriteString(`<tr><td><span class="vm-tag">` + html.EscapeString(rec.Type) + `</span></td>` +
			`<td class="mono text-sm">` + html.EscapeString(rec.Name) + `</td>` +
			`<td class="mono text-sm vm-break"><span class="vm-rec-val">` + html.EscapeString(rec.Value) + `</span>` + vayuDNSCopyBtn(rec.Value) + `</td></tr>`)
	}
	inner := `<p class="muted text-sm">` + html.EscapeString(subtitle) + `</p>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>Type</th><th>Name</th><th>Value</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>`
	return vayuDNSCollapsible(title, "", open, inner)
}

// vayuMailHostSection is the record set the old tab never showed: the A/AAAA that
// make the mail host reachable, the reverse-DNS (PTR) alignment, and the inbound
// ports — plus the load-bearing warning that mail cannot be proxied, so these
// must be published DNS-only (on Cloudflare, the grey cloud, never the orange).
func vayuMailHostSection(mc mail.Config) string {
	host := html.EscapeString(mc.Hostname)
	inner := `<p class="muted text-sm">Mail runs over SMTP/IMAP/POP3, which <strong>cannot be proxied</strong>. These records make your mail host reachable — publish them <strong>DNS-only</strong>. On Cloudflare that is the <strong>grey cloud</strong>; a proxied (orange-cloud) mail host silently breaks inbound mail.</p>
<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>Type</th><th>Name</th><th>Value</th><th>Proxy</th></tr></thead><tbody>
<tr><td><span class="vm-tag">A</span></td><td class="mono text-sm">` + host + `</td><td class="mono text-sm">your server's public IPv4</td><td><span class="badge badge--warn">DNS only</span></td></tr>
<tr><td><span class="vm-tag">AAAA</span></td><td class="mono text-sm">` + host + `</td><td class="mono text-sm">your server's public IPv6 (if any)</td><td><span class="badge badge--warn">DNS only</span></td></tr>
<tr><td><span class="vm-tag">PTR</span></td><td class="mono text-sm">reverse DNS of your IP</td><td class="mono text-sm">` + host + `</td><td><span class="muted text-xs">set at your VPS host</span></td></tr>
</tbody></table></div>
<p class="muted text-xs">Open inbound ports <span class="mono">25</span> (SMTP), <span class="mono">465/587</span> (submission), <span class="mono">993</span> (IMAPS) and <span class="mono">995</span> (POP3S) on your server firewall.</p>`
	return vayuDNSCollapsible("Mail host & networking — "+mc.Hostname, `<span class="badge badge--warn">do not proxy</span>`, true, inner)
}

// vayuDNSPublishSections builds the "records to publish" collapsibles — the
// primary (expanded) plus one per mail_enabled secondary (collapsed) — followed
// by the mail-host & networking section. On a single-domain install only the
// primary and host sections render, so nothing changes for a plain setup.
func (a *App) vayuDNSPublishSections(r *http.Request, mc mail.Config) string {
	var b strings.Builder
	b.WriteString(vayuDNSRecordsSection(
		"Records to publish — "+mc.Domain,
		"Publish these at your DNS provider for "+mc.Domain+". They route your mail (MX) and authenticate it (SPF, DKIM, DMARC).",
		a.vayuMail.PlannedRecords(), true))
	for _, secHost := range a.mailSecondaryHosts(r.Context()) {
		sub := "Secondary mail domain. Its MX points at this install's mail host (" + mc.Hostname +
			"); the DKIM key is shared with the primary, so publish the same key value at " +
			mc.DKIMSelector + "._domainkey." + secHost + "."
		b.WriteString(vayuDNSRecordsSection("Records to publish — "+secHost, sub, a.vayuMail.PlannedRecordsForDomain(secHost), false))
	}
	b.WriteString(vayuMailHostSection(mc))
	return b.String()
}

// vayuDNSVerifyRow renders one verification row (record/check, status badge,
// detail).
func vayuDNSVerifyRow(typ string, ok bool, detail string) string {
	badge := `<span class="badge badge--ok">ok</span>`
	if !ok {
		badge = `<span class="badge badge--warn">action</span>`
	}
	return `<tr><td>` + html.EscapeString(typ) + `</td><td>` + badge + `</td><td class="muted text-sm vm-break">` + html.EscapeString(detail) + `</td></tr>`
}

// vayuDNSVerifyDomainTable renders one domain's live MX/SPF/DKIM/DMARC alignment.
func vayuDNSVerifyDomainTable(domain string, hc *mail.DomainHealth) string {
	var rows strings.Builder
	for _, rh := range hc.Records {
		detail := rh.Found
		if !rh.OK && rh.Message != "" {
			detail = rh.Message
		}
		rows.WriteString(vayuDNSVerifyRow(rh.Type, rh.OK, detail))
	}
	pill := `<span class="badge badge--ok">aligned</span>`
	if !hc.AllOK {
		pill = `<span class="badge badge--warn">check records</span>`
	}
	return `<div class="vm-verify-dom"><h4 class="vm-sub-title">` + html.EscapeString(domain) + ` ` + pill + `</h4>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>Record</th><th>Status</th><th>Found</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
}

// vayuDNSVerifyFragment renders the "DNS verification — all domains" section: live
// MX/SPF/DKIM/DMARC alignment for the primary AND every mail_enabled secondary,
// plus the host-level deliverability self-check (HELO/DKIM-key/PTR, shared across
// domains). It is the HTMX swap target (#vm-dns-verify) and carries its own
// Re-check control, so a refresh re-runs every lookup without a full-page reload.
func (a *App) vayuDNSVerifyFragment(r *http.Request) string {
	ctx := r.Context()
	mc := a.vayuMail.Config()

	domains := append([]string{mc.Domain}, a.mailSecondaryHosts(ctx)...)
	allOK := true
	var tables strings.Builder
	for _, d := range domains {
		hc := a.vayuMail.HealthForDomain(ctx, d)
		if !hc.AllOK {
			allOK = false
		}
		tables.WriteString(vayuDNSVerifyDomainTable(d, hc))
	}

	var deliv strings.Builder
	for _, rh := range a.vayuMail.Deliverability(ctx) {
		if !rh.OK {
			allOK = false
		}
		deliv.WriteString(vayuDNSVerifyRow(rh.Type, rh.OK, rh.Message))
	}

	pill := `<span class="badge badge--ok">all aligned</span>`
	if !allOK {
		pill = `<span class="badge badge--warn">action needed</span>`
	}

	inner := `<div class="vm-verify-head">` + pill +
		`<button type="button" class="btn btn--sm" hx-get="/os/vayumail/dns/verify" hx-target="#vm-dns-verify" hx-swap="outerHTML" hx-indicator="#vm-dns-spin">Re-check</button>` +
		`<span id="vm-dns-spin" class="htmx-indicator vm-spin" aria-hidden="true">checking…</span></div>` +
		tables.String() +
		`<div class="vm-verify-dom"><h4 class="vm-sub-title">Deliverability (mail host)</h4>` +
		`<p class="muted text-xs">The things that most often send legitimate mail to spam — every row should read ok.</p>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>Check</th><th>Status</th><th>Detail</th></tr></thead><tbody>` + deliv.String() + `</tbody></table></div></div>`

	// The fragment root carries the swap id and is itself the collapsible section,
	// so an HTMX outerHTML swap replaces the whole section (button included) with
	// freshly-checked results.
	return `<div id="vm-dns-verify">` + vayuDNSCollapsible("DNS verification — all domains", "", true, inner) + `</div>`
}

// vayuDNSScript is the CSP-nonce'd copy-to-clipboard handler for the record
// values (delegated, so it also covers HTMX-swapped content).
func vayuDNSScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
document.addEventListener('click',function(e){
  var b=e.target.closest('[data-copy]');if(!b)return;
  var v=b.getAttribute('data-copy')||'';
  function done(){var t=b.getAttribute('data-label')||b.textContent;b.setAttribute('data-label',t);b.textContent='Copied';setTimeout(function(){b.textContent=t;},1200);}
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(v).then(done,done);}
  else{try{var ta=document.createElement('textarea');ta.value=v;document.body.appendChild(ta);ta.select();document.execCommand('copy');document.body.removeChild(ta);done();}catch(_){}}
});
})();
</script>`
}

// handleVayuOSMailDNSVerify serves the live DNS-verification fragment for the
// HTMX "Re-check" control (administrator-only, like the DNS tab).
func (a *App) handleVayuOSMailDNSVerify(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	writeOSFragment(w, a.vayuDNSVerifyFragment(r))
}
