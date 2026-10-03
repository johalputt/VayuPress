// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_vayukeep.go — the Backup & Recovery console (/os/vayukeep, ADR-0145).
//
// A full page under Operations, laid out like Monetization: a status banner, an
// at-a-glance strip, then collapsible cards grouped by section. It is reached
// from the Operations hub, not from a sidebar entry of its own.
//
// The page has one rule it must never break: it does not flatter. "Enabled" is a
// configuration value and worth nothing. What an operator needs to know is how
// much work they would lose right now, and whether anything has ever actually
// read a backup back. Those are the two headline figures, and both can — and
// must be able to — read badly.

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	htmpl "html/template"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
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

// keepVerdict is the page's single source of truth for "how are we doing", so
// the state sentence and the bell can never disagree.
type keepVerdict struct {
	// Tone is danger only in the two states with no recovery path at all (a
	// refused start and a failed test restore), as against one that is merely
	// unproven or off; the bell raises it at the same severity.
	Tone   string
	Chip   string  // the bell's word for it
	State  string  // the page's sentence
	Detail ui.HTML // one line under it
}

func keepStatusVerdict(st vayukeep.Status, bootErr string, now time.Time) keepVerdict {
	at := func(target string) string { return `<code>` + html.EscapeString(target) + `</code>` }
	switch {
	case bootErr != "":
		return keepVerdict{"danger", "Refused to start", "Backups refused to start",
			"It declined the settings it was given, so nothing is being backed up automatically. Your site is unaffected."}
	case !st.Enabled:
		return keepVerdict{"warn", "Not set up", "Automatic backup is off",
			"Your only copies are the ones you take by hand."}
	case st.Paused:
		return keepVerdict{"warn", "Paused", "Backups are paused",
			ui.Text(strings.TrimSuffix(st.PauseWhy, ".") + ". Nothing new is being saved.")}
	case !st.LastDrill.IsZero() && !st.LastDrillOK:
		return keepVerdict{"danger", "Test restore FAILED", "The last test restore failed",
			ui.Text(strings.TrimSuffix(st.LastDrillError, ".") + ". Treat this as an outage of your recovery path.")}
	case st.NewestGen.IsZero():
		return keepVerdict{"warn", "Unverified", "No backup has been written yet",
			"The first is written within a few minutes of a change, or with Back up now."}
	case st.LastDrill.IsZero():
		return keepVerdict{"warn", "Unverified", "Backed up " + humanAgo(st.NewestGen, now) + ", but never restored",
			"None has been restored yet: until a test restore passes, these are files rather than proven backups."}
	case st.RPO(now) > 24*time.Hour:
		return keepVerdict{"warn", "Stale", "The newest backup is from " + humanAgo(st.NewestGen, now),
			ui.HTML("Check that writes are reaching " + at(st.Target) + ".")}
	}
	loss := strings.TrimSuffix(humanAgo(st.NewestGen, now), " ago")
	if loss == "just now" {
		loss = "a minute"
	}
	return keepVerdict{"ok", "Protected", "Backed up " + humanAgo(st.NewestGen, now) + ", and it restores",
		ui.HTML("Encrypted, to " + at(st.Target) + ". You would lose at most " + html.EscapeString(loss) + " of work.")}
}

// backupNotification is the verdict as a notification: none while backups are
// proven, otherwise at the verdict's own tone.
func backupNotification(st vayukeep.Status, bootErr string, now time.Time) (osNotification, bool) {
	v := keepStatusVerdict(st, bootErr, now)
	if v.Tone == "ok" {
		return osNotification{}, false
	}
	return osNotification{Title: "Backups", Detail: v.Chip, Href: "/os/vayukeep", Count: 1, Kind: "backup", Severity: v.Tone}, true
}

// detailRow is one label/value line, reusing the connector panel's markup.
func detailRow(label, value string) string {
	return `<div class="cx-detail"><span class="cx-detail__k">` + html.EscapeString(label) +
		`</span><span class="cx-detail__v">` + html.EscapeString(value) + `</span></div>`
}

// drillSummary renders the test-restore outcome as one honest phrase.
func drillSummary(st vayukeep.Status, now time.Time) string {
	if st.LastDrill.IsZero() {
		return "never — no backup has been restored yet"
	}
	if !st.LastDrillOK {
		return "FAILED " + humanAgo(st.LastDrill, now) + " — " + st.LastDrillError
	}
	s := "passed " + humanAgo(st.LastDrill, now)
	if st.LastDrillRows > 0 {
		s += " (" + strconv.FormatInt(st.LastDrillRows, 10) + " post" + plural(int(st.LastDrillRows)) + " read back)"
	}
	return s
}

// ── Cards ────────────────────────────────────────────────────────────────────

// keepSetupCard is the setup form. Everything happens here — choose a folder,
// set a passphrase, press the button. No file editing and no service restart:
// asking an operator to SSH in to enable backups is how installs end up with
// none.
func keepSetupCard(bootErr, currentTarget string, envManaged bool) string {
	problem := ""
	if bootErr != "" {
		problem = `<p class="text-sm"><strong>Backups could not start with the current settings:</strong></p>
<p class="text-sm"><code>` + html.EscapeString(bootErr) + `</code></p>
<p class="text-sm muted">Refusing is deliberate — a backup system that started anyway and quietly did nothing would be worse. Fix it below.</p>
<div class="section-divider"></div>`
	}
	if envManaged {
		return problem + `<p class="text-sm">This install is configured by environment variables (<code>VAYUKEEP_TARGET</code>), so the console does not override it. Change it where those are set, or clear the variable to manage backups from here.</p>`
	}
	target := currentTarget
	if target == "" {
		target = "/var/backups/vayupress"
	}
	return problem + `<p class="text-sm">Pick a folder and a passphrase. VayuPress does the rest — it starts immediately, no restart needed.</p>
<div class="field">
  <label class="field-label" for="vk-target">Where to keep the backups</label>
  <input id="vk-target" class="input" type="text" value="` + html.EscapeString(target) + `" placeholder="/var/backups/vayupress" spellcheck="false">
  <div class="settings-row-hint">Must be outside your site's data folder. The suggested path already works with no other changes.</div>
</div>
<div class="field">
  <label class="field-label" for="vk-pass">Passphrase</label>
  <div style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
    <input id="vk-pass" class="input" type="password" autocomplete="new-password" placeholder="At least 12 characters" spellcheck="false" style="flex:1 1 18rem">
    <button type="button" class="btn btn--sm" data-vk-gen>Generate one</button>
    <button type="button" class="btn btn--ghost btn--sm" data-vk-copy>Copy</button>
  </div>
  <div class="settings-row-hint">Make one up, or press <strong>Generate one</strong> for a strong random passphrase you can copy.</div>
  <div id="vk-pass-warn" class="settings-row-hint mt-2" hidden>
    <strong>Save this somewhere other than this server, now.</strong> VayuPress keeps an encrypted copy so backups can run unattended — but that copy lives on the machine your backups are protecting. If you lose the machine, that copy goes with it, and the backups become unreadable by any tool. A password manager, a note on your phone, a piece of paper. There is no reset.
  </div>
</div>
<div class="mt-3" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
  <button type="button" class="btn btn--primary btn--sm" data-vk-setup>Turn on automatic backup</button>
  <span id="vk-setup-status" role="status" aria-live="polite" class="text-xs muted"></span>
</div>
<div class="section-divider"></div>
<div class="cx-details">` +
		detailRow("What gets saved", "Your whole site — database, media, mailboxes and settings — encrypted, every few minutes while you are working.") +
		detailRow("For real disaster recovery", "Use a separate disk or mounted volume. A copy on the same disk survives a bad edit or a failed migration, but not losing the disk.") +
		detailRow("If a folder is refused", "The service runs sandboxed and may not be allowed to write there. VayuPress tests the folder before saving and tells you exactly what went wrong.") +
		`</div>`
}

