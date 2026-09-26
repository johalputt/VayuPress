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
	"html"
	"strconv"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// readStallSection renders the read pool's tiles, its live state and its stall
// history. shed is the number of uncached page requests asked to retry.
func readStallSection(st dbpkg.StallState, shed int64) string {
	out := `<div class="section-head"><div class="section-head__title">Read connections</div>` +
		`<div class="section-head__hint">Pages, the console and most queries read through a shared pool of ` +
		strconv.Itoa(st.MaxOpen) + ` connections. When every one is taken, requests queue, and this is where ` +
		`that shows up.</div></div>`

	stallValue, stallLabel := "0", "none since boot"
	if st.Total > 0 {
		stallValue, stallLabel = strconv.FormatInt(st.Total, 10), "worst "+shortDur(st.Longest)
	}
	stallTile := monStat("Read stalls", stallValue, stallLabel)
	if st.Stalled && st.Current != nil {
		stallTile = `<div class="stat-card stat-card--warn">
  <div class="stat-card__top"><div class="stat-card__label">Read stalls</div></div>
  <div class="stat-card__value">happening now</div>
  <div class="stat-card__bottom"><span class="muted text-xs">` +
			html.EscapeString(shortDur(st.Current.Duration)+" so far") + `</span></div>
</div>`
	}
	out += `<div class="stat-grid mb-6">` +
		stallTile +
		monStat("Queued for a read connection", shortDur(st.WaitDuration),
			strconv.FormatInt(st.WaitCount, 10)+" callers waited, since boot") +
		monStat("Pages asked to retry", strconv.FormatInt(shed, 10),
			"uncached pages at the render ceiling, since start") +
		`</div>`

	if st.Stalled && st.Current != nil {
		c := st.Current
		out += `<div class="settings-callout"><strong>Every read connection is taken right now.</strong> ` +
			`<span class="text-sm muted">Started ` + html.EscapeString(c.Start.UTC().Format("15:04:05")) +
			` UTC, ` + html.EscapeString(shortDur(c.Duration)) + ` so far. ` +
			html.EscapeString(strconv.FormatInt(c.Waits, 10)) + ` caller(s) have queued, for ` +
			html.EscapeString(shortDur(c.Blocked)) + ` in total. Pages with a cache file are unaffected; ` +
			`anything that reads the database is waiting.</span></div>`
	}
	if !st.Watching {
		out += `<div class="settings-callout"><strong>Not being watched.</strong> ` +
			`<span class="text-sm muted">The read-pool watchdog did not start, so nothing on this card is ` +
			`being measured. This is a fault in the install, not a quiet install.</span></div>`
	}

	out += `<div class="card mb-6">
  <div class="settings-block-title">Recent read stalls</div>
  <p class="text-sm muted">A read stall is a period during which a caller waited for a read connection
  continuously, so every connection in the pool was in use. A snapshot of what each one was doing is saved
  five seconds in; it names the query that held them.</p>
  ` + stallHistoryTable(st.Recent, "No read stall has been recorded since this install last started.") + `
</div>`
	return out
}
