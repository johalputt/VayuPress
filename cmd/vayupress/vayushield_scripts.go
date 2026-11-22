// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_scripts.go — the scripts visitors' browsers refused, on every
// site here, in Shield and in the bell.
//
// Each site's policy runs only the scripts it names, and a browser that
// refuses one reports it to /csp-report (cspevents.go keeps them per site,
// deduplicated by site, directive and source, capped). A refused script is
// how an injected one first shows: a skimmer added to a theme, a tag slipped
// into a hosted site's own code, a compromised outside service. The Outside
// services page of each site lists what it blocked so the operator can allow
// what they recognise; this puts the scripts of every site in one place, and
// a new one in front of the operator without their going to look.

import (
	"context"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/ui"
)

// scriptNewFor is how long a newly reported source counts as new in the bell.
const scriptNewFor = 24 * time.Hour

// refusedScript is one script source a site refused, with where it is
// allowed from.
type refusedScript struct {
	cspBlock
	Services string // the site's Outside services page
}

// refusedScripts is every script source reported by enough visitors on any
// site here, newest first.
func (a *App) refusedScripts(ctx context.Context) []refusedScript {
	pages := map[string]string{strings.ToLower(config.Cfg.Domain): "/os/website/services"}
	if a.domains != nil {
		if list, err := a.domains.List(ctx); err == nil {
			for _, d := range list {
				if !d.IsPrimary {
					pages[strings.ToLower(d.Host)] = "/os/d/" + d.ID + "/services"
				}
			}
		}
	}
	hosts := make([]string, 0, len(pages))
	for h := range pages {
		hosts = append(hosts, h)
	}
	var out []refusedScript
	for _, b := range cspBlocksFor(hosts) {
		if b.Directive == "script-src" {
			out = append(out, refusedScript{cspBlock: b, Services: pages[b.Host]})
		}
	}
	return out
}

// scriptsSectionBody is the Shield section.
func (a *App) scriptsSectionBody(ctx context.Context) string {
	var rows [][]ui.HTML
	for _, s := range a.refusedScripts(ctx) {
		rows = append(rows, []ui.HTML{
			ui.HTML(`<time datetime="` + s.Last.UTC().Format(time.RFC3339) + `">` + config.FormatSite(s.Last, "2 Jan 15:04") + `</time>`),
			ui.Text(s.Host),
			ui.HTML(`<code>` + string(ui.Text(s.Origin)) + `</code>`),
			ui.Text(itoaSafe(s.Count) + " time" + plural(s.Count)),
			ui.HTML(`<a href="` + s.Services + `">Outside services</a>`),
		})
	}
	return `<p class="muted text-sm">Each site runs only the scripts its policy names, and a browser that refuses another one says so. A script you did not add is how injected code first shows: a card skimmer in a theme, a tag slipped into a site's own code, an outside service that was taken over. Allow one on its site's Outside services page only if you recognise it. Anyone can send a report, so a source shows here once more than one visitor has reported it.</p>` +
		string(ui.Table([]string{"Last", "Site", "Script from", "Refused", ""}, rows, "No script has been refused on any site since the server started."))
}

// scriptNotices is the bell's line for script sources first reported within
// scriptNewFor.
func (a *App) scriptNotices(ctx context.Context, now time.Time) []osNotification {
	n := 0
	for _, s := range a.refusedScripts(ctx) {
		if now.Sub(s.First) < scriptNewFor {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []osNotification{said(i18n.Lang(ctx), osNotification{Href: "/os/shield#vs-body-scripts", Count: n, Kind: "security", Severity: "warn", State: true},
		func(l string) string { return i18n.T(l, "A script was refused") },
		func(l string) string {
			return i18n.T(l, "{n, plural, one {# new source} other {# new sources}} in the last day; Shield lists them", "n", n)
		})}
}