// keepWhen names a restore point's moment the way a person says it: today,
// yesterday, or the date. In UTC, as every time on this page is.
func keepWhen(t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }
	switch d := day(now).Sub(day(t)); {
	case d == 0:
		return "Today, " + t.Format("15:04")
	case d == 24*time.Hour:
		return "Yesterday, " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan, 15:04")
	}
	return t.Format("2 Jan 2006, 15:04")
}

// keepPointState says what is known about one restore point's test restore.
// The engine records only the newest point that passed one (proven), so that
// point says so and the newer ones say they have not been tested; an older
// point may have passed once, but nothing recorded it, so it claims nothing.
func keepPointState(name, proven string) ui.HTML {
	switch {
	case name == proven:
		return ui.State("ok", "Test restore passed")
	case proven == "" || name > proven:
		return ui.State("neutral", "Not tested yet")
	}
	return ""
}

// keepPoints is the page's history: the restore points, newest first, each
// opening a sheet that checks, restores or deletes it.
func keepPoints(gens []vayukeep.Generation, proven string, st vayukeep.Status, now time.Time) (section, sheets ui.HTML) {
	if len(gens) == 0 {
		return ui.Section("Restore points", "", ui.Empty("archive", "No restore points yet",
			"One is written within a few minutes of your next change, or with Back up now.", "")), ""
	}
	var list, sh strings.Builder
	list.WriteString(`<ol class="vk-points">`)
	for i, g := range gens {
		id, when := "vk-pt-"+strconv.Itoa(i), keepWhen(g.Taken, now)
		state := keepPointState(g.Name, proven)
		list.WriteString(`<li class="vk-point"><span class="vk-point__when">` + html.EscapeString(when) +
			`</span><span class="vk-point__what">Restore point · ` + html.EscapeString(humanBytes(g.Bytes)) +
			`</span><span class="vk-point__state">` + string(state) + `</span><button type="button" class="btn btn--ghost btn--sm" data-sheet="` +
			id + `" aria-label="Restore the point of ` + html.EscapeString(when) + `">Restore…</button></li>`)
		sh.WriteString(string(keepPointSheet(id, g, state)))
	}
	list.WriteString(`</ol>`)
	hint := strconv.Itoa(len(gens)) + " kept · " + humanBytes(st.TotalBytes) + " · times in UTC"
	return ui.Section("Restore points", hint, ui.HTML(list.String())), ui.HTML(sh.String())
}

// keepPointSheet is one restore point: what it is, and the three things that
// can be done with it, each saying what it does before it is pressed.
func keepPointSheet(id string, g vayukeep.Generation, state ui.HTML) ui.HTML {
	esc := html.EscapeString(g.Name)
	facts := []ui.Fact{
		{Key: "Taken", Value: ui.Text(g.Taken.UTC().Format("2 Jan 2006, 15:04") + " UTC")},
		{Key: "Size", Value: ui.Text(humanBytes(g.Bytes))},
		{Key: "File", Value: ui.HTML(`<code>` + esc + `</code>`)},
	}
	if state != "" {
		facts = append(facts, ui.Fact{Key: "Test restore", Value: state})
	}
	return ui.Sheet(id, "Restore point", ui.Facts(facts...)+ui.HTML(`
<p class="text-sm muted mt-4"><strong>Check</strong> reads it end to end without writing anything. <strong>Restore</strong> puts your site back to this moment and restarts; your current database is copied aside first, so it is reversible. It restores the database (posts, pages, settings, members, comments and mailbox accounts). Uploaded files and stored mail are left alone, because swapping those under a running site is how a half-restored install happens; the command under <em>How this works</em> restores those too.</p>
<div class="vk-point-actions">
  <button type="button" class="btn btn--sm" data-vk-verify="`+esc+`">Check</button>
  <button type="button" class="btn btn--danger btn--sm" data-vk-restore="`+esc+`">Restore</button>
  <button type="button" class="btn btn--ghost btn--sm" data-vk-delete="`+esc+`">Delete</button>
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`))
}

// keepManualCard is the hand-operated half: download a copy to your own machine,
// or restore one you already have. Moved here from Update & Backup so every way
// of protecting and recovering this install lives on one page — an operator
// hunting for "backup" should never have to guess which of two pages has it.
func keepManualCard() string {
	return `<p class="text-sm">Download your whole site as one file, or restore one you downloaded earlier — including onto a different server.</p>
<div class="settings-block-title mt-3">Download a copy</div>
<p class="text-sm muted mb-2">A consistent, checksummed snapshot of the database and every setting, saved to your computer. No size limit: it is prepared at the pace the server can spare, then offered at the top of this page.</p>
<button type="button" class="btn btn--primary btn--sm" data-backup-export>Prepare a download</button>
<div class="section-divider mt-4"></div>
<div class="settings-block-title mt-4">Restore from a file</div>
<p class="text-sm muted mb-2">Your current database is copied aside first, then the service restarts to load the restored data. <strong>This replaces all current content and settings.</strong></p>
<div class="theme-actions">
  <input type="file" id="backup-file" class="input upd-file" accept=".gz,.tgz,application/gzip,application/x-gzip" data-backup-file>
  <button type="button" class="btn btn--danger btn--sm" data-backup-import>Restore from file</button>
  <span class="text-xs muted" data-backup-msg role="status" aria-live="polite"></span>
</div>
<div class="progress mt-3" data-restore-progress hidden><div class="progress__bar progress__bar--ok w-0" data-restore-bar></div></div>`
}

