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
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayuflow"
	"github.com/johalputt/vayupress/internal/vayuflow/flowaudit"
)

// handleOSVayuFlow renders the automation console.
func (a *App) handleOSVayuFlow(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	lang := i18n.Lang(r.Context())
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
		Lang:            lang,
	})

	body := vayuFlowPage(lang, flows, rejected, stats, runs, checks, a.flowStore != nil, flowModel{c: a.aiAssist}.Local()) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-vayuflow.js?v=` + assetVer("js/admin-os-vayuflow.js") + `"></script>`
	full := adminOSShellHead(nonce, "VayuFlow", "vayuflow", cfg) + body +
		adminOSShellFoot(nonce, "", pageUsesAlpine(body))
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
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator can do this."), "")
		return
	}
	if a.flowStore == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", i18n.T(i18n.Lang(r.Context()), "The automation store is not available on this install."), "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", i18n.T(i18n.Lang(r.Context()), "The request names no flow."), "")
		return
	}
	mode, ok := armModeFor(r.URL.Query().Get("mode"))
	if !ok {
		// An unrecognised value must not silently pick a mode. Defaulting to
		// dry-run would be the safe-looking choice and would still be the
		// endpoint answering a question the operator asked.
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			i18n.T(i18n.Lang(r.Context()), "Nothing was changed: the mode must be {live} or {dry}.", "live", "mode=live", "dry", "mode=dry"), "")
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
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator can do this."), "")
		return
	}
	if a.flowStore == nil || a.flowRunner == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-store", i18n.T(i18n.Lang(r.Context()), "The automation engine is not available on this install."), "")
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
func flowCheckChip(lang string, s flowaudit.Status) string {
	switch s {
	case flowaudit.Pass:
		return string(ui.State("ok", i18n.T(lang, "Ok")))
	case flowaudit.Warn:
		return string(ui.State("warn", i18n.T(lang, "Look")))
	case flowaudit.Fail:
		return string(ui.State("danger", i18n.T(lang, "Act")))
	}
	return string(ui.State("neutral", i18n.T(lang, "Note")))
}

// flowModeChip renders a flow's arming state so it reads while collapsed.
// Only a live flow takes the accent: a dry-run one must never read as armed.
func flowModeChip(lang string, f vayuflow.Flow) string {
	if !f.Enabled {
		return string(ui.State("neutral", i18n.T(lang, "Disabled")))
	}
	if f.Mode == vayuflow.RunLive {
		return string(ui.State("accent", i18n.T(lang, "Live")))
	}
	return string(ui.State("neutral", i18n.T(lang, "Dry-run")))
}

// runStatusChip renders one run's outcome.
func runStatusChip(lang string, s vayuflow.RunStatus) string {
	switch s {
	case vayuflow.StatusSucceeded:
		return string(ui.State("ok", i18n.T(lang, "Succeeded")))
	case vayuflow.StatusRefused:
		return string(ui.State("warn", i18n.T(lang, "Refused")))
	case vayuflow.StatusInterrupted:
		return string(ui.State("warn", i18n.T(lang, "Interrupted")))
	case vayuflow.StatusFailed:
		return string(ui.State("danger", i18n.T(lang, "Failed")))
	case vayuflow.StatusRunning:
		return string(ui.State("neutral", i18n.T(lang, "Running")))
	}
	return string(ui.State("neutral", titleFirst(string(s))))
}

// The engine's words for a flow are its own, in English, for the audit trail
// and the JSON the page's script reads; the page names them in lang. Each
// returns the engine's word for a value it does not know.

// flowModeName is a run mode's one word.
func flowModeName(lang string, m vayuflow.RunMode) string {
	switch m {
	case vayuflow.RunDryRun:
		return i18n.T(lang, "dry-run")
	case vayuflow.RunLive:
		return i18n.T(lang, "live")
	}
	return m.String()
}

