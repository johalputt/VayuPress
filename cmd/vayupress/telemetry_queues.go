// SPDX-License-Identifier: Apache-2.0

package main

// telemetry_queues.go — the install's best-effort telemetry queues together:
// page views, collected beacons, reading engagement and the shield's events.
// Each is bounded and batched; this is what they share at the edges: the
// mode that holds them, the shutdown that writes what they hold, and the
// Monitoring rows that say what each has lost.

import (
	"context"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/ui"
)

// telemetryHeld is the hold every queue is given: true while the install's
// mode holds telemetry (mode.Mode.HoldsTelemetry).
func telemetryHeld() bool { return mode.Global.Current().HoldsTelemetry() }

// flushTelemetry writes what the queues hold, for the shutdown, within
// limit: the analytics collectors directly, and the two batch writers by
// waiting for their Run, which drains once the shutdown channel is closed
// (so it is called after it is).
func (a *App) flushTelemetry(limit time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if a.analytics != nil {
		if err := a.analytics.Flush(ctx); err != nil {
			logging.LogError("main", "page views on shutdown", err.Error())
		}
		if err := a.analytics.FlushEvents(ctx); err != nil {
			logging.LogError("main", "collected events on shutdown", err.Error())
		}
	}
	for name, stopped := range map[string]<-chan struct{}{
		"engagement": a.vaEngagement.IngestStopped(), "shield events": a.vayuShield.AsyncWrites().Stopped(),
	} {
		select {
		case <-stopped:
		case <-ctx.Done():
			logging.LogError("main", name+" on shutdown", "not written within "+limit.String())
		}
	}
}

// telemetryQueue is one queue as Monitoring states it.
type telemetryQueue struct {
	name             string
	buffered, cap    int
	dropped, lost    int64
	held             bool
	lastErr          string
	unit             string // "views", "events"
	bufferedIsInKeys bool
}

// telemetryQueues reads every queue's state.
func (a *App) telemetryQueues() []telemetryQueue {
	var out []telemetryQueue
	if a.analytics != nil {
		v, e := a.analytics.CollectorStats(), a.analytics.EventCollectorStats()
		out = append(out,
			telemetryQueue{name: "Page views", buffered: v.Buffered, cap: v.BufferedHi, dropped: v.Dropped, lost: v.Lost, held: v.Held, lastErr: v.LastErr, unit: "views", bufferedIsInKeys: true},
			telemetryQueue{name: "Collected events", buffered: e.Buffered, cap: e.BufferedHi, dropped: e.Dropped, lost: e.Lost, held: e.Held, lastErr: e.LastErr, unit: "events"})
	}
	held := telemetryHeld()
	if a.vaEngagement != nil {
		st := a.vaEngagement.Stats()
		out = append(out, telemetryQueue{name: "Reading engagement", buffered: st.Buffered, cap: st.Cap, dropped: st.Dropped, lost: st.Lost, held: held, unit: "events"})
	}
	if w := a.vayuShield.AsyncWrites(); w != nil {
		n, c := w.Buffered()
		out = append(out, telemetryQueue{name: "Shield events", buffered: n, cap: c, dropped: w.Dropped(), lost: w.Lost(), held: held, unit: "events"})
	}
	return out
}

// telemetrySection is the queues on Monitoring, under the write connection
// they all wait for. What a queue holds, dropped or lost used to be shown for
// page views alone; the others were nowhere.
func telemetrySection(qs []telemetryQueue) string {
	return string(ui.Section("Telemetry", "Counted in memory, written in batches", ui.Join(
		`<p class="page-sub">`+ui.Brief("Each queue is bounded on purpose: losing a count is a rounding error, and losing the site is an outage.")+`</p>`,
		ui.Rows(telemetryRows(qs)...))))
}

// telemetryRows are the queues as Monitoring rows: what each holds against
// its cap, and what it has not written since boot, by why: dropped because
// it was full, lost because the database refused it.
func telemetryRows(qs []telemetryQueue) []ui.Row {
	rows := make([]ui.Row, 0, len(qs))
	for _, q := range qs {
		of := " " + q.unit
		if q.bufferedIsInKeys {
			of = " pages and referrers"
		}
		hint := strconv.Itoa(q.buffered) + " of " + strconv.Itoa(q.cap) + of + " waiting. Since boot, " +
			strconv.FormatInt(q.dropped, 10) + " dropped because the queue was full, " +
			strconv.FormatInt(q.lost, 10) + " lost because the database refused them."
		state := ui.State("ok", "Written")
		switch {
		case q.held:
			state = ui.State("warn", "Held: read-only mode")
		case q.lastErr != "":
			hint += " Last refusal: " + q.lastErr + "."
			state = ui.State("warn", "Refused, tried again")
		case q.lost > 0 || q.dropped > 0:
			state = ui.State("warn", "Some not written")
		}
		rows = append(rows, ui.Row{Label: q.name, Hint: hint, Control: state})
	}
	return rows
}
