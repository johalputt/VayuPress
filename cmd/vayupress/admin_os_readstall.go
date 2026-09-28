// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_readstall.go — the read pool, on the page.
//
// On 2026-09-26 johal.in's console returned 502 for hours while the public site
// served. Crawler renders held every read connection; each console request
// waited out its 30-second deadline behind them. The write card said the writer
// was fine, which was true and beside the point. This is the card that would
// have said what was wrong: the read pool was full, since when, what it cost,
// and where the snapshot naming the query is.
//
// Beside it, the render ceiling's count: how many visitors were asked to retry
// because the uncached pages already had every render slot. A number that
// climbs means the ceiling is doing its job under load; the read pool staying
// clear beside it is the proof that it is enough.

import (
	"strconv"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/ui"
)

// readStallSection is the read pool on the Monitoring page, drawn as the write
// connection is: a stall in progress first, then what the pool has cost since
// boot, then its stalls. shed is the number of uncached page requests asked to
// retry.
func readStallSection(st dbpkg.StallState, shed int64) string {
	var rows []ui.Row
	if st.Stalled && st.Current != nil {
		c := st.Current
		rows = append(rows, ui.Row{Label: "Right now",
			Hint: "Every read connection is taken right now. Started " + c.Start.UTC().Format("15:04:05") +
				" UTC, " + shortDur(c.Duration) + " so far; " + strconv.FormatInt(c.Waits, 10) +
				" caller(s) have queued, for " + shortDur(c.Blocked) +
				" in total. Pages with a cache file are unaffected; anything that reads the database is waiting.",
			Control: ui.State("danger", "Full")})
	}
	if !st.Watching {
		rows = append(rows, ui.Row{Label: "Watching",
			Hint:    "The read-pool watchdog did not start, so nothing here is being measured. This is a fault in the install, not a quiet install.",
			Control: ui.State("danger", "Not being watched")})
	}
	stalls := "none since boot"
	if st.Total > 0 {
		stalls = strconv.FormatInt(st.Total, 10) + ", worst " + shortDur(st.Longest)
	}
	rows = append(rows,
		ui.Row{Label: "Stalls", Control: ui.Text(stalls)},
		ui.Row{Label: "Queued for a read connection", Hint: strconv.FormatInt(st.WaitCount, 10) + " callers waited, since boot",
			Control: ui.Text(shortDur(st.WaitDuration))},
		ui.Row{Label: "Pages asked to retry", Hint: "Uncached pages turned away at the render ceiling, since start. A climbing count with the pool clear beside it is the ceiling doing its job.",
			Control: ui.Text(strconv.FormatInt(shed, 10))},
	)
	return string(ui.Section("Read connections", "", ui.Join(
		`<p class="page-sub">`+ui.Text("Pages, the console and most queries read through a shared pool of "+
			strconv.Itoa(st.MaxOpen)+" connections. When every one is taken, requests queue.")+`</p>`,
		ui.Rows(rows...),
		`<h3 class="settings-block-title">Recent read stalls `+ui.Tip("A read stall is a period during which "+
			"every read connection stayed in use while a caller waited. Five seconds in, a snapshot of what each "+
			"connection was doing is saved; it names the query that held them.")+`</h3>`,
		ui.HTML(stallHistoryTable(st.Recent, "No read stall has been recorded since this install last started.")),
	)))
}
