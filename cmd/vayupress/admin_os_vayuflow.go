// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_vayuflow.go — /os/vayuflow, the automation console (ADR-0151).
//
// The page has one job beyond listing flows: make the blast radius of each one
// legible before it is armed. A flow's dangerous property is never its trigger
// or its name — it is what it may write, whose authority it borrows, and what
// it is allowed to spend. So those three are on the summary line of every
// accordion, visible while collapsed, rather than behind a click.
//
// Dry-run is the default and the page says what that means in the same words
// the engine uses: the whole flow executes, the effects are captured, and the
// capability boundary refuses them. An operator reading "test mode" here and
// "dry-run" in the trail would be reconciling two vocabularies for one state.

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/vayuflow"
	"github.com/johalputt/vayupress/internal/vayuflow/flowaudit"
)

// handleOSVayuFlow renders the automation console.
func (a *App) handleOSVayuFlow(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)

	var (
		flows    []vayuflow.Flow
		rejected map[string]error
		stats    vayuflow.Stats
		runs     []vayuflow.Run
	)
	if a.flowStore != nil {
		flows, rejected, _ = a.flowStore.LoadableFlows(r.Context())
		all, err := a.flowStore.List(r.Context())
		if err == nil {
			flows = all // show disabled flows too; the chip carries their state
		}
	}
	if a.flowRuns != nil {
		stats, _ = a.flowRuns.StatsSince(r.Context(), time.Now().Add(-24*time.Hour))
		runs, _ = a.flowRuns.Recent(r.Context(), "", 25)
	}

	pending := 0
	if a.flowInbox != nil {
		pending, _ = a.flowInbox.PendingCount(r.Context())
	}
	roles := map[string]string{}
	resolve := a.flowRoleResolver()
	for _, f := range flows {
		if _, seen := roles[f.Owner]; seen {
			continue
		}
		// An unresolvable owner is recorded as an EMPTY role rather than
		// skipped, so the report reads it as "could not check" and fails
		// closed — the same answer the runner gives.
		role, err := resolve(r.Context(), f.Owner)
		if err != nil {
			role = ""
		}
		roles[f.Owner] = role
	}
	checks := flowaudit.Run(flowaudit.Inputs{
		Wired:           a.flowStore != nil && a.flowRunner != nil,
		Flows:           flows,
		Rejected:        rejected,
		OwnerRoles:      roles,
		Stats:           stats,
		PendingInbox:    pending,
		OnionMode:       safefetch.ClearnetBlocked(),
		ModelConfigured: a.aiAssist != nil && a.aiAssist.Enabled(),
		ModelLocal:      flowModel{c: a.aiAssist}.Local(),
	})

	body := vayuFlowPage(flows, rejected, stats, runs, checks, a.flowStore != nil)
	full := adminOSShellHead(nonce, "VayuFlow", "vayuflow", cfg) + body +
		adminOSShellFoot(nonce, vayuFlowScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// handleOSVayuFlowArm moves a flow between dry-run and live.
//
// Arming is its own endpoint rather than a field on a save, because it is the
// moment a flow stops being a description and starts being an actor. The audit
// entry records the actor AND the prior mode: "armed" without a from-state
// cannot distinguish a first arming from a re-arming, and the difference
// matters when reading back why something started happening.
func (a *App) handleOSVayuFlowArm(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.flowStore == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", "automation store unavailable", "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "no flow id", "")
		return
	}
	mode, ok := armModeFor(r.URL.Query().Get("mode"))
	if !ok {
		// An unrecognised value must not silently pick a mode. Defaulting to
		// dry-run would be the safe-looking choice and would still be the
		// endpoint answering a question the operator asked.
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			`expected mode=live or mode=dry; nothing was changed`, "")
		return
	}
	prior, err := a.flowStore.SetMode(r.Context(), id, mode)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayuflow.arm", dbpkg.AuditActor(r), id,
		fmt.Sprintf("%s -> %s", prior, mode))
	writeJSON(w, r, http.StatusOK, map[string]string{
		"status": "ok", "mode": mode.String(), "prior": prior.String(),
	})
}

