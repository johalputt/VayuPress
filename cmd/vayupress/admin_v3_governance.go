package main

// admin_v3_governance.go — VayuOS "Governance" panel.
//
// A dedicated control surface for the adaptive-governance runtime, distinct from
// the Monitoring page (which is about throughput/health). Governance focuses on
// the three pillars an operator reasons about when the system protects itself:
//
//   - System mode: the current protective mode and the recorded transition
//     lineage (who/why/when), so an escalation is always explainable.
//   - Error budgets: the severity-classified ledgers that drive escalation, with
//     consumption, window and the mode each would recommend on exhaustion.
//   - Policy engine: a live evaluation of every registered governance policy
//     (P-rules), grouped pass / warning / fail.
//
// Everything is rendered server-side from the same in-process sources the v1
// console and JSON APIs use. CSP posture matches the rest of VayuOS: no inline
// styles, no inline script (this page needs none), every dynamic string escaped.

import (
	"html"
	"net/http"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/policy"
	"github.com/johalputt/vayupress/internal/render"
)

// policyStatusPill maps a policy result to a status pill (class, label).
func policyStatusPill(p policy.PolicyResult) (string, string) {
	switch {
	case p.Passed:
		return "tool-status--on", "pass"
	case p.Severity == policy.SeverityBlocking:
		return "tool-status--off", "fail"
	default:
		return "tool-status--idle", "warn"
	}
}

func (a *App) handleV3Governance(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

	now := time.Now()
	cur := mode.Global.Current()
	history := mode.Global.History()
	budgets := budget.Global.Status(now)
	report := policy.Global.EvaluateAll(policy.Context{})

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
	pass, warn, fail := len(report.Passed), len(report.Warnings), len(report.Failed)

	summary := `<div class="stat-grid mb-6">` +
		monStat("System mode", string(cur), strconv.Itoa(len(history))+" transition(s)") +
		monStat("Policies", strconv.Itoa(pass)+" pass", strconv.Itoa(warn)+" warn · "+strconv.Itoa(fail)+" fail") +
		monStat("Budgets", strconv.Itoa(healthy)+" healthy", strconv.Itoa(atRisk)+" at-risk · "+strconv.Itoa(exhausted)+" exhausted") +
		`</div>`

	// ── Policy engine ────────────────────────────────────────────────────────
	policyRow := func(p policy.PolicyResult) string {
		cls, label := policyStatusPill(p)
		return `<tr>
  <td class="row-title">` + html.EscapeString(p.Name) + `<div class="row-meta">` + html.EscapeString(string(p.Category)) + ` · ` + html.EscapeString(string(p.Severity)) + `</div></td>
  <td class="muted text-sm">` + html.EscapeString(p.Message) + `</td>
  <td><span class="tool-status ` + cls + `">` + label + `</span></td>
</tr>`
	}
	policyRows := ""
	for _, p := range report.Failed {
		policyRows += policyRow(p)
	}
	for _, p := range report.Warnings {
		policyRows += policyRow(p)
	}
	for _, p := range report.Passed {
		policyRows += policyRow(p)
	}
	if policyRows == "" {
		policyRows = `<tr><td colspan="3" class="muted text-sm">No policies registered.</td></tr>`
	}
	policyCard := `<div class="card mb-6">
  <div class="card-title">Policy engine</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Policy</th><th>Detail</th><th>Status</th></tr></thead>
    <tbody>` + policyRows + `</tbody>
  </table></div>
  <div class="text-xs muted mt-3">Failures are shown first. Blocking failures gate releases; warnings are advisory.</div>
</div>`

	// ── Error budgets ────────────────────────────────────────────────────────
	budgetRows := ""
	for _, b := range budgets {
		window := (time.Duration(b.WindowSec) * time.Second).String()
		budgetRows += `<tr>
  <td class="row-title">` + html.EscapeString(b.Name) + `<div class="row-meta">tracks ` + html.EscapeString(b.Tracks) + ` · window ` + html.EscapeString(window) + `</div></td>
  <td class="muted text-sm">` + strconv.Itoa(b.Consumed) + ` / ` + strconv.Itoa(b.Limit) + `</td>
  <td class="muted text-sm">` + html.EscapeString(b.OnExhaust) + `</td>
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
		link("/admin/modes", "Mode transitions", "Drive the system-mode state machine and review the full journal.") +
		link("/admin/policy", "Policy provenance", "Per-policy evaluation log, run history and trend analysis.") +
		`</div>`

	body := `<div class="page-header">
  <h1>Governance</h1>
  <div class="page-actions"><span class="text-sm muted">adaptive runtime</span></div>
</div>` + summary + policyCard + budgetCard + transCard + consoles

	writeV3HTML(w, adminV3Layout(nonce, "Governance", "governance", cfg, body))
}
