// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_shield.go — one site's Security: what VayuShield refused and
// challenged on that site, why, what was being asked for, and when, with the
// request id that leads each refusal to its log line.
//
// The install's Shield page is the whole install, every site added up; on an
// install serving many sites it could not say which of them was under attack.
// Every block and challenge now records its site (migration 115), and this is
// that site's own view. Events recorded before then name no site and appear
// only on the install's page.

import (
	"context"
	"html"
	"net/http"
	"strconv"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// scopedShieldWindows are the windows the page offers, in hours.
var scopedShieldWindows = []struct {
	Hours int
	Label string
}{{24, "24 hours"}, {24 * 7, "7 days"}, {24 * 30, "30 days"}}

// handleOSScopedShield renders the site the URL names.
func (a *App) handleOSScopedShield(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	scope := d.ID
	if d.IsPrimary {
		scope = ""
	}
	body := a.renderSiteShieldBody(r.Context(), d.ID, scope, d.Host, shieldTrailHours(r))
	writeOSHTML(w, r, adminOSLayout(nonce, "Security · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// renderSiteShieldBody is one site's Security report: site is the id in the
// page's address, scope the id its events are recorded under ("" for the
// primary), hours the window.
func (a *App) renderSiteShieldBody(ctx context.Context, site, scope, host string, hours int) string {
	segs := make([]ui.Segment, 0, len(scopedShieldWindows))
	retention := config.Cfg.AnalyticsRetainDays
	for _, o := range scopedShieldWindows {
		// A window longer than the trail is kept would show deleted history
		// as an absence of events.
		if retention > 0 && o.Hours > retention*24 {
			continue
		}
		segs = append(segs, ui.Segment{Label: o.Label, Href: "/os/d/" + site + "/shield?hours=" + strconv.Itoa(o.Hours), Count: -1, On: o.Hours == hours})
	}
	page := ui.OverviewPage{Title: "Security", Actions: ui.Segments("Window", segs...)}

	var store *botdb.Store
	if a.vayuShield != nil {
		store = a.vayuShield.BotStore()
	}
	if store == nil {
		page.State = ui.Text("VayuShield is not recording on this install")
		return string(ui.Overview(page, ui.Band{Title: "Security", Aside: ui.Empty("shield", "Nothing recorded",
			"VayuShield's database is not running, so no block or challenge is kept.", "")}))
	}
	tr, err := store.ReadTrailScoped(ctx, scope, hours, 10, retention)
	if err != nil {
		page.State = ui.Text("The trail could not be read: " + err.Error())
		return string(ui.Overview(page, ui.Band{Title: "Security"}))
	}
	recent, _ := store.RecentBlocks(ctx, scope, 25)

	label := scopedShieldLabel(hours)
	page.State = ui.Text("Nothing refused or challenged on " + host + " in the last " + label)
	if tr.TotalBlocks > 0 || tr.TotalChallenges > 0 {
		page.State = ui.Text(countOf(int(tr.TotalBlocks), "request") + " refused on " + host + " in the last " + label)
	}
	figs := []ui.Figure{
		{Value: strconv.FormatInt(tr.TotalBlocks, 10), Label: "Refused"},
		{Value: strconv.FormatInt(tr.TotalChallenges, 10), Label: "Challenged"},
		{Value: strconv.FormatInt(tr.TotalSolved, 10), Label: "Proved human"},
	}

	countBars := func(rows []botdb.Count) []osChartBar {
		out := make([]osChartBar, 0, len(rows))
		for _, c := range rows {
			out = append(out, osChartBar{Label: c.Key, Value: int(c.Count)})
		}
		return out
	}
	hourBars := make([]osChartBar, 0, len(tr.Hours))
	for _, h := range tr.Hours {
		hourBars = append(hourBars, osChartBar{Label: h.Hour + " UTC", Value: int(h.Blocks + h.Challenges)})
	}
	total := int(tr.TotalBlocks)

	sections := []ui.HTML{
		ui.HTML(analyticsCols(
			string(ui.Section("Why it was refused", "The rule or signal that decided",
				ui.HTML(osBarList(countBars(tr.Reasons), osShareOf(total), "Nothing was refused.")))),
			string(ui.Section("What was asked for", "The paths refused requests named",
				ui.HTML(osBarList(countBars(tr.Paths), osShareOf(total), "Nothing was refused.")))))),
		ui.Section("Where from", "Coarse location only", ui.HTML(osBarList(countBars(tr.Countries), osShareOf(total), "Nothing was refused."))),
		ui.Section("By hour", "Refusals and challenges together, UTC", ui.HTML(osBarList(hourBars, osShareHidden(), "No activity in this window."))),
		ui.Section("Newest refusals", "Each with the request id its log line carries", ui.HTML(scopedRefusalsTable(recent))),
		ui.Explain(ui.HTML(`<p>These are this site's own events. A refusal or a challenge recorded before events carried their site belongs to no site, and is counted only on the install's <a href="/os/shield">Shield</a> page. No address is kept with an event: only a salted hash of it, which is never shown.</p>`)),
	}
	return string(ui.Overview(page, ui.Band{Title: "Security", Hint: "The last " + label, Figures: figs}, sections...))
}

// scopedRefusalsTable lists a site's newest refusals.
func scopedRefusalsTable(rows []botdb.BlockEvent) string {
	if len(rows) == 0 {
		return `<p class="table-empty">No refusal recorded on this site yet.</p>`
	}
	out := `<div class="table-wrap"><table class="table"><thead><tr><th>When (UTC)</th><th>Asked for</th><th>Why</th><th>Request</th></tr></thead><tbody>`
	for _, e := range rows {
		reason := e.Reason
		if reason == "" {
			reason = "(not recorded)"
		}
		out += `<tr><td class="muted">` + html.EscapeString(e.At) + `</td><td class="post-row__name">` + html.EscapeString(e.Path) +
			`</td><td>` + html.EscapeString(reason) + `</td><td class="mono text-xs">` + html.EscapeString(e.RequestID) + `</td></tr>`
	}
	return out + `</tbody></table></div>`
}

// scopedShieldLabel names a window in words.
func scopedShieldLabel(hours int) string {
	for _, o := range scopedShieldWindows {
		if o.Hours == hours {
			return o.Label
		}
	}
	if hours%24 == 0 {
		return countOf(hours/24, "day")
	}
	return countOf(hours, "hour")
}
