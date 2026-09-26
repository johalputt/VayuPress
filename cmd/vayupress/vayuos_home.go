// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_home.go — Home in the Still Air design.
//
// The dashboard it replaced was a quick-compose box, an attention strip, a
// setup card, a grid of workspace cards and a row of job counters. Home keeps
// what an operator acts on and drops what the rail already offers: the
// workspace cards were a second copy of the Content app's sidebar. What remains reads top to
// bottom as a document: what needs you, how the site is doing, the state of
// this install, what just happened.

import (
	"context"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/ui"
)

// saHomeVisitorDays is the window Home reports. Two weeks shows a trend and
// the weekday rhythm without the noise of a single week.
const saHomeVisitorDays = 14

func (a *App) stillAirHomeBody(ctx context.Context, cfg *osSettings, snap *adminMetricsSnapshot) string {
	var b strings.Builder
	now := config.InSite(time.Now())

	// ── Head ──
	m := cfg.Mode
	if m == "" {
		m = mode.ModeNormal
	}
	state := `<span class="sa-dot sa-dot--ok"></span>All systems normal`
	if m != mode.ModeNormal {
		state = `<span class="sa-dot sa-dot--` + saModeTone(m) + `"></span>` + html.EscapeString(saModeLabel(m))
	}
	b.WriteString(`<header class="sa-home__head"><h1 class="sa-home__title">Home</h1>` +
		`<span class="sa-home__date">` + now.Format("Monday, 2 January") + `</span>` +
		`<span class="sa-home__state">` + state + ` · ` + now.Format("15:04") + `</span></header>`)

	// ── Quick compose: a title and Enter starts a draft (admin-os.js). ──
	if cfg.AccessLevel >= osPathMinLevel("/os/editor") {
		b.WriteString(`<div class="quick-compose sa-compose" role="search">` + saIcon("pencil") +
			`<input id="quick-compose-input" class="quick-compose-input" type="text" placeholder="Start a post — type a title and press Enter" autocomplete="off" aria-label="Start a post: type a title and press Enter"></div>`)
	}

	// ── Needs you ──
	b.WriteString(saHomeNeeds(cfg.Notifications))

	// ── Finish setting up ──
	if items := a.osFirstRunChecklist(ctx, cfg.AccessLevel); len(items) > 0 {
		b.WriteString(saHomeSetup(items))
	}

	// ── Visitors · This install ──
	b.WriteString(`<div class="sa-home__cols">`)
	b.WriteString(a.saHomeVisitors(ctx, cfg))
	b.WriteString(saHomeInstall(cfg, snap, m))
	b.WriteString(`</div>`)

	// ── Recent ──
	b.WriteString(`<section class="sa-home__sec" aria-labelledby="sa-recent"><div class="section-head"><span class="section-head__title" id="sa-recent">Recent</span></div>` +
		`<div id="activity-feed" class="activity-list sa-feed"><div class="table-empty">Loading recent activity…</div></div></section>`)
	return b.String()
}

