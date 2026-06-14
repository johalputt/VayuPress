package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/fault"
	"github.com/johalputt/vayupress/internal/mode"
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

// sidebarItem renders one nav row, marking it active when key==active.
func sidebarItem(href, icon, label, key, active string, badge, status string) string {
	cls := "sidebar-item"
	if key == active {
		cls += " active"
	}
	right := ""
	if badge != "" {
		right = `<span class="sidebar-badge">` + badge + `</span>`
	} else if status != "" {
		right = `<span class="sidebar-status ` + status + `"></span>`
	}
	return fmt.Sprintf(`<a href="%s" class="%s"><div class="sidebar-item-left"><span class="sidebar-icon">%s</span>%s</div>%s</a>`,
		href, cls, icon, label, right)
}

// writeConsoleShellHead emits the admin shell through the opening <main> and the
// page header. active selects the highlighted sidebar item.
func (a *App) writeConsoleShellHead(w http.ResponseWriter, r *http.Request, active, pageTitle, pageSub string) string {
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	nonce := render.CSPNonce(r)

	cur := mode.Global.Current()
	modeCls, modeLabel, _ := modeVisual(cur)

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuPress — %s</title><meta name="robots" content="noindex, nofollow">
%s%s</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="app-shell">
<header class="topbar" role="banner">
  <a href="/admin" class="topbar-logo"><span class="omega-mark">Ω</span><span class="topbar-wordmark">VayuPress</span><span class="topbar-sep">/</span><span class="topbar-domain">%s</span></a>
  <div class="topbar-center"><div class="live-chip"><span class="live-dot"></span>LIVE</div><span class="topbar-constitution">Constitution v6.0 · P1–P27 · Ω1–Ω9</span></div>
  <div class="topbar-right"><span class="mode-badge %s"><span class="pulse-dot"></span>%s</span><a href="/admin" class="kbd-hint">← Console</a></div>
</header>
<nav class="sidebar" aria-label="Admin navigation">
  <div class="sidebar-section"><span class="sidebar-section-label">Core</span>%s%s%s</div>
  <div class="sidebar-section"><span class="sidebar-section-label">Observe</span>%s%s%s</div>
  <div class="sidebar-section"><span class="sidebar-section-label">Govern</span>%s%s%s</div>
  <div class="sidebar-section"><span class="sidebar-section-label">System</span>%s%s</div>
  <div class="sidebar-footer"><span class="sidebar-version">v%s</span><span class="sidebar-constitution">Ω1–Ω9 compliant</span></div>
</nav>
<main id="main-content">
<div class="page-header"><div><div class="page-title">%s</div><div class="page-sub">%s</div></div><a href="/admin" class="btn">← Overview</a></div>`,
		pageTitle,
		render.AdminCSSLink(), render.HighContrastCSSLink(),
		config.Cfg.Domain,
		modeCls, modeLabel,
		sidebarItem("/admin", "◈", "Overview", "overview", active, "", ""),
		sidebarItem("/api/v1/articles", "◻", "Articles", "articles", active, "", ""),
		sidebarItem("/api/v1/queue", "⟳", "Queue", "queue", active, "", ""),
		sidebarItem("/api/v1/admin/outbox/events", "◎", "Events", "events", active, "", "s-ok"),
		sidebarItem("/api/v1/admin/traces", "⋯", "Traces", "traces", active, "", ""),
		sidebarItem("/health/dependencies", "♥", "Health", "health", active, "", "s-ok"),
		sidebarItem("/admin/faults", "⊞", "Fault Engine", "faults", active, "", ""),
		sidebarItem("/admin/modes", "⬡", "System Modes", "modes", active, "", ""),
		sidebarItem("/admin/adr", "≡", "ADRs", "adrs", active, "", ""),
		sidebarItem("/health/benchmarks", "⚡", "Benchmarks", "benchmarks", active, "", ""),
		sidebarItem("/metrics", "∼", "Metrics", "metrics", active, "", ""),
		Version,
		pageTitle, pageSub,
	)
	return nonce
}

func writeConsoleShellFoot(w http.ResponseWriter, nonce, script string) {
	fmt.Fprintf(w, `</main></div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg" style="position:fixed;bottom:16px;right:16px;z-index:500;max-width:360px"></div>
<script nonce="%s">
(function(){'use strict';
var msg=document.getElementById('action-msg');
function csrf(){var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});return m?m.split('=')[1]:'';}
window.vpPost=function(url,onok){fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}}).then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});}).then(function(res){show(res.ok?(onok?onok(res.d):'ok'):(res.d.detail||res.d.title||'error'),!res.ok);if(res.ok)setTimeout(function(){location.reload();},650);}).catch(function(e){show('Error: '+e,true);});};
function show(text,isErr){msg.textContent=text;msg.style.borderColor=isErr?'var(--error)':'var(--green)';msg.style.background=isErr?'rgba(239,68,68,.08)':'rgba(16,185,129,.08)';msg.classList.add('visible');}
%s
})();
</script></body></html>`, nonce, script)
}

// =============================================================================
// System Mode Engine page  (GET /admin/modes)
// =============================================================================

func (a *App) handleModesPage(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	curCls, curLabel, curDesc := modeVisual(cur)
	history := mode.Global.History()

	nonce := a.writeConsoleShellHead(w, r, "modes", "System Mode Engine",
		fmt.Sprintf("adaptive runtime state machine · %d transitions recorded · current: %s", len(history), cur))

	// Current mode banner.
	fmt.Fprintf(w, `<div class="mode-banner %s"><div class="mode-banner-pulse"><div class="mode-banner-pulse-dot"></div></div><div class="mode-banner-info"><span class="mode-banner-state">%s</span><span class="mode-banner-desc">%s</span></div></div>
