// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/fault"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// =============================================================================
// Ω9 — Interactive operator console: System Mode Engine + Fault Engine.
// These are real rendered HTML pages backed by POST control endpoints that
// mutate live runtime state, so the operational timeline's causal narrative
// becomes observable from operator action.
// =============================================================================

// modeDescription says in one line what a runtime mode means for the install.
func modeDescription(m mode.Mode) string {
	switch m {
	case mode.ModeNormal:
		return "All subsystems operational · write queue active · policy engine enforcing · fault escalation armed"
	case mode.ModeDegraded:
		return "Partial functionality · non-critical paths disabled · escalation monitoring active"
	case mode.ModeReadOnly:
		return "Write queue paused · read path fully operational · WAL writes blocked"
	case mode.ModeRecovery:
		return "Automated recovery in progress · reduced capacity · monitoring elevated"
	case mode.ModeMaintenance:
		return "Scheduled maintenance window · writes paused · external traffic may be restricted"
	case mode.ModeQuarantined:
		return "Plugin invocations denied · sandbox execution blocked · immediate attention required"
	}
	return ""
}

// writeConsoleShellHead emits the VayuOS shell through the opening <main>. The
// operator consoles (Faults, Topology, Replay, Policy, Decisions) render inside
// the one shell and open with ui.Page. active selects the rail section.
func (a *App) writeConsoleShellHead(w http.ResponseWriter, r *http.Request, active, pageTitle string) string {
	return a.writeConsoleShellHeadStatus(w, r, active, pageTitle, http.StatusOK)
}

