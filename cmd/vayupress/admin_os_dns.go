// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"html"
	htmpl "html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
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
//
// EVERY HOSTED DOMAIN, NOT JUST THE PRIMARY
// VayuPress hosts many domains from one binary, and this page originally showed
// only the primary. That is the same silent-hole failure one level up: a
// secondary domain could be missing its whole set of records — no key discovery,
// no mail — and the page an operator opens to check DNS would show nothing but
// green. Worse, it made a real misconfiguration invisible: a domain parked on
// sync hold is deliberately skipped by every provisioning helper, so its
// certificate is never issued, and nothing anywhere said so. Every registered
// domain now gets its own section, and a held one says why it was skipped.
type dnsRecord struct {
	Host     string // full hostname
	Label    string // what it unlocks
	Why      string // one line on what breaks without it
	Required bool
	ProxyOff bool // CDN proxy must be off
}

// dnsState is what a record's resolution actually tells us.
type dnsState int

const (
	dnsNotPointed  dnsState = iota
	dnsPointedHere          // resolves to an address this machine holds — definitive
	dnsProxied              // resolves to the same addresses as the proxied apex
	dnsUnverified           // resolves, but this machine cannot prove it is the target
	dnsUnknown              // the lookup did not finish — we know nothing, and say so
)

// dnsCheck is a record plus its live resolution result.
type dnsCheck struct {
	dnsRecord
	State dnsState
	Addrs []string // what it resolves to
}

// dnsDomainView is one hosted domain and everything this page knows about it.
type dnsDomainView struct {
	Host         string
	IsPrimary    bool
	MailEnabled  bool
	SyncApproved bool
	TLSState     string
	Checks       []dnsCheck
}

// NeedsAttention reports whether anything about this domain should be surfaced
// rather than folded away. It drives which sections open by default: what is
// wrong is visible without a click, what is fine stays quiet.
func (v dnsDomainView) NeedsAttention() bool {
	if !v.IsPrimary && !v.SyncApproved {
		return true
	}
	if v.NeedsCertificate() {
		return true
	}
	for _, c := range v.Checks {
		if c.State == dnsProxied || (c.Required && c.State == dnsNotPointed) {
			return true
		}
	}
	return false
}

// Resolves reports whether this domain's own apex answers a lookup at all.
func (v dnsDomainView) Resolves() bool {
	for _, c := range v.Checks {
		if c.Host != v.Host {
			continue
		}
		return c.State != dnsNotPointed && c.State != dnsUnknown
	}
	return false
}

// NeedsCertificate reports the one state that looks finished and is not: DNS
// pointed here, and no certificate ever issued for it.
//
// That combination is what an operator hits after doing their half correctly.
// The records go green, every tile reads clean, and the site serves a browser
// security interstitial — because nginx has no vhost for the host, so the
// request falls through to the default one and is answered with the primary's
// certificate. This page already held the answer: dnsDomainView carries
// TLSState for every domain and rendered it nowhere.
//
// Four states are deliberately NOT this, because complaining about them would
// be inventing a fault:
//   - DNS not pointed, or the lookup unfinished — the record's own row says so,
//     and a certificate cannot be issued until it is pointed anyway.
//   - a domain on manual hold — not provisioning it is the point of the hold,
//     and the hold notice already explains it.
//   - the primary — its certificate is managed outside the registry.
//   - a TLS state we do not recognise. Only `pending` and `failed` positively
//     mean no certificate; anything else is a state we cannot read, and this
//     page does not assert faults it cannot substantiate — the same rule that
//     keeps an unfinished lookup out of the "not pointed" column.
func (v dnsDomainView) NeedsCertificate() bool {
	if v.IsPrimary || !v.SyncApproved {
		return false
	}
	if v.TLSState != domain.TLSPending && v.TLSState != domain.TLSFailed {
		return false
	}
	return v.Resolves()
}

// localAddrSet returns the non-loopback addresses this machine holds.
//
// This is the ground truth for "points HERE", and it replaces an earlier check
// that compared each subdomain against the APEX — which was not merely
// imprecise, it was inverted. On any CDN-fronted install the apex resolves to
// the proxy while the direct subdomains resolve to the origin, exactly as the
// documentation instructs. Comparing the two therefore reported a correctly
// configured install as broken, and sent the operator to fix something that was
// already right. A check that cries wolf on the correct configuration is worse
// than no check at all, because it trains people to ignore it.
func localAddrSet() map[string]bool {
	out := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out[ip.String()] = true
		}
	}
	return out
}

