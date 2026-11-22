// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_waf.go — the web application firewall (internal/vayushield/waf)
// in the request path and on the Shield page.
//
// It runs after VayuShield's own gates, so a request Shield refused or
// challenged is never read twice, and only on the public site: the console,
// requests that carry an API key, Shield's own endpoints, health and static
// files are left alone. The console is an authenticated editor whose posts
// are HTML by design; reading them with an injection rule set would refuse
// the operator's own writing.

import (
	"context"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield/waf"
)

// bootWAF makes the firewall and applies the stored mode. Always leaves
// a.waf non-nil, so the middleware asks it unconditionally; off, it reads
// nothing.
func (a *App) bootWAF(ctx context.Context) {
	a.waf = waf.New()
	a.waf.Mask = func(ip string) string {
		if p := net.ParseIP(ip); p != nil {
			return signin.Mask(p)
		}
		return ""
	}
	a.applyWAFSettings(ctx)
}

// applyWAFSettings reads the mode and paranoia level and rebuilds the rule
// set when either changed. A build that fails leaves the firewall as it was
// and says so in the log.
func (a *App) applyWAFSettings(ctx context.Context) {
	if a.waf == nil || a.siteSettings == nil {
		return
	}
	get := func(k string) string { return a.siteSettings.Get(ctx, settings.ForPrimary(), k) }
	pl, _ := strconv.Atoi(strings.TrimSpace(get(settings.KeyShieldWAFParanoia)))
	if err := a.waf.Configure(waf.ParseMode(get(settings.KeyShieldWAF)), pl); err != nil {
		logging.LogError("vayushield", "the web application firewall's rule set did not build", err.Error())
	}
}

// wafExempt is a request the firewall does not read.
func wafExempt(r *http.Request) bool {
	p := r.URL.Path
	for _, pre := range []string{"/os", "/static", "/assets", "/__vayushield", "/health", "/metrics", "/.well-known", "/mcp", "/oauth", "/api/v1/community"} {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	// An API request with a key is the operator's own tooling.
	return strings.HasPrefix(p, "/api/") && (r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "")
}

// wafMiddleware reads public requests through the firewall and answers a
// refused one 403.
func (a *App) wafMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.waf == nil || a.waf.Mode() == waf.ModeOff || wafExempt(r) || a.isTrustedOperator(r) {
			next.ServeHTTP(w, r)
			return
		}
		if a.waf.Inspect(r, auth.ClientIP(r)) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("This request was refused by the site's firewall.\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// postedWAF reads the firewall's fields from a Shield settings save.
func postedWAF(r *http.Request) (mode, paranoia string) {
	pl := strings.TrimSpace(r.PostFormValue("sh_waf_pl"))
	if n, err := strconv.Atoi(pl); err != nil || n < 1 || n > 4 {
		pl = "1"
	}
	return string(waf.ParseMode(r.PostFormValue("sh_waf"))), pl
}

// wafBand is the firewall's part of the Shield settings form.
func (a *App) wafBand() string {
	mode, pl := waf.ModeOff, 1
	if a.waf != nil {
		mode = a.waf.Mode()
		if p := a.waf.Paranoia(); p > 0 {
			pl = p
		}
	}
	opt := func(v, label string, on bool) string {
		sel := ""
		if on {
			sel = " selected"
		}
		return `<option value="` + v + `"` + sel + `>` + label + `</option>`
	}
	var b strings.Builder
	b.WriteString(`<div class="card-title vs-section">Web application firewall</div>`)
	b.WriteString(`<p class="muted text-sm">The OWASP Core Rule Set, run by Coraza, reads each public request that carries something to read, a query, a form, an unusual path or header, for SQL and code injection, cross-site scripting, path traversal and protocol abuse. <strong>Record</strong> lists what it matches and refuses nothing: start there and read the matches below before choosing <strong>Refuse</strong>. The console and API-key requests are never read. Paranoia 1 has the fewest false positives; each level above catches more and mistakes more.</p>`)
	b.WriteString(`<div class="vs-adv vs-adv--open">`)
	b.WriteString(`<div class="vs-field"><label for="sh_waf">Mode</label><select class="input" id="sh_waf" name="sh_waf">` +
		opt("off", "Off", mode == waf.ModeOff) + opt("detect", "Record only", mode == waf.ModeDetect) + opt("block", "Refuse", mode == waf.ModeBlock) + `</select></div>`)
	b.WriteString(`<div class="vs-field"><label for="sh_waf_pl">Paranoia</label><select class="input" id="sh_waf_pl" name="sh_waf_pl">`)
	for i := 1; i <= 4; i++ {
		b.WriteString(opt(strconv.Itoa(i), strconv.Itoa(i), pl == i))
	}
	b.WriteString(`</select></div></div>`)
	return b.String()
}

// wafSectionBody is what the firewall has seen since the last restart: its
// counts, the rules that matched most, and the latest matches.
func (a *App) wafSectionBody() string {
	if a.waf == nil || a.waf.Mode() == waf.ModeOff {
		return string(ui.Empty("shield", "The firewall is off", "Turn it on to record under Settings; nothing is read until then.", ""))
	}
	inspected, matched, refused := a.waf.Counts()
	verb := "would have been refused"
	if a.waf.Mode() == waf.ModeBlock {
		verb = "refused"
	}
	var b strings.Builder
	b.WriteString(`<p class="muted text-sm">Since the last restart: ` + strconv.FormatInt(inspected, 10) + ` request` + plural(inspected) + ` read, ` +
		strconv.FormatInt(matched, 10) + ` matched a rule, ` + strconv.FormatInt(refused, 10) + ` ` + verb + `.</p>`)
	var rules [][]ui.HTML
	for i, c := range a.waf.Rules() {
		if i == 10 {
			break
		}
		rules = append(rules, []ui.HTML{ui.Text(strconv.Itoa(c.RuleID)), ui.Text(c.Message), ui.Text(strconv.Itoa(c.N))})
	}
	b.WriteString(string(ui.Table([]string{"Rule", "What it found", "Times"}, rules, "No rule has matched.")))
	var recent [][]ui.HTML
	for i, m := range a.waf.Recent() {
		if i == 20 {
			break
		}
		what := m.Message
		if m.Refused {
			what += " (" + verb + ")"
		}
		recent = append(recent, []ui.HTML{ui.Text(config.FormatSite(m.At, "2 Jan 15:04")), ui.Text(m.Source), ui.HTML(`<span class="mono">` + html.EscapeString(m.Path) + `</span>`), ui.Text(what)})
	}
	b.WriteString(string(ui.Table([]string{"When", "From", "Path", "What"}, recent, "Nothing has matched yet.")))
	return b.String()
}

// wafMode and wafParanoia are the firewall's state for the posture report.
func (a *App) wafMode() waf.Mode {
	if a.waf == nil {
		return waf.ModeOff
	}
	return a.waf.Mode()
}

func (a *App) wafParanoia() int {
	if a.waf == nil {
		return 0
	}
	return a.waf.Paranoia()
}

// wafStatus is the firewall for the connector.
func (a *App) wafStatus() map[string]any {
	if a.waf == nil {
		return map[string]any{"mode": string(waf.ModeOff)}
	}
	inspected, matched, refused := a.waf.Counts()
	return map[string]any{"mode": string(a.waf.Mode()), "paranoia": a.waf.Paranoia(),
		"inspected": inspected, "matched": matched, "refused": refused}
}
