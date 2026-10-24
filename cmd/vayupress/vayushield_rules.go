// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_rules.go — the operator's request rules on the Security pages:
// the field's explanation and what each rule has matched since it was saved.

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/oprules"
)

// operatorRulesHint is the field's help, shared by the install's page and a
// site's.
const operatorRulesHint = `One rule per line: <code>block</code>, <code>challenge</code>, <code>allow</code>, <code>log</code> or <code>limit 20/min</code>, then conditions that must all hold &mdash; ` +
	`<code>path=</code>, <code>path^=</code> (starts with), <code>path*=</code> (contains), <code>method=GET,POST</code>, <code>country=CN,RU</code>, ` +
	`<code>client=</code> (human, goodbot, badbot, aiagent, headless, unknown), <code>score&gt;=0.6</code>, <code>score&lt;0.2</code>, <code>header:Name</code> or <code>header:Name=value</code>. ` +
	`The first rule that matches decides. Put <code>test</code> in front to count what a rule would do without doing it, and watch the count below before trusting it. ` +
	`A block refuses that one request and nothing else: the visitor's other pages are still served. ` +
	`A limit gives each visitor that many of the requests it matches, refilling evenly over the minute or hour (<code>limit 5/hour method=POST path=/contact</code>); past it they are asked to come back shortly, and nothing else happens to them. ` +
	`Rules run after the shield's own checks, so a verified search engine and a signed-in reader never reach them.`

// ruleDone is what a rule's action did to a request, as the count reads.
var ruleDone = map[oprules.Action]string{oprules.Block: "blocked", oprules.Challenge: "challenged",
	oprules.Allow: "allowed", oprules.Log: "logged"}

// operatorRuleHits lists each rule with what it has matched since it was
// saved, and names the lines that could not be read.
func operatorRuleHits(rules []*oprules.Rule, issues []string, now time.Time) string {
	if len(rules) == 0 && len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	if len(rules) > 0 {
		b.WriteString(`<ul class="muted text-xs" aria-label="What each rule has matched">`)
		for _, r := range rules {
			n, at := r.Hits()
			would := ""
			if r.Test {
				would = "would have "
			}
			count := would + ruleDone[r.Action] + " " + countOf(int(n), "request")
			if r.Action == oprules.Limit {
				// A limit's matches are mostly served; what it did is the refusals.
				count = "matched " + countOf(int(n), "request") + ", " + would + "refused " + strconv.FormatInt(r.Refused(), 10)
			}
			if n > 0 {
				count += ", last " + humanAgo(at, now)
			}
			b.WriteString(`<li>Line ` + strconv.Itoa(r.Line) + ` <code>` + html.EscapeString(r.Text) + `</code> &middot; ` + count + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	if len(issues) > 0 {
		b.WriteString(`<p class="text-sm"><strong>Not understood, so not applied:</strong> the other rules still are.</p><ul class="muted text-xs">`)
		for _, s := range issues {
			b.WriteString(`<li>` + html.EscapeString(s) + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	return b.String()
}

// shieldRuleCounts is the install's request rules for the connector: what each
// did, by line, and never the line itself. A rule can carry a value the
// operator would not hand out, a health-check header's secret for one.
func shieldRuleCounts(m *vayushield.Manager) map[string]any {
	rules, bad := m.OperatorRules()
	lines := make([]map[string]any, 0, len(rules))
	for _, r := range rules {
		n, at := r.Hits()
		row := map[string]any{"line": r.Line, "action": r.Action.String(), "test": r.Test, "hits": n}
		if r.Action == oprules.Limit {
			row["refused"] = r.Refused()
		}
		if !at.IsZero() {
			row["last_hit"] = at.UTC().Format(time.RFC3339)
		}
		lines = append(lines, row)
	}
	return map[string]any{"rules": lines, "unreadable_lines": len(bad)}
}

// expiredEntries names the list entries whose end has passed: they stay in
// the field as written, and without this nothing would say they no longer
// apply.
func expiredEntries(list []string) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="vs-feat"><p class="text-sm"><strong>Ended, so no longer applied:</strong> remove them when you next edit the list.</p><ul class="muted text-xs">`)
	for _, s := range list {
		b.WriteString(`<li>` + html.EscapeString(s) + `</li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// otherLimitsNote states the two limits that guard the install outside the
// shield, with the values the gates enforce, so the page that holds the
// shield's limits is not silent about them.
func (a *App) otherLimitsNote(ctx context.Context) string {
	fails, window, lock := auth.LockoutPolicy()
	afails, awindow, afirst, amax := auth.AccountLockoutPolicy()
	return `<div class="vs-feat"><p class="text-sm"><strong>Limits set elsewhere.</strong> Sign-in: ` + strconv.Itoa(fails) +
		` failed attempts within ` + humanizeWindow(window) + ` lock that address out for ` + humanizeWindow(lock) +
		`; ` + strconv.Itoa(afails) + ` within ` + humanizeWindow(awindow) + ` on one account, from any address and any door (the site, the console, mail apps, the app, recovery and two-factor codes), pause its sign-ins for ` +
		humanizeWindow(afirst) + `, doubling with each pause to ` + strconv.Itoa(int(amax.Hours())) + ` hours, and its owner is told. API and MCP keys: ` +
		rateLabel(a.apiDefaultRatePerMin(ctx)) + ` each, unless a key sets its own, on <a href="/os/apikeys">API keys</a>. None is affected by anything on this page.</p>` +
		lockedAccountsRows() + `</div>`
}