// flowModeSentence says what a mode means, in the engine's own word for it
// ("dry-run", as the trail records it): a panel that said "test mode" while
// the trail says "dry-run" would make an operator reconcile two
// vocabularies for one state.
func flowModeSentence(lang string, m vayuflow.RunMode) string {
	switch m {
	case vayuflow.RunDryRun:
		return i18n.T(lang, "dry-run — effects are captured and refused at the capability boundary")
	case vayuflow.RunLive:
		return i18n.T(lang, "live — effects reach the world, bounded by the declared budget")
	}
	return i18n.T(lang, "unset — this flow cannot run")
}

// flowTriggerSummary renders a trigger in one short phrase; a cron
// expression and an event's name are written as they are.
func flowTriggerSummary(lang string, t vayuflow.Trigger) string {
	switch t.Kind {
	case vayuflow.TriggerSchedule:
		return i18n.T(lang, "schedule · {cron}", "cron", t.Cron)
	case vayuflow.TriggerEvent:
		return i18n.T(lang, "event · {event}", "event", t.Event)
	case vayuflow.TriggerManual:
		return i18n.T(lang, "manual")
	}
	return t.Kind.String()
}

// flowWriteWord names the strongest write an action may perform.
func flowWriteWord(lang string, w vayuflow.WritePolicy) string {
	switch w {
	case vayuflow.WriteNone:
		return i18n.T(lang, "none")
	case vayuflow.WriteDraft:
		return i18n.T(lang, "draft")
	case vayuflow.WriteLive:
		return i18n.T(lang, "live")
	}
	return w.String()
}

// flowOnionWord names what an action does in a Tor Space.
func flowOnionWord(lang string, o vayuflow.OnionPolicy) string {
	switch o {
	case vayuflow.OnionInert:
		return i18n.T(lang, "inert")
	case vayuflow.OnionActive:
		return i18n.T(lang, "active")
	}
	return o.String()
}

// flowUndoWord names whether an action can be undone.
func flowUndoWord(lang string, r vayuflow.Reversible) string {
	switch r {
	case vayuflow.ReversibleByOperator:
		return i18n.T(lang, "reversible")
	case vayuflow.Irreversible:
		return i18n.T(lang, "irreversible")
	}
	return r.String()
}

// recorded is what the engine wrote down, shown as written: an error, what a
// step did or would have done, its output. It is in the language it was
// recorded in, so the page's language does not claim it.
func recorded(tag, class, text string) string {
	return `<` + tag + ` class="` + class + `" translate="no">` + esc(text) + `</` + tag + `>`
}

