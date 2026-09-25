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
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
)

func (a *App) handleOSGovernance(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	now := time.Now()
	cur := mode.Global.Current()
	history := mode.Global.History()
	budgets := budget.Global.Status(now)

	// ── Summary cards ────────────────────────────────────────────────────────
	healthy, atRisk, exhausted := 0, 0, 0
	for _, b := range budgets {
		switch b.State {
		case "healthy":
			healthy++
		case "at-risk":
			atRisk++
		default:
			exhausted++
		}
	}

	summary := `<div class="stat-grid mb-6">` +
		monStat("System mode", string(cur), strconv.Itoa(len(history))+" transition(s)") +
		monStat("Budgets", strconv.Itoa(healthy)+" healthy", strconv.Itoa(atRisk)+" at-risk · "+strconv.Itoa(exhausted)+" exhausted") +
		`</div>`

	// ── Error budgets ────────────────────────────────────────────────────────
	budgetRows := ""
	for _, b := range budgets {
		window := (time.Duration(b.WindowSec) * time.Second).String()
		budgetRows += `<tr>
  <td class="row-title">` + html.EscapeString(b.Name) + `<div class="row-meta">tracks ` + html.EscapeString(b.Tracks) + ` · window ` + html.EscapeString(window) + `</div></td>
  <td class="muted text-sm">` + strconv.Itoa(b.Consumed) + ` / ` + strconv.Itoa(b.Limit) + `</td>
  <td class="muted text-sm">` + html.EscapeString(sentenceWord(strings.ToLower(b.OnExhaust))) + `</td>
  <td><span class="tool-status ` + budgetStateClass(b.State) + `">` + html.EscapeString(b.State) + `</span></td>
</tr>`
	}
	if budgetRows == "" {
		budgetRows = `<tr><td colspan="4" class="muted text-sm">No budgets configured.</td></tr>`
	}
	budgetCard := `<div class="card mb-6">
  <div class="card-title">Error budgets</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Budget</th><th>Consumed</th><th>On exhaust</th><th>State</th></tr></thead>
    <tbody>` + budgetRows + `</tbody>
  </table></div>
  <div class="text-xs muted mt-3">Accounting + recommendation only — mode transitions are operator-gated, never auto-applied.</div>
</div>`

	// ── Mode transition lineage ──────────────────────────────────────────────
	transRows := ""
	hist := history
	// Show the most recent transitions first, capped to the latest 20.
	for i := len(hist) - 1; i >= 0 && i >= len(hist)-20; i-- {
		t := hist[i]
		transRows += `<tr>
  <td class="muted text-sm">` + html.EscapeString(string(t.From)) + ` → ` + html.EscapeString(string(t.To)) + `</td>
  <td class="muted text-sm">` + html.EscapeString(t.Reason) + `</td>
  <td class="muted text-sm">` + t.OccurredAt.UTC().Format("2006-01-02 15:04:05Z") + `</td>
</tr>`
	}
	if transRows == "" {
		transRows = `<tr><td colspan="3" class="muted text-sm">No transitions yet — the runtime has held its current mode since boot.</td></tr>`
	}
	transCard := `<div class="card mb-6">
  <div class="card-title">Mode transition lineage</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Transition</th><th>Reason</th><th>When (UTC)</th></tr></thead>
    <tbody>` + transRows + `</tbody>
  </table></div>
</div>`

	// ── Deep console links ───────────────────────────────────────────────────
	link := func(href, label, desc string) string {
		return `<a class="tool-card" href="` + href + `">
  <div class="tool-card__head"><div class="tool-card__title">` + html.EscapeString(label) + `</div></div>
  <div class="tool-card__desc">` + html.EscapeString(desc) + `</div>
</a>`
	}
	consoles := `<div class="tools-cat">Deep operator consoles</div>
<div class="tools-grid">` +
		link("/os/modes", "Mode transitions", "Drive the system-mode state machine and review the full journal.") +
		`</div>`

	body := `<div class="page-header">
  <h1>Governance</h1>
  <div class="page-actions"><span class="text-sm muted">adaptive runtime</span></div>
</div>
<p class="page-sub">The adaptive runtime that keeps your install healthy — modes, budgets and transparency, all decided on your own server.</p>` + summary + budgetCard + transCard + consoles

	writeOSHTML(w, r, adminOSLayout(nonce, "Governance", "governance", cfg, htmpl.HTML(body)))
}