// dnsLookupTimeout bounds the whole page's resolution work, and dnsLookupWorkers
// bounds how much of it runs at once.
//
// Both matter now that the page covers every hosted domain rather than one. An
// install with twenty domains asks for well over a hundred lookups, and firing
// them all simultaneously is how a resolver starts refusing — which would show
// up here as "not pointed" against records that are perfectly fine.
const (
	dnsLookupTimeout = 8 * time.Second
	dnsLookupWorkers = 12
)

// hostIsRegistrableApex reports whether `www.<host>` is a record the operator
// could plausibly own.
//
// www is a convention of a REGISTRABLE name — example.com, example.co.uk — not
// of an arbitrary host. A site hosted at test.example.com has no
// www.test.example.com and never will, so listing one as required painted a
// permanent warn badge on a correctly configured domain, held its section open
// on every visit, and put it in the "Not pointed" count. Label counting cannot
// answer this (example.co.uk is an apex with three labels), so the public suffix
// list does.
//
// A host that IS a public suffix, or is otherwise unplaceable, is treated as not
// an apex: `www.<public suffix>` is not the operator's to create either, and
// where we cannot substantiate the demand we do not make it.
func hostIsRegistrableApex(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" {
		return false
	}
	etld1, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return false
	}
	return etld1 == h
}

// subdomainRecords returns every record this install cares about for a domain.
//
// The primary carries the install-wide services; a secondary carries only what
// is actually served for it. Listing talk./mcp./api. under every secondary would
// be inventing work for the operator: those hosts exist once per install, on the
// primary, and a second copy would have nothing behind it. The same reasoning
// governs www: it is listed only where it could exist.
func subdomainRecords(dom string, isPrimary, mailEnabled bool) []dnsRecord {
	d := strings.TrimSpace(strings.ToLower(dom))
	recs := []dnsRecord{
		{Host: d, Label: "Website & blog", Why: "The site itself.", Required: true},
	}
	if hostIsRegistrableApex(d) {
		recs = append(recs,
			dnsRecord{Host: "www." + d, Label: "www redirect", Why: "Visitors typing www reach the site.", Required: true})
	}
	if isPrimary || mailEnabled {
		recs = append(recs,
			dnsRecord{Host: "mail." + d, Label: "VayuMail", Why: "Without it mail does not arrive at all — a proxy cannot carry SMTP or IMAP.", ProxyOff: true},
			dnsRecord{Host: "openpgpkey." + d, Label: "VayuPGP key discovery", Why: "Without it key lookup fails SILENTLY and correspondents fall back to unencrypted mail.", ProxyOff: true},
		)
	}
	if isPrimary {
		recs = append(recs,
			dnsRecord{Host: "talk." + d, Label: "VayuTalk", Why: "Without it the live message stream is buffered or challenged, so the app sends but never receives.", ProxyOff: true},
			dnsRecord{Host: "mcp." + d, Label: "VayuMCP", Why: "Without it an MCP client receives a bot-challenge page it has no way to answer.", ProxyOff: true},
			dnsRecord{Host: "api." + d, Label: "VayuAPI", Why: "Without it scripts, CI jobs and agents hit a challenge page instead of JSON.", ProxyOff: true},
		)
	}
	return recs
}

// resolveAll looks up every record of every hosted domain and classifies each,
// sharing one address set, one deadline and one worker pool across the lot.
func resolveAll(ctx context.Context, views []dnsDomainView, apex string) {
	ctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()

	var resolver net.Resolver
	// lookup returns the addresses, and separately whether the answer is
	// trustworthy. A lookup killed by the deadline must never be reported as
	// "not pointed" — that is an assertion we cannot substantiate, about a
	// record that may well be correct.
	lookup := func(host string) (addrs []string, ok bool) {
		a, err := resolver.LookupHost(ctx, host)
		if err == nil {
			return a, true
		}
		var dnsErr *net.DNSError
		if ctx.Err() != nil || (errors.As(err, &dnsErr) && (dnsErr.IsTimeout || dnsErr.IsTemporary)) {
			return nil, false
		}
		return nil, true
	}

	local := localAddrSet()

	// The apex's addresses are used only to RECOGNISE A PROXY, never as a
	// definition of "here". If the apex resolves somewhere this machine is not,
	// something in front of it is answering — a CDN — and a direct-only subdomain
	// sharing those addresses is proxied too, which is the misconfiguration that
	// breaks it silently.
	apexAddrs := map[string]bool{}
	apexIsLocal := false
	apexResolved, _ := lookup(apex)
	for _, a := range apexResolved {
		apexAddrs[a] = true
		if local[a] {
			apexIsLocal = true
		}
	}
	apexProxied := len(apexAddrs) > 0 && !apexIsLocal && len(local) > 0

	type job struct{ d, r int }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < dnsLookupWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rec := views[j.d].Checks[j.r].dnsRecord
				addrs, ok := lookup(rec.Host)
				c := dnsCheck{dnsRecord: rec, Addrs: addrs}
				switch {
				case !ok:
					c.State = dnsUnknown
				case len(addrs) == 0:
					c.State = dnsNotPointed
				default:
					for _, a := range addrs {
						if local[a] {
							c.State = dnsPointedHere
							break
						}
					}
					if c.State != dnsPointedHere {
						shares := false
						for _, a := range addrs {
							if apexAddrs[a] {
								shares = true
								break
							}
						}
						if shares && apexProxied && rec.ProxyOff {
							c.State = dnsProxied
						} else {
							// Resolves, but this machine cannot prove it is the
							// target — normal behind NAT, where the public address
							// is not on any local interface. Reported as unverified
							// rather than wrong, because asserting a fault we cannot
							// substantiate is how a status page loses its credibility.
							c.State = dnsUnverified
						}
					}
				}
				views[j.d].Checks[j.r] = c
			}
		}()
	}
	for d := range views {
		for r := range views[d].Checks {
			jobs <- job{d, r}
		}
	}
	close(jobs)
	wg.Wait()
}

