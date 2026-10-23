// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_rules.go — the operator's request rules on the Security pages:
// the field's explanation and what each rule has matched since it was saved.

import (
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/oprules"
)

// operatorRulesHint is the field's help, shared by the install's page and a
// site's.
const operatorRulesHint = `One rule per line: <code>block</code>, <code>challenge</code>, <code>allow</code> or <code>log</code>, then conditions that must all hold &mdash; ` +
	`<code>path=</code>, <code>path^=</code> (starts with), <code>path*=</code> (contains), <code>method=GET,POST</code>, <code>country=CN,RU</code>, ` +
	`<code>client=</code> (human, goodbot, badbot, aiagent, headless, unknown), <code>score&gt;=0.6</code>, <code>score&lt;0.2</code>, <code>header:Name</code> or <code>header:Name=value</code>. ` +
	`The first rule that matches decides. Put <code>test</code> in front to count what a rule would do without doing it, and watch the count below before trusting it. ` +
	`A block refuses that one request and nothing else: the visitor's other pages are still served. ` +
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
			count := countOf(int(n), "request")
			done := ruleDone[r.Action]
			if r.Test {
				done = "would have " + done
			}
			count = done + " " + count
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
		if !at.IsZero() {
			row["last_hit"] = at.UTC().Format(time.RFC3339)
		}
		lines = append(lines, row)
	}
	return map[string]any{"rules": lines, "unreadable_lines": len(bad)}
}