// keepEveryLabel names a cadence in minutes the way the schedule menu does.
func keepEveryLabel(m int) string {
	switch {
	case m >= 1440 && m%1440 == 0:
		if m == 1440 {
			return "once a day"
		}
		return "every " + strconv.Itoa(m/1440) + " days"
	case m >= 60 && m%60 == 0:
		if m == 60 {
			return "every hour"
		}
		return "every " + strconv.Itoa(m/60) + " hours"
	}
	return "every " + strconv.Itoa(m) + " minutes"
}

// keepRetentionCard sets how often backups are taken and how much history is
// kept, so both are controls rather than environment variables.
func keepRetentionCard(p keepPrefs) string {
	opts := ""
	listed := false
	for _, m := range keepEveryChoices {
		sel := ""
		if m == p.EveryMin {
			sel, listed = " selected", true
		}
		opts += `<option value="` + strconv.Itoa(m) + `"` + sel + `>` + keepEveryLabel(m) + `</option>`
	}
	if !listed { // a value set through VAYUKEEP_MIN_MINUTES stays selectable
		opts = `<option value="` + strconv.Itoa(p.EveryMin) + `" selected>` + keepEveryLabel(p.EveryMin) + `</option>` + opts
	}
	return `<div class="field">
  <label class="field-label" for="vk-every">Back up automatically</label>
  <select id="vk-every" class="input" style="max-width:14rem">` + opts + `</select>
  <span class="field-hint">Only when something changed since the last backup, so a quiet site does no work.</span>
</div>
<div class="field">
  <label class="field-label" for="vk-keep-n">Always keep at least this many</label>
  <input id="vk-keep-n" class="input" type="number" min="1" max="500" value="` + strconv.Itoa(p.RetainGens) + `" style="max-width:9rem">
</div>
<div class="field">
  <label class="field-label" for="vk-keep-d">And anything from the last (days)</label>
  <input id="vk-keep-d" class="input" type="number" min="1" max="3650" value="` + strconv.Itoa(p.RetainDays) + `" style="max-width:9rem">
</div>
<div class="mt-3" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
  <button type="button" class="btn btn--sm" data-vk-retention>Save</button>
  <button type="button" class="btn btn--ghost btn--sm" data-vk-prune>Clean up now</button>
  <span id="vk-retention-status" role="status" aria-live="polite" class="text-xs muted"></span>
</div>
<p class="text-xs muted mt-2">Old restore points are deleted automatically after each new backup, but only those older than a restore point that has passed a test restore: a run of backups that do not restore can never push out the last one that did. A point also survives while it is within <strong>either</strong> limit above. Deleting is permanent — the copy is gone, not moved to a bin.</p>`
}

// keepRestoreCard is the recovery runbook. It leads with the buttons, because an
// operator in trouble should not have to read a manual first; the commands stay
// for the one case the console genuinely cannot cover — a server that will not
// start, or recovering onto a different machine.
func keepRestoreCard(st vayukeep.Status) string {
	target := st.Target
	if target == "" {
		target = "/var/backups/vayupress"
	}
	t := html.EscapeString(target)
	return `<p class="text-sm"><strong>From this page.</strong> In <em>Restore points</em>, open the one you want with <strong>Restore…</strong>, press <strong>Check</strong> to confirm it is readable, then <strong>Restore</strong>. VayuPress copies your current database aside, puts the saved one in its place and restarts. Nothing to type but the confirmation.</p>
<div class="section-divider"></div>
<p class="text-sm"><strong>If this site will not start</strong>, or you are recovering onto a different machine, the console is not reachable — so the same job from a shell:</p>
<pre class="code-block"><code>vayupress restore -in ` + t + `/vk-YYYYMMDD-HHMMSS.vpbk -verify
sudo systemctl stop vayupress
vayupress restore -in ` + t + `/vk-YYYYMMDD-HHMMSS.vpbk -dest /var/lib/vayupress
sudo systemctl start vayupress</code></pre>
<p class="text-xs muted">That form also restores uploaded files and stored mail, which the one-click restore leaves alone.</p>
<div class="section-divider"></div>
<div class="cx-details">` +
		detailRow("Your old data is kept", "The restore moves your current data directory aside and prints where. Nothing is deleted until you delete it.") +
		detailRow("A failed restore is safe", "Files are unpacked into a staging folder and only moved into place once the whole archive verifies. A truncated or tampered file leaves your live site untouched.") +
		detailRow("Restoring to a moment in time", "Pick the newest restore point taken at or BEFORE the moment you want — never a later one, since that is the data you are trying to escape.") +
		detailRow("Restoring on a different server", "Copy the file across and run the same command. You need the passphrase; nothing else.") +
		`</div>`
}

// keepSpecCard states what the protection actually is, without overclaiming.
func keepSpecCard(st vayukeep.Status) string {
	rows := detailRow("Encryption", "AES-256-GCM. Each backup gets its own random key, sealed with an Argon2id key derived from your passphrase.") +
		detailRow("Tamper detection", "Every block is chained to the one before it and the file ends with an authenticated end marker, so a truncated, edited or reordered backup fails to open rather than restoring partially.") +
		detailRow("There is no unencrypted mode", "A copy carries member emails, mailbox contents and comment data. Making encryption optional would make the wrong thing easy.") +
		detailRow("Database consistency", "Copied through a single read transaction, so a backup taken while the site is live is one moment's data. The copy goes in steps, as fast as the server can spare, and slows down while visitors need the disk. The service never needs stopping.") +
		detailRow("What is included", "Database, media, VayuMail mailboxes, settings and public PGP material.") +
		detailRow("What is excluded", "Keystore secrets never leave the machine, so a stolen backup cannot decrypt your stored third-party credentials.") +
		detailRow("Effect on site speed", "None on any page request. Change detection is two file checks, and both backup and test restore stand aside while the site is busy.")
	if st.Enabled {
		rows += detailRow("Changing the passphrase", "Future backups are sealed with the new one. Keep the old passphrase for as long as you keep backups made with it.")
	}
	return `<div class="cx-details">` + rows + `</div>`
}

