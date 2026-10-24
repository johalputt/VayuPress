// SPDX-License-Identifier: Apache-2.0

package main

// analytics_performance.go — Performance on the Analytics pages: what the
// readers' own browsers measured (Core Web Vitals at the 75th percentile)
// beside what this server measured for the same site and range (its
// requests by status, and the time it took to answer its pages).

import (
	"context"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/sitehttp"
	"github.com/johalputt/vayupress/internal/ui"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// vital is one Core Web Vital as the pages say it: its p75 and whether that
// is good, or that no reader reported it, which is not a p75 of 0.
type vital struct {
	Name, Value, Good string
	Reported, OK      bool
	Readings          int64
}

// vitalsOf sets the three vitals of v out for the pages.
func vitalsOf(v *vastore.WebVitals) []vital {
	return []vital{
		{Name: "Largest paint (LCP)", Value: strconv.Itoa(v.P75LCPMs) + " ms", Good: "2.5 s or less", Reported: v.SamplesLCP > 0, OK: v.P75LCPMs <= 2500, Readings: v.SamplesLCP},
		{Name: "Response to input (INP)", Value: strconv.Itoa(v.P75INPMs) + " ms", Good: "200 ms or less", Reported: v.SamplesINP > 0, OK: v.P75INPMs <= 200, Readings: v.SamplesINP},
		{Name: "Layout shift (CLS)", Value: strconv.FormatFloat(float64(v.P75CLSX100)/100, 'f', 2, 64), Good: "0.10 or less", Reported: v.SamplesCLS > 0, OK: v.P75CLSX100 <= 10, Readings: v.SamplesCLS},
	}
}

// osPerformanceSection is the Analytics page's Performance for scope (a
// site, or analytics.ScopeAllDomains).
func (a *App) osPerformanceSection(ctx context.Context, scope string, days int) string {
	server, byDay := a.serverMeasured(ctx, scope, days)
	return string(ui.Section("Performance", "Readers' browsers and this server, over the period", ui.HTML(analyticsCols(
		analyticsPanel("What readers' browsers measured", a.readerVitals(ctx, scope, days)),
		analyticsPanel("What the server measured", server),
	)+byDay+string(ui.Explain(ui.HTML(`<p>A reader's browser measures each page it loads, and the figure is the 75th percentile of those readings. Response to input is reported only when a reader interacted, and layout shift only by browsers that measure it.</p>`+
		`<p>The server counts every request a site's visitors make of it, the shield's refusals among them; the console and the API are the install's own. It counts from the first request this version answered. Server time runs from the request reaching this server to its answer, for the HTML pages answered in full, and each percentile is within a tenth of the exact one.</p>`))))))
}

func (a *App) readerVitals(ctx context.Context, scope string, days int) string {
	if a.vaEngagement == nil {
		return `<p class="table-empty">Speed is measured by the reading beacon, which is not running.</p>`
	}
	var v *vastore.WebVitals
	var err error
	if scope == analytics.ScopeAllDomains {
		v, err = a.vaEngagement.WebVitalsP75(ctx, days)
	} else {
		v, err = a.vaEngagement.WebVitalsP75Scoped(ctx, scope, days)
	}
	if err != nil || v == nil || v.Samples == 0 {
		return `<p class="table-empty">No page has reported its speed yet.</p>`
	}
	rows := make([][]ui.HTML, 0, 3)
	for _, x := range vitalsOf(v) {
		name := ui.HTML(string(ui.Text(x.Name)) + `<br><span class="muted">Good is ` + string(ui.Text(x.Good)) + `</span>`)
		if !x.Reported {
			rows = append(rows, []ui.HTML{name, ui.Text("Not reported"), "0"})
			continue
		}
		tone, word := "ok", "good"
		if !x.OK {
			tone, word = "warn", "needs work"
		}
		rows = append(rows, []ui.HTML{name, ui.HTML(`<span class="mono">` + x.Value + `</span><br>` + string(ui.State(tone, word))), ui.Text(strconv.FormatInt(x.Readings, 10))})
	}
	return string(ui.Table([]string{"Measure", "p75", "Readings"}, rows, ""))
}

// serverMeasured is the server's panel and, for a range short enough to list
// by day, the table of its days, which is set below both panels at the
// section's full width: five columns do not fit half of it.
func (a *App) serverMeasured(ctx context.Context, scope string, days int) (panel, byDayTable string) {
	if dbpkg.DB == nil {
		return `<p class="table-empty">The server's measurements are kept in the database, which is not open.</p>`, ""
	}
	site := scope
	if scope == analytics.ScopeAllDomains {
		site = sitehttp.AllSites
	}
	sum, byDay, err := sitehttp.Read(ctx, dbpkg.Reader(), site, days)
	if err != nil {
		return string(ui.Callout("danger", ui.Text("Not counted: "+err.Error()))), ""
	}
	if sum.Requests == 0 {
		return `<p class="table-empty">No request recorded yet.</p>`, ""
	}
	share := func(n int64) string {
		return strconv.FormatInt(n, 10) + " (" + osShareLabel(int(n), int(sum.Requests)) + ")"
	}
	atMost := func(ms int64) string { return "at most " + strconv.FormatInt(ms, 10) + " ms" }
	facts := [][]ui.HTML{
		{ui.Text("Requests"), ui.Text(strconv.FormatInt(sum.Requests, 10))},
		{ui.Text("Answered"), ui.HTML(share(sum.S2xx))},
		{ui.Text("Redirected"), ui.HTML(share(sum.S3xx))},
		{ui.Text("Not found"), ui.HTML(share(sum.S404))},
		{ui.Text("Refused or bad requests"), ui.HTML(share(sum.S4xx - sum.S404))},
		{ui.Text("Server errors"), ui.HTML(share(sum.S5xx))},
	}
	if sum.S5xx > 0 {
		facts[5][1] = ui.HTML(string(ui.State("danger", share(sum.S5xx))))
	}
	panel = string(ui.Table([]string{"Answered with", "Requests"}, facts, ""))
	if sum.Pages > 0 {
		panel += string(ui.Table([]string{"Server time", "Pages"}, [][]ui.HTML{
			{ui.Text("Pages answered in full"), ui.Text(strconv.FormatInt(sum.Pages, 10))},
			{ui.Text("Half in"), ui.Text(atMost(sum.P50MS))},
			{ui.Text("Three in four in"), ui.Text(atMost(sum.P75MS))},
			{ui.Text("Nineteen in twenty in"), ui.Text(atMost(sum.P95MS))},
			{ui.Text("Mean"), ui.Text(strconv.FormatFloat(sum.MeanMS, 'f', 1, 64) + " ms")},
		}, ""))
	}
	if days <= 31 && len(byDay) > 1 {
		rows := make([][]ui.HTML, 0, len(byDay))
		for _, d := range byDay {
			p75 := "No pages"
			if d.Pages > 0 {
				p75 = atMost(d.P75MS)
			}
			rows = append(rows, []ui.HTML{ui.Text(d.Day), ui.Text(strconv.FormatInt(d.Requests, 10)), ui.Text(p75),
				ui.Text(strconv.FormatInt(d.S404, 10)), ui.Text(strconv.FormatInt(d.S5xx, 10))})
		}
		byDayTable = string(ui.Table([]string{"Day", "Requests", "Pages, p75", "Not found", "Server errors"}, rows, ""))
	}
	return panel, byDayTable
}

// vitalWords is the three vitals for a line of text: each p75, or that it
// was not reported.
func vitalWords(v *vastore.WebVitals) string {
	parts := make([]string, 0, 3)
	for _, x := range vitalsOf(v) {
		val := "not reported"
		if x.Reported {
			val = x.Value
		}
		parts = append(parts, x.Name+" "+val)
	}
	return strings.Join(parts, ", ")
}
