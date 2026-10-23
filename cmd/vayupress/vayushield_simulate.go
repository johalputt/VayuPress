// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_simulate.go — try a draft of the shield's rules before saving
// it: replay the readers' recorded page views against it and say how many
// it would have refused or challenged, and which line did.
//
// What can be replayed is what is recorded: a view's path, its site and its
// visit's country. Readers' addresses are never kept, so a network list
// cannot be tried this way, and a rule that tests a score or a header is
// named as not judged rather than guessed at. A limit depends on a reader's
// pace, which a record does not keep either. The answer is about readers,
// which is the question a draft raises: whom it would cost.

import (
	"context"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield/oprules"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// simulateGroups bounds the replay: the most viewed path, site and country
// groups, far more than a week of a busy install holds.
const simulateGroups = 50000

// shieldDraft is a draft of the rules a replay can judge. InstallRules, set
// when a site's draft is tried, are the install's rules as saved: they run
// before the site's, as in the shield.
type shieldDraft struct {
	InstallRules, Rules                      string
	DenyCountries, ChallengeCountries, Allow []string
	RefuseUnknown                            bool
}

// simOutcome is one group's verdict, and who gave it.
type simOutcome struct {
	views   int
	refused bool
	by      string
	path    string
	country string
}

// simResult is a replay's answer.
type simResult struct {
	Days, Views, Refused, Challenged int
	Truncated                        bool
	Unjudged                         []string // rule lines a recorded view cannot answer
	Issues                           []string // lines the draft could not read
	ByCause                          map[string]int
	Samples                          []simOutcome
}

// simulateShield replays views against d, in the middleware's order: the
// country lists first (they act before classification), then the rules.
// Readers are what the views record, so a rule's client is human.
func simulateShield(views []analytics.PathCountryViews, d shieldDraft, onion bool, days int) simResult {
	res := simResult{Days: days, ByCause: map[string]int{}}
	install, _ := oprules.Parse(d.InstallRules)
	set, bad := oprules.Parse(d.Rules)
	res.Issues = bad
	for _, r := range set.Rules() {
		if !r.Judgeable() {
			res.Unjudged = append(res.Unjudged, "line "+strconv.Itoa(r.Line)+": "+r.Text)
		}
	}
	label := "line "
	if d.InstallRules != "" {
		label = "this site's line "
	}
	pol, _ := policy.Compile(policy.Config{DenyCountries: d.DenyCountries, ChallengeCountries: d.ChallengeCountries,
		AllowCountries: d.Allow, RefuseUnknownCountry: d.RefuseUnknown, OnionMode: onion})
	var hit []simOutcome
	for _, v := range views {
		res.Views += v.Views
		country := v.Country
		if onion {
			country = "" // as the middleware withholds it
		}
		o := simOutcome{views: v.Views, path: v.Path, country: v.Country}
		switch pol.Country(country) {
		case policy.VerdictDeny:
			o.refused, o.by = true, "the country lists"
		case policy.VerdictChallenge:
			o.by = "the country lists"
		default:
			f := oprules.Facts{Path: v.Path, Method: http.MethodGet, Country: country, Client: "human"}
			rule, by := install.Would(f), "the install's line "
			if rule == nil {
				rule, by = set.Would(f), label
			}
			switch {
			case rule == nil || rule.Action == oprules.Allow:
				continue
			case rule.Action == oprules.Block:
				o.refused = true
			}
			o.by = by + strconv.Itoa(rule.Line) + ": " + rule.Text
		}
		if o.refused {
			res.Refused += v.Views
		} else {
			res.Challenged += v.Views
		}
		res.ByCause[o.by] += v.Views
		hit = append(hit, o)
	}
	res.Truncated = len(views) >= simulateGroups
	sort.SliceStable(hit, func(i, j int) bool { return hit[i].views > hit[j].views })
	if len(hit) > 8 {
		hit = hit[:8]
	}
	res.Samples = hit
	return res
}

// readerViews is the install's views (scope analytics.ScopeAllDomains) or one
// site's, over days.
func (a *App) readerViews(ctx context.Context, scope string, days int) ([]analytics.PathCountryViews, error) {
	if a.analytics == nil {
		return nil, nil
	}
	return a.analytics.ViewsByPathCountryScoped(ctx, scope, days, simulateGroups)
}

// shieldSimulateRow offers the replay beside the rules. It posts the whole
// form, so what is tried is what is typed, saved or not.
const shieldSimulateRow = `<div class="vs-adv vs-adv--open"><strong class="text-sm">Try before saving</strong> ` +
	`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/shield/simulate" hx-include="closest form" ` +
	`hx-target="#vs-sim-out" hx-swap="innerHTML">Last 7 days</button> ` +
	`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/shield/simulate" hx-include="closest form" ` +
	`hx-vals='{"sim_days":"1"}' hx-target="#vs-sim-out" hx-swap="innerHTML">Today</button> ` +
	`<span class="muted text-xs">Replays your readers' recorded page views against the rules and country lists as typed above, and says how many it would have refused or challenged, and which line did. Nothing is saved.</span>` +
	`<div id="vs-sim-out"></div></div>`

// handleOSShieldSimulate replays the install's draft, as the form holds it,
// against every site's readers.
func (a *App) handleOSShieldSimulate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid form", "")
		return
	}
	days := 7
	if r.PostFormValue("sim_days") == "1" {
		days = 1
	}
	views, err := a.readerViews(r.Context(), analytics.ScopeAllDomains, days)
	if err != nil {
		writeOSFragment(w, `<p class="text-sm">Could not read the recorded views: `+html.EscapeString(err.Error())+`</p>`)
		return
	}
	d := shieldDraft{
		Rules:              clipPolicyField(r.PostFormValue("sh_rules")),
		DenyCountries:      policyLines(clipPolicyField(r.PostFormValue("sh_deny_countries"))),
		ChallengeCountries: policyLines(clipPolicyField(r.PostFormValue("sh_challenge_countries"))),
		Allow:              policyLines(clipPolicyField(r.PostFormValue("sh_allow_countries"))),
		RefuseUnknown:      r.PostFormValue("sh_refuse_unknown") == "on",
	}
	writeOSFragment(w, simulationReport(simulateShield(views, d, config.Cfg.OnionMode, days)))
}