// keepScheduleCard explains when it runs and what it keeps, from the values in
// force rather than the environment defaults.
func keepScheduleCard(p keepPrefs) string {
	hrs := func(m int) string {
		if m >= 60 && m%60 == 0 {
			return strconv.Itoa(m/60) + " h"
		}
		return strconv.Itoa(m) + " min"
	}
	idle := max(config.Cfg.VayuKeepMaxMin, p.EveryMin)
	return `<div class="cx-details">` +
		detailRow("While you are writing", "A new restore point "+keepEveryLabel(p.EveryMin)+" at most, and only when something actually changed.") +
		detailRow("While nothing changes", "It backs off to "+hrs(idle)+", so an idle site does no work at all.") +
		detailRow("Test restore", "Automatically every "+hrs(config.Cfg.VayuKeepDrillMin)+", after every Back up now, and whenever you press the button.") +
		detailRow("Before an update", "A restore point is taken automatically before an in-place update, so you can roll back to the moment before it.") +
		detailRow("How many are kept", strconv.Itoa(p.RetainGens)+" restore points OR "+strconv.Itoa(p.RetainDays)+" days — whichever keeps more — and never anything newer than the last restore point that passed a test restore.") +
		detailRow("If the target breaks", "After repeated failures it stops trying and says so here, rather than retrying into a full disk. A failing backup never slows or blocks your site.") +
		`</div>
<p class="text-xs muted mt-2">The schedule and retention are set with <em>Back up automatically</em>, under Settings. <code>VAYUKEEP_DRILL_MINUTES</code> sets the test-restore interval; <code>VAYUKEEP_OFF=true</code> turns backup off.</p>`
}

// ── Page ─────────────────────────────────────────────────────────────────────

// keepPrefs are the console-set values the page shows: what is saved, not the
// environment default, so a form reloads with what the operator chose.
type keepPrefs struct {
	RetainGens, RetainDays, EveryMin int
}

// withDefaults fills anything unset from the environment configuration.
func (p keepPrefs) withDefaults() keepPrefs {
	if p.RetainGens <= 0 {
		p.RetainGens = config.Cfg.VayuKeepRetainGen
	}
	if p.RetainDays <= 0 {
		p.RetainDays = config.Cfg.BackupRetainDays
	}
	if p.EveryMin <= 0 {
		p.EveryMin = config.Cfg.VayuKeepMinMin
	}
	return p
}

// osVayuKeepBody builds the Backups page: a Status page (render 05) once
// automatic backup is on or has been asked for, the Setup page until then.
// proven is the newest restore point that passed a test restore.
func osVayuKeepBody(nonce string, st vayukeep.Status, bootErr string, gens []vayukeep.Generation, proven string, now time.Time, currentTarget string, envManaged bool, prefs keepPrefs, run string) string {
	if !st.Enabled && bootErr == "" && !envManaged {
		return run + keepSetupPage(currentTarget) + keepScripts(nonce)
	}
	prefs = prefs.withDefaults()
	v := keepStatusVerdict(st, bootErr, now)
	// Every control reports into this line, and a toast; the sheets report into
	// their own (data-sheet-status), next to the button that was pressed.
	actions := `<span id="vk-status" class="text-xs muted" role="status" aria-live="polite"></span>`
	manual := ui.Row{Label: "A copy on your computer", Hint: "Download the whole site as one file, or restore one you downloaded.",
		Control: `<button type="button" class="btn btn--sm" data-sheet="vk-manual-sheet">Download or restore…</button>`}
	sheets := ui.Sheet("vk-manual-sheet", "Download or restore a copy", ui.HTML(keepManualCard()))
	sections := []ui.HTML{ui.HTML(run)}
	if st.Enabled && bootErr == "" {
		actions += `<button type="button" class="btn" data-vk-drill>Test restore</button>` +
			`<button type="button" class="btn btn--primary" data-vk-backup>Back up now</button>`
		points, pointSheets := keepPoints(gens, proven, st, now)
		every := keepEveryLabel(prefs.EveryMin)
		details := []ui.Fact{
			{Key: "Backing up to", Value: ui.HTML(`<code>` + html.EscapeString(st.Target) + `</code>`)},
			{Key: "Newest backup", Value: ui.Text(humanAgo(st.NewestGen, now) + " · " + humanBytes(st.LastGenBytes))},
			{Key: "Last successful write", Value: ui.Text(humanAgo(st.LastSuccess, now))},
			{Key: "Last test restore", Value: ui.Text(drillSummary(st, now))},
		}
		if st.LastError != "" {
			details = append(details, ui.Fact{Key: "Last error", Value: ui.Text(st.LastError)})
		}
		sections = append(sections, points, ui.Section("Details", "", ui.Facts(details...)),
			ui.Section("Settings", "", ui.Rows(
				ui.Row{Label: "Back up automatically", Hint: strings.ToUpper(every[:1]) + every[1:] + " at most, when something changed. Keeps " +
					strconv.Itoa(prefs.RetainGens) + " restore points or " + strconv.Itoa(prefs.RetainDays) + " days, whichever keeps more.",
					Control: `<button type="button" class="btn btn--sm" data-sheet="vk-schedule-sheet">Change…</button>`},
				manual,
				ui.Row{Label: "Automatic backup", Hint: "On. Turning it off keeps the restore points you have.",
					Control: `<button type="button" class="btn btn--ghost btn--sm" data-vk-disable>Turn off</button>`})))
		sheets += ui.Sheet("vk-schedule-sheet", "Schedule", ui.HTML(keepRetentionCard(prefs))) + pointSheets
	} else {
		// Refused, or configured by the environment and not running: the page
		// says which, and the sheet holds the reason and what fixes it.
		label := "Set up automatic backup"
		if bootErr != "" {
			label = "Fix the settings"
		}
		actions += `<button type="button" class="btn btn--primary" data-sheet="vk-setup-sheet">` + label + `</button>`
		sections = append(sections, ui.Section("Settings", "", ui.Rows(manual)))
		sheets += ui.Sheet("vk-setup-sheet", label, ui.HTML(keepSetupCard(bootErr, currentTarget, envManaged)))
	}
	sections = append(sections, ui.Explain(ui.HTML(`<p class="text-sm"><strong>Back up now</strong> saves a restore point and test-restores it straight away; once it passes you can remove the older ones. <strong>Test restore</strong> takes your newest backup, unpacks it into a temporary folder, opens the database inside it and checks every page, then deletes it. It never touches your live site, and it is the only control here that proves a backup works.</p>
<h3 class="settings-block-title mt-4">How to restore</h3>`+keepRestoreCard(st)+`
<h3 class="settings-block-title mt-4">When it runs</h3>`+keepScheduleCard(prefs)+`
<h3 class="settings-block-title mt-4">Encryption and safety</h3>`+keepSpecCard(st))))
	return string(ui.Status(ui.StatusPage{Title: "Backups", Actions: ui.HTML(actions), Tone: v.Tone, State: v.State, Detail: v.Detail}, sections...)) +
		string(sheets) + keepScripts(nonce)
}

