// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_trail.go — the audit-trail section of /os/vayushield.
//
// vayushield_blocked and vayushield_challenges have been INSERTed on every event
// since they were introduced, and the only other SQL touching either table was a
// DELETE purge. Every event the shield has ever recorded was written and never
// read.
//
// ADR-0137 was right to remove the old scrolling per-IP list — a page of hashed
// addresses was stale the moment it rendered, because the live jail is in memory,
// and it was routinely misread as "these people are blocked right now". But it
// was removed rather than replaced, which left this page with no time dimension
// at all: every counter on it is cumulative-since-boot, so an operator can see
// that 4,000 requests were blocked and not whether that was last night or over
// six weeks.
//
// Aggregates only. Nothing here can identify a visitor.

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// shieldTrailHours reads the window from the request, clamped to something the
// retention policy can actually answer.
func shieldTrailHours(r *http.Request) int {
	if r == nil {
		return 24
	}
	n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("hours")))
	if err != nil {
		return 24
	}
	switch {
	case n <= 0:
		return 24
	case n > 24*90:
		return 24 * 90
	}
	return n
}

func (a *App) shieldTrailBody(r *http.Request) string {
	hours := shieldTrailHours(r)
	var b strings.Builder
	b.WriteString(vsRefresh("trail", "vs-body-trail", "?hours="+strconv.Itoa(hours)))

	store := a.vayuShield.BotStore()
	if store == nil {
		b.WriteString(`<p class="muted text-sm">The adaptive database is not available, so there is no recorded history to summarise.</p>`)
		return b.String()
	}
	tr, err := store.ReadTrail(r.Context(), hours, 8, config.Cfg.AnalyticsRetainDays)
	if err != nil {
		b.WriteString(`<p class="muted text-sm">Could not read the trail: ` + html.EscapeString(err.Error()) + `</p>`)
		return b.String()
	}

	// Window picker. Offering a window longer than retention would present an
	// empty stretch as "nothing happened" when the truth is "those rows were
	// deleted", so the choices stop at the retention boundary.
	b.WriteString(`<p class="muted text-sm">`)
	for _, opt := range []struct {
		h     int
		label string
	}{{24, "24 hours"}, {24 * 7, "7 days"}, {24 * 30, "30 days"}} {
		if tr.RetentionDays > 0 && opt.h > tr.RetentionDays*24 {
			continue
		}
		cls := "btn btn--sm"
		if opt.h == hours {
			cls = "btn btn--primary btn--sm"
		}
		b.WriteString(`<button type="button" class="` + cls + `" hx-get="/os/shield/section/trail?hours=` +
			strconv.Itoa(opt.h) + `" hx-target="#vs-body-trail" hx-swap="innerHTML">` + opt.label + `</button> `)
	}
	b.WriteString(`</p>`)

	if tr.TotalBlocks == 0 && tr.TotalChallenges == 0 {
		b.WriteString(`<p class="muted text-sm">No blocks or challenges recorded in this window. On a quiet site that is the expected result, not a fault.</p>`)
		b.WriteString(shieldTrailRetentionNote(tr))
		return b.String()
	}

	b.WriteString(`<div class="stat-grid">`)
	b.WriteString(shieldStat("Blocked", strconv.FormatInt(tr.TotalBlocks, 10), "requests refused outright"))
	b.WriteString(shieldStat("Challenged", strconv.FormatInt(tr.TotalChallenges, 10), "asked to prove a real browser"))
	if tr.TotalChallenges >= 10 {
		rate := float64(tr.TotalSolved) / float64(tr.TotalChallenges) * 100
		b.WriteString(shieldStat("Solved", fmt.Sprintf("%.0f%%", rate), "of challenges passed"))
	} else {
		// Below ten samples a percentage is noise dressed as a measurement, and an
		// operator who retunes thresholds on it is acting on nothing.
		b.WriteString(shieldStat("Solved", strconv.FormatInt(tr.TotalSolved, 10), "too few to state a rate"))
	}
	b.WriteString(`</div>`)

	b.WriteString(shieldTrailTable("Why requests were refused", tr.Reasons, tr.TotalBlocks))
	b.WriteString(shieldTrailTable("What was being hit", tr.Paths, tr.TotalBlocks))
	b.WriteString(shieldTrailTable("Where from", tr.Countries, tr.TotalBlocks))
	b.WriteString(shieldTrailHourly(tr))
	b.WriteString(shieldTrailRetentionNote(tr))
	return b.String()
}

