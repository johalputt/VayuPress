// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/fault"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/policy"
	"github.com/johalputt/vayupress/internal/render"
)

// =============================================================================
// Ω9 — Interactive operator console: System Mode Engine + Fault Engine.
// These are real rendered HTML pages backed by POST control endpoints that
// mutate live runtime state, so the operational timeline's causal narrative
// becomes observable from operator action.
// =============================================================================

// modeVisual maps a runtime mode to its banner class, label, and description.
func modeVisual(m mode.Mode) (cls, label, desc string) {
	switch m {
	case mode.ModeNormal:
		return "mode-normal", "NORMAL", "All subsystems operational · write queue active · policy engine enforcing · fault escalation armed"
	case mode.ModeDegraded:
		return "mode-degraded", "DEGRADED", "Partial functionality · non-critical paths disabled · escalation monitoring active"
	case mode.ModeReadOnly:
		return "mode-readonly", "READ-ONLY", "Write queue paused · read path fully operational · WAL writes blocked"
	case mode.ModeRecovery:
		return "mode-recovery", "RECOVERY", "Automated recovery in progress · reduced capacity · monitoring elevated"
	case mode.ModeMaintenance:
		return "mode-maintenance", "MAINTENANCE", "Scheduled maintenance window · writes paused · external traffic may be restricted"
	case mode.ModeQuarantined:
		return "mode-quarantined", "QUARANTINED", "Plugin invocations denied · sandbox execution blocked · immediate attention required"
	}
	return "mode-normal", strings.ToUpper(string(m)), ""
}

// modeShortClass returns the m-* accent class for a mode tile/badge.
func modeShortClass(m mode.Mode) string {
	switch m {
	case mode.ModeNormal:
		return "m-normal"
	case mode.ModeDegraded:
		return "m-degraded"
	case mode.ModeReadOnly:
		return "m-readonly"
	case mode.ModeRecovery:
		return "m-recovery"
	case mode.ModeMaintenance:
		return "m-maintenance"
	case mode.ModeQuarantined:
		return "m-quarantined"
	}
	return "m-normal"
}