// armModeFor maps the query value to a mode. It returns ok=false for anything
// it does not recognise rather than choosing.
func armModeFor(v string) (vayuflow.RunMode, bool) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "live":
		return vayuflow.RunLive, true
	case "dry", "dry-run", "dryrun":
		return vayuflow.RunDryRun, true
	}
	return 0, false
}

// handleOSVayuFlowRun executes a flow once, now, on the operator's press.
func (a *App) handleOSVayuFlowRun(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.flowStore == nil || a.flowRunner == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", "automation engine unavailable", "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	f, err := a.flowStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", err.Error(), "")
		return
	}
	// Pressing Run twice IS two runs, so the identity is fresh each press.
	identity := "manual-" + newFlowArticleID()
	run, err := a.flowRunner.Execute(r.Context(), f, "manual · "+dbpkg.AuditActor(r), identity, vayuflow.Subject{})
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "run-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayuflow.run", dbpkg.AuditActor(r), id, string(run.Status))
	writeJSON(w, r, http.StatusOK, map[string]string{
		"status": "ok", "run": run.ID, "outcome": string(run.Status),
		"writes": fmt.Sprintf("%d / %d", run.Spend.Writes, run.Budget.MaxWritesPerRun),
	})
}

// flowCheckChip renders a posture verdict. Only Pass gets the "on" tone — a
// Context row is a fact, not a success, and toning it green would let the
// honest-ceiling row read as reassurance.
func flowCheckChip(s flowaudit.Status) string {
	switch s {
	case flowaudit.Pass:
		return `<span class="mon-chip mon-chip--on" title="` + s.String() + `">ok</span>`
	case flowaudit.Warn:
		return `<span class="mon-chip mon-chip--off" title="` + s.String() + `">look</span>`
	case flowaudit.Fail:
		return `<span class="mon-chip mon-chip--off" title="` + s.String() + `">act</span>`
	}
	return `<span class="mon-chip mon-chip--off" title="` + s.String() + `">note</span>`
}

func flowCheckIcon(s flowaudit.Status) string {
	switch s {
	case flowaudit.Pass:
		return "✓"
	case flowaudit.Fail:
		return "✕"
	case flowaudit.Warn:
		return "!"
	}
	return "·"
}

// flowModeChip renders a flow's arming state so it reads while collapsed.
func flowModeChip(f vayuflow.Flow) string {
	if !f.Enabled {
		return `<span class="mon-chip mon-chip--off">disabled</span>`
	}
	if f.Mode == vayuflow.RunLive {
		return `<span class="mon-chip mon-chip--on">live</span>`
	}
	return `<span class="mon-chip mon-chip--off">dry-run</span>`
}

// runStatusChip renders one run's outcome.
func runStatusChip(s vayuflow.RunStatus) string {
	switch s {
	case vayuflow.StatusSucceeded:
		return `<span class="mon-chip mon-chip--on">succeeded</span>`
	case vayuflow.StatusRefused:
		return `<span class="mon-chip mon-chip--off">refused</span>`
	case vayuflow.StatusInterrupted:
		return `<span class="mon-chip mon-chip--off">interrupted</span>`
	case vayuflow.StatusFailed:
		return `<span class="mon-chip mon-chip--off">failed</span>`
	}
	return `<span class="mon-chip mon-chip--off">` + html.EscapeString(string(s)) + `</span>`
}

