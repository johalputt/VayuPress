// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_governance.go — VayuOS "Governance" panel.
//
// A dedicated control surface for the adaptive-governance runtime, distinct from
// the Monitoring page (which is about throughput/health). Governance focuses on
// the two pillars an operator reasons about when the system protects itself:
//
//   - System mode: the current protective mode and the recorded transition
//     lineage (who/why/when), so an escalation is always explainable.
//   - Error budgets: the severity-classified ledgers that drive escalation, with
//     consumption, window and the mode each would recommend on exhaustion.
//
// Everything is rendered server-side from the same in-process sources the v1
// console and JSON APIs use. CSP posture matches the rest of VayuOS: no inline
// styles, no inline script (this page needs none), every dynamic string escaped.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

func (a *App) handleOSGovernance(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	now := time.Now()
	cur := mode.Global.Current()
	history := mode.Global.History()
	budgets := budget.Global.Status(now)

	atRisk, exhausted := 0, 0
	budgetRows := make([][]ui.HTML, 0, len(budgets))
	for _, b := range budgets {
		switch b.State {
		case "healthy":
		case "at-risk":
			atRisk++
		default:
			exhausted++
		}
		window := (time.Duration(b.WindowSec) * time.Second).String()
		budgetRows = append(budgetRows, []ui.HTML{
			`<div>` + ui.Text(b.Name) + `</div><div class="muted text-xs">Tracks ` + ui.Text(strings.ToLower(b.Tracks)) + ` · ` + ui.Text(window) + ` window</div>`,
			ui.Text(strconv.Itoa(b.Consumed) + " of " + strconv.Itoa(b.Limit)),
			ui.Text(titleFirst(strings.ToLower(b.OnExhaust))),
			ui.State(budgetTone(b.State), budgetStateLabel(b.State)),
		})
	}

	// Most recent first, the latest 20. A restart brings the journal's history
	// back, so this reads the same after one as before it.
	transRows := [][]ui.HTML{}
	for i := len(history) - 1; i >= 0 && i >= len(history)-20; i-- {
		t := history[i]
		transRows = append(transRows, []ui.HTML{
			ui.Text(saModeLabel(t.From) + " → " + saModeLabel(t.To)),
			ui.Text(t.Reason),
			`<span class="muted">` + ui.Text(config.InSite(t.OccurredAt).Format("2 Jan 15:04")) + `</span>`,
		})
	}

	// Whether a budget's recommendation changes the mode by itself is a setting,
	// so the page reads it rather than asserting either answer.
	applied := "Recommendations only: changing the mode is yours, on System state."
	if budget.GlobalActuator.Enabled() {
		applied = "Applied automatically: an exhausted budget moves the install into the mode it names."
	}

	// The sentence says the mode and whether any budget wants attention: the
	// two things this page exists to answer, and the figures that said them
	// before read zero on almost every visit.
	tone, state := governanceState(cur, atRisk, exhausted)
	detail := "The install has not changed mode."
	if n := len(history); n > 0 {
		last := history[n-1]
		detail = strconv.Itoa(n) + " mode change" + plural(n) + "; the last on " + config.InSite(last.OccurredAt).Format("2 Jan 15:04")
	}
	body := ui.Status(ui.StatusPage{
		Title:   "Governance",
		Actions: `<a class="btn btn--sm" href="/os/modes">System state</a>`,
		Tone:    tone,
		State:   state,
		Detail:  ui.Text(detail),
	},
		ui.Section("Error budgets", applied,
			ui.Table([]string{"Budget", "Consumed", "On exhaust", "State"}, budgetRows, "No budgets configured.")),
		ui.Section("Mode changes", "Newest first",
			ui.Table([]string{"Change", "Reason", "When"}, transRows, "The install has not changed mode.")),
	)
	writeOSHTML(w, r, adminOSLayout(nonce, "Governance", "governance", cfg, body))
}

// governanceState is the sentence Governance opens on: the mode, and whether
// any budget wants attention. An exhausted budget outranks the mode's own
// tone, because it is what would move the mode next.
func governanceState(cur mode.Mode, atRisk, exhausted int) (tone, state string) {
	tone, state = saModeTone(cur), "In "+strings.ToLower(saModeLabel(cur))+" mode"
	switch {
	case exhausted > 0:
		return "danger", state + ", with " + strconv.Itoa(exhausted) + " budget" + plural(exhausted) + " exhausted"
	case atRisk > 0:
		if tone == "ok" {
			tone = "warn"
		}
		return tone, state + ", with " + strconv.Itoa(atRisk) + " budget" + plural(atRisk) + " at risk"
	}
	return tone, state + ", and every budget is healthy"
}
