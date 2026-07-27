// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_vayukeep.go — the replication panel on Power & Maintenance (ADR-0145).
//
// It lives inside the existing operations page rather than claiming a sidebar
// entry of its own: replication is something an operator checks alongside
// maintenance mode and restarts, not a destination they navigate to.
//
// The panel's one job is to refuse to flatter. "Enabled" is a configuration
// value and worth nothing; what an operator needs to know is how much work they
// would lose right now and whether anything has actually read a generation back.
// So the headline figures are the recovery point and the last VERIFIED restore,
// and both can — and must be able to — read badly.

import (
	"context"
	"html"
	"net/http"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// humanAgo renders "how long ago" in the shortest honest form. A zero time is
// "never" — deliberately not "—", which reads as "not applicable" when it
// actually means "this has not happened".
func humanAgo(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " days ago"
}

// osVayuKeepStats is the at-a-glance strip, in the Monetization / API Keys idiom.
//
// The recovery-point tile is the one that matters and it is deliberately the one
// most likely to be red: it answers "how much work would I lose", which is the
// question a backup page exists to answer and the question "backups: on" dodges.
func osVayuKeepStats(st vayukeep.Status, now time.Time) string {
	tile := func(value, label, tone string) string {
		cls := "stat-card"
		if tone != "" {
			cls += " stat-card--" + tone
		}
		return `<div class="` + cls + `"><div class="stat-card__label">` + html.EscapeString(label) +
			`</div><div class="stat-card__value">` + html.EscapeString(value) + `</div></div>`
	}

	rpoVal, rpoTone := "never", "warn"
	if !st.NewestGen.IsZero() {
		rpoVal = humanAgo(st.NewestGen, now)
		rpoTone = ""
		if st.RPO(now) > 24*time.Hour {
			rpoTone = "warn"
		}
	}
	verVal, verTone := "never", "warn"
	if !st.LastDrill.IsZero() {
		verVal = humanAgo(st.LastDrill, now)
		if !st.LastDrillOK {
			verVal = "FAILED " + verVal
			verTone = "warn"
		} else {
			verTone = ""
		}
	}
	if !st.Enabled {
		rpoVal, verVal = "off", "off"
		rpoTone, verTone = "warn", "warn"
	}
	return `<div class="stat-grid">` +
		tile(rpoVal, "Recovery point", rpoTone) +
		tile(verVal, "Last verified restore", verTone) +
		tile(strconv.Itoa(st.Generations), "Generations kept", "") +
		tile(humanBytes(st.TotalBytes), "Replica size", "") +
		`</div>`
}

// osVayuKeepSection renders the whole replication block for the operations page.
func osVayuKeepSection(st vayukeep.Status, bootErr string, now time.Time) string {
	body := `<div class="section-head"><span class="section-head__title">Backup &amp; recovery</span>` +
		`<span class="section-head__hint">Encrypted generations of everything, continuously proven restorable</span></div>`

	// Not configured, or refused to start. Both are states an operator must be
	// able to tell apart at a glance, because one means "I have not set this up"
	// and the other means "I set this up and it is not running".
	if bootErr != "" {
		return body + `<div class="card">
  <div class="settings-block-title">Replication is not running <span class="badge badge--warn">Refused to start</span></div>
  <p class="text-sm muted">VayuKeep declined the configuration it was given, so <strong>nothing is being backed up</strong>. Your site is unaffected.</p>
  <p class="text-sm"><code>` + html.EscapeString(bootErr) + `</code></p>
</div>`
	}
	if !st.Enabled {
		return body + `<div class="card">
  <div class="settings-block-title">Replication is off <span class="badge">Not configured</span></div>
  <p class="text-sm muted">Point <code>VAYUKEEP_TARGET</code> at a directory VayuPress can write to — a second disk, a mounted volume, anything outside your data directory — and set <code>VAYU_BACKUP_PASSPHRASE</code>. VayuPress then keeps encrypted, consistent generations of your database, media, mailboxes and settings, and restores one on a schedule to prove they work.</p>
  <p class="text-sm muted">Until then your only copies are the ones you take by hand with <code>vayupress backup</code>.</p>
</div>`
	}

	// Running. Lead with the honest headline, then the detail.
	badge := `<span class="badge badge--ok">Verified</span>`
	headline := `Replication is running and the last restore drill passed.`
	switch {
	case st.Paused:
		badge = `<span class="badge badge--warn">Paused</span>`
		headline = `Replication is <strong>paused</strong>: ` + html.EscapeString(st.PauseWhy) + `. Nothing new is being backed up.`
	case st.LastDrill.IsZero():
		badge = `<span class="badge badge--warn">Unverified</span>`
		headline = `Generations are being written, but <strong>none has been restored yet</strong>. Until a drill passes, these are files rather than proven backups.`
	case !st.LastDrillOK:
		badge = `<span class="badge badge--warn">Restore FAILED</span>`
		headline = `The last restore drill <strong>failed</strong>: ` + html.EscapeString(st.LastDrillError) + `. Treat this as an outage of your recovery path.`
	case st.RPO(now) > 24*time.Hour:
		badge = `<span class="badge badge--warn">Stale</span>`
		headline = `The newest generation is ` + html.EscapeString(humanAgo(st.NewestGen, now)) + `. Check that writes are reaching the target.`
	}

	rows := detailRow("Target", st.Target) +
		detailRow("Newest generation", humanAgo(st.NewestGen, now)) +
		detailRow("Last successful write", humanAgo(st.LastSuccess, now)) +
		detailRow("Last restore drill", drillSummary(st, now)) +
		detailRow("Generations kept", strconv.Itoa(st.Generations)+" ("+humanBytes(st.TotalBytes)+")") +
		detailRow("Newest generation size", humanBytes(st.LastGenBytes))
	if st.LastError != "" {
		rows += detailRow("Last error", st.LastError)
	}

	detail := `<div class="cx-details">` + rows + `</div>
<div class="mt-3" style="display:flex;gap:.5rem;flex-wrap:wrap">
  <button type="button" class="btn btn--sm" data-vk-backup>Back up now</button>
  <button type="button" class="btn btn--sm btn--ghost" data-vk-drill>Run a restore drill</button>
  <span id="vk-status" role="status" aria-live="polite" class="text-xs muted"></span>
</div>
<p class="text-xs muted mt-2">A drill restores the newest generation into a temporary directory, opens the database inside it and runs <code>integrity_check</code>, then throws it away. It never touches your live data.</p>`

	return body + osVayuKeepStats(st, now) + `<div class="card">
  <div class="settings-block-title">Backup &amp; recovery ` + badge + `</div>
  <p class="text-sm muted">` + headline + `</p>
</div>` +
		monAcc(iconVCB, "Replication detail", "Target, cadence, generations and the last verified restore", "", false, detail)
}

// drillSummary renders the drill outcome as one honest phrase.
func drillSummary(st vayukeep.Status, now time.Time) string {
	if st.LastDrill.IsZero() {
		return "never — no generation has been restored yet"
	}
	if !st.LastDrillOK {
		return "FAILED " + humanAgo(st.LastDrill, now) + " — " + st.LastDrillError
	}
	s := "passed " + humanAgo(st.LastDrill, now)
	if st.LastDrillRows > 0 {
		s += " (" + strconv.FormatInt(st.LastDrillRows, 10) + " posts read back)"
	}
	return s
}

// detailRow is one label/value line inside the accordion, reusing the connector
// panel's markup so the two read identically.
func detailRow(label, value string) string {
	return `<div class="cx-detail"><span class="cx-cap">` + html.EscapeString(label) +
		`</span><span>` + html.EscapeString(value) + `</span></div>`
}

// ── Endpoints ────────────────────────────────────────────────────────────────

// handleOSVayuKeepBackup takes a generation on demand.
func (a *App) handleOSVayuKeepBackup(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.vayuKeep == nil || !config.Cfg.VayuKeepEnabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayukeep-off", "replication is not running", "")
		return
	}
	a.vayuKeep.TriggerNow()
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok":     true,
		"detail": "A generation was requested — it appears here once written.",
	})
}

// handleOSVayuKeepDrill runs a restore drill synchronously and reports the real
// outcome. It is deliberately synchronous: an operator who clicks this is asking
// "do my backups work", and the only useful answer is the one that made them wait.
func (a *App) handleOSVayuKeepDrill(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.vayuKeep == nil || !config.Cfg.VayuKeepEnabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayukeep-off", "replication is not running", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res := a.vayuKeep.Drill(ctx)
	detail := "Restore drill PASSED — the newest generation decrypted, unpacked and its database passed integrity_check."
	if res.Rows > 0 {
		detail += " " + strconv.FormatInt(res.Rows, 10) + " posts were read back."
	}
	if !res.OK {
		detail = "Restore drill FAILED — " + res.Err
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok":         res.OK,
		"detail":     detail,
		"generation": res.Generation,
		"ms":         res.Duration.Milliseconds(),
	})
}
