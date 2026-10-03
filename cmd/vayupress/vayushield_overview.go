// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_overview.go — Shield as the Overview kind (render 06): its state
// in one sentence beside the title, its three pages as tabs, what it turned
// away beside the crawlers it let through, its layers as rows, then its
// reports laid open. The settings form rises in a sheet.

import (
	htmpl "html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// shieldChartDays is how far back the turned-away chart reaches.
const shieldChartDays = 14

// shieldTrailDays totals the recorded history by day for the chart, oldest
// first and with a zero for a day nothing was recorded (a gap in the line
// would read as missing data), and the blocks and challenges of the last day.
func shieldTrailDays(tr botdb.Trail, days int, now time.Time) (daily []int, blocks, challenges int64) {
	byDay := map[string]int{}
	cut := now.UTC().Add(-24 * time.Hour).Format("2006-01-02 15:00")
	for _, h := range tr.Hours {
		if len(h.Hour) >= 10 {
			byDay[h.Hour[:10]] += int(h.Blocks)
		}
		if h.Hour >= cut {
			blocks += h.Blocks
			challenges += h.Challenges
		}
	}
	for i := days - 1; i >= 0; i-- {
		daily = append(daily, byDay[now.UTC().AddDate(0, 0, -i).Format("2006-01-02")])
	}
	return daily, blocks, challenges
}

// shieldState is the page's state beside its title. Observe mode ranks first:
// an install that is observing enforces nothing, and the way that goes wrong
// is being left on and forgotten. "No reader challenged" is said only when the
// visitor check has just shown ordinary visitors being served; it is the
// render's reassurance, and without that evidence it would be a promise.
func shieldState(cur vayushield.Settings, stt vayushield.Status, haveTrail bool, blocks int64, readersServed bool) (string, string) {
	tone, text := "ok", "Protecting"
	switch {
	case stt.ObserveOnly:
		tone, text = "warn", "Observing: nothing is enforced"
	case !cur.Enabled:
		tone, text = "warn", "Off"
	case stt.SurgeActive:
		tone, text = "warn", "Surge: verifying visitors"
	case stt.UnderAttack:
		tone, text = "danger", "Under attack"
	}
	if haveTrail {
		if blocks == 0 {
			text += " · nothing turned away today"
		} else {
			text += " · " + osGroupInt(int(blocks)) + " turned away today"
		}
	}
	if readersServed {
		text += ", no reader challenged"
	} else {
		tone, text = "danger", text+", ordinary visitors are being challenged"
	}
	return tone, text
}

// shieldLayersBody is the layers as rows, cheapest first, each with what it
// did and whether it is working. Every read is an in-memory counter (and two
// small control-file reads for L1), so the section refreshes every 10 seconds.
// The per-layer counters that once sat in their own "Live throughput" panel
// are here, beside the layer they count.
func (a *App) shieldLayersBody() htmpl.HTML {
	var stt vayushield.Status
	if a.vayuShield != nil {
		stt = a.vayuShield.Status()
	}
	count := func(main, sub string) ui.HTML {
		h := `<span class="sa-layer__count">` + string(ui.Text(main))
		if sub != "" {
			h += `<span class="sa-layer__sub">` + string(ui.Text(sub)) + `</span>`
		}
		return ui.HTML(h + `</span>`)
	}
	working := ui.State("ok", "Working")
	busy := ui.State("warn", "Busy")

	var inflight, lane, shed int64
	l0 := working
	if a.sovereign != nil {
		inflight, lane, shed = a.sovereign.Inflight(), a.sovereign.Cap(), a.sovereign.Shed()
		if inflight*4 >= lane*3 {
			l0 = busy
		}
	}
	rows := []ui.Row{{Tag: "L0", Label: "Console lane", Hint: "The console and verified crawlers keep their headroom",
		Control: count(strconv.FormatInt(inflight, 10)+" in flight", "of "+strconv.FormatInt(lane, 10)+" · "+strconv.FormatInt(shed, 10)+" shed") + " " + l0}}

	l1state, l1count := shieldOffloadStatus()
	switch l1state {
	case "active":
		rows = append(rows, ui.Row{Tag: "L1", Label: "Kernel offload", Hint: "Jailed addresses dropped before they reach the app",
			Control: count(l1count+" banned", "") + " " + working})
	case "error":
		rows = append(rows, ui.Row{Tag: "L1", Label: "Kernel offload", Hint: "Its agent reported an error; see Network hardening",
			Control: ui.State("danger", "Failing")})
	default:
		rows = append(rows, ui.Row{Tag: "L1", Label: "Kernel offload", Hint: "Jailed addresses dropped before they reach the app",
			Control: count("Off", "needs root") + " " + ui.State("neutral", "Off")})
	}

	l2 := working
	if stt.FairShed > 0 {
		l2 = busy
	}
	rows = append(rows, ui.Row{Tag: "L2", Label: "Fair share", Hint: "Heavy hitters are shed first; a fair budget never is",
		Control: count(osGroupInt(int(stt.FairShed))+" shed", osGroupInt(int(stt.WindowRate))+" in the window") + " " + l2})

	sub := osGroupInt(int(stt.ChallengesServed-stt.ChallengesPassed)) + " failed"
	if stt.CalibrationBias > 0 {
		sub += " · eased +" + ftoa2(stt.CalibrationBias)
	}
	rows = append(rows, ui.Row{Tag: "L4", Label: "Silent challenges", Hint: "Only when a client looks automated, and eased if they bother people",
		Control: count(osGroupInt(int(stt.ChallengesPassed))+" passed", sub) + " " + working})

	l5 := working
	if stt.RepJailed > 0 {
		l5 = busy
	}
	rows = append(rows, ui.Row{Tag: "L5", Label: "Reputation", Hint: "Jails in minutes, forgives on its own",
		Control: count(osGroupInt(stt.RepJailed)+" jailed", osGroupInt(stt.Suspects)+" suspected · "+osGroupInt(int(stt.Pardons))+" forgiven") + " " + l5})

	if total := stt.SigCacheHits + stt.SigCacheMisses; total > 0 {
		rows = append(rows, ui.Row{Tag: "L6", Label: "Signature cache", Hint: "A client seen before is judged without scoring it again",
			Control: count(strconv.FormatInt(stt.SigCacheHits*100/total, 10)+"% hits", "") + " " + working})
	}

	// The split, not a total: a count that is nearly all "payload" is most
	// likely this site's own search box seeing the words it publishes about,
	// while probes are unambiguously scanners.
	l7 := working
	if stt.InspectFindings[0] > 0 {
		l7 = busy
	}
	rows = append(rows, ui.Row{Tag: "L7", Label: "Request inspection", Hint: "Names scanners on their first request; never blocks alone",
		Control: count(osGroupInt(int(stt.InspectFindings[0]))+" probes",
			osGroupInt(int(stt.InspectFindings[1]))+" traversal · "+osGroupInt(int(stt.InspectFindings[2]))+" payload") + " " + l7})

	if peers, in, _, sent, failed := a.vayuShield.ClusterStats(); peers > 0 {
		rows = append(rows, ui.Row{Label: "Shared verdicts", Hint: countOf(peers, "peer node") + " exchanging what each has learned",
			Control: count(osGroupInt(int(in))+" received", osGroupInt(int(sent))+" sent · "+osGroupInt(int(failed))+" failed") + " " + working})
	}
	return ui.Rows(rows...)
}

// handleOSShield renders Shield's first tab, Bot protection.
func (a *App) handleOSShield(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	cur := a.shieldCurrentSettings()
	var stt vayushield.Status
	if a.vayuShield != nil {
		stt = a.vayuShield.Status()
	}
	var tr botdb.Trail
	haveTrail := false
	if a.vayuShield != nil {
		if store := a.vayuShield.BotStore(); store != nil {
			if t, err := store.ReadTrail(ctx, shieldChartDays*24, 1, config.Cfg.AnalyticsRetainDays); err == nil {
				tr, haveTrail = t, true
			}
		}
	}
	daily, blocks, challenges := shieldTrailDays(tr, shieldChartDays, time.Now())
	selfTest := a.cachedShieldCanary()
	tone, state := shieldState(cur, stt, haveTrail, blocks, selfTest.readers == len(canaryReaders))

	// A figure only when it has a value. That nobody was challenged is said in
	// the state, where the visitor check stands behind it.
	var figs []ui.Figure
	if blocks > 0 {
		figs = append(figs, ui.Figure{Value: osGroupInt(int(blocks)), Label: "Turned away today"})
	}
	if n := stt.Blocklisted + stt.RepJailed; n > 0 {
		figs = append(figs, ui.Figure{Value: osGroupInt(n), Label: "Addresses jailed", Live: true})
	}
	if challenges > 0 {
		figs = append(figs, ui.Figure{Value: osGroupInt(int(challenges)), Label: "Challenged today"})
	}
	// A line flat at zero reads as a dash, not as "nothing happened", so an
	// empty fortnight is said in words.
	chart := ui.HTML(`<p class="table-empty">Nothing has been recorded yet.</p>`)
	if haveTrail {
		chart = ui.HTML(`<p class="table-empty">Nothing turned away in the last ` + strconv.Itoa(shieldChartDays) + ` days.</p>`)
		for _, n := range daily {
			if n > 0 {
				chart = ui.HTML(`<div class="sparkline-wrap">` + osSparkline(daily, "Turned away a day, last "+strconv.Itoa(shieldChartDays)+" days") + `</div>`)
				break
			}
		}
	}

	sections := []ui.HTML{
		ui.Section("Layers", "Cheapest first; each acts only under pressure", ui.HTML(
			`<div id="vs-body-aegis" hx-get="/os/shield/section/aegis" hx-trigger="every 10s, vs-refresh from:body" hx-swap="innerHTML">`+
				string(a.shieldLayersBody())+`</div>`)),
		ui.Section("Readers and crawlers", "Each is sent through the shield as it is set now", ui.HTML(
			`<div id="vs-body-selftest" hx-get="/os/shield/section/selftest" hx-trigger="vs-refresh from:body" hx-swap="innerHTML">`+
				shieldSelfTestBody(selfTest)+`</div>`)),
		ui.Section("What is enforcing", "Verified, not assumed", ui.HTML(
			`<div id="vs-body-audit" hx-get="/os/shield/section/audit" hx-trigger="every 30s" hx-swap="innerHTML">`+a.shieldAuditBody(r)+`</div>`)),
		ui.Section("Network hardening", "nftables and the nginx edge, on the server itself", ui.HTML(
			`<div id="vs-body-hardening" hx-get="/os/shield/section/hardening" hx-trigger="every 10s" hx-swap="innerHTML">`+a.shieldHardeningBody(r)+`</div>`)),
	}
	if a.vayuShield != nil && a.vayuShield.BotStore() != nil {
		sections = append(sections,
			ui.Section("Bot signatures", "Learned here, and shared by the community", ui.HTML(
				`<div id="vs-body-signatures" hx-get="/os/shield/section/signatures" hx-trigger="vs-refresh-sig from:body" hx-swap="innerHTML">`+a.shieldSignaturesBody(ctx)+`</div>`)),
			ui.Section("Review queue", "Clients it learned, waiting for your word", ui.HTML(
				`<div id="vs-body-queue" hx-get="/os/shield/section/queue" hx-trigger="vs-refresh-sig from:body" hx-swap="innerHTML">`+a.shieldQueueBody(ctx)+`</div>`)))
	}
	sections = append(sections, ui.Section("History", "Blocks and challenges over time", ui.HTML(
		`<div id="vs-body-trail" hx-get="/os/shield/section/trail" hx-trigger="vs-refresh from:body" hx-swap="innerHTML">`+a.shieldTrailBody(r)+`</div>`)))
	if a.vaEngagement != nil {
		sections = append(sections, ui.Section("Engagement", "Time on page, scroll depth and sources, without cookies", ui.HTML(
			`<div id="vs-body-engagement">`+a.shieldEngagementBody(ctx, shieldDays(r))+`</div>`)))
	}

	// The settings save over HTMX and reply with an HX-Trigger that fires
	// vs-refresh, so the sections above reload in place and the page never does.
	settingsForm := ui.Sheet("vs-settings", "Protection settings", ui.HTML(
		`<form hx-post="/os/api/shield/settings" hx-swap="none"><div id="vs-body-protection" hx-get="/os/shield/section/protection" hx-trigger="vs-refresh from:body" hx-swap="innerHTML">`+
			a.shieldProtectionBody(ctx, a.shieldGeoIsBlind(r))+`</div></form>`))

	page := ui.Overview(ui.OverviewPage{Title: "Shield", State: ui.State(tone, state),
		// The live region is where vayuos.js announces a refused write, on every
		// page that has one; the settings save over HTMX, so without it a failed
		// save would be silent to a screen reader.
		Actions: `<span id="vs-status" role="status" aria-live="polite" class="text-xs muted"></span>` +
			`<button type="button" class="btn btn--sm" data-sheet="vs-settings">` + ui.Icon("settings") + ` Settings</button>`,
		Tabs: saTabsFor(cfg, "shield", "/os/shield")},
		ui.Band{Title: "Turned away", Hint: "Last " + strconv.Itoa(shieldChartDays) + " days", Figures: figs, Chart: chart,
			Aside: a.crawlersServed("Crawlers let through")},
		sections...)

	// A broken host resolver silently staleifies every feed the shield reads, so
	// it is said before anything else rather than let stale protection pass as
	// current.
	body := dnsResolverNotice() + string(page) + string(settingsForm) + `
<script nonce="` + nonce + `">
(function(){'use strict';
// A save fires vs-refresh. The confirmation is a status, so a screen reader
// hears it as well as the eye sees it, and the sheet closes on it.
document.body.addEventListener('vs-refresh',function(){
  var d=document.getElementById('vs-settings');if(d&&d.open)d.close();
  var t=document.createElement('div');t.className='vs-toast';t.setAttribute('role','status');t.textContent='Settings applied to every request';
  document.body.appendChild(t);requestAnimationFrame(function(){t.classList.add('is-in');});
  setTimeout(function(){t.classList.remove('is-in');setTimeout(function(){t.remove();},400);},2200);
});
document.addEventListener('click',function(e){
  var btn=e.target&&e.target.closest?e.target.closest('.vs-copy-btn'):null;if(!btn)return;
  var el=document.getElementById(btn.getAttribute('data-copy'));if(!el)return;
  var text=(el.textContent||'').trim();
  var done=function(){var o=btn.textContent;btn.textContent='Copied';btn.classList.add('is-copied');setTimeout(function(){btn.textContent=o;btn.classList.remove('is-copied');},1500);};
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(text).then(done).catch(done);}else{done();}
});
})();
</script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Shield", "shield", cfg, htmpl.HTML(body)))
}

// shieldSelfTestBody is the visitor check: each synthetic visitor is driven
// through the REAL middleware with the current settings, so the answer is
// this install's, not a documented intention.
func shieldSelfTestBody(res shieldCanaryResult) string {
	if len(res.probes) == 0 {
		return `<p class="settings-row-hint">VayuShield is not running, so there is nothing to test.</p>`
	}
	lead := "Every ordinary visitor and every major crawler is served your pages under these settings."
	switch {
	case res.readers != len(canaryReaders):
		lead = "Ordinary visitors are being stopped: at least one browser met a verification page or a refusal instead of your page. Lower the block or challenge score, or turn off Sovereign Surge, then check again."
	case !res.ok():
		lead = "A crawler is not being served your pages. Refusals on real addresses read as crawl errors and cost indexing; loosen the thresholds and check again."
	}
	b := `<p class="settings-row-hint">` + string(ui.Text(lead)) + `</p>`
	for _, group := range []string{"Readers", "Crawlers"} {
		var rows []ui.Row
		for _, p := range res.probes {
			if p.Group != group {
				continue
			}
			row := ui.Row{Label: p.Name, Hint: "HTTP " + strconv.Itoa(p.Status), Control: ui.State("ok", "Served")}
			switch {
			case p.OK:
			case p.NotTestable:
				// A synthetic crawler cannot hold its vendor's addresses, so this
				// probe proves nothing; calling it blocked would be a false alarm.
				row.Hint, row.Control = "Confirmed by its published addresses in production", ui.State("neutral", "Not testable here")
			default:
				word := "Challenged"
				switch p.Status {
				case http.StatusForbidden:
					word = "Blocked"
				case http.StatusTooManyRequests:
					word = "Throttled"
				}
				if p.Why != "" {
					row.Hint += ": " + p.Why
				}
				row.Control = ui.State("danger", word)
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			b += `<h3 class="sa-panel__title">` + group + `</h3>` + string(ui.Rows(rows...))
		}
	}
	return b
}