<div class="console-note">Transitions follow the permitted graph. Reachable targets are actionable; blocked targets require an intermediate transition or operator override. Every transition is journaled and appears on the Overview timeline.</div>
<div class="section-title">Mode State Machine</div>
<div class="mode-grid">`, curCls, curLabel, curDesc)

	for _, m := range mode.AllModes() {
		cls := modeShortClass(m)
		_, label, desc := modeVisual(m)
		isCur := m == cur
		reachable := mode.IsAllowed(cur, m)
		tileCls := "mode-tile " + cls
		if isCur {
			tileCls += " current"
		}
		var badge, btn string
		switch {
		case isCur:
			badge = `<span class="mode-tile-badge cur">● Active</span>`
			btn = `<button class="mode-tile-btn" disabled>current state</button>`
		case reachable:
			badge = `<span class="mode-tile-badge reach">Reachable</span>`
			btn = fmt.Sprintf(`<button class="mode-tile-btn" onclick="vpMode('%s',false)">Transition →</button>`, m)
		default:
			badge = `<span class="mode-tile-badge block">Blocked</span>`
			btn = fmt.Sprintf(`<button class="mode-tile-btn" onclick="vpMode('%s',true)">Force override</button>`, m)
		}
		fmt.Fprintf(w, `<div class="mode-tile %s"><div class="mode-tile-top"><span class="mode-tile-name">%s</span>%s</div><div class="mode-tile-desc">%s</div>%s</div>`,
			strings.TrimPrefix(tileCls, "mode-tile "), label, badge, desc, btn)
	}
	fmt.Fprint(w, `</div>`)

	// Transition lineage rendered with the operational timeline component.
	fmt.Fprint(w, `<div class="section-title">Transition Lineage</div>`)
	if len(history) == 0 {
		fmt.Fprint(w, `<div class="console-note">No transitions yet — the runtime has held NORMAL since boot. Trigger a transition above or simulate faults in the Fault Engine to populate the lineage.</div>`)
	} else {
		var entries []tlEntry
		for _, t := range history {
			sev := "tl-warn"
			switch t.To {
			case mode.ModeReadOnly, mode.ModeQuarantined:
				sev = "tl-err"
			case mode.ModeNormal:
				sev = "tl-ok"
			case mode.ModeRecovery:
				sev = "tl-info"
			}
			causal := "operator-initiated"
			if t.Cause != "" && t.Cause != "operator" {
				causal = "caused by " + t.Cause
			}
			entries = append(entries, tlEntry{
				Clock: t.OccurredAt.UTC().Format("15:04:05"), Cat: "mode", CatClass: "tl-cat-mode", Sev: sev,
				Msg:    fmt.Sprintf("%s → %s · %s", strings.ToUpper(string(t.From)), strings.ToUpper(string(t.To)), t.Reason),
				Causal: causal,
			})
		}
		fmt.Fprintf(w, `<div class="timeline-panel">%s</div>`, renderTimelineBody(entries))
	}

	writeConsoleShellFoot(w, nonce, `window.vpMode=function(to,force){vpPost('/admin/mode/transition?to='+encodeURIComponent(to)+(force?'&force=true':''),function(d){return 'Mode → '+(d.mode||to).toUpperCase();});};`)
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
<div class="section-title">Fault Points & Escalation Rules</div>
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
		fmt.Fprintf(w, `<tr id="fe-%s"><td class="fe-name">%s</td><td><span class="%s">%d</span></td><td>×%d</td><td>%s</td><td><span class="%s">%s</span></td><td><button class="fe-sim-btn" onclick="vpFault('%s')">Simulate ⚡</button></td></tr>`,
			template.HTMLEscapeString(rule.FaultName), rule.FaultName, countCls, count, rule.Threshold, window, tgtCls, strings.ToUpper(string(rule.TargetMode)), rule.FaultName)
	}
	fmt.Fprint(w, `</tbody></table>`)

	// Escalation chain visualization for the canonical WAL-write path.
	fmt.Fprint(w, `<div class="section-title">Escalation Chain — Example</div>
<div class="trace-panel"><div class="esc-chain">
  <span class="esc-step">fault: db.wal.write</span><span class="esc-arrow">→</span>
  <span class="esc-step">counter ×3 / 5min</span><span class="esc-arrow">→</span>
  <span class="esc-step">threshold exceeded</span><span class="esc-arrow">→</span>
  <span class="esc-step" style="border-color:rgba(239,68,68,.4);color:var(--error)">mode: NORMAL → READ-ONLY</span><span class="esc-arrow">→</span>
  <span class="esc-step">write queue paused</span>
</div></div>`)

	writeConsoleShellFoot(w, nonce, `window.vpFault=function(name){vpPost('/admin/fault/simulate?name='+encodeURIComponent(name),function(d){var m=(d.current_mode||'').toUpperCase();return 'Fired '+name+' ×'+d.trigger_count+(d.escalated?(' → escalated to '+m):'');});};`)
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
		writeAPIError(w, r, 400, "invalid_mode", "unknown target mode: "+string(to), "https://docs.vayupress.com/operations/modes")
		return
	}

	from := mode.Global.Current()
	if force {
		mode.Global.ForceTransition(to, "operator console override")
	} else if err := mode.Global.Transition(to, "operator console transition", "operator"); err != nil {
		writeAPIError(w, r, 409, "transition_not_permitted", err.Error(), "https://docs.vayupress.com/operations/modes")
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
		writeAPIError(w, r, 400, "unknown_fault", "unknown fault point: "+name, "https://docs.vayupress.com/operations/faults")
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
