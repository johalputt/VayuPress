// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_monitoring.go — VayuOS "Monitoring" surface (VayuOS consolidation).
//
// This folds the at-a-glance half of the classic v1 SRE console into the single
// os admin: current system mode, live performance percentiles, storage/queue
// health, and the governance error-budget ledger — all rendered server-side
// from the same in-process sources the v1 console and JSON APIs use, then kept
// fresh by a small poll loop against the existing /api/v1/admin/{mode,budgets}
// endpoints. Deep interactive consoles (mode transitions, topology, fault
// simulation, replay, ADR registry) remain at their /admin/* routes and are
// linked from here, so nothing regresses while the surfaces converge.
//
// CSP posture matches the rest of VayuOS: no inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped.

import (
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/budget"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// budgetStateLabel is a budget state as the console says it.
func budgetStateLabel(state string) string {
	switch state {
	case "healthy":
		return "Healthy"
	case "at-risk":
		return "At risk"
	default:
		return "Exhausted"
	}
}

// budgetTone is a budget state as a state's tone.
func budgetTone(state string) string {
	switch state {
	case "healthy":
		return "ok"
	case "at-risk":
		return "warn"
	default: // exhausted
		return "danger"
	}
}

// monitoringState is the sentence the Monitoring page opens on: the most
// urgent thing true now, or that the install is well. The order is what an
// operator needs first: something waiting right now, then a mode that limits
// the site, then work that failed, then what is running out.
func monitoringState(snap *adminMetricsSnapshot, cur mode.Mode, ws, rs dbpkg.StallState, rec analytics.CollectorState, budgets []budget.Status) (tone, state string) {
	switch {
	case ws.Stalled && ws.Current != nil:
		return "danger", "Writes are waiting: the write connection has been held for " + shortDur(ws.Current.Duration)
	case rs.Stalled && rs.Current != nil:
		return "danger", "Reads are waiting: every read connection has been taken for " + shortDur(rs.Current.Duration)
	case cur != mode.ModeNormal:
		return saModeTone(cur), "Running in " + strings.ToLower(saModeLabel(cur)) + " mode"
	case !ws.Watching || !rs.Watching:
		return "warn", "Stalls are not being watched, so nothing below is measured"
	case !rec.Running:
		return "danger", "Page views are not being written"
	case snap.FailedJobs > 0:
		return "warn", strconv.Itoa(snap.FailedJobs) + " background job" + plural(snap.FailedJobs) + " failed"
	case snap.StoragePct >= 90:
		return "warn", "Storage is " + strconv.Itoa(int(snap.StoragePct)) + "% full"
	}
	for _, b := range budgets {
		if b.State == "exhausted" {
			return "warn", "The " + b.Name + " budget is exhausted"
		}
	}
	return "ok", "Running normally: requests answer in " + strconv.FormatInt(snap.HTTPP95, 10) + " ms at the 95th percentile"
}

func (a *App) handleOSMonitoring(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	snap := a.getAdminSnapshot()
	cur := mode.Global.Current()
	ws, rs := dbpkg.WriteStall(), dbpkg.ReadStall()
	rec := a.analytics.CollectorStats()
	budgets := budget.Global.Status(time.Now())
	tone, state := monitoringState(snap, cur, ws, rs, rec, budgets)

	uptime := time.Duration(snap.UptimeSeconds) * time.Second
	now := ui.Section("Now", "", ui.Facts(
		ui.Fact{Key: "System mode", Value: ui.HTML(monModePill(cur, false))},
		ui.Fact{Key: "Requests", Value: ui.Text(strconv.FormatInt(snap.HTTPP95, 10) + " ms at the 95th percentile, last 15 minutes")},
		ui.Fact{Key: "Pages drawn", Value: ui.Text(strconv.FormatInt(snap.RenderP99, 10) + " ms at the 99th percentile")},
		ui.Fact{Key: "Render cache", Value: ui.Text(strconv.Itoa(int(snap.CacheHitRatio*100)) + "% served from the cache")},
		ui.Fact{Key: "Background jobs", Value: ui.Text(strconv.Itoa(snap.PendingJobs) + " waiting · " + strconv.Itoa(snap.FailedJobs) +
			" failed · " + strconv.FormatInt(snap.WorkersAlive, 10) + " worker" + plural(int(snap.WorkersAlive)) +
			" · " + strconv.FormatInt(snap.WriteP99, 10) + " ms at the 99th percentile")},
		ui.Fact{Key: "Storage", Value: `<a href="/os/storage">` + ui.Text(strconv.Itoa(int(snap.StoragePct))+"% of "+dbpkg.FormatBytes(snap.QuotaBytes)+" used") + `</a>`},
	))

	var brows [][]ui.HTML
	for _, b := range budgets {
		brows = append(brows, []ui.HTML{
			ui.Text(b.Name) + `<div class="row-meta">tracks ` + ui.Text(b.Tracks) + `</div>`,
			ui.Text(strconv.Itoa(b.Consumed) + " / " + strconv.Itoa(b.Limit)),
			ui.HTML(monBudgetStatePill(b.Name, b.State, false)),
		})
	}
	budgetsSection := ui.Section("Error budgets", "counted, never acted on without you",
		ui.Table([]string{"Budget", "Consumed", "State"}, brows, "No budget is defined."))

	link := func(href, label, hint string) ui.Row {
		return ui.Row{Label: label, Hint: hint, Control: `<a class="btn btn--ghost btn--sm" href="` + ui.HTML(href) + `">Open</a>`}
	}
	deeper := ui.Section("Deeper", "", ui.Rows(
		link("/os/modes", "Mode transitions", "Move the install between modes and read the journal of every change."),
		link("/os/topology", "Topology", "How the parts depend on each other, and their health now."),
		link("/os/faults", "Fault simulation", "Inject a controlled fault to see recovery work. Not for production."),
		link("/os/replay", "Replay", "Jobs that failed, and a safe way to run them again."),
		link("/os/adr", "Decisions", "The architecture decisions this install is built on."),
	))

	// The page renders a complete snapshot. This invisible poller keeps an
	// open page fresh: every 5 s it asks for out-of-band fragments that swap
	// the mode, each budget's state and the updated stamp in place.
	poller := `<div hx-get="/os/monitoring/live" hx-trigger="every 5s" hx-swap="none" aria-hidden="true"></div>`

	body := ui.Status(ui.StatusPage{
		Title:   "Monitoring",
		Actions: ui.HTML(monUpdatedStamp(time.Now(), a.nowSnapAge(), false)),
		Tone:    tone,
		State:   state,
		Detail:  ui.Text("Up " + uptime.Truncate(time.Second).String() + " · refreshed as you watch"),
	}, now, ui.HTML(routeLatencySection()), ui.HTML(writeStallSection(ws, rec)),
		ui.HTML(readStallSection(rs, coldRenderShed.Load())), budgetsSection, deeper)

	writeOSHTML(w, r, adminOSLayout(nonce, "Monitoring", "monitoring", cfg, htmpl.HTML(poller+string(body))))
}

// routeLatencySection lists the slowest routes of the last 15 minutes.
func routeLatencySection() string {
	var rows [][]ui.HTML
	for _, st := range metrics.RouteLatency.Slowest(8) {
		rows = append(rows, []ui.HTML{
			`<code>` + ui.Text(st.Route) + `</code>`,
			ui.Text(strconv.FormatInt(st.Requests, 10)),
			ui.Text(strconv.FormatInt(st.P95, 10) + " ms"),
			ui.Text(humanMS(st.Total)),
		})
	}
	return string(ui.Section("Where the time goes", "slowest routes · last 15 min",
		ui.Table([]string{"Route", "Requests", "p95", "Time taken"}, rows, "No requests in the last 15 minutes.")))
}

// humanMS reads a total of milliseconds the way a person says it.
func humanMS(ms int64) string {
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + " ms"
	}
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}
