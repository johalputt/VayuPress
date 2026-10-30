// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/ui"
)

// visitLogSize is how many visits the log shows.
const visitLogSize = 100

// visitLogReaders is the readers the visit log on view shows, and whether
// that view has one: Live shows the latest of all, a narrowed overview the
// latest of those it is narrowed to, and a page's report the latest that
// read the page.
func visitLogReaders(view string, per analyticsSpan) (analyticsSpan, bool) {
	switch {
	case view == "live":
		return per, true
	case view == "overview" && len(per.focus) > 0:
		return per, true
	case view == "page" && per.page != "":
		read, _ := per.with(analytics.ExploreFilter{Dim: "path", Op: analytics.FilterIs, Value: per.page})
		return read, true
	}
	return per, false
}

// osVisitLog is the latest visits of the readers ctx is narrowed to, each as
// it went: when, from where as finely as enough visits share it, on what,
// arriving how, then its steps, each page a way to that page's report with
// per's period and filters.
func (a *App) osVisitLog(ctx context.Context, v analyticsView, per analyticsSpan, now time.Time) ui.HTML {
	trails, err := a.analytics.VisitTrailsScoped(ctx, v.scope, per.days, visitLogSize)
	var list string
	switch {
	case err != nil:
		list = `<p class="table-empty">The visits could not be read: ` + esc(err.Error()) + `</p>`
	case len(trails) == 0:
		list = `<p class="table-empty">No visits ` + esc(per.when()) + `.</p>`
	default:
		var b strings.Builder
		b.WriteString(`<ol class="sa-trails">`)
		for _, t := range trails {
			b.WriteString(`<li><div class="sa-trails__meta">` + esc(trailMeta(t, now)) + `</div><div>` + trailPath(t, v.base, per) + `</div></li>`)
		}
		b.WriteString(`</ol>`)
		list = b.String()
	}
	return ui.HTML(`<div data-part="visits">`) + ui.Section("Latest visits",
		"Each visit as it went, newest first. A city is named once five visits share it; nothing names a reader, and only administrators see this.",
		ui.HTML(list)) + `</div>`
}

// trailMeta is a visit's first line: when it began, where from, on what, and
// how it arrived.
func trailMeta(t analytics.VisitTrail, now time.Time) string {
	parts := []string{humanAgo(t.Started, now)}
	place := countryName(t.Country)
	if t.Country == "" {
		place = "Unknown place"
	}
	if t.City != "" {
		place = t.City + ", " + place
	}
	parts = append(parts, place)
	for _, s := range []string{t.Device, t.Browser} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if t.Referrer == "" {
		parts = append(parts, "came directly")
	} else {
		parts = append(parts, "from "+t.Referrer)
	}
	return strings.Join(parts, " · ")
}

// trailPath is a visit's steps in order, then how long it stayed and whether
// it converted.
func trailPath(t analytics.VisitTrail, base string, per analyticsSpan) string {
	steps := make([]string, 0, len(t.Steps)+1)
	for _, s := range t.Steps {
		switch s.Kind {
		case "page":
			steps = append(steps, `<a href="`+esc(pageReportHref(base, s.Value, per))+`">`+esc(s.Value)+`</a>`)
		case "out":
			steps = append(steps, "out to "+esc(s.Value))
		case "download":
			steps = append(steps, "fetched "+esc(s.Value))
		case "event":
			steps = append(steps, "“"+esc(s.Value)+"”")
		case "search":
			steps = append(steps, "searched the site")
		}
	}
	if t.MoreSteps > 0 {
		steps = append(steps, strconv.Itoa(t.MoreSteps)+" more")
	}
	end := []string{strings.Join(steps, " → ")}
	if t.Seconds >= 1 {
		end = append(end, osDuration(t.Seconds)+" on the site")
	}
	if t.Converted {
		end = append(end, "converted")
	}
	return strings.Join(end, " · ")
}