// saHomeNeeds lists what needs the operator, worst first, each with the one
// action that clears it. Nothing to do is said plainly, not decorated.
func saHomeNeeds(notifs []osNotification) string {
	weight := map[string]int{"danger": 0, "warn": 1}
	sorted := append([]osNotification(nil), notifs...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			wj, ok := weight[sorted[j].Severity]
			if !ok {
				wj = 2
			}
			wp, ok := weight[sorted[j-1].Severity]
			if !ok {
				wp = 2
			}
			if wj >= wp {
				break
			}
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var b strings.Builder
	b.WriteString(`<section class="sa-home__sec" aria-labelledby="sa-needs"><div class="section-head"><span class="section-head__title" id="sa-needs">Needs you</span>`)
	if len(sorted) > 0 {
		b.WriteString(`<span class="section-head__hint">` + strconv.Itoa(len(sorted)) + ` item` + plural(len(sorted)) + `</span>`)
	}
	b.WriteString(`</div>`)
	if len(sorted) == 0 {
		b.WriteString(`<p class="sa-home__none">Nothing needs you right now.</p></section>`)
		return b.String()
	}
	for _, n := range sorted {
		tone, icon := "info", saNotifIcon(n.Kind)
		switch n.Severity {
		case "danger":
			tone, icon = "danger", "error"
		case "warn":
			tone = "warn"
		}
		b.WriteString(`<a class="sa-need sa-need--` + tone + `" href="` + html.EscapeString(n.Href) + `">` + saIcon(icon) +
			`<span class="sa-need__t">` + html.EscapeString(n.Title) + `</span>` +
			`<span class="sa-need__d">` + html.EscapeString(n.line()) + `</span>` +
			`<span class="sa-need__go">Open` + saIcon("arrow-r") + `</span></a>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// saNotifIcon names the icon for a notification kind.
func saNotifIcon(kind string) string {
	switch kind {
	case "mail":
		return "mail"
	case "comment":
		return "talk"
	case "message":
		return "inbox"
	case "domain":
		return "globe"
	case "update":
		return "refresh"
	case "backup":
		return "archive"
	case "security":
		return "shield"
	case "jobs":
		return "flow"
	case "storage":
		return "disk"
	case "mode":
		return "pulse"
	case "member":
		return "user"
	case "post":
		return "pencil"
	}
	return "info"
}

// saHomeSetup is the first-run checklist as a short list with its progress,
// only while something is left to do.
func saHomeSetup(items []osChecklistItem) string {
	done := 0
	for _, it := range items {
		if it.Done {
			done++
		}
	}
	if done == len(items) {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section class="sa-home__sec" aria-labelledby="sa-setup"><div class="section-head"><span class="section-head__title" id="sa-setup">Finish setting up</span>` +
		`<span class="section-head__hint">` + strconv.Itoa(done) + ` of ` + strconv.Itoa(len(items)) + ` done</span></div>`)
	for _, it := range items {
		if it.Done {
			continue
		}
		b.WriteString(`<a class="sa-need sa-need--info" href="` + html.EscapeString(it.Href) + `">` + saIcon("chev-r") +
			`<span class="sa-need__t">` + html.EscapeString(it.Label) + `</span>` +
			`<span class="sa-need__d">` + html.EscapeString(it.Detail) + `</span>` +
			`<span class="sa-need__go">Open` + saIcon("arrow-r") + `</span></a>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// saHomeVisitors shows the last two weeks: the three figures that answer "how
// is the site doing", and one line for the trend.
func (a *App) saHomeVisitors(ctx context.Context, cfg *osSettings) string {
	if cfg.AccessLevel < osPathMinLevel("/os/analytics") {
		return ""
	}
	head := `<section class="sa-home__sec" aria-labelledby="sa-visitors"><div class="section-head"><span class="section-head__title" id="sa-visitors">Visitors</span>` +
		`<span class="section-head__hint">Last ` + strconv.Itoa(saHomeVisitorDays) + ` days</span></div>`
	if a.analytics == nil {
		return head + `<p class="sa-home__none">Analytics is not running on this install.</p></section>`
	}
	ov, err := a.analytics.OverviewSince(ctx, saHomeVisitorDays)
	if err != nil || ov == nil {
		return head + `<p class="sa-home__none">Visitor figures are unavailable right now.</p></section>`
	}
	series, _ := a.analytics.PageviewSeries(ctx, saHomeVisitorDays)
	points := make([]int, 0, len(series))
	for _, d := range series {
		points = append(points, d.Visitors)
	}
	dur := time.Duration(ov.AvgDuration * float64(time.Second))
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`<div class="sa-kpis">` +
		saKPI(osGroupInt(ov.UniqueVisitors), "Visitors") +
		saKPI(osGroupInt(ov.TotalPageviews), "Page views") +
		saKPI(saShortDuration(dur), "Average visit") + `</div>`)
	b.WriteString(saSparkline(points))
	b.WriteString(`<p class="sa-home__more"><a href="/os/analytics">Open Analytics</a></p></section>`)
	return b.String()
}

func saKPI(value, label string) string {
	return `<div class="sa-kpi"><div class="sa-kpi__v">` + html.EscapeString(value) + `</div><div class="sa-kpi__l">` + label + `</div></div>`
}

// saShortDuration writes a visit length the way a person says it: "2m 41s".
func saShortDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	s := int(d.Round(time.Second).Seconds())
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	return strconv.Itoa(s/60) + "m " + strconv.Itoa(s%60) + "s"
}

// saSparkline draws the series as one line over a faint area, scaled to its own
// range. Server-rendered SVG: no chart library, nothing for the CSP to allow.
func saSparkline(points []int) string {
	const w, h = 600.0, 120.0
	if len(points) < 2 {
		return `<p class="sa-home__none">Not enough days recorded yet to draw a trend.</p>`
	}
	lo, hi := math.MaxInt, 0
	for _, p := range points {
		lo, hi = min(lo, p), max(hi, p)
	}
	span := float64(hi - lo)
	if span == 0 {
		span = 1
	}
	var line strings.Builder
	for i, p := range points {
		x := float64(i) * w / float64(len(points)-1)
		y := h - 6 - (float64(p-lo)/span)*(h-18)
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.1f,%.1f ", cmd, x, y)
	}
	path := strings.TrimSpace(line.String())
	return `<svg class="sa-spark" viewBox="0 0 600 120" preserveAspectRatio="none" role="img" aria-label="Visitors per day, last ` +
		strconv.Itoa(len(points)) + ` days: from ` + strconv.Itoa(points[0]) + ` to ` + strconv.Itoa(points[len(points)-1]) + `">` +
		`<path class="sa-spark__grid" d="M0,40H600M0,80H600"/>` +
		`<path class="sa-spark__area" d="` + path + ` L600,120 L0,120 Z"/>` +
		`<path class="sa-spark__line" d="` + path + `"/></svg>`
}

// saHomeInstall is the state of this install in plain rows. Only facts the
// process knows — nothing here is a claim it cannot back.
func saHomeInstall(cfg *osSettings, snap *adminMetricsSnapshot, m mode.Mode) string {
	var b strings.Builder
	link := ""
	if cfg.AccessLevel >= osPathMinLevel("/os/modes") {
		link = `<a class="section-head__hint" href="/os/modes">System</a>`
	}
	b.WriteString(`<section class="sa-home__sec" aria-labelledby="sa-install"><div class="section-head"><span class="section-head__title" id="sa-install">This install</span>` + link + `</div>`)
	var facts []ui.Fact
	row := func(k, v string) { facts = append(facts, ui.Fact{Key: k, Value: ui.HTML(v)}) }
	row("Mode", `<span class="sa-dot sa-dot--`+saModeTone(m)+`"></span>`+html.EscapeString(saModeLabel(m)))
	world := "Clearnet"
	if config.Cfg.OnionMode {
		world = "Tor"
	}
	row("World", world)
	row("Version", `<span class="mono">`+html.EscapeString(Version)+`</span>`)
	if snap != nil && snap.UptimeSeconds > 0 {
		row("Up for", saUptime(time.Duration(snap.UptimeSeconds*float64(time.Second))))
	}
	if snap != nil {
		jobs := `No jobs waiting`
		switch {
		case snap.FailedJobs > 0:
			jobs = `<span class="sa-warn">` + osGroupInt(snap.FailedJobs) + ` failed</span> · ` + osGroupInt(snap.PendingJobs) + ` waiting`
		case snap.PendingJobs > 0:
			jobs = osGroupInt(snap.PendingJobs) + ` waiting`
		}
		row("Background work", jobs)
		if snap.HTTPP95 > 0 {
			row("Response time", ``+strconv.FormatInt(snap.HTTPP95, 10)+` ms at the 95th percentile`)
		}
		if snap.QuotaBytes > 0 {
			pct := snap.StoragePct
			free := snap.QuotaBytes - snap.StorageBytes
			if free < 0 {
				free = 0
			}
			// A drawn bar, not a styled one: an inline style attribute is refused
			// by the console's CSP rules, an SVG width attribute is not.
			used := strconv.Itoa(int(math.Round(math.Min(100, math.Max(0, pct)))))
			bar := `<svg class="sa-meter" viewBox="0 0 100 4" preserveAspectRatio="none" role="img" aria-label="` + used + `% used">` +
				`<rect class="sa-meter__track" width="100" height="4" rx="2"/><rect class="sa-meter__fill" width="` + used + `" height="4" rx="2"/></svg>`
			row("Storage", html.EscapeString(humanBytes(free))+` free of `+html.EscapeString(humanBytes(snap.QuotaBytes))+bar)
		}
	}
	b.WriteString(string(ui.Facts(facts...)) + `</section>`)
	return b.String()
}

// saUptime writes an uptime the way a person reads it: "18 days, 4 hours".
func saUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return strconv.Itoa(days) + " day" + plural(days) + ", " + strconv.Itoa(hours) + " hour" + plural(hours)
	case hours > 0:
		return strconv.Itoa(hours) + " hour" + plural(hours) + ", " + strconv.Itoa(mins) + " minute" + plural(mins)
	}
	return strconv.Itoa(mins) + " minute" + plural(mins)
}