// keepSetupPage is the page until automatic backup is on: what it does, what it
// needs, and one button, with the setup form in a sheet. Downloading a copy or
// restoring one from a file does not wait on automatic backup, so it stays one
// tap away in a sheet of its own; a download being prepared shows above (run).
// A backup that failed to start (bootErr) or one the environment configures is
// not this page: those are the status page, which says what is wrong.
func keepSetupPage(currentTarget string) string {
	return string(ui.Setup(ui.SetupPage{
		Icon:  "archive",
		Title: "Backups aren't set up yet",
		What:  "Encrypted copies of your whole site (database, media, mailboxes and settings), taken every few minutes while you work and tested on a schedule, so you know they restore.",
		Steps: []ui.SetupStep{
			{Title: "A folder", Detail: "Outside the site's data folder. A separate disk or mounted volume also survives losing this one."},
			{Title: "A passphrase", Detail: "It encrypts every copy. Keep it somewhere other than this server: there is no reset."},
			{Title: "The first backup, and a test restore", Detail: "Both run as soon as it is on, with no restart."},
		},
		Action: `<button type="button" class="btn btn--primary" data-sheet="vk-setup-sheet">Set up automatic backup</button>`,
		More:   `<button type="button" class="btn btn--ghost" data-sheet="vk-manual-sheet">Download or restore a copy</button>`,
	})) +
		string(ui.Sheet("vk-setup-sheet", "Set up automatic backup", ui.HTML(keepSetupCard("", currentTarget, false)))) +
		string(ui.Sheet("vk-manual-sheet", "Download or restore a copy", ui.HTML(keepManualCard())))
}

