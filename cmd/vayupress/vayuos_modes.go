// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_modes.go — System state in the Still Air design, and the one table
// that says what each system mode refuses.
//
// The page it replaced described read-only as "write queue paused · WAL writes
// blocked", which nothing enforces: saving and publishing a post go through in
// read-only mode. So this design says only what the code does. saModeEffects
// lists every refusal with the files whose guard enforces it, and
// TestEveryModeRefusalNamesItsGuard fails when a listed guard is missing, so
// the page cannot describe a control that is not there.

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
)

type saModeEffect struct {
	// What is refused, as the operator would say it.
	What string
	// Modes refusing it. With AllBut set, every mode except these refuses it.
	Modes  []mode.Mode
	AllBut bool
	// Guards are the files whose mode check enforces this refusal, relative to
	// the repository root.
	Guards []string
}

var (
	roQ      = []mode.Mode{mode.ModeReadOnly, mode.ModeQuarantined}
	roQMaint = []mode.Mode{mode.ModeReadOnly, mode.ModeQuarantined, mode.ModeMaintenance}
)

var saModeEffects = []saModeEffect{
	{What: "Deleting posts", Modes: roQ, Guards: []string{"cmd/vayupress/admin_os_editor.go"}},
	{What: "Saving a post's Markdown source", Modes: roQ, Guards: []string{"cmd/vayupress/handlers_sources.go"}},
	{What: "Uploading and importing media", Modes: roQ, Guards: []string{"cmd/vayupress/handlers_media.go", "cmd/vayupress/handlers_media_import.go", "cmd/vayupress/admin_os_media.go"}},
	{What: "Changing the theme and the site icon", Modes: roQ, Guards: []string{"cmd/vayupress/handlers_theme.go", "cmd/vayupress/handlers_theme_assets.go", "cmd/vayupress/admin_os_theme.go", "cmd/vayupress/handlers_favicon.go"}},
	{What: "Link and diagram previews", Modes: roQ, Guards: []string{"cmd/vayupress/handlers_embed_unfurl.go", "cmd/vayupress/handlers_diagram.go", "cmd/vayupress/mcp_server.go"}},
	{What: "Regenerating the sitemap, RSS feed and robots.txt", Modes: roQ, Guards: []string{"cmd/vayupress/admin_shared.go"}},
	{What: "Announcing new posts to search engines (IndexNow)", Modes: roQMaint, Guards: []string{"cmd/vayupress/app.go"}},
	{What: "Installing updates", Modes: roQMaint, Guards: []string{"internal/update/apply.go"}},
	{What: "Keeping the site search index in step", Modes: []mode.Mode{mode.ModeNormal, mode.ModeDegraded}, AllBut: true, Guards: []string{"cmd/vayupress/handlers_search.go"}},
	{What: "Running plugins", Modes: []mode.Mode{mode.ModeQuarantined}, Guards: []string{"internal/sandbox/subprocess.go"}},
}

// saModeRefusals lists what m refuses, in table order.
func saModeRefusals(m mode.Mode) []string {
	var out []string
	for _, e := range saModeEffects {
		listed := false
		for _, x := range e.Modes {
			if x == m {
				listed = true
			}
		}
		if listed != e.AllBut {
			out = append(out, e.What)
		}
	}
	return out
}

// saModeTitle is the page title for a mode: what is happening, in words.
func saModeTitle(m mode.Mode) string {
	switch m {
	case mode.ModeNormal:
		return "Everything is running normally"
	case mode.ModeDegraded:
		return "Running in degraded mode"
	case mode.ModeReadOnly:
		return "Read-only"
	case mode.ModeRecovery:
		return "Recovering"
	case mode.ModeMaintenance:
		return "In maintenance"
	case mode.ModeQuarantined:
		return "Quarantined"
	}
	return saModeLabel(m)
}

