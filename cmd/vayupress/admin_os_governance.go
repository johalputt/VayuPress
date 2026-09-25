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

	healthy, atRisk, exhausted := 0, 0, 0
	budgetRows := make([][]ui.HTML, 0, len(budgets))
	for _, b := range budgets {
		tone := "ok"
		switch b.State {
		case "healthy":
			healthy++
		case "at-risk":
			atRisk++
			tone = "warn"
		default:
			exhausted++
			tone = "danger"
		}
		window := (time.Duration(b.WindowSec) * time.Second).String()
		budgetRows = append(budgetRows, []ui.HTML{
			`<div>` + ui.Text(b.Name) + `</div><div class="muted text-xs">Tracks ` + ui.Text(strings.ToLower(b.Tracks)) + ` · ` + ui.Text(window) + ` window</div>`,
			ui.Text(strconv.Itoa(b.Consumed) + " of " + strconv.Itoa(b.Limit)),
			ui.Text(titleFirst(strings.ToLower(b.OnExhaust))),
			ui.Tag(tone, budgetStateLabel(b.State)),
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

	modeTone := ""
	if t := saModeTone(cur); t != "ok" {
		modeTone = t
	}
	budgetTone := ""
	if exhausted > 0 {
		budgetTone = "danger"
	} else if atRisk > 0 {
		budgetTone = "warn"
	}
	body := ui.Join(
		ui.Page("Governance", "How the install protects itself: the mode it is in, the budgets that move it, and why it changed.",
			`<a class="btn btn--sm" href="/os/modes">System state</a>`),
		ui.Figures(
			ui.Figure{Value: saModeLabel(cur), Label: "System mode", Tone: modeTone,
				Note: strconv.Itoa(len(history)) + " recorded transition" + plural(len(history))},
			ui.Figure{Value: strconv.Itoa(healthy) + " of " + strconv.Itoa(len(budgets)), Label: "Budgets healthy", Tone: budgetTone,
				Note: strconv.Itoa(atRisk) + " at risk · " + strconv.Itoa(exhausted) + " exhausted"},
		),
		ui.Section("Error budgets", applied,
			ui.Table([]string{"Budget", "Consumed", "On exhaust", "State"}, budgetRows, "No budgets configured.")),
		ui.Section("Mode changes", "Newest first",
			ui.Table([]string{"Change", "Reason", "When"}, transRows, "The install has not changed mode.")),
	)
	writeOSHTML(w, r, adminOSLayout(nonce, "Governance", "governance", cfg, body))
}