// vayuFlowPage builds the console body. Pure, so it can be rendered and
// asserted on without a request or a database.
func vayuFlowPage(flows []vayuflow.Flow, rejected map[string]error,
	stats vayuflow.Stats, runs []vayuflow.Run, checks []flowaudit.Check, wired bool) string {
	esc := html.EscapeString
	var b strings.Builder

	b.WriteString(`<div class="page-header"><h1>VayuFlow</h1><div class="page-actions">` +
		`<a class="btn btn--ghost btn--sm" href="/os/adr">ADR-0151</a>` +
		`<span id="flow-status" class="text-sm muted" role="status" aria-live="polite"></span>` +
		`</div></div>`)
	b.WriteString(`<p class="page-sub">Automations this install runs on its own: when something ` +
		`happens, if a condition holds, do a bounded piece of work. Every flow is armed by a named ` +
		`operator, spends against ceilings it declared before it could be saved, and records what it ` +
		`did. <b>A new flow starts in dry-run</b>: the whole flow executes and every effect is ` +
		`captured and refused at the capability boundary, so what you read is what a live run would ` +
		`do.</p>`)

	armed := 0
	for _, f := range flows {
		if f.Enabled && f.Mode == vayuflow.RunLive {
			armed++
		}
	}
	b.WriteString(`<div class="stat-grid">` +
		osStatTile("Armed live", strconv.Itoa(armed), tone(armed > 0)) +
		osStatTile("Runs · 24h", strconv.Itoa(stats.Runs), "") +
		osStatTile("Refusals · 24h", strconv.Itoa(stats.Refused), "") +
		osStatTile("At a ceiling · 24h", strconv.Itoa(stats.BudgetCapped), tone(stats.BudgetCapped > 0)) +
		`</div>`)

	if !wired {
		b.WriteString(`<div class="warn-box">The automation engine is not running in this build, so ` +
			`nothing on this page can fire. Flows are listed from storage for reference only.</div>`)
	}

	// ── Posture ──────────────────────────────────────────────────────────────
	pass, warn, fail, ctxRows := flowaudit.Summary(checks)
	b.WriteString(`<div class="section-head"><span class="section-head__title">Posture</span>` +
		`<span class="section-head__hint">Computed now, from live state — ` +
		strconv.Itoa(fail) + ` to act on, ` + strconv.Itoa(warn) + ` to look at, ` +
		strconv.Itoa(pass) + ` ok, ` + strconv.Itoa(ctxRows) + ` noted</span></div>`)
	b.WriteString(`<div class="mon-stack">`)
	for i, c := range checks {
		b.WriteString(monAcc(flowCheckIcon(c.Status), esc(c.Title), "", flowCheckChip(c.Status),
			i == 0 && c.Status != flowaudit.Pass,
			`<div class="card"><p class="text-sm muted">`+esc(c.Detail)+`</p></div>`))
	}
	b.WriteString(`</div>`)

	// ── What can be automated ────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">What a flow may do</span>` +
		`<span class="section-head__hint">The capability registry, in full</span></div>`)
	b.WriteString(`<div class="card"><p class="text-sm muted">Every action an automation can take is ` +
		`registered with what it touches, the strongest write it may perform, what it does in a Tor ` +
		`Space, whether it can be undone, and the role its owner must still hold. An action with no ` +
		`registration cannot be invoked at all.</p>`)
	b.WriteString(`<table class="table"><thead><tr><th>Action</th><th>Writes</th><th>Tor</th>` +
		`<th>Undo</th><th>Owner must be</th></tr></thead><tbody>`)
	for _, c := range vayuflow.Capabilities() {
		b.WriteString(`<tr><td><span class="mono">` + esc(c.Action) + `</span></td>` +
			`<td>` + esc(c.Writes.String()) + `</td>` +
			`<td>` + esc(c.Onion.String()) + `</td>` +
			`<td>` + esc(c.Undo.String()) + `</td>` +
			`<td>` + esc(c.MinRole) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p class="text-sm muted">Settings, users, keys, domains, protection tiers and ` +
		`payment configuration are deliberately absent and cannot be automated in this version.</p>`)
	b.WriteString(`</div>`)

	// ── Flows ────────────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Flows</span>` +
		`<span class="section-head__hint">Arm, inspect &amp; run</span></div>`)
	if len(flows) == 0 {
		b.WriteString(`<div class="card"><p class="text-sm muted">No automations are defined yet.</p></div>`)
	}
	b.WriteString(`<div class="mon-stack">`)
	for i, f := range flows {
		var body strings.Builder
		body.WriteString(`<div class="card">`)
		body.WriteString(`<div class="settings-block-title">Blast radius</div>`)
		body.WriteString(`<p class="text-sm muted">Highest write <b>` + esc(f.HighestWrite().String()) +
			`</b> · owner <span class="mono">` + esc(f.Owner) + `</span> must hold <b>` +
			esc(f.MinOwnerRole()) + `</b> when it runs · ceilings: ` +
			strconv.Itoa(f.Budget.MaxStepsPerRun) + ` steps, ` +
			strconv.Itoa(f.Budget.MaxWritesPerRun) + ` writes, ` +
			strconv.Itoa(f.Budget.MaxEgressPerRun) + ` fetches, ` +
			strconv.Itoa(f.Budget.MaxRunsPerHour) + `/hour, ` +
			esc(f.Budget.Timeout.String()) + ` wall clock.</p>`)
		body.WriteString(`<div class="settings-block-title">Mode</div>`)
		body.WriteString(`<p class="text-sm muted">` + esc(vayuflow.DescribeMode(f.Mode)) + `</p>`)
		body.WriteString(`<div class="settings-block-title">Steps</div>`)
		body.WriteString(`<p class="text-sm"><span class="mono">` + esc(vayuflow.ActionSummary(f.Steps)) + `</span></p>`)
		if f.NeedsEgress() {
			body.WriteString(`<div class="warn-box">This flow reaches a remote host. It is inert while ` +
				`the install runs as a Tor Space.</div>`)
		}
		body.WriteString(`<div class="flex-between">` +
			`<button type="button" class="btn btn--primary btn--sm" data-flow-run="` + esc(f.ID) + `">Run once now</button>` +
			`<button type="button" class="btn btn--sm" data-flow-arm="` + esc(f.ID) + `" data-flow-mode="live">Arm live</button>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-flow-arm="` + esc(f.ID) + `" data-flow-mode="dry">Return to dry-run</button>` +
			`</div>`)
		body.WriteString(`</div>`)

		sub := vayuflow.TriggerSummary(f.Trigger) + " · v" + strconv.Itoa(f.Version)
		b.WriteString(monAcc("⚙", esc(f.Name), esc(sub), flowModeChip(f), i == 0, body.String()))
	}
	b.WriteString(`</div>`)

	if len(rejected) > 0 {
		b.WriteString(`<div class="card"><div class="settings-block-title">Flows that cannot run</div>` +
			`<p class="text-sm muted">These are enabled but their stored definition no longer ` +
			`validates, so the engine will not fire them. They are listed rather than skipped, because ` +
			`an automation an operator believes is armed and which silently never runs is the worst ` +
			`state this page can hide.</p><ul class="reset-list">`)
		for id, err := range rejected {
			b.WriteString(`<li class="text-sm"><span class="mono">` + esc(id) + `</span> — ` + esc(err.Error()) + `</li>`)
		}
		b.WriteString(`</ul></div>`)
	}

	// ── The trail ────────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Recent runs</span>` +
		`<span class="section-head__hint">What actually happened, and what it spent</span></div>`)
	b.WriteString(`<div class="card">`)
	if len(runs) == 0 {
		b.WriteString(`<p class="text-sm muted">Nothing has run yet.</p>`)
	}
	for _, run := range runs {
		var body strings.Builder
		body.WriteString(`<div class="card"><p class="text-sm muted">` +
			`Flow version <b>v` + strconv.Itoa(run.FlowVersion) + `</b> · cause <span class="mono">` +
			esc(run.Cause) + `</span> · mode <b>` + esc(run.Mode.String()) + `</b> · owner ` +
			`<span class="mono">` + esc(run.Owner) + `</span> ran as <b>` + esc(run.OwnerRole) + `</b></p>`)
		// Spend beside its ceiling. A count without its limit means nothing, and
		// a limit without its count hides the flow living at the edge of it.
		body.WriteString(`<p class="text-sm">Spent — steps <b>` +
			strconv.Itoa(run.Spend.Steps) + ` / ` + strconv.Itoa(run.Budget.MaxStepsPerRun) + `</b>, writes <b>` +
			strconv.Itoa(run.Spend.Writes) + ` / ` + strconv.Itoa(run.Budget.MaxWritesPerRun) + `</b>, fetches <b>` +
			strconv.Itoa(run.Spend.Egress) + ` / ` + strconv.Itoa(run.Budget.MaxEgressPerRun) + `</b></p>`)
		if run.Error != "" {
			body.WriteString(`<div class="warn-box">` + esc(run.Error) + `</div>`)
		}
		for _, st := range run.Steps {
			body.WriteString(`<div class="settings-block-title"><span class="mono">` + esc(st.Action) + `</span></div>`)
			if st.Did != "" {
				// What the step actually performed. A model call belongs here
				// even in a dry run: §8 requires the generation to be real, and
				// a reader must be able to tell that it was.
				body.WriteString(`<p class="text-sm muted">` + esc(st.Did) + `</p>`)
			}
			if st.Refused != "" {
				// The dry-run diff: what this step WOULD have done.
				body.WriteString(`<pre class="code-block">` + esc(st.Refused) + `</pre>`)
			}
			if st.Output != "" {
				body.WriteString(`<p class="text-sm">` + esc(st.Output) + `</p>`)
			}
			if st.Error != "" {
				body.WriteString(`<p class="text-sm"><span class="mono">` + esc(st.Error) + `</span></p>`)
			}
		}
		body.WriteString(`</div>`)
		when := run.StartedAt.Format("2006-01-02 15:04")
		b.WriteString(monAcc("▷", esc(when), esc(run.FlowID), runStatusChip(run.Status), false, body.String()))
	}
	b.WriteString(`</div>`)

	return b.String()
}