// keepScripts wires every control on the page, in both its forms (set up, or
// not yet). Each looks its element up and does nothing without it.
func keepScripts(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
function toast(msg,kind){if(window.vpToast){window.vpToast(msg,kind);}}
// Every control reports the real outcome. Back up now and Test restore answer
// at once because a paced run can outlast any request, but the page then shows
// the run and its outcome (#vk-run): never an optimistic "started" that a later
// failure fails to correct.
function vkPost(url,payload,btn,working,outId,then){
  // A button in a sheet reports next to itself, where the operator is looking.
  var sheet=btn&&btn.closest('.sa-sheet');
  var out=(sheet&&sheet.querySelector('[data-sheet-status]'))||document.getElementById(outId||'vk-status');
  var label=btn?btn.textContent:'';
  if(btn){btn.disabled=true;btn.textContent=working;}
  if(out){out.textContent='Working…';}
  fetch(url,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})})
    .then(function(r){return r.json().catch(function(){return {ok:false,detail:'Unexpected response ('+r.status+').'};});})
    .then(function(d){
      if(out){out.textContent=d.detail||'';}
      toast(d.detail||'Done',d.ok?'success':'error');
      if(then){then(d);}
      if(d.restart){
        setTimeout(function(){
          fetch('/os/api/power/restart',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:'{}'})
            .finally(function(){setTimeout(function(){location.reload();},6000);});
        },800);
      } else if(d.reload){setTimeout(function(){location.reload();},1500);}
    })
    .catch(function(e){
      var m='Request failed: '+e;
      if(out){out.textContent=m;}
      toast(m,'error');
    })
    .finally(function(){ if(btn){btn.disabled=false;btn.textContent=label;} });
}
var b=document.querySelector('[data-vk-backup]');
// Back up now starts the backup and the page shows it running (#vk-run polls
// itself). Only a backup that passed its test restore offers the older points
// for removal, and the server refuses the removal unless a tested point exists.
if(b){b.addEventListener('click',function(){vkPost('/os/api/vayukeep/backup',{},b,'Starting…');});}
// The offer arrives in a polled fragment, after this script ran: delegated.
document.addEventListener('click',function(ev){
  var clr=ev.target.closest('[data-vk-clear-older]'); if(!clr){return;}
  var n=clr.getAttribute('data-vk-older'), g=clr.getAttribute('data-vk-generation');
  vpConfirm({title:'Remove older restore points',message:'Delete the '+n+' restore point'+(n==='1'?'':'s')+' older than '+g+'? '+g+' passed its test restore and is kept. This cannot be undone.',confirm:'Remove'},function(){
    vkPost('/os/api/vayukeep/clear-older',{},clr,'Removing…','vk-status');
  });
});
// A large site's download takes longer to prepare than a proxy waits for its
// first byte, so it is prepared as background work and offered in #vk-run.
var ex=document.querySelector('[data-backup-export]');
if(ex){ex.addEventListener('click',function(){vkPost('/os/api/backup/export/start',{},ex,'Starting…');});}
var d=document.querySelector('[data-vk-drill]');
if(d){d.addEventListener('click',function(){vkPost('/os/api/vayukeep/drill',{},d,'Restoring…');});}
Array.prototype.forEach.call(document.querySelectorAll('[data-vk-verify]'),function(el){
  el.addEventListener('click',function(){
    vkPost('/os/api/vayukeep/verify',{name:el.getAttribute('data-vk-verify')},el,'Checking…','vk-verify-status');
  });
});
// Generate a passphrase in the browser so it never needs a round trip before
// the operator has it. 20 characters from a 32-symbol unambiguous alphabet is
// ~100 bits — and readable enough to copy onto paper, which is the point.
var genBtn=document.querySelector('[data-vk-gen]');
if(genBtn){genBtn.addEventListener('click',function(){
  var alpha='abcdefghjkmnpqrstuvwxyz23456789'; // no i/l/o/0/1 — they get mistranscribed
  var buf=new Uint8Array(20); (window.crypto||window.msCrypto).getRandomValues(buf);
  var out=''; for(var i=0;i<buf.length;i++){ if(i&&i%5===0)out+='-'; out+=alpha[buf[i]%alpha.length]; }
  var f=document.getElementById('vk-pass');
  if(f){f.type='text';f.value=out;f.focus();f.select();}
  var warn=document.getElementById('vk-pass-warn'); if(warn){warn.hidden=false;}
});}
var copyBtn=document.querySelector('[data-vk-copy]');
if(copyBtn){copyBtn.addEventListener('click',function(){
  var f=document.getElementById('vk-pass');
  if(!f||!f.value){toast('Nothing to copy yet — generate or type a passphrase first.','error');return;}
  f.type='text';f.select();
  var done=function(){toast('Passphrase copied. Save it somewhere other than this server.','success');
    var warn=document.getElementById('vk-pass-warn'); if(warn){warn.hidden=false;}};
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(f.value).then(done,function(){document.execCommand('copy');done();});}
  else{document.execCommand('copy');done();}
});}
Array.prototype.forEach.call(document.querySelectorAll('[data-vk-delete]'),function(el){
  el.addEventListener('click',function(){
    var name=el.getAttribute('data-vk-delete');
    vpConfirm({title:'Delete restore point',message:'Delete '+name+' permanently? This copy is gone, not moved to a bin.',confirm:'Delete'},function(){
      vkPost('/os/api/vayukeep/delete',{name:name},el,'Deleting…','vk-verify-status');
    });
  });
});
var retBtn=document.querySelector('[data-vk-retention]');
if(retBtn){retBtn.addEventListener('click',function(){
  var n=document.getElementById('vk-keep-n'), d=document.getElementById('vk-keep-d'), ev=document.getElementById('vk-every');
  vkPost('/os/api/vayukeep/retention',{generations:parseInt(n?n.value:'0',10),days:parseInt(d?d.value:'0',10),every_minutes:parseInt(ev?ev.value:'0',10)},retBtn,'Saving…','vk-retention-status');
});}
var pruneBtn=document.querySelector('[data-vk-prune]');
if(pruneBtn){pruneBtn.addEventListener('click',function(){
  vpConfirm({title:'Prune restore points',message:'Delete every restore point that is outside both limits?',confirm:'Prune'},function(){
    vkPost('/os/api/vayukeep/prune',{},pruneBtn,'Cleaning…','vk-retention-status');
  });
});}
var setupBtn=document.querySelector('[data-vk-setup]');
if(setupBtn){setupBtn.addEventListener('click',function(){
  var t=document.getElementById('vk-target'), p=document.getElementById('vk-pass');
  vkPost('/os/api/vayukeep/setup',{target:t?t.value:'',passphrase:p?p.value:''},setupBtn,'Setting up…','vk-setup-status');
});}
var offBtn=document.querySelector('[data-vk-disable]');
if(offBtn){offBtn.addEventListener('click',function(){
  vpConfirm({title:'Turn automatic backup off?',message:'Your existing restore points are kept, but no new ones will be made.',confirm:'Turn off'},function(){
    vkPost('/os/api/vayukeep/disable',{},offBtn,'Turning off…');
  });
});}
Array.prototype.forEach.call(document.querySelectorAll('[data-vk-restore]'),function(el){
  el.addEventListener('click',function(){
    var name=el.getAttribute('data-vk-restore');
    // Typed confirmation, not a click. This replaces the live database.
    vpPrompt({title:'Restore '+name+'?',message:'This puts your site back to '+name+' and restarts. Your current database is copied aside first, so it is reversible.',label:'Type RESTORE to confirm',placeholder:'RESTORE',confirm:'Restore'},function(typed){
      if(typed!=='RESTORE')return;
      vkPost('/os/api/vayukeep/restore',{name:name,confirm:typed},el,'Restoring…','vk-verify-status');
    });
  });
});
})();
</script>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-update.js?v=` + assetVer("js/admin-os-update.js") + `"></script>`
}

// handleOSVayuKeep renders the Backup & Recovery console.
func (a *App) handleOSVayuKeep(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	st := a.vayuKeepStatus()
	var gens []vayukeep.Generation
	proven := ""
	if a.vayuKeep != nil {
		gens, _ = a.vayuKeep.List()
		proven = a.vayuKeep.ProvenGeneration()
	}
	envManaged := strings.TrimSpace(config.Cfg.VayuKeepTarget) != ""
	writeOSHTML(w, r, adminOSLayout(nonce, "Backups", "operations", cfg,
		htmpl.HTML(osVayuKeepBody(nonce, st, a.vayuKeepErr, gens, proven, time.Now().UTC(),
			a.resolveKeepTarget(r.Context()), envManaged, keepPrefs{
				RetainGens: a.keepInt(r.Context(), settings.KeyVayuKeepRetainGen, config.Cfg.VayuKeepRetainGen),
				RetainDays: a.keepInt(r.Context(), settings.KeyVayuKeepRetainDays, config.Cfg.BackupRetainDays),
				EveryMin:   a.keepEveryMin(r.Context()),
			}, keepRunHTML(&a.keepRun)))))
}

// ── Endpoints ────────────────────────────────────────────────────────────────

// keepGuard rejects the request unless an admin is asking and replication runs.
func (a *App) keepGuard(w http.ResponseWriter, r *http.Request) bool {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return false
	}
	if !a.keepRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayukeep-off", "automatic backup is not set up", "")
		return false
	}
	return true
}

// handleOSVayuKeepBackup takes a restore point on demand. It answers at once:
// the copy is paced by the server's load and a large site's can take far longer
// than any request may stay open, so the page shows it running (keepRunHTML)
// and then its outcome. That outcome is still the test restore's, never an
// optimistic "started" left standing.
func (a *App) handleOSVayuKeepBackup(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	if !a.keepRun.startManual("Sealing the restore point and testing that it restores", time.Now()) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": "A backup or test restore is already running; its progress is on this page."})
		return
	}
	actor := dbpkg.AuditActor(r)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), keepManualTimeout)
		defer cancel()
		res := a.vayuKeep.BackupNow(ctx)
		a.keepRun.finishManual(a.keepResultFor(res, actor, time.Now()))
	}()
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
		"detail": "Backing up. The copy goes as fast as the server can spare; its progress is on this page."})
}