// stillAirModesPage is /os/modes in the Still Air design.
func (a *App) stillAirModesPage(w http.ResponseWriter, r *http.Request, cfg *osSettings) {
	csrfTokenFor(w, r)
	nonce := render.CSPNonce(r)
	cur := mode.Global.Current()
	history := mode.Global.History()
	admin := cfg.AccessLevel >= accessAdmin

	var b strings.Builder
	b.WriteString(`<div class="page-header"><h1 class="sa-mode__title"><span class="badge sa-mode__tag sa-mode__tag--` + saModeTone(cur) + `">` +
		saIcon(map[string]string{"ok": "check-c", "warn": "warn", "danger": "error"}[saModeTone(cur)]) + html.EscapeString(saModeLabel(cur)) + `</span>` +
		html.EscapeString(saModeTitle(cur)) + `</h1></div>`)

	lede := "VayuOS has not changed mode since it started " + saUptime(time.Since(bootTime)) + " ago."
	if n := len(history); n > 0 {
		last := history[n-1]
		reason := strings.TrimSuffix(strings.TrimSpace(last.Reason), ".")
		if reason != "" {
			reason = strings.ToUpper(reason[:1]) + reason[1:]
		}
		lede = "Since " + config.InSite(last.OccurredAt).Format("15:04 on 2 January") + ". " + reason + "."
	}
	b.WriteString(`<p class="page-sub">` + html.EscapeString(lede))
	// The machine cause, when a machine caused it; "operator" says nothing the
	// reason did not.
	if n := len(history); n > 0 && history[n-1].Cause != "" && history[n-1].Cause != "operator" {
		b.WriteString(` <span class="mono">` + html.EscapeString(history[n-1].Cause) + `</span>`)
	}
	b.WriteString(`</p>`)

	// What this mode refuses — and, by saying "everything else", what works.
	b.WriteString(`<div class="sa-mode__cols"><section><div class="section-head"><span class="section-head__title">Refused in this mode</span></div>`)
	if refused := saModeRefusals(cur); len(refused) == 0 {
		b.WriteString(`<p class="sa-home__none">Nothing. Every part of VayuOS accepts work.</p>`)
	} else {
		b.WriteString(`<ul class="sa-mode__list">`)
		for _, what := range refused {
			b.WriteString(`<li>` + saIcon("error") + html.EscapeString(what) + `</li>`)
		}
		b.WriteString(`</ul><p class="sa-mode__rest">Everything else keeps working, including reading, writing and publishing posts, mail and the public site.</p>`)
	}
	b.WriteString(`</section>`)

	// Recent changes.
	b.WriteString(`<section><div class="section-head"><span class="section-head__title">Recent changes</span></div>`)
	if len(history) == 0 {
		b.WriteString(`<p class="sa-home__none">No mode changes since VayuOS started.</p>`)
	} else {
		b.WriteString(`<ol class="sa-steps">`)
		start := max(0, len(history)-8)
		for i := len(history) - 1; i >= start; i-- {
			t := history[i]
			who := "by an operator"
			if t.Cause != "" && t.Cause != "operator" {
				who = `caused by <span class="mono">` + html.EscapeString(t.Cause) + `</span>`
			}
			cls := "sa-step"
			if i == len(history)-1 {
				cls += " sa-step--now"
			}
			b.WriteString(`<li class="` + cls + `"><span class="sa-step__t">` + config.InSite(t.OccurredAt).Format("15:04") + `</span><span class="sa-step__n" aria-hidden="true"></span>` +
				`<span class="sa-step__d"><b>` + html.EscapeString(saModeLabel(t.From)) + ` → ` + html.EscapeString(saModeLabel(t.To)) + `</b> · ` + html.EscapeString(t.Reason) + `<br><span class="muted">` + who + `</span></span></li>`)
		}
		b.WriteString(`</ol>`)
	}
	b.WriteString(`</section></div>`)

	// Changing the mode: every other mode, what it would refuse, and whether the
	// transition graph allows it directly.
	if admin {
		b.WriteString(`<section class="sa-mode__change"><div class="section-head"><span class="section-head__title">Change the mode</span>` +
			`<span class="section-head__hint">Every change is journaled and survives a restart</span></div>`)
		for _, m := range mode.AllModes() {
			if m == cur {
				continue
			}
			refused := saModeRefusals(m)
			desc := "Refuses nothing."
			if len(refused) > 0 {
				desc = "Refuses: " + strings.ToLower(strings.Join(refused, ", ")) + "."
			}
			btn := `<button type="button" class="btn btn--sm" data-mode-to="` + string(m) + `">Switch to ` + html.EscapeString(strings.ToLower(saModeLabel(m))) + `</button>`
			if !mode.IsAllowed(cur, m) {
				btn = `<button type="button" class="btn btn--sm btn--danger" data-mode-to="` + string(m) + `" data-mode-force>Override to ` + html.EscapeString(strings.ToLower(saModeLabel(m))) + `</button>`
				desc += " Not reachable from " + strings.ToLower(saModeLabel(cur)) + " directly; an override skips the permitted path."
			}
			b.WriteString(`<div class="sa-opt"><div class="sa-opt__t"><span class="sa-dot sa-dot--` + saModeTone(m) + `"></span>` + html.EscapeString(saModeTitle(m)) + `</div>` +
				`<div class="sa-opt__d">` + html.EscapeString(desc) + `</div>` + btn + `</div>`)
		}
		b.WriteString(`<p class="sa-mode__rest">The Fault Engine and the Replay Explorer, under Diagnostics, show what can move the system between modes on its own.</p></section>`)
	}

	script := `window.vpMode=function(to,force){vpPost('/admin/mode/transition?to='+encodeURIComponent(to)+(force?'&force=true':''),function(d){return 'Mode changed to '+(d.mode||to)+'.';});};
document.addEventListener('click',function(e){var btn=e.target.closest('[data-mode-to]');if(btn)vpMode(btn.getAttribute('data-mode-to'),btn.hasAttribute('data-mode-force'));});`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	fmt.Fprint(w, adminOSShellHead(nonce, "System state", "modes", cfg)+b.String()+adminOSShellFoot(nonce, script, false))
}

// saModeStripText summarises what a mode refuses for the state strip, from the
// same table as the System state page.
func saModeStripText(m mode.Mode) string {
	refused := saModeRefusals(m)
	if len(refused) == 0 {
		return ""
	}
	shown := refused
	more := ""
	if len(refused) > 3 {
		shown = refused[:3]
		more = fmt.Sprintf(" and %d more", len(refused)-3)
	}
	lower := make([]string, len(shown))
	for i, s := range shown {
		lower[i] = strings.ToLower(s[:1]) + s[1:]
	}
	return "Refused until it ends: " + strings.Join(lower, ", ") + more + "."
}