// writeConsoleShellHeadStatus is writeConsoleShellHead for a page that answers
// with another status (a 404 that stays inside the console).
func (a *App) writeConsoleShellHeadStatus(w http.ResponseWriter, r *http.Request, active, pageTitle string, status int) string {
	csrfTokenFor(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	nonce := render.CSPNonce(r)
	fmt.Fprint(w, adminOSShellHead(nonce, pageTitle, active, a.getOSSettings(r.Context())))
	return nonce
}

func writeConsoleShellFoot(w http.ResponseWriter, nonce, script string) {
	// Streaming operator/console pages never host an Alpine island, so they opt
	// out of the Alpine runtime entirely (ADR-0136 — Alpine is the exception).
	fmt.Fprint(w, adminOSShellFoot(nonce, script, false))
}

// =============================================================================
// System Mode Engine page  (GET /admin/modes)
// =============================================================================

func (a *App) handleModesPage(w http.ResponseWriter, r *http.Request) {
	a.stillAirModesPage(w, r, a.getOSSettings(r.Context()))
}

// =============================================================================
// Fault Engine page  (GET /admin/faults)
// =============================================================================

func (a *App) handleFaultPage(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	rules := fault.DefaultRules()
	nonce := a.writeConsoleShellHead(w, r, "faults", "Faults")

	rows := make([][]ui.HTML, 0, len(rules))
	var example fault.EscalationRule
	for _, rule := range rules {
		if rule.FaultName == fault.FaultWALWrite {
			example = rule
		}
		count := fault.Global.TriggerCount(rule.FaultName)
		fired := ui.Text(strconv.FormatInt(count, 10))
		switch {
		case rule.Threshold > 0 && count >= rule.Threshold:
			fired = ui.Tag("danger", strconv.FormatInt(count, 10)+" · escalated")
		case count > 0:
			fired = ui.Tag("warn", strconv.FormatInt(count, 10))
		}
		rows = append(rows, []ui.HTML{
			`<span class="mono">` + ui.Text(rule.FaultName) + `</span>`,
			fired,
			ui.Text(strconv.FormatInt(rule.Threshold, 10)),
			ui.Text(faultWindow(rule.Window)),
			ui.Tag(saModeTone(rule.TargetMode), saModeLabel(rule.TargetMode)),
			`<button type="button" class="btn btn--sm" data-fault="` + ui.Text(rule.FaultName) + `">Fire</button>`,
		})
	}

	// The example is the write-ahead log's own rule, so its numbers are the ones
	// the escalator uses, not a description of them.
	steps := ui.Steps(
		ui.Step{Mark: "1", Title: "A fault fires", Detail: example.FaultName + " is recorded and its counter goes up by one."},
		ui.Step{Mark: "2", Title: "Its counter reaches the threshold",
			Detail: strconv.FormatInt(example.Threshold, 10) + " times " + faultWindowPhrase(example.Window) + "."},
		ui.Step{Mark: "3", Title: "The install changes mode",
			Detail: saModeLabel(mode.ModeNormal) + " to " + saModeLabel(example.TargetMode) + "."},
		ui.Step{Mark: "4", Title: "Refusals begin", Detail: "System state lists what the new mode refuses."},
	)

	fmt.Fprint(w, ui.Join(
		ui.Page("Faults", "Fire a fault point to see how the runtime escalates, and how close each one is to its threshold.", ""),
		ui.Callout("warn", ui.Text("Firing a fault changes live runtime state. A fault that reaches its threshold within its window moves the whole install into the mode it escalates to. The install is "+saModeLabel(cur)+" now.")),
		ui.Section("Fault points", strconv.Itoa(len(rules))+" rules armed",
			ui.Table([]string{"Fault point", "Fired", "Threshold", "Window", "Escalates to", ""}, rows, "No fault points are defined.")),
		ui.Section("How a fault escalates", "", steps),
	))

	writeConsoleShellFoot(w, nonce, `window.vpFault=function(name){vpPost('/admin/fault/simulate?name='+encodeURIComponent(name),{},function(d){vpToast('Fired '+name+' ('+d.trigger_count+')'+(d.escalated?(', now '+d.current_mode):''),d.escalated?'warn':'ok');setTimeout(function(){location.reload();},650);});};
document.addEventListener('click',function(e){var b=e.target.closest('[data-fault]');if(b)vpFault(b.getAttribute('data-fault'));});`)
}

// faultWindow and faultWindowPhrase say an escalation window as a column value
// ("5 min") and inside a sentence ("within 5 min"); 0 is the fault's lifetime.
func faultWindow(d time.Duration) string {
	if d <= 0 {
		return "Lifetime"
	}
	return humanizeWindow(d)
}

func faultWindowPhrase(d time.Duration) string {
	if d <= 0 {
		return "over its lifetime"
	}
	return "within " + humanizeWindow(d)
}

// =============================================================================
// Runtime Topology  (GET /admin/topology)
// =============================================================================

type topoNode struct {
	ID, Label, Sub, Status, Band string
	X, Y                         float64
}

const topoNodeW, topoNodeH = 162.0, 52.0

// topoTone and topoBand turn a node's status and band into the class suffixes
// the stylesheet defines, and only those: the result is always one of the
// literals below, so nothing reaches a class attribute that this code did not
// write, whatever a node carries.
func topoTone(status string, cur mode.Mode) string {
	if strings.HasPrefix(status, "mode-") {
		status = map[string]string{"warn": "warn", "danger": "err"}[saModeTone(cur)]
	}
	switch status {
	case "warn":
		return "warn"
	case "err":
		return "err"
	}
	return "ok"
}

func topoBand(band string) string {
	switch band {
	case "read":
		return "read"
	case "govern":
		return "govern"
	case "observe":
		return "observe"
	}
	return "write"
}

func topoAnchor(n topoNode, side byte) (float64, float64) {
	cx, cy := n.X+topoNodeW/2, n.Y+topoNodeH/2
	switch side {
	case 'r':
		return n.X + topoNodeW, cy
	case 'l':
		return n.X, cy
	case 't':
		return cx, n.Y
	case 'b':
		return cx, n.Y + topoNodeH
	}
	return cx, cy
}

func (a *App) handleTopologyPage(w http.ResponseWriter, r *http.Request) {
	snap := a.getAdminSnapshot()
	cur := mode.Global.Current()

	// Live status derivations.
	queueStatus := "ok"
	if snap.FailedJobs > 0 {
		queueStatus = "warn"
	}
	// The WAL node is not turned red in read-only mode: read-only does not stop
	// WAL writes (System state lists what it does refuse), and a red node here
	// said it did.
	searchStatus := "ok"
	searchSub := "VayuFind (built-in)"
	if a.search == nil {
		searchStatus, searchSub = "warn", "unavailable"
	} else if n, err := a.search.DocCount(r.Context()); err == nil {
		searchSub = fmt.Sprintf("VayuFind · %d indexed", n)
	}
	var faultTotal int64
	for _, rule := range fault.DefaultRules() {
		faultTotal += fault.Global.TriggerCount(rule.FaultName)
	}
	escStatus, faultStatus := "ok", "ok"
	if faultTotal > 0 {
		escStatus, faultStatus = "warn", "warn"
	}
	modeStatus := "mode-" + string(cur)
	modeLabel := saModeLabel(cur)

	// Names in sentence case. Every sub-line is either measured or says what the
	// subsystem is; none is a figure written in ("6/6 PASS" and "3 workers"
	// were). A node is drawn only for something the running binary does: there
	// is no signing node (nothing signs with an Ed25519 key), no federation node
	// (no ActivityPub code ships) and no policy node (every evaluation was
	// handed an empty context, so its verdict described nothing).
	nodes := []topoNode{
		{"ingress", "HTTP ingress", "chi router · TLS", "ok", "write", 30, 70},
		{"auth", "Auth and CSRF", "API key · rate limit", "ok", "write", 250, 70},
		{"queue", "Write queue", fmt.Sprintf("%d pending · %d workers", snap.PendingJobs, snap.WorkersAlive), queueStatus, "write", 470, 70},
		{"wal", "WAL · SQLite", "WAL journal · PRAGMAs", "ok", "write", 690, 70},
		{"search", "Search", searchSub, searchStatus, "read", 30, 185},
		{"cache", "Render cache", fmt.Sprintf("%.0f%% hit ratio", snap.CacheHitRatio*100), "ok", "read", 250, 185},
		{"replay", "Replay store", "dead letter · quarantine", "ok", "read", 470, 185},
		{"outbox", "Outbox relay", "transactional events", "ok", "read", 690, 185},
		{"mode", "Mode engine", modeLabel, modeStatus, "govern", 250, 415},
		{"escalator", "Escalation engine", fmt.Sprintf("%d rules armed", len(fault.DefaultRules())), escStatus, "govern", 470, 415},
		{"faults", "Fault points", fmt.Sprintf("%d fired", faultTotal), faultStatus, "govern", 690, 415},
		{"tracing", "Tracing", "correlation spans", "ok", "observe", 250, 525},
		{"metrics", "Metrics", "Prometheus", "ok", "observe", 470, 525},
		{"health", "Health", "liveness · readiness", "ok", "observe", 690, 525},
	}
	idx := map[string]topoNode{}
	for _, n := range nodes {
		idx[n.ID] = n
	}

	type edge struct {
		from, to string
		fs, ts   byte
		cls      string
	}
	edges := []edge{
		{"ingress", "auth", 'r', 'l', "flow"},
		{"auth", "queue", 'r', 'l', "flow"},
		{"queue", "wal", 'r', 'l', "flow"},
		{"ingress", "search", 'b', 't', ""},
		{"auth", "cache", 'b', 't', ""},
		{"queue", "replay", 'b', 't', ""},
		{"wal", "outbox", 'b', 't', "flow"},
		{"faults", "escalator", 'l', 'r', "ctrl"},
		{"escalator", "mode", 'l', 'r', "ctrl"},
		{"mode", "wal", 't', 'b', "ctrl"},
		{"mode", "tracing", 'b', 't', ""},
		{"escalator", "metrics", 'b', 't', ""},
		{"faults", "health", 'b', 't', ""},
	}

	nonce := a.writeConsoleShellHead(w, r, "topology", "Topology")
	fmt.Fprint(w, ui.Join(
		ui.Page("Topology", "How a request moves through the runtime, and what governs it. Each subsystem shows its state as of this page load.", ""),
		`<p class="page-sub">Solid lines are the data path. Dashed lines are the control plane: fault points feed the escalation engine, which drives the mode engine, which constrains the write path. The install is `+ui.Text(saModeLabel(cur))+` now.</p>`,
	))
	fmt.Fprint(w, `<div class="topo-wrap"><svg class="topo-svg" viewBox="0 0 1000 600" role="img" aria-label="Runtime topology graph">
<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path class="topo-arrow" d="M0,0 L10,5 L0,10 z"/></marker></defs>`)

	// Band labels.
	bands := []struct {
		y     float64
		label string
	}{{96, "Write path"}, {211, "Delivery and read"}, {441, "Governance"}, {551, "Observability"}}
	for _, b := range bands {
		// Above the row, aligned with its first node: to the left of it the label
		// had six units and was clipped to "Writ".
		fmt.Fprintf(w, `<text class="topo-band" x="30" y="%.0f">%s</text>`, b.y-32, b.label)
	}

	// Edges first (under nodes).
	for _, e := range edges {
		x1, y1 := topoAnchor(idx[e.from], e.fs)
		x2, y2 := topoAnchor(idx[e.to], e.ts)
		// Smooth cubic bezier with control points biased along the exit/entry axis.
		mx := (x1 + x2) / 2
		my := (y1 + y2) / 2
		c1x, c1y, c2x, c2y := mx, y1, mx, y2
		if e.fs == 't' || e.fs == 'b' {
			c1x, c1y, c2x, c2y = x1, my, x2, my
		}
		cls := "topo-edge"
		if e.cls != "" {
			cls += " " + e.cls
		}
		marker := ` marker-end="url(#arrow)"`
		if e.cls == "ctrl" {
			marker = ""
		}
		fmt.Fprintf(w, `<path class="%s" d="M%.0f,%.0f C%.0f,%.0f %.0f,%.0f %.0f,%.0f"%s/>`,
			cls, x1, y1, c1x, c1y, c2x, c2y, x2, y2, marker)
	}

	// Nodes. Health and band are classes, so each scheme colours them; the dots
	// no longer pulse — an animation nobody can switch off, on every node,
	// saying nothing the colour does not.
	for _, n := range nodes {
		tone, band := topoTone(n.Status, cur), topoBand(n.Band)
		fmt.Fprintf(w, `<g class="topo-node">
<rect class="topo-rect topo-rect--%s" x="%.0f" y="%.0f" width="%.0f" height="%.0f" rx="7"/>
<rect class="topo-bar topo-bar--%s" x="%.0f" y="%.0f" width="3" height="%.0f" rx="1.5"/>
<circle class="topo-dot topo-dot--%s" cx="%.0f" cy="%.0f" r="4"/>
<text class="topo-label" x="%.0f" y="%.0f">%s</text>
<text class="topo-sub" x="%.0f" y="%.0f">%s</text>
</g>`,
			tone, n.X, n.Y, topoNodeW, topoNodeH,
			band, n.X, n.Y, topoNodeH,
			tone, n.X+topoNodeW-16, n.Y+16,
			n.X+14, n.Y+22, template.HTMLEscapeString(n.Label),
			n.X+14, n.Y+38, template.HTMLEscapeString(n.Sub))
	}
	fmt.Fprint(w, `</svg></div>
<div class="topo-legend">
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--ok"></span>Healthy</span>
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--warn"></span>Degraded</span>
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--danger"></span>Blocked or faulted</span>
  <span class="tl-leg"><span class="tl-leg-line"></span>Data path</span>
  <span class="tl-leg"><span class="tl-leg-line ctrl"></span>Control plane</span>
</div>`)

	writeConsoleShellFoot(w, nonce, ``)
}

// =============================================================================
// Replay Explorer  (GET /admin/replay)
// =============================================================================

type replayJob struct {
	ID            int64
	Op            string
	DeadReason    string
	CorrelationID string
	Slug          string
	Retries       int
	ReplayCount   int
	CreatedAt     string
}

func queueCount(status string) int {
	var n int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status=?`, status).Scan(&n)
	return n
}

func loadJobs(status string, limit int) []replayJob {
	rows, err := dbpkg.DB.Query(
		`SELECT id,op,dead_reason,retries,replay_count,correlation_id,article_json,created_at FROM write_jobs WHERE status=? ORDER BY created_at DESC LIMIT ?`, status, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []replayJob
	for rows.Next() {
		var j replayJob
		var aj string
		if rows.Scan(&j.ID, &j.Op, &j.DeadReason, &j.Retries, &j.ReplayCount, &j.CorrelationID, &aj, &j.CreatedAt) != nil {
			continue
		}
		var meta struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		}
		if json.Unmarshal([]byte(aj), &meta) == nil {
			j.Slug = meta.Slug
			if j.Slug == "" {
				j.Slug = meta.Title
			}
		}
		if j.Slug == "" {
			j.Slug = "—"
		}
		out = append(out, j)
	}
	_ = rows.Err() // iteration errors are non-critical for UI display
	return out
}

func (a *App) handleReplayPage(w http.ResponseWriter, r *http.Request) {
	pending := queueCount("pending")
	processing := queueCount("processing")
	completed := queueCount("completed")
	failed := queueCount("failed")
	deadLetter := queueCount("dead_letter")
	quarantined := queueCount("quarantined")
	deadJobs := loadJobs("dead_letter", 60)
	poisonJobs := loadJobs("quarantined", 30)
	maxReplay, batch := config.Cfg.MaxReplayCount, config.Cfg.ReplayBatchLimit

	nonce := a.writeConsoleShellHead(w, r, "replay", "Replay")

	actions := ui.HTML("")
	if deadLetter > 0 {
		actions = `<button type="button" class="btn btn--primary" data-replay-all>Replay dead letters</button>`
	}
	tone := func(n int, t string) string {
		if n > 0 {
			return t
		}
		return ""
	}
	count := strconv.Itoa

	// Every field of a job arrives from outside (the correlation ID is whatever
	// the request sent), so each cell is text.
	corr := func(c string) ui.HTML {
		if c == "" {
			return ui.Text("—")
		}
		if len(c) > 12 {
			c = c[:12]
		}
		return `<span class="mono">` + ui.Text(c) + `</span>`
	}
	job := func(j replayJob) ui.HTML {
		return `<span class="mono">#` + ui.Text(strconv.FormatInt(j.ID, 10)) + `</span> ` + ui.Text(j.Slug)
	}
	var deadRows [][]ui.HTML
	for _, j := range deadJobs {
		reasonTone := "warn"
		if j.DeadReason == "parse_error" || j.DeadReason == "unknown_op" {
			reasonTone = "danger"
		}
		deadRows = append(deadRows, []ui.HTML{
			job(j), ui.Text(j.Op), ui.Tag(reasonTone, j.DeadReason), ui.Text(count(j.Retries)),
			ui.Text(count(j.ReplayCount) + " of " + count(maxReplay)), corr(j.CorrelationID), ui.Text(j.CreatedAt),
			`<button type="button" class="btn btn--sm" data-replay-job="` + ui.Text(strconv.FormatInt(j.ID, 10)) + `">Replay</button>`,
		})
	}
	var poisonRows [][]ui.HTML
	for _, j := range poisonJobs {
		poisonRows = append(poisonRows, []ui.HTML{
			job(j), ui.Text(j.Op), ui.Tag("danger", j.DeadReason), ui.Text(count(j.ReplayCount)), corr(j.CorrelationID), ui.Text(j.CreatedAt),
		})
	}
	deadBody := ui.Empty("check-c", "No dead letters", "No job has used up its retries.", "")
	if len(deadRows) > 0 {
		deadBody = ui.Table([]string{"Job", "Operation", "Reason", "Retries", "Replays", "Correlation", "Created", ""}, deadRows, "")
	}
	poisonBody := ui.Empty("check-c", "Nothing quarantined", "No job has been replayed past the ceiling.", "")
	if len(poisonRows) > 0 {
		poisonBody = ui.Table([]string{"Job", "Operation", "Reason", "Replays", "Correlation", "Created"}, poisonRows, "")
	}

	fmt.Fprint(w, ui.Join(
		ui.Page("Replay", "Write jobs that still fail after three retries wait here. Replay them once the cause is fixed; a job replayed "+count(maxReplay)+" times is set aside as poison.", actions),
		ui.Figures(
			ui.Figure{Value: count(pending), Label: "Pending"},
			ui.Figure{Value: count(processing), Label: "Processing"},
			ui.Figure{Value: count(completed), Label: "Completed"},
			ui.Figure{Value: count(failed), Label: "Failed", Tone: tone(failed, "warn")},
			ui.Figure{Value: count(deadLetter), Label: "Dead letters", Tone: tone(deadLetter, "warn")},
			ui.Figure{Value: count(quarantined), Label: "Quarantined", Tone: tone(quarantined, "danger")},
		),
		ui.Section("How a job moves", "", ui.Steps(
			ui.Step{Mark: "1", Title: "Pending, then processing", Detail: "A worker takes the job from the queue."},
			ui.Step{Mark: "2", Title: "Completed, or retried", Detail: "A failed job is retried up to three times, waiting longer each time."},
			ui.Step{Mark: "3", Title: "Dead letter", Detail: "If it fails once more it waits here for you. A job the queue cannot read or run comes straight here."},
			ui.Step{Mark: "4", Title: "Replayed", Detail: "Replay sends it back to pending. Replay dead letters takes up to " + count(batch) + " at a time."},
			ui.Step{Mark: "5", Title: "Quarantined", Detail: "A job replayed " + count(maxReplay) + " times is poison and is not replayed again."},
		)),
		ui.Section("Dead letters", count(deadLetter)+" jobs", deadBody),
		ui.Section("Quarantined as poison", count(quarantined)+" jobs", poisonBody),
	))

	writeConsoleShellFoot(w, nonce, `window.vpReplay=function(id){vpPost('/admin/replay/job?id='+id,{},function(d){vpToast(d.replayed?('Requeued job #'+id):'Job #'+id+' is no longer a dead letter',d.replayed?'ok':'warn');setTimeout(function(){location.reload();},650);});};
window.vpReplayAll=function(){vpPost('/api/v1/queue/replay',{},function(d){vpToast('Replayed '+d.replayed+', quarantined '+d.skipped_quarantined,'ok');setTimeout(function(){location.reload();},650);});};
document.addEventListener('click',function(e){var b=e.target.closest('[data-replay-job],[data-replay-all]');if(!b)return;if(b.hasAttribute('data-replay-all'))vpReplayAll();else vpReplay(b.getAttribute('data-replay-job'));});`)
}