// handleOSVayuKeepClearOlder deletes every restore point older than the newest
// one that passed a test restore. The engine refuses without such a point, so
// this can never leave the site with only backups that do not restore.
func (a *App) handleOSVayuKeepClearOlder(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	removed, err := a.vayuKeep.RemoveOlderThanProven()
	if errors.Is(err, vayukeep.ErrNotProven) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
			"detail": "Nothing was removed: no restore point has passed a test restore yet. Press Back up now or Test restore now first."})
		return
	}
	var freed int64
	for _, g := range removed {
		freed += g.Bytes
	}
	if len(removed) > 0 {
		dbpkg.AuditLog("vayukeep.clear-older", dbpkg.AuditActor(r), strconv.Itoa(len(removed))+" restore points",
			"older than "+a.vayuKeep.ProvenGeneration()+"; "+humanBytes(freed)+" freed")
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
		"detail": "Removed " + strconv.Itoa(len(removed)) + " older restore point" + plural(len(removed)) + " (" + humanBytes(freed) + " freed). Kept " + a.vayuKeep.ProvenGeneration() + ", which passed a test restore."})
}

// handleOSVayuKeepDrill starts a test restore and answers at once; the page
// shows it running and then its real outcome, as for Back up now.
func (a *App) handleOSVayuKeepDrill(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	if !a.keepRun.startManual("Testing that the newest restore point restores", time.Now()) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": "A backup or test restore is already running; its progress is on this page."})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), keepManualTimeout)
		defer cancel()
		res := a.vayuKeep.Drill(ctx)
		detail := "Test restore PASSED: your newest backup unpacked and its database checked out clean."
		if res.Rows > 0 {
			detail += " " + strconv.FormatInt(res.Rows, 10) + " post" + plural(int(res.Rows)) + " read back."
		}
		if !res.OK {
			detail = "Test restore FAILED: " + res.Err
		}
		a.keepRun.finishManual(keepResult{OK: res.OK, Detail: detail, Generation: res.Generation, At: time.Now()})
	}()
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
		"detail": "Testing a restore. It goes as fast as the server can spare; its progress is on this page."})
}

// handleOSVayuKeepVerify reads one named restore point end to end without
// writing anything.
func (a *App) handleOSVayuKeepVerify(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Resolve the name against our own listing rather than joining it onto a
	// path. The value arrives from the browser, so treating it as a filename
	// would make this a path-traversal primitive; matching it against generations
	// we already found means an unknown value is simply not found.
	gens, err := a.vayuKeep.List()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
		return
	}
	for _, g := range gens {
		if g.Name != body.Name {
			continue
		}
		if verr := a.vayuKeep.VerifyGeneration(g); verr != nil {
			writeJSON(w, r, http.StatusOK, map[string]any{
				"ok":     false,
				"detail": g.Name + " is NOT usable — " + verr.Error(),
			})
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{
			"ok":     true,
			"detail": g.Name + " checks out: the passphrase is right, every block authenticates, the chain is unbroken and the file is complete.",
		})
		return
	}
	writeAPIError(w, r, http.StatusNotFound, "not-found", "no restore point by that name", "")
}