// shieldStat renders one tile in the house style: label first, then value, then
// a hint. stat-card__hint is not a class the stylesheet defines, so the hint
// reuses the existing muted text-xs pair rather than depending on CSS that would
// have to be added for it.
func shieldStat(label, value, hint string) string {
	return `<div class="stat-card"><div class="stat-card__label">` + html.EscapeString(label) +
		`</div><div class="stat-card__value">` + html.EscapeString(value) +
		`</div><div class="muted text-xs">` + html.EscapeString(hint) + `</div></div>`
}

func shieldTrailTable(title string, rows []botdb.Count, total int64) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="card"><div class="settings-block-title">` + html.EscapeString(title) + `</div><table class="vs-trail"><tbody>`)
	for _, c := range rows {
		pct := ""
		if total > 0 {
			pct = fmt.Sprintf("%.0f%%", float64(c.Count)/float64(total)*100)
		}
		b.WriteString(`<tr><td>` + html.EscapeString(c.Key) + `</td><td class="vs-trail-n">` +
			strconv.FormatInt(c.Count, 10) + `</td><td class="vs-trail-n muted">` + pct + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// shieldTrailHourly renders the per-hour series as a text sparkline. A bar chart
// would need either inline styles, which assertCSPSafe forbids, or a charting
// library this page deliberately does not load.
func shieldTrailHourly(tr botdb.Trail) string {
	if len(tr.Hours) == 0 {
		return ""
	}
	peak := int64(0)
	for _, h := range tr.Hours {
		if n := h.Blocks + h.Challenges; n > peak {
			peak = n
		}
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var spark strings.Builder
	for _, h := range tr.Hours {
		n := h.Blocks + h.Challenges
		idx := 0
		if peak > 0 && n > 0 {
			idx = int(int64(len(blocks)-1) * n / peak)
		}
		spark.WriteRune(blocks[idx])
	}

	var b strings.Builder
	b.WriteString(`<div class="card"><div class="settings-block-title">Activity by hour</div>`)
	b.WriteString(`<p class="vs-trail-spark">` + html.EscapeString(spark.String()) + `</p>`)
	b.WriteString(`<p class="muted text-xs">` + html.EscapeString(tr.Hours[0].Hour) + ` → ` +
		html.EscapeString(tr.Hours[len(tr.Hours)-1].Hour) + ` UTC, peak ` +
		strconv.FormatInt(peak, 10) + ` events in an hour. Only hours with activity appear.</p>`)

	// The pass rate over time, which is the signal that tells an operator whether
	// the thresholds are catching bots or bothering readers — and the one the
	// panel has never been able to show, because every counter on it is
	// cumulative since boot.
	var rows strings.Builder
	for _, h := range tr.Hours {
		rate, ok := h.PassRate()
		if !ok {
			continue
		}
		rows.WriteString(`<tr><td>` + html.EscapeString(h.Hour) + `</td><td class="vs-trail-n">` +
			strconv.FormatInt(h.Challenges, 10) + `</td><td class="vs-trail-n">` +
			fmt.Sprintf("%.0f%%", rate*100) + `</td></tr>`)
	}
	if rows.Len() > 0 {
		b.WriteString(`<div class="settings-block-title">Challenge pass rate</div><table class="vs-trail"><tbody>` +
			rows.String() + `</tbody></table>`)
		b.WriteString(`<p class="muted text-xs">Hours with fewer than ten challenges are omitted: a percentage over three samples is noise, and retuning thresholds on it is worse than not looking.</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// shieldTrailRetentionNote states the retention boundary with the report. A "last
// 30 days" view over a table pruned at 14 is not a quiet approximation — it
// presents deleted history as an absence of events.
func shieldTrailRetentionNote(tr botdb.Trail) string {
	if tr.RetentionDays <= 0 {
		return `<p class="muted text-xs">Recorded events are kept indefinitely on this install.</p>`
	}
	return `<p class="muted text-xs">Recorded events are pruned after ` + strconv.Itoa(tr.RetentionDays) +
		` days, so nothing older than that can appear here — an empty stretch beyond that point means the rows were deleted, not that nothing happened.</p>`
}