// vayuFlowScript wires the arm and run buttons. One nonce'd script, no inline
// handlers, no Alpine.
const vayuFlowScript = `
(function(){'use strict';
var status=document.getElementById('flow-status');
function say(t){ if(status){ status.textContent=t; } }
function post(url,done){
  fetch(url,{method:'POST',headers:{'X-CSRF-Token':(document.cookie.match(/(?:^|; )vayu_csrf=([^;]*)/)||[])[1]||''}})
    .then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});})
    .then(function(res){ done(res); })
    .catch(function(){ say('Request failed.'); });
}
document.addEventListener('click',function(ev){
  var arm=ev.target.closest('[data-flow-arm]');
  if(arm){
    var id=arm.getAttribute('data-flow-arm'), mode=arm.getAttribute('data-flow-mode');
    say('Working…');
    post('/os/api/vayuflow/arm?id='+encodeURIComponent(id)+'&mode='+encodeURIComponent(mode),function(res){
      if(res.ok){ say('Mode: '+res.d.prior+' → '+res.d.mode+'. Reload to refresh.'); }
      else { say((res.d&&res.d.error&&(res.d.error.message||res.d.error))||'Could not change mode.'); }
    });
    return;
  }
  var run=ev.target.closest('[data-flow-run]');
  if(run){
    say('Running…');
    post('/os/api/vayuflow/run?id='+encodeURIComponent(run.getAttribute('data-flow-run')),function(res){
      if(res.ok){ say('Run '+res.d.outcome+' — writes '+res.d.writes+'. Reload to see the trail.'); }
      else { say((res.d&&res.d.error&&(res.d.error.message||res.d.error))||'Run failed.'); }
    });
  }
});
})();
`

// flowRoleResolver reads an owner's CURRENT role from the user store. It is the
// function that makes a demotion take effect: nothing about authority is cached
// on the flow.
func (a *App) flowRoleResolver() vayuflow.RoleResolver {
	return func(ctx context.Context, owner string) (string, error) {
		if a.userStore == nil {
			return "", fmt.Errorf("vayuflow: no user store; cannot resolve owner authority")
		}
		u, err := a.userStore.GetByID(ctx, owner)
		if err != nil {
			return "", err
		}
		return u.Role, nil
	}
}