// handleOSVayuKeepSetup turns automatic backup on from the console: it validates
// the folder, seals the passphrase, saves the setting and restarts the engine —
// no file editing, no service restart.
func (a *App) handleOSVayuKeepSetup(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil || a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings storage is not ready", "")
		return
	}
	var body struct {
		Target     string `json:"target"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	target := strings.TrimSpace(body.Target)
	pass := strings.TrimSpace(body.Passphrase)

	if target == "" {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Choose a folder to keep the backups in."})
		return
	}
	// Sanitise FIRST, and use only what comes back. Everything below — the
	// data-directory check, the write test, and what gets stored — operates on the
	// validated value, so no path derived from the request body ever reaches a
	// filesystem call unchecked.
	safeTarget, terr := sanitizeKeepTarget(target)
	if terr != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": terr.Error()})
		return
	}
	target = safeTarget
	// A short passphrase on the one artefact that leaves the machine is not a
	// preference to respect. Refuse it here rather than let an operator believe
	// they are protected.
	existing := a.resolveKeepPassphrase(r.Context())
	if pass == "" && existing == "" {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Set a passphrase. It is the only key to these backups — without it nobody, including you, can read them."})
		return
	}
	if pass != "" && len(pass) < 12 {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Use at least 12 characters. This one passphrase protects every copy of your whole site."})
		return
	}

	// Reject a target inside the data directory before saving it, so the console
	// gives the same answer the engine would — with the reason.
	dataDir := filepath.Dir(config.Cfg.DBPath)
	if abs, err := filepath.Abs(filepath.Clean(target)); err == nil {
		if rel, rerr := filepath.Rel(dataDir, abs); rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
				"detail": "That folder is inside your data directory. A copy on the disk it is meant to protect, replicating its own output, is not a backup — pick somewhere outside " + dataDir + "."})
			return
		}
	}
	if err := validateKeepTargetWritable(target); err != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
			"detail": "VayuPress cannot write to " + target + " — " + err.Error() + ". If this is a new location, add it to the service's ReadWritePaths."})
		return
	}

	if pass != "" {
		if _, err := a.secrets.Upsert(r.Context(), secrets.ProviderVayuKeep, "Backup passphrase", "", pass, true, false); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "secrets-error", err.Error(), "")
			return
		}
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{
		settings.KeyVayuKeepTarget:  target,
		settings.KeyVayuKeepEnabled: "true",
	}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayukeep.enable", dbpkg.AuditActor(r), target, "")

	if err := a.applyKeepConfig(r.Context()); err != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Saved, but backups could not start: " + err.Error()})
		return
	}
	// Take the first one immediately so the operator sees proof rather than a promise.
	a.vayuKeep.TriggerNow()
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "reload": true,
		"detail": "Automatic backup is on. The first copy is being written now — then press Test restore to prove it works.",
	})
}

// handleOSVayuKeepDisable turns automatic backup off. Existing copies are left
// exactly where they are: turning the schedule off is not consent to delete
// what it already saved.
func (a *App) handleOSVayuKeepDisable(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings storage is not ready", "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyVayuKeepEnabled: "false"}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayukeep.disable", dbpkg.AuditActor(r), "", "")
	_ = a.applyKeepConfig(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "reload": true,
		"detail": "Automatic backup is off. Your existing restore points are untouched.",
	})
}

// handleOSVayuKeepRestore stages a restore point's database and restarts, so a
// recovery is one click instead of an SSH session.
//
// It restores the DATABASE — posts, pages, settings, members, mailbox metadata,
// comments. Media files and mail message files on disk are not swapped from here,
// because doing that under a running process is how a half-restored install
// happens; the page says so rather than implying a completeness it cannot deliver.
//
// The mechanism is the one already proven for snapshot imports: stage the file
// beside the database, let the boot path swap it in atomically after taking a
// safety copy of the current one, then restart.
func (a *App) handleOSVayuKeepRestore(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	// Typed confirmation. This replaces the live database; a misclick must not be
	// enough on its own.
	if strings.TrimSpace(body.Confirm) != "RESTORE" {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Type RESTORE to confirm."})
		return
	}

	gens, err := a.vayuKeep.List()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
		return
	}
	var chosen *vayukeep.Generation
	for i := range gens {
		if gens[i].Name == body.Name {
			chosen = &gens[i]
			break
		}
	}
	if chosen == nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "no restore point by that name", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	staged, err := a.vayuKeepStageRestore(ctx, *chosen)
	if err != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Restore could not be prepared — " + err.Error() + ". Nothing was changed."})
		return
	}
	dbpkg.AuditLog("vayukeep.restore", dbpkg.AuditActor(r), chosen.Name, staged)
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "restart": true,
		"detail": "Restore prepared from " + chosen.Name + ". Restarting now — your current database is copied aside first, so this is itself reversible.",
	})
}

// handleOSVayuKeepDelete removes one restore point permanently.
//
// Like Check and Restore, the name is resolved against the engine's own listing
// rather than joined onto a path — this endpoint deletes files, so treating a
// browser-supplied string as a filename would be the most dangerous traversal
// primitive on the page.
func (a *App) handleOSVayuKeepDelete(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	g, err := a.deleteRestorePoint(body.Name)
	switch {
	case errors.Is(err, errOnlyRestorePoint):
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": onlyRestorePointRefusal})
	case errors.Is(err, errNoRestorePoint):
		writeAPIError(w, r, http.StatusNotFound, "not-found", err.Error(), "")
	case err != nil:
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
	default:
		dbpkg.AuditLog("vayukeep.delete", dbpkg.AuditActor(r), g.Name, humanBytes(g.Bytes))
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": g.Name + " deleted (" + humanBytes(g.Bytes) + " freed)."})
	}
}

var (
	errOnlyRestorePoint = errors.New("the only restore point")
	errNoRestorePoint   = errors.New("no restore point by that name")
)

// onlyRestorePointRefusal is what either page says when asked to delete the
// last restore point.
const onlyRestorePointRefusal = "That is your only restore point. Take a new one first if you really want to remove it."

// restorePointDeletes serialises deleteRestorePoint, so two deletes at once
// (Backups in one tab, Storage in another) cannot each see the other's file
// still there and together remove the last copy.
var restorePointDeletes sync.Mutex

// deleteRestorePoint removes one restore point by name, never the last one.
// It is the only way a restore point is deleted by hand: Backups and Storage
// both list the same files, and the button that would remove the last copy
// looks identical to the one that removes the ninth of ten, so the rule lives
// here rather than on either page.
func (a *App) deleteRestorePoint(name string) (vayukeep.Generation, error) {
	restorePointDeletes.Lock()
	defer restorePointDeletes.Unlock()
	gens, err := a.vayuKeep.List()
	if err != nil {
		return vayukeep.Generation{}, err
	}
	for _, g := range gens {
		if g.Name != name {
			continue
		}
		if len(gens) == 1 {
			return g, errOnlyRestorePoint
		}
		return g, a.vayuKeep.Delete(g)
	}
	return vayukeep.Generation{}, errNoRestorePoint
}

// handleOSVayuKeepRetention saves how much history to keep and applies it.
func (a *App) handleOSVayuKeepRetention(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings storage is not ready", "")
		return
	}
	var body struct {
		Generations int `json:"generations"`
		Days        int `json:"days"`
		Every       int `json:"every_minutes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	// Both bounds must be at least one. Zero would mean "keep nothing", which no
	// operator means and which the form's own minimums already disallow — this is
	// the guard for anything that does not come from the form.
	if body.Generations < 1 || body.Days < 1 {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Both limits must be at least 1."})
		return
	}
	vals := map[string]string{
		settings.KeyVayuKeepRetainGen:  strconv.Itoa(body.Generations),
		settings.KeyVayuKeepRetainDays: strconv.Itoa(body.Days),
	}
	if body.Every != 0 {
		// Only the cadences the menu offers: a one-minute schedule would take a
		// full backup on every edit.
		if !slices.Contains(keepEveryChoices, body.Every) {
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": "Choose a schedule from the list."})
			return
		}
		vals[settings.KeyVayuKeepEveryMin] = strconv.Itoa(body.Every)
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), vals); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayukeep.retention", dbpkg.AuditActor(r),
		strconv.Itoa(body.Generations)+" generations", strconv.Itoa(body.Days)+" days; backup "+keepEveryLabel(a.keepEveryMin(r.Context())))
	_ = a.applyKeepConfig(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
		"detail": "Saved — backing up " + keepEveryLabel(a.keepEveryMin(r.Context())) + " while the site changes, keeping at least " + strconv.Itoa(body.Generations) + " restore points and anything from the last " + strconv.Itoa(body.Days) + " days."})
}

// handleOSVayuKeepPrune applies retention immediately instead of at the next cycle.
func (a *App) handleOSVayuKeepPrune(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	before, _ := a.vayuKeep.List()
	if err := a.vayuKeep.Prune(); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
		return
	}
	after, _ := a.vayuKeep.List()
	removed := len(before) - len(after)
	detail := "Nothing to clean up — every restore point is still within your limits."
	if removed == 0 && a.vayuKeep.ProvenGeneration() == "" {
		detail = "Nothing removed: old restore points are only deleted once a newer one has passed a test restore, and none has since VayuPress started. Press Test restore now first."
	}
	if removed > 0 {
		detail = strconv.Itoa(removed) + " restore point" + plural(removed) + " removed."
		dbpkg.AuditLog("vayukeep.prune", dbpkg.AuditActor(r), strconv.Itoa(removed), "")
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": removed > 0, "detail": detail})
}
