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
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/ui"
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
func vayuDNSWizard(lang string, h dnsHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="vm-wizard" id="vm-dns-wizard"><div class="vm-wizard-head">`)
	if h.AllOK {
		b.WriteString(string(ui.State("ok", i18n.T(lang, "All checks pass"))) + `<p class="vm-wizard-lead">` + esc(i18n.T(lang, "Every mail domain is aligned and the mail-host checks pass.")) + `</p>`)
	} else {
		b.WriteString(string(ui.State("warn", i18n.T(lang, "Needs attention"))) + `<p class="vm-wizard-lead">` + esc(i18n.T(lang, "Each line is a live check. The first unfinished one is the thing to fix next.")) + `</p>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="vm-wizard-groups">`)
	for _, d := range h.Domains {
		if d.Health != nil {
			b.WriteString(vayuWizardGroup(lang, i18n.T(lang, "Mail domain · {domain}", "domain", d.Domain), d.Health.Records))
		}
	}
	b.WriteString(vayuWizardGroup(lang, i18n.T(lang, "Mail host · deliverability"), h.Deliverability))
	b.WriteString(`</div>`)
	if fix, ok := firstDNSUnfinished(lang, h); ok {
		b.WriteString(`<p class="vm-wizard-next">` + escFill(i18n.T(lang, "{next} {fix}", "next", htmlMark(0), "fix", fix), `<strong>`+esc(i18n.T(lang, "Next:"))+`</strong>`) + `</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// vayuWizardGroup renders one titled group of checks.
func vayuWizardGroup(lang, title string, rows []mail.RecordHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="vm-wizard-group"><div class="vm-wizard-group-title">` + html.EscapeString(title) + `</div><ul class="vm-wizard-list">`)
	if len(rows) == 0 {
		b.WriteString(`<li class="vm-wizard-item"><span class="vm-wizard-check">` + esc(i18n.T(lang, "no checks reported")) + `</span></li>`)
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
func firstDNSUnfinished(lang string, h dnsHealth) (string, bool) {
	describe := func(what string, r mail.RecordHealth) string {
		if msg := strings.TrimSpace(r.Message); msg != "" {
			return i18n.T(lang, "{what} — {why}", "what", what, "why", msg)
		}
		return what
	}
	for _, d := range h.Domains {
		if d.Health == nil {
			continue
		}
		for _, r := range d.Health.Records {
			if !r.OK {
				return describe(i18n.T(lang, "{record} for {domain}", "record", r.Type, "domain", d.Domain), r), true
			}
		}
	}
	for _, r := range h.Deliverability {
		if !r.OK {
			return describe(i18n.T(lang, "{record} on the mail host", "record", r.Type), r), true
		}
	}
	return "", false
}

// vayuDNSWizardOOB renders the checklist for an out-of-band swap, so pressing
// "Re-check" refreshes the summary as well as the tables — a summary still showing
// the previous verdict beside fresh results is worse than no summary at all.
func vayuDNSWizardOOB(lang string, h dnsHealth) string {
	return strings.Replace(vayuDNSWizard(lang, h), `id="vm-dns-wizard"`, `id="vm-dns-wizard" hx-swap-oob="true"`, 1)
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
	return `<details class="vm-sec"` + o + `>
  <summary class="vm-sec__head"><span class="vm-sec__title">` + html.EscapeString(title) + `</span>` + metaHTML + `<span class="vm-sec__chev" aria-hidden="true">▾</span></summary>
  <div class="vm-sec__body">` + inner + `</div>
</details>`
}

// vayuDNSCopyBtn renders a copy-to-clipboard control carrying the record value in
// a data attribute (escaped so the long DKIM value stays attribute-safe).
func vayuDNSCopyBtn(lang, val string) string {
	return `<button type="button" class="vm-copy" data-copy="` + html.EscapeString(val) + `" data-copied="` + esc(i18n.T(lang, "Copied")) + `" aria-label="` + esc(i18n.T(lang, "Copy value")) + `">` + esc(i18n.T(lang, "Copy")) + `</button>`
}

// vayuDNSRecordsSection renders one domain's "records to publish" collapsible: the
// MX/SPF/DKIM/DMARC table with a copy button on each value.
func vayuDNSRecordsSection(lang, title, subtitle string, recs []mail.DNSRecord, open bool) string {
	var rows strings.Builder
	for _, rec := range recs {
		rows.WriteString(`<tr><td><span class="vm-tag">` + html.EscapeString(rec.Type) + `</span></td>` +
			`<td class="mono text-sm">` + html.EscapeString(rec.Name) + `</td>` +
			`<td class="mono text-sm vm-break"><span class="vm-rec-val">` + html.EscapeString(rec.Value) + `</span>` + vayuDNSCopyBtn(lang, rec.Value) + `</td></tr>`)
	}
	inner := `<p class="muted text-sm">` + html.EscapeString(subtitle) + `</p>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>` + esc(i18n.T(lang, "Type")) + `</th><th>` + esc(i18n.T(lang, "Name")) + `</th><th>` + esc(i18n.T(lang, "Value")) + `</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>`
	return vayuDNSCollapsible(title, "", open, inner)
}

// vayuMailHostSection is the record set the old tab never showed: the A/AAAA that
// make the mail host reachable, the reverse-DNS (PTR) alignment, and the inbound
// ports — plus the load-bearing warning that mail cannot be proxied, so these
// must be published DNS-only (on Cloudflare, the grey cloud, never the orange).
func vayuMailHostSection(lang string, mc mail.Config) string {
	host := html.EscapeString(mc.Hostname)
	mono := func(t string) string { return `<span class="mono">` + t + `</span>` }
	dnsOnly := esc(i18n.T(lang, "DNS only"))
	inner := `<p class="muted text-sm">` + escFill(i18n.T(lang, "Mail runs over SMTP/IMAP/POP3, which {proxy}. These records make your mail host reachable — publish them {dnsonly}. On Cloudflare that is the {grey}; a proxied (orange-cloud) mail host silently breaks inbound mail.",
		"proxy", htmlMark(0), "dnsonly", htmlMark(1), "grey", htmlMark(2)), `<strong>`+esc(i18n.T(lang, "cannot be proxied"))+`</strong>`, `<strong>`+esc(i18n.T(lang, "DNS-only"))+`</strong>`, `<strong>`+esc(i18n.T(lang, "grey cloud"))+`</strong>`) + `</p>
<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>` + esc(i18n.T(lang, "Type")) + `</th><th>` + esc(i18n.T(lang, "Name")) + `</th><th>` + esc(i18n.T(lang, "Value")) + `</th><th>` + esc(i18n.T(lang, "Proxy")) + `</th></tr></thead><tbody>
<tr><td><span class="vm-tag">A</span></td><td class="mono text-sm">` + host + `</td><td class="mono text-sm">` + esc(i18n.T(lang, "your server's public IPv4")) + `</td><td>` + dnsOnly + `</td></tr>
<tr><td><span class="vm-tag">` + recAAAA + `</span></td><td class="mono text-sm">` + host + `</td><td class="mono text-sm">` + esc(i18n.T(lang, "your server's public IPv6 (if any)")) + `</td><td>` + dnsOnly + `</td></tr>
<tr><td><span class="vm-tag">` + recPTR + `</span></td><td class="mono text-sm">` + esc(i18n.T(lang, "reverse DNS of your IP")) + `</td><td class="mono text-sm">` + host + `</td><td><span class="muted text-xs">` + esc(i18n.T(lang, "set at your VPS host")) + `</span></td></tr>
</tbody></table></div>
<p class="muted text-xs">` + escFill(i18n.T(lang, "Open inbound ports {smtp} (SMTP), {submission} (submission), {imaps} (IMAPS) and {pop3s} (POP3S) on your server firewall.",
		"smtp", htmlMark(0), "submission", htmlMark(1), "imaps", htmlMark(2), "pop3s", htmlMark(3)), mono("25"), mono("465/587"), mono("993"), mono("995")) + `</p>`
	return vayuDNSCollapsible(i18n.T(lang, "Mail host & networking — {host}", "host", mc.Hostname), string(ui.State("warn", i18n.T(lang, "Do not proxy"))), true, inner)
}

// vayuDNSPublishSections builds the "records to publish" collapsibles — the
// primary (expanded) plus one per mail_enabled secondary (collapsed) — followed
// by the mail-host & networking section. On a single-domain install only the
// primary and host sections render, so nothing changes for a plain setup.
func (a *App) vayuDNSPublishSections(r *http.Request, mc mail.Config) string {
	lang := i18n.Lang(r.Context())
	var b strings.Builder
	// Named by domain alone: the page section around them is already
	// "Records to publish".
	b.WriteString(vayuDNSRecordsSection(lang,
		mc.Domain,
		i18n.T(lang, "Publish these at your DNS provider for {domain}. They route your mail (MX) and authenticate it (SPF, DKIM, DMARC).", "domain", mc.Domain),
		a.vayuMail.PlannedRecords(), true))
	for _, secHost := range a.mailSecondaryHosts(r.Context()) {
		sub := i18n.T(lang, "Secondary mail domain. Its MX points at this install's mail host ({host}); the DKIM key is shared with the primary, so publish the same key value at {record}.",
			"host", mc.Hostname, "record", mc.DKIMSelector+"._domainkey."+secHost)
		b.WriteString(vayuDNSRecordsSection(lang, secHost, sub, a.vayuMail.PlannedRecordsForDomain(secHost), false))
	}
	b.WriteString(vayuMailHostSection(lang, mc))
	return b.String()
}

// vayuDNSVerifyRow renders one verification row (record/check, status badge,
// detail).
func vayuDNSVerifyRow(lang, typ string, ok bool, detail string) string {
	badge := string(ui.State("ok", i18n.T(lang, "OK")))
	if !ok {
		badge = string(ui.State("warn", i18n.T(lang, "To do")))
	}
	return `<tr><td>` + html.EscapeString(typ) + `</td><td>` + badge + `</td><td class="muted text-sm vm-break">` + html.EscapeString(detail) + `</td></tr>`
}

// vayuDNSVerifyDomainTable renders one domain's live MX/SPF/DKIM/DMARC alignment.
func vayuDNSVerifyDomainTable(lang, domain string, hc *mail.DomainHealth) string {
	var rows strings.Builder
	for _, rh := range hc.Records {
		detail := rh.Found
		if !rh.OK && rh.Message != "" {
			detail = rh.Message
		}
		rows.WriteString(vayuDNSVerifyRow(lang, rh.Type, rh.OK, detail))
	}
	pill := string(ui.State("ok", i18n.T(lang, "Aligned")))
	if !hc.AllOK {
		pill = string(ui.State("warn", i18n.T(lang, "Check records")))
	}
	return `<div class="vm-verify-dom"><h4 class="vm-sub-title">` + html.EscapeString(domain) + ` ` + pill + `</h4>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>` + esc(i18n.T(lang, "Record")) + `</th><th>` + esc(i18n.T(lang, "Status")) + `</th><th>` + esc(i18n.T(lang, "Found")) + `</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
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
func vayuDNSVerifyFragmentWith(lang string, h dnsHealth) string {
	var tables strings.Builder
	for _, d := range h.Domains {
		if d.Health != nil {
			tables.WriteString(vayuDNSVerifyDomainTable(lang, d.Domain, d.Health))
		}
	}

	var deliv strings.Builder
	for _, rh := range h.Deliverability {
		deliv.WriteString(vayuDNSVerifyRow(lang, rh.Type, rh.OK, rh.Message))
	}

	pill := string(ui.State("ok", i18n.T(lang, "All aligned")))
	if !h.AllOK {
		pill = string(ui.State("warn", i18n.T(lang, "Needs attention")))
	}

	inner := `<div class="vm-verify-head">` + pill +
		`<button type="button" class="btn btn--sm" hx-get="/os/vayumail/dns/verify" hx-target="#vm-dns-verify" hx-swap="outerHTML" hx-indicator="#vm-dns-spin">` + esc(i18n.T(lang, "Re-check")) + `</button>` +
		`<span id="vm-dns-spin" class="htmx-indicator vm-spin" aria-hidden="true">` + esc(i18n.T(lang, "checking…")) + `</span></div>` +
		tables.String() +
		`<div class="vm-verify-dom"><h4 class="vm-sub-title">` + esc(i18n.T(lang, "Deliverability (mail host)")) + `</h4>` +
		`<p class="muted text-xs">` + esc(i18n.T(lang, "The things that most often send legitimate mail to spam — every row should read ok.")) + `</p>` +
		`<div class="table-wrap"><table class="table vm-dns-table"><thead><tr><th>` + esc(i18n.T(lang, "Check")) + `</th><th>` + esc(i18n.T(lang, "Status")) + `</th><th>` + esc(i18n.T(lang, "Detail")) + `</th></tr></thead><tbody>` + deliv.String() + `</tbody></table></div></div>`

	// The fragment root carries the swap id and is itself the collapsible section,
	// so an HTMX outerHTML swap replaces the whole section (button included) with
	// freshly-checked results.
	return `<div id="vm-dns-verify">` + inner + `</div>`
}

// vayuDNSScript is the CSP-nonce'd copy-to-clipboard handler for the record
// values (delegated, so it also covers HTMX-swapped content).
func vayuDNSScript(nonce string) string {
	return `<script nonce="` + nonce + `">` + vayuDNSCopyScript + `</script>`
}

// vayuDNSCopyScript is the handler itself: code, kept apart from the page's
// sentences. The word it shows once a value is copied is the button's own
// data-copied, in the page's language.
const vayuDNSCopyScript = `
(function(){'use strict';
document.addEventListener('click',function(e){
  var b=e.target.closest('[data-copy]');if(!b)return;
  var v=b.getAttribute('data-copy')||'';
  function done(){var t=b.getAttribute('data-label')||b.textContent;b.setAttribute('data-label',t);b.textContent=b.getAttribute('data-copied')||b.textContent;setTimeout(function(){b.textContent=t;},1200);}
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(v).then(done,done);}
  else{try{var ta=document.createElement('textarea');ta.value=v;document.body.appendChild(ta);ta.select();document.execCommand('copy');document.body.removeChild(ta);done();}catch(_){}}
});
})();
`

// recAAAA and recPTR are record types, which are not translated.
const recAAAA, recPTR = "AAAA", "PTR"

// handleVayuOSMailDNSVerify serves the live DNS-verification fragment for the
// HTMX "Re-check" control (administrator-only, like the DNS tab).
func (a *App) handleVayuOSMailDNSVerify(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", i18n.T(i18n.Lang(r.Context()), "VayuMail is not active"), "")
		return
	}
	// No clearnet DNS lookups from a Tor Space (ADR-0141).
	if config.Cfg.OnionMode {
		writeAPIError(w, r, http.StatusServiceUnavailable, "onion-mode", i18n.T(i18n.Lang(r.Context()), "DNS checks are disabled in the Tor world (no clearnet lookups)"), "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "administrators only"), "")
		return
	}
	h := a.vayuDNSHealth(r.Context())
	lang := i18n.Lang(r.Context())
	writeOSFragment(w, vayuDNSVerifyFragmentWith(lang, h)+vayuDNSWizardOOB(lang, h))
}