// vayuFlowPage builds the console body. Pure, so it can be rendered and
// asserted on without a request or a database.
// modelLocal is passed rather than inferred because the flow document cannot
// know it: a model step is outbound only when the configured provider is
// remote, and only the install can say which it has. The banner below used to
// answer without asking, and got it wrong for every local provider.
func vayuFlowPage(lang string, flows []vayuflow.Flow, rejected map[string]error,
	stats vayuflow.Stats, runs []vayuflow.Run, checks []flowaudit.Check,
	wired, modelLocal bool) string {
	var b strings.Builder
	t := func(s string) string { return esc(s) }

	armed := 0
	for _, f := range flows {
		if f.Enabled && f.Mode == vayuflow.RunLive {
			armed++
		}
	}
	pass, warn, fail, ctxRows := flowaudit.Summary(checks)
	tone, state := vayuFlowState(lang, wired, fail, warn, armed)
	head := ui.StatusPage{
		Title: "VayuFlow",
		Actions: ui.HTML(`<span id="flow-status" class="text-sm muted" role="status" aria-live="polite"></span>` +
			`<a class="btn btn--ghost btn--sm" href="/os/adr">` + flowADR + `</a>` +
			`<button type="button" class="btn btn--primary btn--sm" data-sheet="flow-create">` + t(i18n.T(lang, "Create a flow")) + `</button>`),
		Tone:  tone,
		State: state,
		Detail: ui.Text(i18n.T(lang, "{runs, plural, one {# run} other {# runs}}, {refused} refused and {capped} at a ceiling in the last day",
			"runs", stats.Runs, "refused", stats.Refused, "capped", stats.BudgetCapped)),
	}
	b.WriteString(string(ui.ExplainTitled(i18n.T(lang, "How this works"), ui.HTML(`<p>`+escFill(i18n.T(lang, "Every flow is armed by a named operator, spends against ceilings it declared before it could be saved, and records what it did. {lead}: the whole flow executes and every effect is captured and refused at the capability boundary, so what you read is what a live run would do.",
		"lead", htmlMark(0)), `<b>`+t(i18n.T(lang, "A new flow starts in dry-run"))+`</b>`)+`</p>`))))

	// ── Posture ──────────────────────────────────────────────────────────────
	posture := make([]ui.Row, 0, len(checks))
	for _, c := range checks {
		posture = append(posture, ui.Row{Label: c.Title, Hint: c.Detail, Control: ui.HTML(flowCheckChip(lang, c.Status))})
	}
	b.WriteString(string(ui.Section(i18n.T(lang, "Posture"),
		i18n.T(lang, "computed now: {fail} to act on, {warn} to look at, {pass} ok, {noted} noted", "fail", fail, "warn", warn, "pass", pass, "noted", ctxRows),
		ui.Rows(posture...))))

	// ── What can be automated ────────────────────────────────────────────────
	var caps [][]ui.HTML
	for _, c := range vayuflow.Capabilities() {
		caps = append(caps, []ui.HTML{`<span class="mono">` + ui.Text(c.Action) + `</span>`, ui.Text(flowWriteWord(lang, c.Writes)),
			ui.Text(flowOnionWord(lang, c.Onion)), ui.Text(flowUndoWord(lang, c.Undo)), ui.Text(teamRoleName(lang, c.MinRole))})
	}
	b.WriteString(string(ui.Section(i18n.T(lang, "What a flow may do"), i18n.T(lang, "an action not listed cannot be invoked"), ui.Join(
		ui.Table([]string{i18n.T(lang, "Action"), i18n.T(lang, "Writes"), "Tor", i18n.T(lang, "Undo"), i18n.T(lang, "Owner must be")}, caps, ""),
		ui.HTML(`<p class="text-sm muted">`+t(i18n.T(lang, "Settings, users, keys, domains, protection and payments cannot be automated."))+
			string(ui.Tip(i18n.T(lang, "Every action an automation can take is registered with what it touches, the strongest write it may perform, what it does in a Tor Space, whether it can be undone, and the role its owner must still hold.")))+`</p>`),
	))))

	// ── Flows ────────────────────────────────────────────────────────────────
	b.WriteString(`<section><div class="section-head"><h2 class="section-head__title">` + t(i18n.T(lang, "Flows")) + `</h2>` +
		`<span class="section-head__hint">` + t(i18n.T(lang, "{n} · {live} live", "n", len(flows), "live", armed)) + `</span></div>`)
	if len(flows) == 0 {
		b.WriteString(string(ui.Empty("flow", i18n.T(lang, "No automations yet"), i18n.T(lang, "One you create starts switched off, in dry-run."),
			ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="flow-create">`+t(i18n.T(lang, "Create a flow"))+`</button>`))))
	}
	b.WriteString(`<div class="mon-stack">`)
	for i, f := range flows {
		var body strings.Builder
		body.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "Blast radius")) + `</div>`)
		body.WriteString(`<p class="text-sm muted">` + escFill(i18n.T(lang, "Highest write {write} · owner {owner} must hold {role} when it runs · ceilings: {steps} steps, {writes} writes, {fetches} fetches, {hourly}/hour, {timeout} wall clock.",
			"write", htmlMark(0), "owner", htmlMark(1), "role", htmlMark(2), "steps", f.Budget.MaxStepsPerRun, "writes", f.Budget.MaxWritesPerRun,
			"fetches", f.Budget.MaxEgressPerRun, "hourly", f.Budget.MaxRunsPerHour, "timeout", f.Budget.Timeout.String()),
			`<b>`+t(flowWriteWord(lang, f.HighestWrite()))+`</b>`, `<span class="mono">`+t(f.Owner)+`</span>`, `<b>`+t(teamRoleName(lang, f.MinOwnerRole()))+`</b>`) + `</p>`)
		body.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "Mode")) + `</div>`)
		body.WriteString(`<p class="text-sm muted">` + t(flowModeSentence(lang, f.Mode)) + `</p>`)
		body.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "Steps")) + `</div>`)
		body.WriteString(`<p class="text-sm"><span class="mono">` + esc(vayuflow.ActionSummary(f.Steps)) + `</span></p>`)
		if f.NeedsEgress() {
			body.WriteString(`<p class="text-sm">` + string(ui.State("warn", i18n.T(lang, "This flow reaches a remote host."))) +
				` ` + t(i18n.T(lang, "It is inert while the install runs as a Tor Space.")) + `</p>`)
		}
		// Said separately, and only about what is true. A model step reaches out
		// when the provider is remote and does not when it runs here, so the
		// banner names which of the two this install has rather than assuming the
		// dangerous one and calling every local generation an outbound call.
		if f.NeedsModel() {
			if modelLocal {
				body.WriteString(`<div class="text-sm muted">` + t(i18n.T(lang, "This flow calls a model. The configured provider runs on this host, so the step is not an outbound call and stays active in a Tor Space.")) + `</div>`)
			} else {
				body.WriteString(`<p class="text-sm">` + string(ui.State("warn", i18n.T(lang, "This flow calls a model, and the configured provider is remote."))) +
					` ` + t(i18n.T(lang, "That makes the step an outbound call, so it is inert while the install runs as a Tor Space.")) + `</p>`)
			}
		}
		enableLabel, enableTo := i18n.T(lang, "Switch on"), "true"
		if f.Enabled {
			enableLabel, enableTo = i18n.T(lang, "Switch off"), "false"
		}
		body.WriteString(`<div class="flex-between">` +
			`<button type="button" class="btn btn--primary btn--sm" data-flow-run="` + esc(f.ID) + `">` + t(i18n.T(lang, "Run once now")) + `</button>` +
			`<button type="button" class="btn btn--sm" data-flow-enable="` + esc(f.ID) + `" data-flow-on="` + enableTo + `">` + t(enableLabel) + `</button>` +
			`<button type="button" class="btn btn--sm" data-flow-arm="` + esc(f.ID) + `" data-flow-mode="live">` + t(i18n.T(lang, "Arm live")) + `</button>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-flow-arm="` + esc(f.ID) + `" data-flow-mode="dry">` + t(i18n.T(lang, "Return to dry-run")) + `</button>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-flow-delete="` + esc(f.ID) + `" data-flow-name="` + esc(f.Name) + `">` + t(i18n.T(lang, "Delete")) + `</button>` +
			`</div>`)

		sub := flowTriggerSummary(lang, f.Trigger) + " · v" + strconv.Itoa(f.Version)
		// A flow's name is its operator's words, in their language, so the
		// page's language does not claim it.
		b.WriteString(strings.Replace(monAcc(saIcon("settings"), f.Name, sub, flowModeChip(lang, f), i == 0, body.String()),
			`<span class="mon-acc__title">`, `<span class="mon-acc__title" translate="no">`, 1))
	}
	b.WriteString(`</div></section>`)

	if len(rejected) > 0 {
		// Listed rather than skipped: an automation an operator believes is
		// armed and which silently never runs is the worst state this page can
		// hide. The reason is the engine's, as it recorded it.
		var rows []ui.Row
		for id, err := range rejected {
			rows = append(rows, ui.Row{Label: id, Hint: err.Error(), Control: ui.State("danger", i18n.T(lang, "Will not fire"))})
		}
		b.WriteString(string(ui.Section(i18n.T(lang, "Flows that cannot run"), i18n.T(lang, "enabled, but no longer valid"), ui.Rows(rows...))))
	}

	// ── The trail ────────────────────────────────────────────────────────────
	b.WriteString(`<section><div class="section-head"><h2 class="section-head__title">` + t(i18n.T(lang, "Recent runs")) + `</h2>` +
		`<span class="section-head__hint">` + t(i18n.T(lang, "what happened, and what it spent")) + `</span></div>`)
	if len(runs) == 0 {
		b.WriteString(`<p class="text-sm muted">` + t(i18n.T(lang, "Nothing has run yet.")) + `</p>`)
	}
	b.WriteString(`<div class="mon-stack">`)
	for _, run := range runs {
		var body strings.Builder
		body.WriteString(`<p class="text-sm muted">` + escFill(i18n.T(lang, "Flow version {version} · cause {cause} · mode {mode} · owner {owner} ran as {role}",
			"version", htmlMark(0), "cause", htmlMark(1), "mode", htmlMark(2), "owner", htmlMark(3), "role", htmlMark(4)),
			`<b>v`+strconv.Itoa(run.FlowVersion)+`</b>`, `<span class="mono" translate="no">`+esc(run.Cause)+`</span>`,
			`<b>`+t(flowModeName(lang, run.Mode))+`</b>`, `<span class="mono">`+esc(run.Owner)+`</span>`, `<b>`+t(teamRoleName(lang, run.OwnerRole))+`</b>`) + `</p>`)
		// Spend beside its ceiling. A count without its limit means nothing, and
		// a limit without its count hides the flow living at the edge of it.
		body.WriteString(`<p class="text-sm">` + escFill(i18n.T(lang, "Spent — steps {steps}, writes {writes}, fetches {fetches}",
			"steps", htmlMark(0), "writes", htmlMark(1), "fetches", htmlMark(2)),
			`<b>`+strconv.Itoa(run.Spend.Steps)+` / `+strconv.Itoa(run.Budget.MaxStepsPerRun)+`</b>`,
			`<b>`+strconv.Itoa(run.Spend.Writes)+` / `+strconv.Itoa(run.Budget.MaxWritesPerRun)+`</b>`,
			`<b>`+strconv.Itoa(run.Spend.Egress)+` / `+strconv.Itoa(run.Budget.MaxEgressPerRun)+`</b>`) + `</p>`)
		if run.Error != "" {
			body.WriteString(recorded("p", "text-sm status--danger", run.Error))
		}
		for _, st := range run.Steps {
			body.WriteString(`<div class="settings-block-title"><span class="mono">` + esc(st.Action) + `</span></div>`)
			if st.Did != "" {
				// What the step actually performed. A model call belongs here
				// even in a dry run: §8 requires the generation to be real, and
				// a reader must be able to tell that it was.
				body.WriteString(recorded("p", "text-sm muted", st.Did))
			}
			if st.Refused != "" {
				// The dry-run diff: what this step WOULD have done.
				body.WriteString(recorded("pre", "code-block", st.Refused))
			}
			if st.Output != "" {
				body.WriteString(recorded("p", "text-sm", st.Output))
			}
			if st.Error != "" {
				body.WriteString(`<p class="text-sm">` + recorded("span", "mono", st.Error) + `</p>`)
			}
		}
		when := run.StartedAt.Format("2006-01-02 15:04")
		b.WriteString(monAcc(saIcon("play"), when, run.FlowID, runStatusChip(lang, run.Status), false, body.String()))
	}
	b.WriteString(`</div></section>`)

	return string(ui.Status(head, ui.HTML(b.String()))) + string(ui.SheetClosing("flow-create", i18n.T(lang, "Create a flow"), i18n.T(lang, "Close"), ui.HTML(flowEditorCard(lang))))
}

// flowADR is the decision VayuFlow is built to, named as the Decisions page
// names it.
const flowADR = "ADR-0151"

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

// flowEditorCard renders the create/edit form.
//
// Every choice it offers is read from the ENGINE — the action list from the
// capability registry, the condition list from LeafConditionKinds, the events
// from KnownEvents. A form carrying its own copy of any of those is a second
// list that drifts from the first, and the operator finds out by saving
// something the store then refuses for a reason the page never mentioned.
//
// The budget fields have no "unlimited" and no blank default. ADR-0151 §3 makes
// unlimited inexpressible on purpose; a form that let the field stay empty would
// reintroduce it as a shrug.
func flowEditorCard(lang string) string {
	var b strings.Builder
	t := func(s string) string { return esc(s) }
	b.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "What it is")) + `</div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-name">` + t(i18n.T(lang, "Name")) + `</label>` +
		`<input class="input" id="ff-name" type="text" placeholder="` + t(i18n.T(lang, "Monday digest")) + `"></div>`)
	b.WriteString(`<input id="ff-id" type="hidden" value="">`)

	b.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "What starts it")) + `</div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-trigger">` + t(i18n.T(lang, "Trigger")) + `</label>` +
		`<select class="input" id="ff-trigger">` +
		`<option value="manual">` + t(i18n.T(lang, "manual — only when you press Run")) + `</option>` +
		`<option value="schedule">` + t(i18n.T(lang, "schedule — on a cron expression")) + `</option>` +
		`<option value="event">` + t(i18n.T(lang, "event — when something happens here")) + `</option>` +
		`</select></div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-cron">` + t(i18n.T(lang, "Cron (schedule only)")) + `</label>` +
		`<input class="input" id="ff-cron" type="text" placeholder="` + flowCronExample + `">` +
		`<div class="field-hint">` + t(i18n.T(lang, "Five fields, install timezone. Parsed when you save, not when it first fires — an expression that only fails at 9am on Monday is one you find out about on Tuesday.")) + `</div></div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-event">` + t(i18n.T(lang, "Event (event only)")) + `</label>` +
		`<select class="input" id="ff-event"><option value="">—</option>`)
	for _, e := range vayuflow.KnownEvents() {
		b.WriteString(`<option value="` + esc(e) + `" translate="no">` + esc(e) + `</option>`)
	}
	b.WriteString(`</select></div>`)

	b.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "Which subjects")) + `</div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-cond">` + t(i18n.T(lang, "Condition")) + `</label>` +
		`<select class="input" id="ff-cond">`)
	for _, n := range vayuflow.LeafConditionNames() {
		b.WriteString(`<option value="` + esc(n) + `" translate="no">` + esc(n) + `</option>`)
	}
	b.WriteString(`</select><div class="field-hint">` + escFill(i18n.T(lang, "{always} is a real answer, not a missing one. Combining conditions with {combine} is not expressible here yet.",
		"always", htmlMark(0), "combine", htmlMark(1)), `<code>`+flowCondAlways+`</code>`, `<code>`+flowCondCombine+`</code>`) + `</div></div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="ff-condval">` + t(i18n.T(lang, "Compare against")) + `</label>` +
		`<input class="input" id="ff-condval" type="text" translate="no" placeholder="release-notes"></div>`)

	b.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "What it does")) + `</div>`)
	b.WriteString(`<p class="text-sm muted">` + t(i18n.T(lang, "Up to four steps, in order; a blank row stops there.")) +
		string(ui.Tip(i18n.T(lang, "Each step names an action from the registry above. Parameters are {form}, one per line. A parameter whose whole value is {prev} receives the previous step's output.",
			"form", "key=value", "prev", "$prev"))) + `</p>`)
	for i := 1; i <= 4; i++ {
		n := strconv.Itoa(i)
		b.WriteString(`<div class="field"><label class="field-label" for="ff-act` + n + `">` + t(i18n.T(lang, "Step {n}", "n", i)) + `</label>` +
			`<select class="input" id="ff-act` + n + `"><option value="">—</option>`)
		for _, c := range vayuflow.Capabilities() {
			b.WriteString(`<option value="` + esc(c.Action) + `">` + t(i18n.T(lang, "{action} — writes {write}, needs {role}",
				"action", c.Action, "write", flowWriteWord(lang, c.Writes), "role", teamRoleName(lang, c.MinRole))) + `</option>`)
		}
		b.WriteString(`</select>` +
			`<textarea class="input" id="ff-par` + n + `" rows="2" placeholder="` + t(i18n.T(lang, "title=Weekly digest")) + `"></textarea></div>`)
	}

	b.WriteString(`<div class="settings-block-title">` + t(i18n.T(lang, "What it may spend")) + `</div>`)
	b.WriteString(`<p class="text-sm muted">` + t(i18n.T(lang, "Every ceiling is required; nothing is unlimited.")) +
		string(ui.Tip(i18n.T(lang, "The run trail shows what was spent against what was allowed."))) + `</p>`)
	for _, f := range []struct{ id, label, def string }{
		{"ff-bsteps", i18n.T(lang, "Steps per run"), "4"},
		{"ff-bruns", i18n.T(lang, "Runs per hour"), "2"},
		{"ff-bwrites", i18n.T(lang, "Writes per run"), "1"},
		{"ff-begress", i18n.T(lang, "Fetches per run"), "1"},
		{"ff-btimeout", i18n.T(lang, "Wall clock (seconds)"), "60"},
	} {
		b.WriteString(`<div class="field"><label class="field-label" for="` + f.id + `">` + t(f.label) + `</label>` +
			`<input class="input" id="` + f.id + `" type="number" min="1" value="` + f.def + `"></div>`)
	}

	b.WriteString(`<div class="flex-between">` +
		`<button type="button" class="btn btn--primary btn--sm" id="ff-save">` + t(i18n.T(lang, "Save flow")) + `</button>` +
		`<button type="button" class="btn btn--ghost btn--sm" id="ff-reset">` + t(i18n.T(lang, "Clear the form")) + `</button>` +
		`</div>`)
	b.WriteString(`<p class="text-sm muted">` + t(i18n.T(lang, "Saved off and in dry-run; arming is its own button.")) +
		string(ui.Tip(i18n.T(lang, "Neither this form nor an edit ever changes those. Arming has its own entry in the audit trail."))) + `</p>`)
	return b.String()
}

// A cron expression and the condition language's own words, shown as written.
const (
	flowCronExample = "0 9 * * 1"
	flowCondAlways  = "always"
	flowCondCombine = "all/any/not"
)

// vayuFlowState is the sentence VayuFlow opens on. An engine that is not
// running comes first, because then nothing below can fire, whatever it says.
func vayuFlowState(lang string, wired bool, fail, warn, armed int) (tone, state string) {
	switch {
	case !wired:
		return "danger", i18n.T(lang, "The automation engine is not running in this build, so no flow can fire")
	case fail > 0:
		return "danger", i18n.T(lang, "{n, plural, one {# posture check} other {# posture checks}} to act on", "n", fail)
	case warn > 0:
		return "warn", i18n.T(lang, "{n, plural, one {# posture check} other {# posture checks}} to look at", "n", warn)
	case armed > 0:
		return "ok", i18n.T(lang, "{n, plural, one {# flow armed live, within its ceilings} other {# flows armed live, within their ceilings}}", "n", armed)
	}
	return "ok", i18n.T(lang, "No flow is armed live; a new one starts in dry-run")
}
