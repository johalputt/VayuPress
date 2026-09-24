// SPDX-License-Identifier: Apache-2.0

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
	"context"
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// dnsDomainHealth is one mail domain's live record verdicts.
type dnsDomainHealth struct {
	Domain string
	Health *mail.DomainHealth
}

// dnsHealth is one render's worth of DNS verdicts: every mail domain's records
// plus the host-level deliverability self-check.
//
// It is computed ONCE per render and handed to both the checklist and the
// reference tables, so the two can never tell different stories — and so a page
// costs one round of lookups rather than one per section.
type dnsHealth struct {
	Domains        []dnsDomainHealth
	Deliverability []mail.RecordHealth
	AllOK          bool
}

// vayuDNSHealth runs the live checks for the primary domain, every mail_enabled
// secondary, and the mail host itself.
func (a *App) vayuDNSHealth(ctx context.Context) dnsHealth {
	h := dnsHealth{AllOK: true}
	mc := a.vayuMail.Config()
	domains := append([]string{mc.Domain}, a.mailSecondaryHosts(ctx)...)
	for _, d := range domains {
		hc := a.vayuMail.HealthForDomain(ctx, d)
		if !hc.AllOK {
			h.AllOK = false
		}
		h.Domains = append(h.Domains, dnsDomainHealth{Domain: d, Health: hc})
	}
	for _, rh := range a.vayuMail.Deliverability(ctx) {
		if !rh.OK {
			h.AllOK = false
		}
		h.Deliverability = append(h.Deliverability, rh)
	}
	a.recordMailDNS(h)
	return h
}

// vayuDNSWizard is the guided checklist that sits above the reference tables:
// what is already right, what is missing, and — as one named line — the next
// thing to do.
//
// It exists because this tab was an excellent reference and a poor guide: an
// operator could read every record, publish all of them, and still have no way to
// tell whether they were finished. The reference view stays exactly as it was
// below this card.
func vayuDNSWizard(h dnsHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="card vm-wizard" id="vm-dns-wizard">`)
	b.WriteString(`<div class="vm-wizard-head"><h2 class="vm-wizard-title">Domain health — are we done?</h2>`)
	if h.AllOK {
		b.WriteString(`<span class="badge badge--ok">all checks pass</span>`)
	} else {
		b.WriteString(`<span class="badge badge--warn">action needed</span>`)
	}
	b.WriteString(`</div>`)
	if h.AllOK {
		b.WriteString(`<p class="muted text-sm">Every mail domain is aligned and the mail-host checks pass. Nothing to do here.</p>`)
	} else {
		b.WriteString(`<p class="muted text-sm">Each line below is a live check. Work down the list; the first unfinished one is the thing to fix next.</p>`)
	}
	b.WriteString(`<div class="vm-wizard-groups">`)
	for _, d := range h.Domains {
		if d.Health != nil {
			b.WriteString(vayuWizardGroup("Mail domain · "+d.Domain, d.Health.Records))
		}
	}
	b.WriteString(vayuWizardGroup("Mail host · deliverability", h.Deliverability))
	b.WriteString(`</div>`)
	if fix, ok := firstDNSUnfinished(h); ok {
		b.WriteString(`<p class="vm-wizard-next"><strong>Next:</strong> ` + html.EscapeString(fix) + `</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// vayuWizardGroup renders one titled group of checks.
func vayuWizardGroup(title string, rows []mail.RecordHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="vm-wizard-group"><div class="vm-wizard-group-title">` + html.EscapeString(title) + `</div><ul class="vm-wizard-list">`)
	if len(rows) == 0 {
		b.WriteString(`<li class="vm-wizard-item"><span class="vm-wizard-check">no checks reported</span></li>`)
	}
	for _, r := range rows {
		cls, mark := "vm-wizard-item--ok", "✓"
		detail := r.Found
		if !r.OK {
			cls, mark = "vm-wizard-item--todo", "•"
			if strings.TrimSpace(r.Message) != "" {
				detail = r.Message
			}
		}
		if strings.TrimSpace(detail) == "" {
			detail = "—"
		}
		b.WriteString(`<li class="vm-wizard-item ` + cls + `">` +
			`<span class="vm-wizard-mark" aria-hidden="true">` + mark + `</span>` +
			`<span class="vm-wizard-check">` + html.EscapeString(r.Type) + `</span>` +
			`<span class="vm-wizard-detail">` + html.EscapeString(detail) + `</span></li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// firstDNSUnfinished names the single next thing to fix, so the card ends with an
// instruction rather than a verdict.
func firstDNSUnfinished(h dnsHealth) (string, bool) {
	describe := func(what string, r mail.RecordHealth) string {
		if msg := strings.TrimSpace(r.Message); msg != "" {
			return what + " — " + msg
		}
		return what
	}
	for _, d := range h.Domains {
		if d.Health == nil {
			continue
		}
		for _, r := range d.Health.Records {
			if !r.OK {
				return describe(r.Type+" for "+d.Domain, r), true
			}
		}
	}
	for _, r := range h.Deliverability {
		if !r.OK {
			return describe(r.Type+" on the mail host", r), true
		}
	}
	return "", false
}

// vayuDNSWizardOOB renders the checklist for an out-of-band swap, so pressing
// "Re-check" refreshes the summary as well as the tables — a summary still showing
// the previous verdict beside fresh results is worse than no summary at all.
func vayuDNSWizardOOB(h dnsHealth) string {
	return strings.Replace(vayuDNSWizard(h), `id="vm-dns-wizard"`, `id="vm-dns-wizard" hx-swap-oob="true"`, 1)
}

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

// vayuDNSVerifyFragmentWith renders the "DNS verification — all domains" section: live
// MX/SPF/DKIM/DMARC alignment for the primary AND every mail_enabled secondary,
// plus the host-level deliverability self-check (HELO/DKIM-key/PTR, shared across
// domains). It is the HTMX swap target (#vm-dns-verify) and carries its own
// Re-check control, so a refresh re-runs every lookup without a full-page reload.
//
// It renders the reference tables from an already-computed
// verdict set, so the page pays for one round of lookups and the checklist above
// can never disagree with the tables below.
func vayuDNSVerifyFragmentWith(h dnsHealth) string {
	var tables strings.Builder
	for _, d := range h.Domains {
		if d.Health != nil {
			tables.WriteString(vayuDNSVerifyDomainTable(d.Domain, d.Health))
		}
	}

	var deliv strings.Builder
	for _, rh := range h.Deliverability {
		deliv.WriteString(vayuDNSVerifyRow(rh.Type, rh.OK, rh.Message))
	}

	pill := `<span class="badge badge--ok">all aligned</span>`
	if !h.AllOK {
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
	// No clearnet DNS lookups from a Tor Space (ADR-0141).
	if config.Cfg.OnionMode {
		writeAPIError(w, r, http.StatusServiceUnavailable, "onion-mode", "DNS checks are disabled in the Tor world (no clearnet lookups)", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	h := a.vayuDNSHealth(r.Context())
	writeOSFragment(w, vayuDNSVerifyFragmentWith(h)+vayuDNSWizardOOB(h))
}