// handleOSScopedShieldSimulate replays a site's draft, as its form holds it,
// against that site's readers: the install's lists and rules as saved, then
// the site's, as the shield runs them there.
func (a *App) handleOSScopedShieldSimulate(w http.ResponseWriter, r *http.Request) {
	d, posted, ok := a.scopedSiteBody(w, r)
	if !ok {
		return
	}
	days := 7
	if posted["days"] == "1" {
		days = 1
	}
	views, err := a.readerViews(r.Context(), d.ID, days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "read-failed", err.Error(), "")
		return
	}
	inst := a.vayuShield.CurrentSettings()
	// The site's lists are added to the install's, never written into them.
	joinLists := func(install, site []string) []string { return append(append([]string(nil), install...), site...) }
	draft := shieldDraft{
		InstallRules:       inst.Rules,
		Rules:              posted[settings.KeyShieldSiteRules],
		DenyCountries:      joinLists(inst.Policy.DenyCountries, policyLines(posted[settings.KeyShieldSiteDenyCountries])),
		ChallengeCountries: joinLists(inst.Policy.ChallengeCountries, policyLines(posted[settings.KeyShieldSiteChallengeCountries])),
		Allow:              joinLists(inst.Policy.AllowCountries, policyLines(posted[settings.KeyShieldSiteAllowCountries])),
		RefuseUnknown:      inst.Policy.RefuseUnknownCountry,
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"report": simulationReport(simulateShield(views, draft, config.Cfg.OnionMode, days))})
}

// simulationReport says what a replay found, in figures and in the lines
// that did it.
func simulationReport(s simResult) string {
	var b strings.Builder
	period := "the last 7 days"
	if s.Days == 1 {
		period = "today"
	}
	if s.Views == 0 {
		return `<p class="text-sm" role="status">No reader views recorded in ` + period + `, so there is nothing to try this against.</p>`
	}
	pct := func(n int) string { return strconv.FormatFloat(100*float64(n)/float64(s.Views), 'f', 1, 64) + "%" }
	b.WriteString(`<p class="text-sm" role="status">Against ` + strconv.Itoa(s.Views) + ` reader views in ` + period + `, this draft would have refused <strong>` +
		strconv.Itoa(s.Refused) + `</strong> (` + pct(s.Refused) + `) and challenged <strong>` + strconv.Itoa(s.Challenged) + `</strong> (` + pct(s.Challenged) + `).`)
	if s.Truncated {
		b.WriteString(` Only the ` + strconv.Itoa(simulateGroups) + ` most viewed path and country pairs were tried.`)
	}
	b.WriteString(` Network lists and limits are not tried: no reader's address or pace is kept.</p>`)
	if len(s.ByCause) > 0 {
		causes := make([]string, 0, len(s.ByCause))
		for c := range s.ByCause {
			causes = append(causes, c)
		}
		sort.Slice(causes, func(i, j int) bool {
			if s.ByCause[causes[i]] != s.ByCause[causes[j]] {
				return s.ByCause[causes[i]] > s.ByCause[causes[j]]
			}
			return causes[i] < causes[j]
		})
		b.WriteString(`<ul class="muted text-xs" aria-label="What would have acted">`)
		for _, c := range causes {
			b.WriteString(`<li>` + html.EscapeString(c) + ` &middot; ` + countOf(s.ByCause[c], "view") + `</li>`)
		}
		b.WriteString(`</ul>`)
		b.WriteString(`<ul class="muted text-xs" aria-label="The most viewed pages it would have reached">`)
		for _, o := range s.Samples {
			what := "challenged"
			if o.refused {
				what = "refused"
			}
			where := o.country
			if where == "" {
				where = "country unknown"
			}
			b.WriteString(`<li><code>` + html.EscapeString(o.path) + `</code> from ` + html.EscapeString(where) + ` &middot; ` +
				countOf(o.views, "view") + ` ` + what + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	if len(s.Unjudged) > 0 {
		b.WriteString(`<p class="text-xs muted">Not judged, as a recorded view keeps no score or header: ` + html.EscapeString(strings.Join(s.Unjudged, "; ")) + `.</p>`)
	}
	if len(s.Issues) > 0 {
		b.WriteString(`<p class="text-xs muted">Not understood, so left out: ` + html.EscapeString(strings.Join(s.Issues, "; ")) + `.</p>`)
	}
	return b.String()
}