// writeConsoleShellHead emits the VayuOS shell through the opening <main> and a
// VayuOS-styled page header. The operator consoles (System Modes, Policy,
// Topology, Replay, Faults, ADRs) all render inside the single VayuOS shell —
// there is no separate admin panel. active selects the highlighted sidebar item
// (one of: modes, policy, topology, replay, faults, adrs).
func (a *App) writeConsoleShellHead(w http.ResponseWriter, r *http.Request, active, pageTitle, pageSub string) string {
	csrfTokenFor(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	nonce := render.CSPNonce(r)

	cfg := a.getOSSettings(r.Context())
	fmt.Fprint(w, adminOSShellHead(nonce, pageTitle, active, cfg))
	fmt.Fprintf(w, `<div class="os-page-head"><div><h1 class="os-page-title">%s</h1><p class="os-page-sub">%s</p></div></div>`,
		html.EscapeString(pageTitle), html.EscapeString(pageSub))
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

	nonce := a.writeConsoleShellHead(w, r, "faults", "Fault Engine",
		fmt.Sprintf("fault injection & escalation lineage · %d rules armed · current mode: %s", len(rules), cur))

	fmt.Fprint(w, `<div class="console-note">Simulating a fault increments its escalation counter. When a fault crosses its threshold within the window, the runtime auto-escalates to the target mode — visible on the Overview timeline and the System Modes lineage. This is live: it mutates real runtime state.</div>
<div class="section-title">Fault points and escalation rules</div>
<table class="fe-table"><thead><tr><th>Fault Point</th><th>Triggers</th><th>Threshold</th><th>Window</th><th>Escalates To</th><th>Action</th></tr></thead><tbody>`)

	for _, rule := range rules {
		count := fault.Global.TriggerCount(rule.FaultName)
		countCls := "fe-count"
		if count > 0 && count < rule.Threshold {
			countCls = "fe-count hot"
		} else if rule.Threshold > 0 && count >= rule.Threshold {
			countCls = "fe-count crit"
		}
		window := "∞ (lifetime)"
		if rule.Window > 0 {
			window = humanizeWindow(rule.Window)
		}
		tgtCls := "fe-target " + strings.ReplaceAll(modeShortClass(rule.TargetMode), "m-", "t-")
		fmt.Fprintf(w, `<tr id="fe-%s"><td class="fe-name">%s</td><td><span class="%s">%d</span></td><td>×%d</td><td>%s</td><td><span class="%s">%s</span></td><td><button class="fe-sim-btn" data-fault="%s">Simulate</button></td></tr>`,
			template.HTMLEscapeString(rule.FaultName), rule.FaultName, countCls, count, rule.Threshold, window, tgtCls, saModeLabel(rule.TargetMode), rule.FaultName)
	}
	fmt.Fprint(w, `</tbody></table>`)

	// Escalation chain visualization for the canonical WAL-write path.
	fmt.Fprint(w, `<div class="section-title">Escalation chain — an example</div>
<div class="card"><div class="esc-chain">
  <span class="esc-step">fault: db.wal.write</span><span class="esc-arrow">→</span>
  <span class="esc-step">counter ×3 / 5min</span><span class="esc-arrow">→</span>
  <span class="esc-step">threshold exceeded</span><span class="esc-arrow">→</span>
  <span class="esc-step esc-step--danger">mode: normal → read-only</span><span class="esc-arrow">→</span>
  <span class="esc-step">refusals begin (see System state)</span>
</div></div>`)

	writeConsoleShellFoot(w, nonce, `window.vpFault=function(name){vpPost('/admin/fault/simulate?name='+encodeURIComponent(name),function(d){var m=(d.current_mode||'').toUpperCase();return 'Fired '+name+' ×'+d.trigger_count+(d.escalated?(' → escalated to '+m):'');});};
document.addEventListener('click',function(e){var b=e.target.closest('[data-fault]');if(b)vpFault(b.getAttribute('data-fault'));});`)
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
	fedStatus := "ok"
	if cur == mode.ModeDegraded {
		fedStatus = "warn"
	}
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
	// Every figure on a node is measured; none is a constant that happens to
	// read like one ("6/6 PASS" and "3 workers" were).
	live := policy.Global.EvaluateAll(policy.Context{})
	polPass, polTotal := len(live.Passed), len(live.Passed)+len(live.Warnings)+len(live.Failed)
	polStatus := "ok"
	switch {
	case len(live.Failed) > 0:
		polStatus = "err"
	case len(live.Warnings) > 0:
		polStatus = "warn"
	}
	modeStatus := "mode-" + string(cur)
	modeLabel := saModeLabel(cur)

	nodes := []topoNode{
		{"ingress", "HTTP Ingress", "chi router · TLS", "ok", "write", 30, 70},
		{"auth", "Auth / CSRF", "API-key · rate-limit", "ok", "write", 250, 70},
		{"queue", "Write Queue", fmt.Sprintf("%d pending · %d workers", snap.PendingJobs, snap.WorkersAlive), queueStatus, "write", 470, 70},
		{"wal", "WAL · SQLite", "WAL+journal · PRAGMAs", "ok", "write", 690, 70},
		{"search", "Search", searchSub, searchStatus, "read", 30, 185},
		{"cache", "Render Cache", fmt.Sprintf("%.0f%% hit ratio", snap.CacheHitRatio*100), "ok", "read", 250, 185},
		{"replay", "Replay Store", "dead-letter · quarantine", "ok", "read", 470, 185},
		{"signing", "Signing", "Ed25519 loaded", "ok", "write", 690, 185},
		{"outbox", "Outbox Relay", "transactional events", "ok", "read", 470, 300},
		{"federation", "Federation", "ActivityPub deliver", fedStatus, "read", 690, 300},
		{"policy", "Policy Engine", fmt.Sprintf("%d of %d pass", polPass, polTotal), polStatus, "govern", 30, 415},
		{"mode", "Mode Engine", modeLabel, modeStatus, "govern", 250, 415},
		{"escalator", "Escalation Engine", fmt.Sprintf("%d rules armed", len(fault.DefaultRules())), escStatus, "govern", 470, 415},
		{"faults", "Fault Points", fmt.Sprintf("%d fired", faultTotal), faultStatus, "govern", 690, 415},
		{"tracing", "Tracing", "correlation spans", "ok", "observe", 250, 525},
		{"metrics", "Metrics", "Prometheus", "ok", "observe", 470, 525},
		{"health", "Health", "12 contracts", "ok", "observe", 690, 525},
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
		{"wal", "signing", 'r', 'l', "flow"},
		{"ingress", "search", 'b', 't', ""},
		{"auth", "cache", 'b', 't', ""},
		{"queue", "replay", 'b', 't', ""},
		{"wal", "outbox", 'b', 't', "flow"},
		{"outbox", "federation", 'r', 'l', "flow"},
		{"policy", "mode", 'r', 'l', "ctrl"},
		{"faults", "escalator", 'l', 'r', "ctrl"},
		{"escalator", "mode", 'l', 'r', "ctrl"},
		{"mode", "wal", 't', 'b', "ctrl"},
		{"mode", "tracing", 'b', 't', ""},
		{"escalator", "metrics", 'b', 't', ""},
		{"faults", "health", 'b', 't', ""},
	}

	nonce := a.writeConsoleShellHead(w, r, "topology", "Runtime Topology",
		fmt.Sprintf("live subsystem graph · write path · governance overlay · current mode: %s", cur))

	fmt.Fprint(w, `<div class="console-note">A live map of the runtime. Solid edges trace the write/read data path; dashed purple edges are the governance control plane — faults feed the escalator, which drives the mode engine, which constrains the write path. Node colour reflects current health.</div>
<div class="topo-wrap"><svg class="topo-svg" viewBox="0 0 1000 600" role="img" aria-label="Runtime topology graph">
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
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--ok"></span>healthy</span>
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--warn"></span>degraded</span>
  <span class="tl-leg"><span class="tl-leg-dot tl-leg-dot--danger"></span>blocked / fault</span>
  <span class="tl-leg"><span class="tl-leg-line"></span>data path</span>
  <span class="tl-leg"><span class="tl-leg-line ctrl"></span>control plane</span>
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

	nonce := a.writeConsoleShellHead(w, r, "replay", "Replay Explorer",
		fmt.Sprintf("write-job lifecycle · dead-letter & poison queue · %d dead-letter · %d quarantined", deadLetter, quarantined))

	fmt.Fprintf(w, `<div class="console-note">Jobs that exhaust 3 retries enter the dead-letter queue. Replaying requeues them (replay_count++); a job that crosses MAX_REPLAY_COUNT=%d is quarantined as poison. Batch replay processes up to REPLAY_BATCH_LIMIT=%d per call. Actions mutate the live write queue.</div>
<div class="q-strip">
  <div class="q-stat"><div class="q-stat-val" style="color:var(--accent2)">%d</div><div class="q-stat-label">Pending</div></div>
  <div class="q-stat"><div class="q-stat-val" style="color:var(--cyan)">%d</div><div class="q-stat-label">Processing</div></div>
  <div class="q-stat"><div class="q-stat-val" style="color:var(--green)">%d</div><div class="q-stat-label">Completed</div></div>
  <div class="q-stat"><div class="q-stat-val" style="color:var(--gold)">%d</div><div class="q-stat-label">Failed</div></div>
  <div class="q-stat"><div class="q-stat-val" style="color:var(--error)">%d</div><div class="q-stat-label">Dead-letter</div></div>
  <div class="q-stat"><div class="q-stat-val" style="color:var(--red)">%d</div><div class="q-stat-label">Quarantined</div></div>
</div>
<div class="section-title">Job lifecycle</div>
<div class="card"><div class="esc-chain">
  <span class="esc-step">pending</span><span class="esc-arrow">→</span>
  <span class="esc-step">processing</span><span class="esc-arrow">→</span>
  <span class="esc-step esc-step--ok">completed</span>
  <span class="esc-arrow" style="margin:0 4px">⟲</span>
  <span class="esc-step esc-step--warn">retry ×3</span><span class="esc-arrow">→</span>
  <span class="esc-step esc-step--danger">dead-letter</span><span class="esc-arrow">→</span>
  <span class="esc-step">replay ×%d</span><span class="esc-arrow">→</span>
  <span class="esc-step esc-step--danger">quarantined</span>
</div></div>`,
		config.Cfg.MaxReplayCount, config.Cfg.ReplayBatchLimit,
		pending, processing, completed, failed, deadLetter, quarantined,
		config.Cfg.MaxReplayCount)

	// Dead-letter table.
	fmt.Fprintf(w, `<div class="section-title">Dead-letter queue (%d)</div>`, deadLetter)
	if deadLetter > 0 {
		fmt.Fprintf(w, `<div class="action-row"><button class="btn btn--primary" data-replay-all>⟲ Replay all dead-letter (≤%d)</button></div>`, config.Cfg.ReplayBatchLimit)
	}
	if len(deadJobs) == 0 {
		fmt.Fprint(w, `<div class="console-note">Dead-letter queue is empty — no jobs have exhausted their retries.</div>`)
	} else {
		fmt.Fprint(w, `<table class="fe-table"><thead><tr><th>Job</th><th>Op</th><th>Reason</th><th>Retries</th><th>Replays</th><th>Correlation</th><th>Created</th><th>Action</th></tr></thead><tbody>`)
		for _, j := range deadJobs {
			reasonCls := "fe-target t-degraded"
			if j.DeadReason == "parse_error" || j.DeadReason == "unknown_op" {
				reasonCls = "fe-target t-readonly"
			}
			corr := j.CorrelationID
			if corr == "" {
				corr = "—"
			} else if len(corr) > 12 {
				corr = corr[:12]
			}
			fmt.Fprintf(w, `<tr><td class="fe-name">#%d %s</td><td>%s</td><td><span class="%s">%s</span></td><td>%d</td><td>%d/%d</td><td style="color:var(--muted)">%s</td><td>%s</td><td><button class="fe-sim-btn" data-replay-job="%d">⟲ Replay</button></td></tr>`,
				j.ID, template.HTMLEscapeString(j.Slug), template.HTMLEscapeString(j.Op), reasonCls, template.HTMLEscapeString(j.DeadReason), j.Retries, j.ReplayCount, config.Cfg.MaxReplayCount, template.HTMLEscapeString(corr), template.HTMLEscapeString(j.CreatedAt), j.ID)
		}
		fmt.Fprint(w, `</tbody></table>`)
	}

	// Quarantined (poison) table.
	fmt.Fprintf(w, `<div class="section-title">Quarantined as poison (%d)</div>`, quarantined)
	if len(poisonJobs) == 0 {
		fmt.Fprint(w, `<div class="console-note">No quarantined jobs — nothing has crossed the replay ceiling.</div>`)
	} else {
		fmt.Fprint(w, `<table class="fe-table"><thead><tr><th>Job</th><th>Op</th><th>Reason</th><th>Replays</th><th>Correlation</th><th>Created</th></tr></thead><tbody>`)
		for _, j := range poisonJobs {
			corr := j.CorrelationID
			if corr == "" {
				corr = "—"
			} else if len(corr) > 12 {
				corr = corr[:12]
			}
			fmt.Fprintf(w, `<tr><td class="fe-name">#%d %s</td><td>%s</td><td><span class="fe-target t-readonly">%s</span></td><td>%d</td><td style="color:var(--muted)">%s</td><td style="color:var(--muted)">%s</td></tr>`,
				j.ID, template.HTMLEscapeString(j.Slug), template.HTMLEscapeString(j.Op), template.HTMLEscapeString(j.DeadReason), j.ReplayCount, template.HTMLEscapeString(corr), template.HTMLEscapeString(j.CreatedAt))
		}
		fmt.Fprint(w, `</tbody></table>`)
	}

	writeConsoleShellFoot(w, nonce, `window.vpReplay=function(id){vpPost('/admin/replay/job?id='+id,function(d){return d.replayed?('Requeued job #'+id):'Job not replayable';});};
window.vpReplayAll=function(){vpPost('/api/v1/queue/replay',function(d){return 'Replayed '+d.replayed+' · quarantined '+d.skipped_quarantined;});};
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
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// =============================================================================
// Ω11 — Policy Provenance Inspector (GET /admin/policy)
// Shows the live policy engine state plus full evaluation history from SQLite.
// =============================================================================

func (a *App) handlePolicyPage(w http.ResponseWriter, r *http.Request) {
	nonce := a.writeConsoleShellHead(w, r, "policy", "Policy Provenance Inspector",
		"governance rules · evaluation history · provenance lineage")

	// Live evaluation for current display.
	live := policy.Global.EvaluateAll(policy.Context{})
	pass := len(live.Passed)
	warn := len(live.Warnings)
	fail := len(live.Failed)
	total := pass + warn + fail

	// Historical data from journal.
	var rows []policy.EvalRow
	var runs []policy.RunSummary
	if policy.GlobalJournal != nil {
		rows, _ = policy.GlobalJournal.History(120)
		runs, _ = policy.GlobalJournal.RunHistory(20)
	}

	sb := &strings.Builder{}
	sb.WriteString(`<div style="padding:18px 22px 0">`)

	// Live status strip.
	statusCls := "ps-pass"
	if fail > 0 {
		statusCls = "ps-fail"
	} else if warn > 0 {
		statusCls = "ps-warn"
	}
	sb.WriteString(`<div class="policy-strip">`)
	sb.WriteString(fmt.Sprintf(`<div class="policy-stat"><div class="policy-stat-val %s">%d/%d</div><div class="policy-stat-label">Pass</div></div>`, statusCls, pass, total))
	sb.WriteString(fmt.Sprintf(`<div class="policy-stat"><div class="policy-stat-val ps-warn">%d</div><div class="policy-stat-label">Warn</div></div>`, warn))
	sb.WriteString(fmt.Sprintf(`<div class="policy-stat"><div class="policy-stat-val ps-fail">%d</div><div class="policy-stat-label">Fail</div></div>`, fail))
	sb.WriteString(fmt.Sprintf(`<div class="policy-stat"><div class="policy-stat-val" style="color:var(--text2)">%d</div><div class="policy-stat-label">Run history</div></div>`, len(runs)))

	// Trend sparkline from run history.
	if len(runs) > 0 {
		sb.WriteString(`<div class="policy-stat" style="padding-top:6px"><div class="policy-stat-label">Recent trend</div><div class="policy-trend">`)
		maxTotal := 1
		for _, rs := range runs {
			if t := rs.Pass + rs.Warn + rs.Fail; t > maxTotal {
				maxTotal = t
			}
		}
		// Show newest on right — reverse.
		for i := len(runs) - 1; i >= 0; i-- {
			rs := runs[i]
			t := rs.Pass + rs.Warn + rs.Fail
			if t == 0 {
				t = 1
			}
			h := 36 * t / maxTotal
			if h < 3 {
				h = 3
			}
			cls := "tb-pass"
			if rs.Fail > 0 {
				cls = "tb-fail"
			} else if rs.Warn > 0 {
				cls = "tb-warn"
			}
			sb.WriteString(fmt.Sprintf(`<div class="trend-bar %s" style="height:%dpx" title="%s: %dp %dw %df"></div>`, cls, h, rs.EvaluatedAt.UTC().Format("15:04"), rs.Pass, rs.Warn, rs.Fail))
		}
		sb.WriteString(`</div></div>`)
	}
	sb.WriteString(`</div>`) // end policy-strip

	// Live policy results section.
	sb.WriteString(`<div class="section-head"><span class="section-head__title">Live evaluation</span><span class="section-head__hint">`)
	sb.WriteString(time.Now().UTC().Format("15:04:05Z"))
	sb.WriteString(`</span></div>`)
	sb.WriteString(`<table class="policy-history"><thead><tr>`)
	for _, h := range []string{"Result", "Policy", "Category", "Severity", "Detail"} {
		sb.WriteString(`<th>` + h + `</th>`)
	}
	sb.WriteString(`</tr></thead><tbody>`)

	allLive := append(append(live.Passed, live.Warnings...), live.Failed...)
	for _, r := range allLive {
		res := "pass"
		if !r.Passed {
			if r.Severity == policy.SeverityWarning || r.Severity == policy.SeverityAdvisory {
				res = "warn"
			} else {
				res = "fail"
			}
		}
		badge := fmt.Sprintf(`<span class="pol-badge pol-%s">%s</span>`, res, sentenceWord(res))
		sb.WriteString(`<tr>`)
		sb.WriteString(`<td>` + badge + `</td>`)
		sb.WriteString(`<td><span class="pol-name">` + html.EscapeString(r.Name) + `</span></td>`)
		sb.WriteString(`<td><span class="pol-cat">` + html.EscapeString(string(r.Category)) + `</span></td>`)
		sb.WriteString(`<td><span class="pol-cat">` + html.EscapeString(string(r.Severity)) + `</span></td>`)
		sb.WriteString(`<td><span class="pol-detail">` + html.EscapeString(r.Message) + `</span></td>`)
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table>`)

	// Historical evaluation log.
	if len(rows) > 0 {
		sb.WriteString(`<div class="section-head"><span class="section-head__title">Evaluation history</span><span class="section-head__hint">last `)
		sb.WriteString(fmt.Sprintf("%d", len(rows)))
		sb.WriteString(` entries</span></div>`)
		sb.WriteString(`<table class="policy-history"><thead><tr>`)
		for _, h := range []string{"Timestamp", "Run ID", "Result", "Policy", "Category", "Detail"} {
			sb.WriteString(`<th>` + h + `</th>`)
		}
		sb.WriteString(`</tr></thead><tbody>`)
		for _, row := range rows {
			badge := fmt.Sprintf(`<span class="pol-badge pol-%s">%s</span>`, row.Result, sentenceWord(row.Result))
			ts := row.EvaluatedAt.UTC().Format("2006-01-02 15:04:05Z")
			runShort := row.RunID
			if len(runShort) > 12 {
				runShort = runShort[:12] + "…"
			}
			sb.WriteString(`<tr>`)
			sb.WriteString(`<td><span class="pol-ts">` + ts + `</span></td>`)
			sb.WriteString(`<td><span class="pol-runid">` + html.EscapeString(runShort) + `</span></td>`)
			sb.WriteString(`<td>` + badge + `</td>`)
			sb.WriteString(`<td><span class="pol-name">` + html.EscapeString(row.PolicyName) + `</span></td>`)
			sb.WriteString(`<td><span class="pol-cat">` + html.EscapeString(row.Category) + `</span></td>`)
			sb.WriteString(`<td><span class="pol-detail">` + html.EscapeString(row.Detail) + `</span></td>`)
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table>`)
	} else {
		sb.WriteString(`<div style="margin:22px 0;color:var(--muted);font:400 12px var(--mono)">No evaluation history yet — first run records on startup.</div>`)
	}

	sb.WriteString(`</div>`) // end padding wrapper
	fmt.Fprint(w, sb.String())
	writeConsoleShellFoot(w, nonce, "")
}

// sentenceWord capitalises the first letter of a machine word ("pass" →
// "Pass"); the console reads in sentence case, not in capitals.
func sentenceWord(w string) string {
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}