// hostedDomainViews builds the page's work list: the primary from config (it is
// provisioned outside the registry and must appear even on an install that has
// never registered a secondary), then every active registered domain.
func (a *App) hostedDomainViews(ctx context.Context, primary string) []dnsDomainView {
	views := []dnsDomainView{{
		Host: strings.ToLower(primary), IsPrimary: true, MailEnabled: true, SyncApproved: true,
	}}
	seen := map[string]bool{views[0].Host: true}

	if a.domains != nil {
		if list, err := a.domains.List(ctx); err == nil {
			for _, d := range list {
				h := strings.ToLower(strings.TrimSpace(d.Host))
				if h == "" || d.Status != domain.StatusActive {
					continue
				}
				if d.IsPrimary {
					// Already present from config; carry its recorded TLS state.
					if seen[h] {
						views[0].TLSState = d.TLSState
						continue
					}
				}
				if seen[h] {
					continue
				}
				seen[h] = true
				views = append(views, dnsDomainView{
					Host:         h,
					IsPrimary:    d.IsPrimary,
					MailEnabled:  d.MailEnabled,
					SyncApproved: d.IsSyncApproved(),
					TLSState:     d.TLSState,
				})
			}
		}
	}

	for i := range views {
		recs := subdomainRecords(views[i].Host, views[i].IsPrimary, views[i].MailEnabled)
		views[i].Checks = make([]dnsCheck, len(recs))
		for j, rec := range recs {
			views[i].Checks[j] = dnsCheck{dnsRecord: rec}
		}
	}
	return views
}

// dnsRecordState is one record's state as a dot and a word.
func dnsRecordState(c dnsCheck) ui.HTML {
	switch {
	case c.State == dnsPointedHere:
		return ui.State("ok", "Pointed here")
	case c.State == dnsProxied:
		// The real misconfiguration, and the only one worth a warning: a
		// machine-to-machine host sitting behind the same proxy as the apex. It
		// resolves, a certificate may even issue, and the service still fails —
		// because the client cannot answer a bot challenge.
		return ui.State("warn", "Behind the proxy")
	case c.State == dnsUnverified:
		return ui.State("ok", "Resolving")
	case c.State == dnsUnknown:
		return ui.State("neutral", "Not checked")
	case c.Required:
		return ui.State("warn", "Not pointed")
	default:
		return ui.State("neutral", "Not pointed")
	}
}

// resolving counts the records that answer, here or behind NAT.
func (v dnsDomainView) resolving() int {
	n := 0
	for _, c := range v.Checks {
		if c.State == dnsPointedHere || c.State == dnsUnverified {
			n++
		}
	}
	return n
}

// state is the one word a domain's row carries: the thing most in need of the
// operator, or that all is well. The order is the order of the fix: a held
// domain is approved first, then certified, then its records corrected.
func (v dnsDomainView) state() ui.HTML {
	if !v.IsPrimary && !v.SyncApproved {
		return ui.State("warn", "On hold")
	}
	if v.NeedsCertificate() {
		return ui.State("warn", "No certificate")
	}
	unknown := false
	for _, c := range v.Checks {
		if c.State == dnsProxied {
			return ui.State("warn", "Behind the proxy")
		}
		unknown = unknown || c.State == dnsUnknown
	}
	for _, c := range v.Checks {
		if c.Required && c.State == dnsNotPointed {
			return ui.State("warn", "Not pointed")
		}
	}
	if unknown {
		return ui.State("neutral", "Not checked")
	}
	return ui.State("ok", "Pointed")
}

