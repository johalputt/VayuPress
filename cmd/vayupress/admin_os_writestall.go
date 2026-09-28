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
	"github.com/johalputt/vayupress/internal/ui"
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

// writeStallSection is the write connection on the Monitoring page: whether it
// is held now, what it has cost since boot, the view recorder that is its
// biggest writer, then the stalls it has had. Every value is a row with its
// state as a dot and a word; the stall in progress is the first row, because an
// operator opening the page mid-incident wants that before any history.
func writeStallSection(st dbpkg.StallState, rec analytics.CollectorState) string {
	var rows []ui.Row
	if st.Stalled && st.Current != nil {
		c := st.Current
		rows = append(rows, ui.Row{Label: "Right now",
			Hint: "The write connection is contended right now. Started " + c.Start.UTC().Format("15:04:05") +
				" UTC, " + shortDur(c.Duration) + " so far; " + strconv.FormatInt(c.Waits, 10) +
				" caller(s) have queued behind it, for " + shortDur(c.Blocked) +
				" in total. Reads and cached pages are unaffected; anything that writes is waiting.",
			Control: ui.State("danger", "Contended")})
	}
	if !st.Watching {
		rows = append(rows, ui.Row{Label: "Watching",
			Hint:    "The write-stall watchdog did not start, so nothing here is being measured. This is a fault in the install, not a quiet install.",
			Control: ui.State("danger", "Not being watched")})
	}
	stalls := "none since boot"
	if st.Total > 0 {
		stalls = strconv.FormatInt(st.Total, 10) + ", worst " + shortDur(st.Longest)
	}
	// The compression ratio is the number that says why counting views this
	// way is safe: views counted per statement actually written.
	ratio := "—"
	if rec.Writes > 0 {
		ratio = strconv.FormatFloat(float64(rec.Flushed)/float64(rec.Writes), 'f', 1, 64) + "×"
	}
	// View counting has a row of its own because a recorder that buffers into a
	// map nobody drains loses every view in silence.
	counting := ui.State("ok", "Running")
	if !rec.Running {
		counting = ui.State("danger", "Off: views are NOT being written")
	}
	last := "never"
	if !rec.LastFlush.IsZero() {
		last = shortDur(time.Since(rec.LastFlush)) + " ago"
	}
	rows = append(rows,
		ui.Row{Label: "Stalls", Control: ui.Text(stalls)},
		ui.Row{Label: "Queued for the writer", Hint: strconv.FormatInt(st.WaitCount, 10) + " callers waited, since boot",
			Control: ui.Text(shortDur(st.WaitDuration))},
		ui.Row{Label: "View counting",
			Hint:    "Page views are counted in memory and written in batches, so traffic never queues on the write connection.",
			Control: counting},
		ui.Row{Label: "Views per write", Hint: strconv.FormatInt(rec.Flushed, 10) + " counted · " +
			strconv.FormatInt(rec.Writes, 10) + " statement" + plural(rec.Writes), Control: ui.Text(ratio)},
		ui.Row{Label: "Buffered now", Control: ui.Text(strconv.Itoa(rec.Buffered) + " / " + strconv.Itoa(rec.BufferedHi) + " keys")},
		ui.Row{Label: "Awaiting the next write", Control: ui.Text(strconv.FormatInt(rec.Pending, 10) + " view" + plural(rec.Pending))},
		ui.Row{Label: "Last written", Control: ui.Text(last)},
	)
	if rec.Dropped > 0 {
		rows = append(rows, ui.Row{Label: "Dropped because the buffer was full",
			Hint:    "The buffer is bounded on purpose, since losing a view count is a rounding error and losing the site is an outage.",
			Control: ui.State("warn", strconv.FormatInt(rec.Dropped, 10)+" view"+plural(rec.Dropped))})
	}
	if rec.LastErr != "" {
		rows = append(rows, ui.Row{Label: "Last flush error", Control: `<code>` + ui.Text(rec.LastErr) + `</code>`})
	}
	return string(ui.Section("Write connection", "", ui.Join(
		`<p class="page-sub">`+ui.Brief("SQLite has one writer, so everything that writes shares a single connection. When something holds it, other writes queue, and this is where that shows up.")+`</p>`,
		ui.Rows(rows...),
		`<h3 class="settings-block-title">Recent write stalls `+ui.Tip("A stall is a period during which a caller was "+
			"waiting for the write connection continuously. Brief contention is normal on a busy install and is not "+
			"listed. Queued is the time callers spent waiting, summed across them, so it exceeds the stall's own length "+
			"whenever more than one was affected.")+`</h3>`,
		ui.HTML(stallHistoryTable(st.Recent, "No write stall has been recorded since this install last started.")),
	)))
}

// stallHistoryTable lists a pool's recent stalls, newest first.
func stallHistoryTable(recent []dbpkg.StallEvent, none string) string {
	rows := ""
	for i := len(recent) - 1; i >= 0; i-- {
		e := recent[i]
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
		rows = `<tr><td colspan="5" class="muted text-sm">` + html.EscapeString(none) + `</td></tr>`
	}
	return `<div class="table-wrap"><table class="table">
    <thead><tr><th>Started</th><th>Lasted</th><th>Callers delayed</th><th>Queued</th><th>Snapshot</th></tr></thead>
    <tbody>` + rows + `</tbody>
  </table></div>`
}