// handleReplayJob requeues a single dead-letter job back to pending.
func (a *App) handleReplayJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeAPIError(w, r, 400, "invalid_id", "id must be a positive integer", "/docs/operations/replay")
		return
	}
	res, err := dbpkg.WDB.Exec(
		`UPDATE write_jobs SET status='pending',retries=0,retry_at=NULL,replay_count=replay_count+1 WHERE id=? AND status='dead_letter'`, id)
	if err != nil {
		writeAPIError(w, r, 500, "replay_failed", err.Error(), "/docs/operations/replay")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, r, 200, map[string]interface{}{"replayed": n > 0, "id": id})
}

// =============================================================================
// Control endpoints (CSRF-protected POST)
// =============================================================================

// handleModeTransition attempts an operator-driven mode transition.
func (a *App) handleModeTransition(w http.ResponseWriter, r *http.Request) {
	to := mode.Mode(r.URL.Query().Get("to"))
	force := r.URL.Query().Get("force") == "true"

	valid := false
	for _, m := range mode.AllModes() {
		if m == to {
			valid = true
			break
		}
	}
	if !valid {
		writeAPIError(w, r, 400, "invalid_mode", "unknown target mode: "+string(to), "/docs/operations/modes")
		return
	}

	from := mode.Global.Current()
	if force {
		mode.Global.ForceTransition(to, "operator console override")
	} else if err := mode.Global.Transition(to, "operator console transition", "operator"); err != nil {
		writeAPIError(w, r, 409, "transition_not_permitted", err.Error(), "/docs/operations/modes")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{
		"mode": string(mode.Global.Current()), "from": string(from), "forced": force,
		"snapshot_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleFaultSimulate fires a named fault, advancing its escalation counter and
// recording it with the global escalator (which may transition the mode).
func (a *App) handleFaultSimulate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	known := false
	for _, rule := range fault.DefaultRules() {
		if rule.FaultName == name {
			known = true
			break
		}
	}
	if !known {
		writeAPIError(w, r, 400, "unknown_fault", "unknown fault point: "+name, "/docs/operations/faults")
		return
	}

	modeBefore := mode.Global.Current()
	count := fault.Global.Trigger(name)
	fault.GlobalEscalator.Record(name)
	modeAfter := mode.Global.Current()

	writeJSON(w, r, 200, map[string]interface{}{
		"fault": name, "trigger_count": count,
		"current_mode": string(modeAfter), "escalated": modeAfter != modeBefore,
		"snapshot_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// humanizeWindow formats an escalation rolling window compactly.
func humanizeWindow(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}