// dnsInspector renders one hosted domain in the inspector: what is wrong with
// it in words that name the next action, then each of its records.
func dnsInspector(v dnsDomainView) string {
	var b strings.Builder
	meta := "A hosted site"
	if v.IsPrimary {
		meta = "The primary site: install-wide services live here"
	}
	if !v.IsPrimary && v.MailEnabled {
		meta += " · mail on"
	}
	b.WriteString(`<div class="sa-insp__title mono">` + html.EscapeString(v.Host) + `</div><div class="sa-insp__meta">` + meta + `</div>`)

	// A held domain is skipped by every provisioning helper by design. Saying so
	// here is the entire point: the previous behaviour was to skip it in silence,
	// which is indistinguishable from having provisioned it.
	if !v.IsPrimary && !v.SyncApproved {
		b.WriteString(`<p class="text-sm">This site is on <strong>manual hold</strong>, so no certificate or vhost is issued for it and its records are informational only. Approve it under <a href="/os/domains">Sites</a> (Sync now), then press <strong>Provision subdomains</strong>.</p>`)
	}

	// The state that reads as finished and is not. Pointing DNS is the operator's
	// half; issuing the certificate is the privileged helper's, and until it runs
	// nginx has no vhost for this host — so the request falls through to the
	// default one and the browser is handed the primary's certificate. Every
	// record is green while the site shows a security warning, which is
	// precisely the silent failure this page exists to catch.
	if v.NeedsCertificate() {
		reason := `no certificate has been issued for it yet`
		if v.TLSState == domain.TLSFailed {
			reason = `the last attempt to issue its certificate <strong>failed</strong>`
		}
		b.WriteString(`<p class="text-sm"><strong>No certificate: ` + html.EscapeString(v.Host) + ` resolves here, but ` + reason + `.</strong> ` +
			`Until one exists there is no vhost for this host, so a visitor is served the primary ` +
			`domain's certificate and the browser refuses the page ` +
			`(<code>ERR_CERT_COMMON_NAME_INVALID</code>). Press <strong>Provision subdomains</strong>: ` +
			`the DNS is pointed now, which is the condition the last run was waiting for. It also runs daily on its own.</p>`)
	}

	b.WriteString(`<ul class="sa-records">`)
	for _, c := range v.Checks {
		b.WriteString(`<li><div class="sa-records__head"><code class="mono">` + html.EscapeString(c.Host) + `</code>` + string(dnsRecordState(c)) + `</div>` +
			`<div class="text-xs muted">` + html.EscapeString(c.Label) + `. ` + html.EscapeString(c.Why))
		if c.ProxyOff {
			b.WriteString(` The CDN proxy must be <strong>off</strong> (DNS only).`)
		}
		if len(c.Addrs) > 0 {
			b.WriteString(` Resolves to <span class="mono">` + html.EscapeString(strings.Join(c.Addrs, ", ")) + `</span>.`)
		}
		b.WriteString(`</div></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// dnsList renders the page for resolved views: a row per hosted domain, the
// selected one's records in the inspector, and the legend folded below.
func dnsList(views []dnsDomainView, primary, view, q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	attention := 0
	for _, v := range views {
		if v.NeedsAttention() {
			attention++
		}
	}
	var shown []dnsDomainView
	for _, v := range views {
		if (view == "attention" && !v.NeedsAttention()) || (q != "" && !strings.Contains(v.Host, q)) {
			continue
		}
		shown = append(shown, v)
	}
	href := func(v string) string {
		u := url.Values{}
		if v != "" {
			u.Set("view", v)
		}
		if q != "" {
			u.Set("q", q)
		}
		if enc := u.Encode(); enc != "" {
			return "/os/dns?" + enc
		}
		return "/os/dns"
	}
	keep := [][2]string{}
	if view == "attention" {
		keep = append(keep, [2]string{"view", view})
	}

	var list, inspector string
	if len(shown) == 0 {
		list = `<p class="table-empty">No domain matches that. <a href="/os/dns">Show every domain</a>.</p>`
	} else {
		var rows, panels strings.Builder
		for i, v := range shown {
			key := "dns-" + strconv.Itoa(i)
			sel, hidden := "false", " hidden"
			if i == 0 {
				sel, hidden = "true", ""
			}
			role := ""
			if v.IsPrimary {
				role = ` <span class="muted">primary</span>`
			}
			rows.WriteString(`<tr class="post-row" data-list-row data-list-panel="` + key + `" tabindex="0" aria-selected="` + sel + `">` +
				`<td><span class="post-row__name mono">` + html.EscapeString(v.Host) + `</span>` + role + `</td>` +
				`<td class="post-row__date">` + strconv.Itoa(v.resolving()) + ` of ` + strconv.Itoa(len(v.Checks)) + ` resolve</td>` +
				`<td>` + string(v.state()) + `</td></tr>`)
			panels.WriteString(`<div data-list-panel-id="` + key + `"` + hidden + `>` + dnsInspector(v) + `</div>`)
		}
		list = `<div class="table-wrap"><table class="table post-table"><thead><tr><th>Domain</th><th class="sa-col--count">Records</th><th class="sa-col--state">State</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div>`
		inspector = panels.String()
	}

	legend := `<p><strong>Pointed here</strong>: resolves to an address this server holds. <strong>Resolving</strong>: resolves, but this server cannot prove it is the target; normal behind NAT, and not a fault. <strong>Behind the proxy</strong>: resolves to the same front as your apex, so a bot challenge sits in front of a client that cannot answer one; switch it to DNS only. <strong>Not checked</strong>: the lookup did not finish in time, so nothing is claimed either way.</p>
<p>Direct hosts must be <strong>DNS only</strong> (in Cloudflare, the grey cloud). Every one of them is machine-to-machine: a mail server, GnuPG, a CI job and an MCP client have no JavaScript engine, so a proxy's “checking your browser” page stops them dead. Your apex and <code>www</code> keep full protection for human traffic. <code>talk</code>, <code>mcp</code> and <code>api</code> are install-wide and live on the primary only.</p>`
	// What a correspondent's PGP client actually requests, to verify from any
	// machine. Key discovery is per domain: a key is findable only at its own
	// domain's openpgpkey host, with its own certificate.
	if vpgp.WKDURL("you@"+primary) != "" {
		legend += `<p>To check key discovery from any machine: <code class="mono">gpg --locate-keys you@` + html.EscapeString(primary) + `</code>. Per-mailbox addresses are under VayuPGP.</p>`
	}
	list += string(ui.Explain(ui.HTML(legend)))

	return string(ui.List(ui.ListPage{
		Title: "Domains & DNS",
		Count: strconv.Itoa(len(views)),
		Views: ui.Segments("Show",
			ui.Segment{Label: "All", Href: href(""), Count: len(views), On: view != "attention"},
			ui.Segment{Label: "Needs attention", Href: href("attention"), Count: attention, On: view == "attention"}),
		Search: ui.Search(ui.SearchBox{Action: "/os/dns", Name: "q", Value: q, Placeholder: "Search domains", Keep: keep}),
		Actions: ui.HTML(`<button type="button" class="btn btn--primary" data-sheet="dns-provision">Provision subdomains</button>` +
			`<a class="btn btn--ghost" href="/os/domains">Sites</a>`),
		Sub: ui.HTML(`Every record each site needs, and whether it points here.` +
			string(ui.Tip("Each subdomain unlocks one product and is optional, but a missing one fails quietly, so this page checks rather than assumes."))),
	}, ui.HTML(list), ui.HTML(inspector)))
}

// handleOSDNS renders the Domains & DNS page.
func (a *App) handleOSDNS(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, osHome)
		return
	}

	primary := strings.TrimSpace(config.Cfg.Domain)
	if primary == "" || primary == "localhost" {
		writeOSHTML(w, r, adminOSLayout(nonce, "Domains & DNS", "vayuos", cfg, ui.List(ui.ListPage{Title: "Domains & DNS"},
			ui.Empty("globe", "No domain yet", "Set DOMAIN and restart, and every record this install needs is checked here.", ""), "")))
		return
	}

	views := a.hostedDomainViews(r.Context(), primary)
	resolveAll(r.Context(), views, primary)

	// The provisioning control is the same one Update & Backup carries, because
	// this is the page an operator lands on when a record is wrong, and sending
	// them somewhere else to act on it is how a fix gets postponed.
	body := dnsList(views, primary, r.URL.Query().Get("view"), r.URL.Query().Get("q")) +
		string(ui.Sheet("dns-provision", "Provision subdomains", ui.HTML(provisionCardHTML()))) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-update.js?v=` + assetVer("js/admin-os-update.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Domains & DNS", "vayuos", cfg, htmpl.HTML(body)))
}
