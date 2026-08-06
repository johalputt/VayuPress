// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_writestall.go — the write connection, on the page.
//
// The standing rule in the contributor notes: diagnostics belong on the page.
// "Run this and paste me the output" is a product failure in diagnostic
// clothing — the console should already be showing it.
//
// This is that rule applied to the fault that prompted it (ADR-0156). A live install
// returned 502 for minutes and recovered by itself; the process was up, the
// database was fine, there was no restart, no OOM kill and nothing in the log.
// Every signal the product offered said "healthy", because the one thing that
// was not healthy — the queue in front of SQLite's single write connection —
// was not measured anywhere.
//
// It is measured now, and this is where an operator reads it: how many times
// the writer has jammed since boot, how long the worst one lasted, what it cost
// the callers waiting on it, and whether a goroutine snapshot was captured
// while it was stuck.
//
// The card says nothing it cannot measure. There is no "live waiters" figure,
// because database/sql does not expose one, and a number invented for a panel
// is the same defect as a posture row for a control nobody verified.

import (
	"html"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// shortDur renders a duration for a panel: seconds below a minute, then m/s.
func shortDur(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return strconv.Itoa(m) + "m " + strconv.Itoa(s) + "s"
}

// writeStallStats returns the stat tiles for the writer and the view recorder.
func writeStallStats(st dbpkg.WriteStallState, rec analytics.CollectorState) string {
	stallLabel := "none since boot"
	stallValue := "0"
	if st.Total > 0 {
		stallValue = strconv.FormatInt(st.Total, 10)
		stallLabel = "worst " + shortDur(st.Longest)
	}
	stallCard := monStat("Write stalls", stallValue, stallLabel)
	if st.Stalled {
		// A tile that wants attention, per the house style.
		stallCard = `<div class="stat-card stat-card--warn">
  <div class="stat-card__top"><div class="stat-card__label">Write stalls</div></div>
  <div class="stat-card__value">happening now</div>
  <div class="stat-card__bottom"><span class="muted text-xs">` +
			html.EscapeString(shortDur(st.Current.Duration)+" so far") + `</span></div>
</div>`
	}

	// View counting. "Running" is a tile of its own because a recorder that
	// buffers into a map nobody drains loses every view in silence.
	countLabel := "flusher running"
	countValue := "on"
	if !rec.Running {
		countValue = "off"
		countLabel = "views are NOT being written"
	}
	// The compression ratio is the number that says why this is safe: views
	// counted per statement actually written.
	ratio := "—"
	if rec.Writes > 0 {
		ratio = strconv.FormatFloat(float64(rec.Flushed)/float64(rec.Writes), 'f', 1, 64) + "×"
	}

	return `<div class="stat-grid mb-6">` +
		stallCard +
		monStat("Queued for the writer", shortDur(st.WaitDuration),
			strconv.FormatInt(st.WaitCount, 10)+" callers waited, since boot") +
		monStat("View counting", countValue, countLabel) +
		monStat("Views per write", ratio,
			strconv.FormatInt(rec.Flushed, 10)+" counted · "+strconv.FormatInt(rec.Writes, 10)+" statements") +
		`</div>`
}

// writeStallCard renders the explanatory card and the stall history.
func writeStallCard(st dbpkg.WriteStallState, rec analytics.CollectorState) string {
	out := `<div class="section-head"><div class="section-head__title">Write connection</div>` +
		`<div class="section-head__hint">SQLite has one writer, so everything that writes shares a single connection. ` +
		`When something holds it, other writes queue — this is where that shows up.</div></div>`

	// The live case first: an operator opening this page mid-incident wants the
	// answer above the fold, not in a history table.
	if st.Stalled && st.Current != nil {
		c := st.Current
		out += `<div class="settings-callout"><strong>The write connection is contended right now.</strong> ` +
			`<span class="text-sm muted">Started ` + html.EscapeString(c.Start.UTC().Format("15:04:05")) +
			` UTC, ` + html.EscapeString(shortDur(c.Duration)) + ` so far. ` +
			html.EscapeString(strconv.FormatInt(c.Waits, 10)) + ` caller(s) have queued behind it, for ` +
			html.EscapeString(shortDur(c.Blocked)) + ` in total. Reads and cached pages are unaffected; ` +
			`anything that writes is waiting.</span></div>`
	}

	if !st.Watching {
		out += `<div class="settings-callout"><strong>Not being watched.</strong> ` +
			`<span class="text-sm muted">The write-stall watchdog did not start, so nothing on this ` +
			`card is being measured. This is a fault in the install, not a quiet install.</span></div>`
	}

	rows := ""
	for i := len(st.Recent) - 1; i >= 0; i-- {
		e := st.Recent[i]
		dump := `<span class="muted text-xs">—</span>`
		if e.Dump != "" {
			// The path, not the contents. A goroutine dump is an operator's
			// artefact; the panel says it exists and where.
			dump = `<code class="text-xs">` + html.EscapeString(e.Dump) + `</code>`
		}
		rows += `<tr>
  <td class="row-title">` + html.EscapeString(e.Start.UTC().Format("2006-01-02 15:04:05")) + ` UTC</td>
  <td class="muted text-sm">` + html.EscapeString(shortDur(e.Duration)) + `</td>
  <td class="muted text-sm">` + strconv.FormatInt(e.Waits, 10) + `</td>
  <td class="muted text-sm">` + html.EscapeString(shortDur(e.Blocked)) + `</td>
  <td>` + dump + `</td>
</tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="5" class="muted text-sm">No write stall has been recorded since this ` +
			`install last started.</td></tr>`
	}
	out += `<div class="card mb-6">
  <div class="settings-block-title">Recent write stalls</div>
  <p class="text-sm muted">A stall is a period during which a caller was waiting for the write connection
  continuously. Brief contention is normal on a busy install and is not listed here. "Queued" is the total
  time callers spent waiting, summed across all of them, so it exceeds the stall's own length whenever more
  than one was affected.</p>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Started</th><th>Lasted</th><th>Callers delayed</th><th>Queued</th><th>Snapshot</th></tr></thead>
    <tbody>` + rows + `</tbody>
  </table></div>
</div>`

	// The recorder's own state, because it is the biggest single writer on a
	// content site and the one most likely to be misconfigured into silence.
	dropNote := ""
	if rec.Dropped > 0 {
		dropNote = ` <strong>` + strconv.FormatInt(rec.Dropped, 10) + `</strong> view(s) were dropped because
		the buffer was full — the buffer is bounded on purpose, since losing a view count is a rounding error
		and losing the site is an outage.`
	}
	errNote := ""
	if rec.LastErr != "" {
		errNote = `<p class="text-sm muted">Last flush error: <code>` + html.EscapeString(rec.LastErr) + `</code></p>`
	}
	last := "never"
	if !rec.LastFlush.IsZero() {
		last = shortDur(time.Since(rec.LastFlush)) + " ago"
	}
	out += `<div class="card mb-6">
  <div class="settings-block-title">View counting</div>
  <p class="text-sm muted">Page views are counted in memory and written in batches, so traffic never queues on
  the write connection. A page under load costs one row update every few seconds however many people read it.` +
		dropNote + `</p>
  <div class="flex justify-between mt-3"><span class="text-sm muted">Buffered now</span><span>` +
		strconv.Itoa(rec.Buffered) + ` / ` + strconv.Itoa(rec.BufferedHi) + ` keys</span></div>
  <div class="flex justify-between mt-2"><span class="text-sm muted">Awaiting the next write</span><span>` +
		strconv.FormatInt(rec.Pending, 10) + ` view(s)</span></div>
  <div class="flex justify-between mt-2"><span class="text-sm muted">Last written</span><span>` +
		html.EscapeString(last) + `</span></div>
  ` + errNote + `
</div>`
	return out
}
