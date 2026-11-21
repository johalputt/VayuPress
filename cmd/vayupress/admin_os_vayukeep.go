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
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// humanAgo renders "how long ago" in the shortest honest form, in English;
// humanAgoIn is the same in a page's language. A zero time is "never" —
// deliberately not "—", which reads as "not applicable" when it actually
// means "this has not happened".
func humanAgo(t, now time.Time) string { return humanAgoIn("en", t, now) }

// humanAgoIn is humanAgo in lang.
func humanAgoIn(lang string, t, now time.Time) string {
	if t.IsZero() {
		return i18n.T(lang, "never")
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return i18n.T(lang, "just now")
	case d < time.Hour:
		return i18n.T(lang, "{n} min ago", "n", int(d.Minutes()))
	case d < 48*time.Hour:
		return i18n.T(lang, "{n} h ago", "n", int(d.Hours()))
	}
	return i18n.T(lang, "{n} days ago", "n", int(d.Hours()/24))
}

// keepLossIn is how much work would be lost if the site went now, in lang:
// the age of the newest backup, said as a length of time ("a minute" when it
// is newer than one), not as when it happened.
func keepLossIn(lang string, t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return i18n.T(lang, "a minute")
	case d < time.Hour:
		return i18n.T(lang, "{n} min", "n", int(d.Minutes()))
	case d < 48*time.Hour:
		return i18n.T(lang, "{n} h", "n", int(d.Hours()))
	}
	return i18n.T(lang, "{n, plural, one {# day} other {# days}}", "n", int(d.Hours()/24))
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

// left names what a backup leaves out though the install keeps data there
// (leftOut); a backup that restores is still only part of the site then.
// State and Detail are in lang; Chip stays English, the bell's key
// (backupChipWord says it). A paused backup's reason and a failed test
// restore's error are the engine's own.
func keepStatusVerdict(lang string, st vayukeep.Status, bootErr string, now time.Time, left []string) keepVerdict {
	at := `<code>` + html.EscapeString(st.Target) + `</code>`
	ago := humanAgoIn(lang, st.NewestGen, now)
	switch {
	case bootErr != "":
		return keepVerdict{"danger", chipRefused, i18n.T(lang, "Backups refused to start"),
			ui.Text(i18n.T(lang, "It declined the settings it was given, so nothing is being backed up automatically. Your site is unaffected."))}
	case !st.Enabled:
		return keepVerdict{"warn", chipNotSetUp, i18n.T(lang, "Automatic backup is off"),
			ui.Text(i18n.T(lang, "Your only copies are the ones you take by hand."))}
	case st.Paused:
		return keepVerdict{"warn", chipPaused, i18n.T(lang, "Backups are paused"),
			ui.Text(i18n.T(lang, "{why}. Nothing new is being saved.", "why", strings.TrimSuffix(st.PauseWhy, ".")))}
	case !st.LastDrill.IsZero() && !st.LastDrillOK:
		return keepVerdict{"danger", chipDrillFailed, i18n.T(lang, "The last test restore failed"),
			ui.Text(i18n.T(lang, "{why}. Treat this as an outage of your recovery path.", "why", strings.TrimSuffix(st.LastDrillError, ".")))}
	case st.NewestGen.IsZero():
		return keepVerdict{"warn", chipUnverified, i18n.T(lang, "No backup has been written yet"),
			ui.Text(i18n.T(lang, "The first is written within a few minutes of a change, or with Back up now."))}
	case st.LastDrill.IsZero():
		return keepVerdict{"warn", chipUnverified, i18n.T(lang, "Backed up {when}, but never restored", "when", ago),
			ui.Text(i18n.T(lang, "None has been restored yet: until a test restore passes, these are files rather than proven backups."))}
	case st.RPO(now) > 24*time.Hour:
		return keepVerdict{"warn", chipStale, i18n.T(lang, "The newest backup is from {when}", "when", ago),
			ui.HTML(escFill(i18n.T(lang, "Check that writes are reaching {folder}.", "folder", htmlMark(0)), at))}
	}
	if len(left) > 0 {
		return keepVerdict{"warn", chipPartial, i18n.T(lang, "Backed up {when}, but not all of it", "when", ago),
			ui.Text(i18n.T(lang, "Not in it: {what}. What a backup holds, below, says why.", "what", strings.Join(placesWhatIn(lang, left), ", ")))}
	}
	return keepVerdict{"ok", chipProtected, i18n.T(lang, "Backed up {when}, and it restores", "when", ago),
		ui.HTML(escFill(i18n.T(lang, "Encrypted, to {folder}. You would lose at most {time} of work.", "folder", htmlMark(0), "time", keepLossIn(lang, st.NewestGen, now)), at))}
}

// backupNotification is the verdict as a notification, in lang: none while
// backups are proven, otherwise at the verdict's own tone.
func backupNotification(lang string, st vayukeep.Status, bootErr string, now time.Time, left []string) (osNotification, bool) {
	v := keepStatusVerdict("en", st, bootErr, now, left)
	if v.Tone == "ok" {
		return osNotification{}, false
	}
	return said(lang, osNotification{Href: "/os/vayukeep", Count: 1, Kind: "backup", Severity: v.Tone, State: true},
		func(l string) string { return i18n.T(l, "Backups") }, func(l string) string { return backupChipWord(l, v.Chip) }), true
}

// A verdict's Chip: its English word, the bell's key (fingerprint), which
// tests read too; backupChipWord says it in a page's language.
const (
	chipRefused     = "Refused to start"
	chipNotSetUp    = "Not set up"
	chipPaused      = "Paused"
	chipDrillFailed = "Test restore FAILED"
	chipUnverified  = "Unverified"
	chipStale       = "Stale"
	chipPartial     = "Partial"
	chipProtected   = "Protected"
)

// backupChipWord is a verdict's word for the bell in lang. The verdict keeps
// its English word, which the Backups page's tests and the bell's key read.
func backupChipWord(lang, chip string) string {
	switch chip {
	case chipRefused:
		return i18n.T(lang, "Refused to start")
	case chipNotSetUp:
		return i18n.T(lang, "Not set up")
	case chipPaused:
		return i18n.T(lang, "Paused")
	case chipDrillFailed:
		return i18n.T(lang, "Test restore FAILED")
	case chipUnverified:
		return i18n.T(lang, "Unverified")
	case chipStale:
		return i18n.T(lang, "Stale")
	case chipPartial:
		return i18n.T(lang, "Partial")
	}
	return chip
}

// detailRow is one label/value line, reusing the connector panel's markup.
func detailRow(label, value string) string {
	return `<div class="cx-detail"><span class="cx-detail__k">` + html.EscapeString(label) +
		`</span><span class="cx-detail__v">` + html.EscapeString(value) + `</span></div>`
}

// drillSummary renders the test-restore outcome as one honest phrase, in
// lang. A failure's error is the engine's own.
func drillSummary(lang string, st vayukeep.Status, now time.Time) string {
	if st.LastDrill.IsZero() {
		return i18n.T(lang, "never: no backup has been restored yet")
	}
	ago := humanAgoIn(lang, st.LastDrill, now)
	if !st.LastDrillOK {
		return i18n.T(lang, "FAILED {when}: {why}", "when", ago, "why", st.LastDrillError)
	}
	var read []string
	if st.LastDrillRows > 0 {
		read = append(read, i18n.T(lang, "{n, plural, one {# post} other {# posts}}", "n", st.LastDrillRows))
	}
	if st.LastDrillContents != "" {
		read = append(read, st.LastDrillContents)
	}
	if len(read) > 0 {
		return i18n.T(lang, "passed {when} ({what} read back)", "when", ago, "what", strings.Join(read, ", "))
	}
	return i18n.T(lang, "passed {when}", "when", ago)
}

// ── Cards ────────────────────────────────────────────────────────────────────

// placesWhatIn is each of whats (leftOut's names) in lang.
func placesWhatIn(lang string, whats []string) []string {
	out := make([]string, len(whats))
	for i, w := range whats {
		out[i] = placeWhatIn(lang, w)
	}
	return out
}

// keepStrong is a word the page's sentences set in bold: a control's name,
// in the page's language.
func keepStrong(s string) string { return `<strong>` + esc(s) + `</strong>` }

// keepSetupCard is the setup form. Everything happens here — choose a folder,
// set a passphrase, press the button. No file editing and no service restart:
// asking an operator to SSH in to enable backups is how installs end up with
// none.
func keepSetupCard(lang, bootErr, currentTarget string, envManaged bool) string {
	problem := ""
	if bootErr != "" {
		problem = `<p class="text-sm"><strong>` + esc(i18n.T(lang, "Backups could not start with the current settings:")) + `</strong></p>
<p class="text-sm"><code>` + html.EscapeString(bootErr) + `</code></p>
<p class="text-sm muted">` + esc(i18n.T(lang, "Refusing is deliberate: a backup system that started anyway and quietly did nothing would be worse. Fix it below.")) + `</p>
<div class="section-divider"></div>`
	}
	if envManaged {
		return problem + `<p class="text-sm">` + escFill(i18n.T(lang, "This install is configured by environment variables ({variable}), so the console does not override it. Change it where those are set, or clear the variable to manage backups from here.", "variable", htmlMark(0)), `<code>`+keepTargetVar+`</code>`) + `</p>`
	}
	target := currentTarget
	if target == "" {
		target = keepDefaultTarget
	}
	generate := i18n.T(lang, "Generate one")
	return problem + `<p class="text-sm">` + esc(i18n.T(lang, "Pick a folder and a passphrase. VayuPress does the rest: it starts immediately, no restart needed.")) + `</p>
<div class="field">
  <label class="field-label" for="vk-target">` + esc(i18n.T(lang, "Where to keep the backups")) + `</label>
  <input id="vk-target" class="input" type="text" value="` + html.EscapeString(target) + `" placeholder="` + keepDefaultTarget + `" spellcheck="false">
  <div class="settings-row-hint">` + esc(i18n.T(lang, "Must be outside your site's data folder. The suggested path already works with no other changes.")) + `</div>
</div>
<div class="field">
  <label class="field-label" for="vk-pass">` + esc(i18n.T(lang, "Passphrase")) + `</label>
  <div style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
    <input id="vk-pass" class="input" type="password" autocomplete="new-password" placeholder="` + esc(i18n.T(lang, "At least 12 characters")) + `" spellcheck="false" style="flex:1 1 18rem">
    <button type="button" class="btn btn--sm" data-vk-gen>` + esc(generate) + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-vk-copy>` + esc(i18n.T(lang, "Copy")) + `</button>
  </div>
  <div class="settings-row-hint">` + escFill(i18n.T(lang, "Make one up, or press {generate} for a strong random passphrase you can copy.", "generate", htmlMark(0)), keepStrong(generate)) + `</div>
  <div id="vk-pass-warn" class="settings-row-hint mt-2" hidden>
    <strong>` + esc(i18n.T(lang, "Save this somewhere other than this server, now.")) + `</strong> ` + esc(i18n.T(lang, "VayuPress keeps an encrypted copy so backups can run unattended, but that copy lives on the machine your backups are protecting. If you lose the machine, that copy goes with it, and the backups become unreadable by any tool. A password manager, a note on your phone, a piece of paper. There is no reset.")) + `
  </div>
</div>
<div class="mt-3" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
  <button type="button" class="btn btn--primary btn--sm" data-vk-setup>` + esc(i18n.T(lang, "Turn on automatic backup")) + `</button>
  <span id="vk-setup-status" role="status" aria-live="polite" class="text-xs muted"></span>
</div>
<div class="section-divider"></div>
<div class="cx-details">` +
		detailRow(i18n.T(lang, "What gets saved"), i18n.T(lang, "Your whole site (database, media, mailboxes and settings), encrypted, every few minutes while you are working.")) +
		detailRow(i18n.T(lang, "For real disaster recovery"), i18n.T(lang, "Use a separate disk or mounted volume. A copy on the same disk survives a bad edit or a failed migration, but not losing the disk.")) +
		detailRow(i18n.T(lang, "If a folder is refused"), i18n.T(lang, "The service runs sandboxed and may not be allowed to write there. VayuPress tests the folder before saving and tells you exactly what went wrong.")) +
		`</div>`
}

// The code the page shows, the same in every language: the folder setup
// suggests, the variables that configure backups, and the restore commands.
const (
	keepDefaultTarget = "/var/backups/vayupress"
	keepTargetVar     = "VAYUKEEP_TARGET"
	keepDrillVar      = "VAYUKEEP_DRILL_MINUTES"
	keepOffVar        = "VAYUKEEP_OFF=true"
)

// keepRestoreCommands is the shell restore for backups kept in target (escaped).
func keepRestoreCommands(target string) string {
	return `vayupress restore -in ` + target + `/vk-YYYYMMDD-HHMMSS.vpbk -verify
sudo systemctl stop vayupress
vayupress restore -in ` + target + `/vk-YYYYMMDD-HHMMSS.vpbk -dest /var/lib/vayupress
sudo systemctl start vayupress`
}

// keepSecretLabel is the passphrase's name in the keystore, which the API
// keys page lists.
const keepSecretLabel = "Backup passphrase"

// keepWhen names a restore point's moment the way a person says it: today,
// yesterday, or the date, in lang. In UTC, as every time on this page is.
func keepWhen(lang string, t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }
	clock := t.Format("15:04")
	switch d := day(now).Sub(day(t)); {
	case d == 0:
		return i18n.T(lang, "Today, {time}", "time", clock)
	case d == 24*time.Hour:
		return i18n.T(lang, "Yesterday, {time}", "time", clock)
	case t.Year() == now.Year():
		return i18n.T(lang, "{date}, {time}", "date", i18n.Date(lang, t, i18n.DayMonthShort), "time", clock)
	}
	return i18n.Date(lang, t, i18n.DayMonthYearShortAt)
}

// keepPointState says what is known about one restore point's test restore.
// The engine records only the newest point that passed one (proven), so that
// point says so and the newer ones say they have not been tested; an older
// point may have passed once, but nothing recorded it, so it claims nothing.
func keepPointState(lang, name, proven string) ui.HTML {
	switch {
	case name == proven:
		return ui.State("ok", i18n.T(lang, "Test restore passed"))
	case proven == "" || name > proven:
		return ui.State("neutral", i18n.T(lang, "Not tested yet"))
	}
	return ""
}

// keepPoints is the page's history: the restore points, newest first, each
// opening a sheet that checks, restores or deletes it.
func keepPoints(lang string, gens []vayukeep.Generation, proven string, st vayukeep.Status, now time.Time) (section, sheets ui.HTML) {
	title := i18n.T(lang, "Restore points")
	if len(gens) == 0 {
		return ui.Section(title, "", ui.Empty("archive", i18n.T(lang, "No restore points yet"),
			i18n.T(lang, "One is written within a few minutes of your next change, or with Back up now."), "")), ""
	}
	var list, sh strings.Builder
	list.WriteString(`<ol class="vk-points">`)
	for i, g := range gens {
		id, when := "vk-pt-"+strconv.Itoa(i), keepWhen(lang, g.Taken, now)
		state := keepPointState(lang, g.Name, proven)
		list.WriteString(`<li class="vk-point"><span class="vk-point__when">` + html.EscapeString(when) +
			`</span><span class="vk-point__what">` + esc(i18n.T(lang, "Restore point · {size}", "size", humanBytes(g.Bytes))) +
			`</span><span class="vk-point__state">` + string(state) + `</span><button type="button" class="btn btn--ghost btn--sm" data-sheet="` +
			id + `" aria-label="` + esc(i18n.T(lang, "Restore the point of {when}", "when", when)) + `">` + esc(i18n.T(lang, "Restore…")) + `</button></li>`)
		sh.WriteString(string(keepPointSheet(lang, id, g, state)))
	}
	list.WriteString(`</ol>`)
	hint := i18n.T(lang, "{n} kept · {size} · times in UTC", "n", len(gens), "size", humanBytes(st.TotalBytes))
	return ui.Section(title, hint, ui.HTML(list.String())), ui.HTML(sh.String())
}

// keepPointSheet is one restore point: what it is, and the three things that
// can be done with it, each saying what it does before it is pressed.
func keepPointSheet(lang, id string, g vayukeep.Generation, state ui.HTML) ui.HTML {
	name := html.EscapeString(g.Name)
	facts := []ui.Fact{
		{Key: i18n.T(lang, "Taken"), Value: ui.Text(i18n.Date(lang, g.Taken.UTC(), i18n.DayMonthYearShortAt) + " UTC")},
		{Key: i18n.T(lang, "Size"), Value: ui.Text(humanBytes(g.Bytes))},
		{Key: i18n.T(lang, "File"), Value: ui.HTML(`<code>` + name + `</code>`)},
	}
	if state != "" {
		facts = append(facts, ui.Fact{Key: i18n.T(lang, "Test restore"), Value: state})
	}
	check, restore := i18n.T(lang, "Check"), i18n.T(lang, "Restore")
	return ui.SheetClosing(id, i18n.T(lang, "Restore point"), i18n.T(lang, "Close"), ui.Facts(facts...)+ui.HTML(`
<p class="text-sm muted mt-4">`+escFill(i18n.T(lang, "{check} reads it end to end without writing anything. {restore} puts your site back to this moment and restarts; your current database is copied aside first, so it is reversible. It restores the database (posts, pages, settings, members, comments and mailbox accounts). Uploaded files and stored mail are left alone, because swapping those under a running site is how a half-restored install happens; the command under {how} restores those too.",
		"check", htmlMark(0), "restore", htmlMark(1), "how", htmlMark(2)), keepStrong(check), keepStrong(restore), `<em>`+esc(i18n.T(lang, "How this works"))+`</em>`)+`</p>
<div class="vk-point-actions">
  <button type="button" class="btn btn--sm" data-vk-verify="`+name+`">`+esc(check)+`</button>
  <button type="button" class="btn btn--danger btn--sm" data-vk-restore="`+name+`">`+esc(restore)+`</button>
  <button type="button" class="btn btn--ghost btn--sm" data-vk-delete="`+name+`">`+esc(i18n.T(lang, "Delete"))+`</button>
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`))
}

// keepManualCard is the hand-operated half: download a copy to your own machine,
// or restore one you already have. Moved here from Update & Backup so every way
// of protecting and recovering this install lives on one page — an operator
// hunting for "backup" should never have to guess which of two pages has it.
func keepManualCard(lang string) string {
	return `<p class="text-sm">` + esc(i18n.T(lang, "Download your whole site as one file, or restore one you downloaded earlier, including onto a different server.")) + `</p>
<div class="settings-block-title mt-3">` + esc(i18n.T(lang, "Download a copy")) + `</div>
<p class="text-sm muted mb-2">` + esc(i18n.T(lang, "A consistent, checksummed snapshot of the database and every setting, saved to your computer. No size limit: it is prepared at the pace the server can spare, then offered at the top of this page.")) + `</p>
<button type="button" class="btn btn--primary btn--sm" data-backup-export>` + esc(i18n.T(lang, "Prepare a download")) + `</button>
<div class="section-divider mt-4"></div>
<div class="settings-block-title mt-4">` + esc(i18n.T(lang, "Restore from a file")) + `</div>
<p class="text-sm muted mb-2">` + esc(i18n.T(lang, "Your current database is copied aside first, then the service restarts to load the restored data.")) + ` <strong>` + esc(i18n.T(lang, "This replaces all current content and settings.")) + `</strong></p>
<div class="theme-actions">
  <input type="file" id="backup-file" class="input upd-file" accept=".gz,.tgz,application/gzip,application/x-gzip" data-backup-file>
  <button type="button" class="btn btn--danger btn--sm" data-backup-import>` + esc(i18n.T(lang, "Restore from file")) + `</button>
  <span class="text-xs muted" data-backup-msg role="status" aria-live="polite"></span>
</div>
<div class="progress mt-3" data-restore-progress hidden><div class="progress__bar progress__bar--ok w-0" data-restore-bar></div></div>`
}

// keepEveryLabel names a cadence in minutes the way the schedule menu does,
// in English, for the audit record; keepEveryLabelIn says it in a page's
// language.
func keepEveryLabel(m int) string { return keepEveryLabelIn("en", m) }

// keepEveryLabelIn is keepEveryLabel in lang.
func keepEveryLabelIn(lang string, m int) string {
	switch {
	case m >= 1440 && m%1440 == 0:
		if m == 1440 {
			return i18n.T(lang, "once a day")
		}
		return i18n.T(lang, "every {n} days", "n", m/1440)
	case m >= 60 && m%60 == 0:
		if m == 60 {
			return i18n.T(lang, "every hour")
		}
		return i18n.T(lang, "every {n} hours", "n", m/60)
	}
	return i18n.T(lang, "every {n} minutes", "n", m)
}

// keepRetentionCard sets how often backups are taken and how much history is
// kept, so both are controls rather than environment variables.
func keepRetentionCard(lang string, p keepPrefs) string {
	opts := ""
	listed := false
	for _, m := range keepEveryChoices {
		sel := ""
		if m == p.EveryMin {
			sel, listed = " selected", true
		}
		opts += `<option value="` + strconv.Itoa(m) + `"` + sel + `>` + esc(keepEveryLabelIn(lang, m)) + `</option>`
	}
	if !listed { // a value set through VAYUKEEP_MIN_MINUTES stays selectable
		opts = `<option value="` + strconv.Itoa(p.EveryMin) + `" selected>` + esc(keepEveryLabelIn(lang, p.EveryMin)) + `</option>` + opts
	}
	return `<div class="field">
  <label class="field-label" for="vk-every">` + esc(i18n.T(lang, "Back up automatically")) + `</label>
  <select id="vk-every" class="input" style="max-width:14rem">` + opts + `</select>
  <span class="field-hint">` + esc(i18n.T(lang, "Only when something changed since the last backup, so a quiet site does no work.")) + `</span>
</div>
<div class="field">
  <label class="field-label" for="vk-keep-n">` + esc(i18n.T(lang, "Always keep at least this many")) + `</label>
  <input id="vk-keep-n" class="input" type="number" min="1" max="500" value="` + strconv.Itoa(p.RetainGens) + `" style="max-width:9rem">
</div>
<div class="field">
  <label class="field-label" for="vk-keep-d">` + esc(i18n.T(lang, "And anything from the last (days)")) + `</label>
  <input id="vk-keep-d" class="input" type="number" min="1" max="3650" value="` + strconv.Itoa(p.RetainDays) + `" style="max-width:9rem">
</div>
<div class="mt-3" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
  <button type="button" class="btn btn--sm" data-vk-retention>` + esc(i18n.T(lang, "Save")) + `</button>
  <button type="button" class="btn btn--ghost btn--sm" data-vk-prune>` + esc(i18n.T(lang, "Clean up now")) + `</button>
  <span id="vk-retention-status" role="status" aria-live="polite" class="text-xs muted"></span>
</div>
<p class="text-xs muted mt-2">` + escFill(i18n.T(lang, "Old restore points are deleted automatically after each new backup, but only those older than a restore point that has passed a test restore: a run of backups that do not restore can never push out the last one that did. A point also survives while it is within {either} limit above. Deleting is permanent: the copy is gone, not moved to a bin.", "either", htmlMark(0)), keepStrong(i18n.T(lang, "either"))) + `</p>`
}

// keepRestoreCard is the recovery runbook. It leads with the buttons, because an
// operator in trouble should not have to read a manual first; the commands stay
// for the one case the console genuinely cannot cover — a server that will not
// start, or recovering onto a different machine.
func keepRestoreCard(lang string, st vayukeep.Status) string {
	target := st.Target
	if target == "" {
		target = keepDefaultTarget
	}
	t := html.EscapeString(target)
	return `<p class="text-sm">` + escFill(i18n.T(lang, "{from} In {points}, open the one you want with {open}, press {check} to confirm it is readable, then {restore}. VayuPress copies your current database aside, puts the saved one in its place and restarts. Nothing to type but the confirmation.",
		"from", htmlMark(0), "points", htmlMark(1), "open", htmlMark(2), "check", htmlMark(3), "restore", htmlMark(4)),
		keepStrong(i18n.T(lang, "From this page.")), `<em>`+esc(i18n.T(lang, "Restore points"))+`</em>`, keepStrong(i18n.T(lang, "Restore…")),
		keepStrong(i18n.T(lang, "Check")), keepStrong(i18n.T(lang, "Restore"))) + `</p>
<div class="section-divider"></div>
<p class="text-sm">` + escFill(i18n.T(lang, "{start}, or you are recovering onto a different machine, the console is not reachable, so here is the same job from a shell:", "start", htmlMark(0)), keepStrong(i18n.T(lang, "If this site will not start"))) + `</p>
<pre class="code-block"><code>` + keepRestoreCommands(t) + `</code></pre>
<p class="text-xs muted">` + esc(i18n.T(lang, "That form also restores uploaded files and stored mail, which the one-click restore leaves alone.")) + `</p>
<div class="section-divider"></div>
<div class="cx-details">` +
		detailRow(i18n.T(lang, "Your old data is kept"), i18n.T(lang, "The restore moves your current data directory aside and prints where. Nothing is deleted until you delete it.")) +
		detailRow(i18n.T(lang, "A failed restore is safe"), i18n.T(lang, "Files are unpacked into a staging folder and only moved into place once the whole archive verifies. A truncated or tampered file leaves your live site untouched.")) +
		detailRow(i18n.T(lang, "Restoring to a moment in time"), i18n.T(lang, "Pick the newest restore point taken at or BEFORE the moment you want, never a later one, since that is the data you are trying to escape.")) +
		detailRow(i18n.T(lang, "Restoring on a different server"), i18n.T(lang, "Copy the file across and run the same command. You need the passphrase; nothing else.")) +
		`</div>`
}

// keepSpecCard states what the protection actually is, without overclaiming.
func keepSpecCard(lang string, st vayukeep.Status) string {
	rows := detailRow(i18n.T(lang, "Encryption"), i18n.T(lang, "AES-256-GCM. Each backup gets its own random key, sealed with an Argon2id key derived from your passphrase.")) +
		detailRow(i18n.T(lang, "Tamper detection"), i18n.T(lang, "Every block is chained to the one before it and the file ends with an authenticated end marker, so a truncated, edited or reordered backup fails to open rather than restoring partially.")) +
		detailRow(i18n.T(lang, "There is no unencrypted mode"), i18n.T(lang, "A copy carries member emails, mailbox contents and comment data. Making encryption optional would make the wrong thing easy.")) +
		detailRow(i18n.T(lang, "Database consistency"), i18n.T(lang, "Copied through a single read transaction, so a backup taken while the site is live is one moment's data. The copy goes in steps, as fast as the server can spare, and slows down while visitors need the disk. The service never needs stopping.")) +
		detailRow(i18n.T(lang, "What is included"), i18n.T(lang, "Database, media, VayuMail mailboxes, settings and public PGP material.")) +
		detailRow(i18n.T(lang, "What is excluded"), i18n.T(lang, "Keystore secrets never leave the machine, so a stolen backup cannot decrypt your stored third-party credentials.")) +
		detailRow(i18n.T(lang, "Effect on site speed"), i18n.T(lang, "None on any page request. Change detection is two file checks, and both backup and test restore stand aside while the site is busy."))
	if st.Enabled {
		rows += detailRow(i18n.T(lang, "Changing the passphrase"), i18n.T(lang, "Future backups are sealed with the new one. Keep the old passphrase for as long as you keep backups made with it."))
	}
	return `<div class="cx-details">` + rows + `</div>`
}

// keepScheduleCard explains when it runs and what it keeps, from the values in
// force rather than the environment defaults.
func keepScheduleCard(lang string, p keepPrefs) string {
	hrs := func(m int) string {
		if m >= 60 && m%60 == 0 {
			return i18n.T(lang, "{n} h", "n", m/60)
		}
		return i18n.T(lang, "{n} min", "n", m)
	}
	idle := max(config.Cfg.VayuKeepMaxMin, p.EveryMin)
	return `<div class="cx-details">` +
		detailRow(i18n.T(lang, "While you are writing"), i18n.T(lang, "A new restore point {every} at most, and only when something actually changed.", "every", keepEveryLabelIn(lang, p.EveryMin))) +
		detailRow(i18n.T(lang, "While nothing changes"), i18n.T(lang, "It backs off to {time}, so an idle site does no work at all.", "time", hrs(idle))) +
		detailRow(i18n.T(lang, "Test restore"), i18n.T(lang, "Automatically every {time}, after every Back up now, and whenever you press the button.", "time", hrs(config.Cfg.VayuKeepDrillMin))) +
		detailRow(i18n.T(lang, "Before an update"), i18n.T(lang, "A restore point is taken automatically before an in-place update, so you can roll back to the moment before it.")) +
		detailRow(i18n.T(lang, "How many are kept"), i18n.T(lang, "{points} restore points OR {days} days, whichever keeps more, and never anything newer than the last restore point that passed a test restore.", "points", p.RetainGens, "days", p.RetainDays)) +
		detailRow(i18n.T(lang, "If the target breaks"), i18n.T(lang, "After repeated failures it stops trying and says so here, rather than retrying into a full disk. A failing backup never slows or blocks your site.")) +
		`</div>
<p class="text-xs muted mt-2">` + escFill(i18n.T(lang, "The schedule and retention are set with {setting}, under Settings. {drill} sets the test-restore interval; {off} turns backup off.", "setting", htmlMark(0), "drill", htmlMark(1), "off", htmlMark(2)),
		`<em>`+esc(i18n.T(lang, "Back up automatically"))+`</em>`, `<code>`+keepDrillVar+`</code>`, `<code>`+keepOffVar+`</code>`) + `</p>`
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

// osVayuKeepBody builds the Backups page in lang: a Status page once
// automatic backup is on or has been asked for, the Setup page until then.
// proven is the newest restore point that passed a test restore.
func osVayuKeepBody(lang, nonce string, st vayukeep.Status, bootErr string, gens []vayukeep.Generation, proven string, now time.Time, currentTarget string, envManaged bool, prefs keepPrefs, run string, places []backupPlace) string {
	if !st.Enabled && bootErr == "" && !envManaged {
		return run + keepSetupPage(lang, currentTarget) + keepScripts(nonce)
	}
	prefs = prefs.withDefaults()
	v := keepStatusVerdict(lang, st, bootErr, now, leftOut(places))
	closeLabel := i18n.T(lang, "Close")
	// Every control reports into this line, and a toast; the sheets report into
	// their own (data-sheet-status), next to the button that was pressed.
	actions := `<span id="vk-status" class="text-xs muted" role="status" aria-live="polite"></span>`
	manual := ui.Row{Label: i18n.T(lang, "A copy on your computer"), Hint: i18n.T(lang, "Download the whole site as one file, or restore one you downloaded."),
		Control: ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="vk-manual-sheet">` + esc(i18n.T(lang, "Download or restore…")) + `</button>`)}
	sheets := ui.SheetClosing("vk-manual-sheet", i18n.T(lang, "Download or restore a copy"), closeLabel, ui.HTML(keepManualCard(lang)))
	sections := []ui.HTML{ui.HTML(run)}
	backUpNow, testRestore := i18n.T(lang, "Back up now"), i18n.T(lang, "Test restore")
	if st.Enabled && bootErr == "" {
		actions += `<button type="button" class="btn" data-vk-drill>` + esc(testRestore) + `</button>` +
			`<button type="button" class="btn btn--primary" data-vk-backup>` + esc(backUpNow) + `</button>`
		points, pointSheets := keepPoints(lang, gens, proven, st, now)
		details := []ui.Fact{
			{Key: i18n.T(lang, "Backing up to"), Value: ui.HTML(`<code>` + html.EscapeString(st.Target) + `</code>`)},
			{Key: i18n.T(lang, "Newest backup"), Value: ui.Text(humanAgoIn(lang, st.NewestGen, now) + " · " + humanBytes(st.LastGenBytes))},
			{Key: i18n.T(lang, "Last successful write"), Value: ui.Text(humanAgoIn(lang, st.LastSuccess, now))},
			{Key: i18n.T(lang, "Last test restore"), Value: ui.Text(drillSummary(lang, st, now))},
		}
		if st.LastError != "" {
			details = append(details, ui.Fact{Key: i18n.T(lang, "Last error"), Value: ui.Text(st.LastError)})
		}
		sections = append(sections, points, ui.Section(i18n.T(lang, "Details"), "", ui.Facts(details...)), keepHoldsSection(lang, places),
			ui.Section(i18n.T(lang, "Settings"), "", ui.Rows(
				ui.Row{Label: i18n.T(lang, "Back up automatically"),
					Hint: titleFirst(i18n.T(lang, "{every} at most, when something changed. Keeps {points} restore points or {days} days, whichever keeps more.",
						"every", keepEveryLabelIn(lang, prefs.EveryMin), "points", prefs.RetainGens, "days", prefs.RetainDays)),
					Control: ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="vk-schedule-sheet">` + esc(i18n.T(lang, "Change…")) + `</button>`)},
				manual,
				ui.Row{Label: i18n.T(lang, "Automatic backup"), Hint: i18n.T(lang, "On. Turning it off keeps the restore points you have."),
					Control: ui.HTML(`<button type="button" class="btn btn--ghost btn--sm" data-vk-disable>` + esc(i18n.T(lang, "Turn off")) + `</button>`)})))
		sheets += ui.SheetClosing("vk-schedule-sheet", i18n.T(lang, "Schedule"), closeLabel, ui.HTML(keepRetentionCard(lang, prefs))) + pointSheets
	} else {
		// Refused, or configured by the environment and not running: the page
		// says which, and the sheet holds the reason and what fixes it.
		label := i18n.T(lang, "Set up automatic backup")
		if bootErr != "" {
			label = i18n.T(lang, "Fix the settings")
		}
		actions += `<button type="button" class="btn btn--primary" data-sheet="vk-setup-sheet">` + esc(label) + `</button>`
		sections = append(sections, ui.Section(i18n.T(lang, "Settings"), "", ui.Rows(manual)))
		sheets += ui.SheetClosing("vk-setup-sheet", label, closeLabel, ui.HTML(keepSetupCard(lang, bootErr, currentTarget, envManaged)))
	}
	sections = append(sections, ui.ExplainTitled(i18n.T(lang, "How this works"), ui.HTML(`<p class="text-sm">`+
		escFill(i18n.T(lang, "{backup} saves a restore point and test-restores it straight away; once it passes you can remove the older ones. {test} takes your newest backup, unpacks it into a temporary folder, opens the database inside it and checks every page, then deletes it. It never touches your live site, and it is the only control here that proves a backup works.",
			"backup", htmlMark(0), "test", htmlMark(1)), keepStrong(backUpNow), keepStrong(testRestore))+`</p>
<h3 class="settings-block-title mt-4">`+esc(i18n.T(lang, "How to restore"))+`</h3>`+keepRestoreCard(lang, st)+`
<h3 class="settings-block-title mt-4">`+esc(i18n.T(lang, "When it runs"))+`</h3>`+keepScheduleCard(lang, prefs)+`
<h3 class="settings-block-title mt-4">`+esc(i18n.T(lang, "Encryption and safety"))+`</h3>`+keepSpecCard(lang, st))))
	return string(ui.Status(ui.StatusPage{Title: i18n.T(lang, "Backups"), Actions: ui.HTML(actions), Tone: v.Tone, State: v.State, Detail: v.Detail}, sections...)) +
		string(sheets) + keepScripts(nonce)
}

// keepSetupPage is the page until automatic backup is on: what it does, what it
// needs, and one button, with the setup form in a sheet. Downloading a copy or
// restoring one from a file does not wait on automatic backup, so it stays one
// tap away in a sheet of its own; a download being prepared shows above (run).
// A backup that failed to start (bootErr) or one the environment configures is
// not this page: those are the status page, which says what is wrong.
func keepSetupPage(lang, currentTarget string) string {
	setUp, manual, closeLabel := i18n.T(lang, "Set up automatic backup"), i18n.T(lang, "Download or restore a copy"), i18n.T(lang, "Close")
	return string(ui.Setup(ui.SetupPage{
		Icon:  "archive",
		Title: i18n.T(lang, "Backups aren't set up yet"),
		What:  i18n.T(lang, "Encrypted copies of your whole site (database, media, mailboxes and settings), taken every few minutes while you work and tested on a schedule, so you know they restore."),
		Steps: []ui.SetupStep{
			{Title: i18n.T(lang, "A folder"), Detail: i18n.T(lang, "Outside the site's data folder. A separate disk or mounted volume also survives losing this one.")},
			{Title: i18n.T(lang, "A passphrase"), Detail: i18n.T(lang, "It encrypts every copy. Keep it somewhere other than this server: there is no reset.")},
			{Title: i18n.T(lang, "The first backup, and a test restore"), Detail: i18n.T(lang, "Both run as soon as it is on, with no restart.")},
		},
		Action: ui.HTML(`<button type="button" class="btn btn--primary" data-sheet="vk-setup-sheet">` + esc(setUp) + `</button>`),
		More:   ui.HTML(`<button type="button" class="btn btn--ghost" data-sheet="vk-manual-sheet">` + esc(manual) + `</button>`),
	})) +
		string(ui.SheetClosing("vk-setup-sheet", setUp, closeLabel, ui.HTML(keepSetupCard(lang, "", currentTarget, false)))) +
		string(ui.SheetClosing("vk-manual-sheet", manual, closeLabel, ui.HTML(keepManualCard(lang))))
}

// keepScripts wires every control on the page (admin-os-vayukeep.js), and the
// download and restore-from-file controls (admin-os-update.js).
func keepScripts(nonce string) string {
	return `<script nonce="` + nonce + `" src="/os/static/js/admin-os-vayukeep.js?v=` + assetVer("js/admin-os-vayukeep.js") + `"></script>` +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-update.js?v=` + assetVer("js/admin-os-update.js") + `"></script>`
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
	lang := i18n.Lang(r.Context())
	writeOSHTML(w, r, adminOSLayout(nonce, i18n.T(lang, "Backups"), "operations", cfg,
		htmpl.HTML(osVayuKeepBody(lang, nonce, st, a.vayuKeepErr, gens, proven, time.Now().UTC(),
			a.resolveKeepTarget(r.Context()), envManaged, keepPrefs{
				RetainGens: a.keepInt(r.Context(), settings.KeyVayuKeepRetainGen, config.Cfg.VayuKeepRetainGen),
				RetainDays: a.keepInt(r.Context(), settings.KeyVayuKeepRetainDays, config.Cfg.BackupRetainDays),
				EveryMin:   a.keepEveryMin(r.Context()),
			}, keepRunHTML(lang, &a.keepRun), backupPlaces(filepath.Dir(config.Cfg.DBPath))))))
}

// ── Endpoints ────────────────────────────────────────────────────────────────

// keepGuard rejects the request unless an admin is asking and replication runs.
func (a *App) keepGuard(w http.ResponseWriter, r *http.Request) bool {
	lang := i18n.Lang(r.Context())
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only an administrator can do this."), "")
		return false
	}
	if !a.keepRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayukeep-off", i18n.T(lang, "Automatic backup is not set up."), "")
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
	lang := i18n.Lang(r.Context())
	if !a.keepRun.startManual(func(l string) string { return i18n.T(l, "Sealing the restore point and testing that it restores") }, time.Now()) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": i18n.T(lang, "A backup or test restore is already running; its progress is on this page.")})
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
		"detail": i18n.T(lang, "Backing up. The copy goes as fast as the server can spare; its progress is on this page.")})
}

// handleOSVayuKeepClearOlder deletes every restore point older than the newest
// one that passed a test restore. The engine refuses without such a point, so
// this can never leave the site with only backups that do not restore.
func (a *App) handleOSVayuKeepClearOlder(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	lang := i18n.Lang(r.Context())
	removed, err := a.vayuKeep.RemoveOlderThanProven()
	if errors.Is(err, vayukeep.ErrNotProven) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
			"detail": i18n.T(lang, "Nothing was removed: no restore point has passed a test restore yet. Press Back up now or Test restore now first.")})
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
		"detail": i18n.T(lang, "{n, plural, one {Removed # older restore point} other {Removed # older restore points}} ({size} freed). Kept {point}, which passed a test restore.",
			"n", len(removed), "size", humanBytes(freed), "point", a.vayuKeep.ProvenGeneration())})
}

// handleOSVayuKeepDrill starts a test restore and answers at once; the page
// shows it running and then its real outcome, as for Back up now.
func (a *App) handleOSVayuKeepDrill(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	lang := i18n.Lang(r.Context())
	if !a.keepRun.startManual(func(l string) string { return i18n.T(l, "Testing that the newest restore point restores") }, time.Now()) {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": i18n.T(lang, "A backup or test restore is already running; its progress is on this page.")})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), keepManualTimeout)
		defer cancel()
		res := a.vayuKeep.Drill(ctx)
		a.keepRun.finishManual(keepResult{OK: res.OK, Generation: res.Generation, At: time.Now(), Say: func(l string) string {
			if !res.OK {
				return i18n.T(l, "Test restore FAILED: {why}", "why", res.Err)
			}
			said := i18n.T(l, "Test restore PASSED: your newest backup unpacked and its database checked out clean.")
			if res.Rows > 0 {
				said += " " + i18n.T(l, "{n, plural, one {# post} other {# posts}} read back.", "n", res.Rows)
			}
			return said
		}})
	}()
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
		"detail": i18n.T(lang, "Testing a restore. It goes as fast as the server can spare; its progress is on this page.")})
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
	lang := i18n.Lang(r.Context())

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
				"detail": i18n.T(lang, "{point} is NOT usable: {why}", "point", g.Name, "why", verr.Error()),
			})
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{
			"ok":     true,
			"detail": i18n.T(lang, "{point} checks out: the passphrase is right, every block authenticates, the chain is unbroken and the file is complete.", "point", g.Name),
		})
		return
	}
	writeAPIError(w, r, http.StatusNotFound, "not-found", i18n.T(lang, "There is no restore point by that name."), "")
}

// handleOSVayuKeepSetup turns automatic backup on from the console: it validates
// the folder, seals the passphrase, saves the setting and restarts the engine —
// no file editing, no service restart.
func (a *App) handleOSVayuKeepSetup(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only an administrator can do this."), "")
		return
	}
	if a.siteSettings == nil || a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", i18n.T(lang, "Settings cannot be saved yet: their storage is not ready."), "")
		return
	}
	var body struct {
		Target     string `json:"target"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(lang, "The request could not be read."), "")
		return
	}
	target := strings.TrimSpace(body.Target)
	pass := strings.TrimSpace(body.Passphrase)

	if target == "" {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Choose a folder to keep the backups in.")})
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
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Set a passphrase. It is the only key to these backups: without it nobody, including you, can read them.")})
		return
	}
	if pass != "" && len(pass) < 12 {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Use at least 12 characters. This one passphrase protects every copy of your whole site.")})
		return
	}

	// Reject a target inside the data directory before saving it, so the console
	// gives the same answer the engine would — with the reason.
	dataDir := filepath.Dir(config.Cfg.DBPath)
	if abs, err := filepath.Abs(filepath.Clean(target)); err == nil {
		if rel, rerr := filepath.Rel(dataDir, abs); rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
				"detail": i18n.T(lang, "That folder is inside your data directory. A copy on the disk it is meant to protect, replicating its own output, is not a backup: pick somewhere outside {folder}.", "folder", dataDir)})
			return
		}
	}
	if err := validateKeepTargetWritable(target); err != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false,
			"detail": i18n.T(lang, "VayuPress cannot write to {folder}: {why}. If this is a new location, add it to the service's ReadWritePaths.", "folder", target, "why", err.Error())})
		return
	}

	if pass != "" {
		if _, err := a.secrets.Upsert(r.Context(), secrets.ProviderVayuKeep, keepSecretLabel, "", pass, true, false); err != nil {
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
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Saved, but backups could not start: {why}", "why", err.Error())})
		return
	}
	// Take the first one immediately so the operator sees proof rather than a promise.
	a.vayuKeep.TriggerNow()
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "reload": true,
		"detail": i18n.T(lang, "Automatic backup is on. The first copy is being written now; then press Test restore to prove it works."),
	})
}

// handleOSVayuKeepDisable turns automatic backup off. Existing copies are left
// exactly where they are: turning the schedule off is not consent to delete
// what it already saved.
func (a *App) handleOSVayuKeepDisable(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Lang(r.Context())
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only an administrator can do this."), "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", i18n.T(lang, "Settings cannot be saved yet: their storage is not ready."), "")
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
		"detail": i18n.T(lang, "Automatic backup is off. Your existing restore points are untouched."),
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
	lang := i18n.Lang(r.Context())
	// Typed confirmation. This replaces the live database; a misclick must not be
	// enough on its own. The word is the same in every language: it is typed.
	if strings.TrimSpace(body.Confirm) != "RESTORE" {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Type {word} to confirm.", "word", "RESTORE")})
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
		writeAPIError(w, r, http.StatusNotFound, "not-found", i18n.T(lang, "There is no restore point by that name."), "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	staged, err := a.vayuKeepStageRestore(ctx, *chosen)
	if err != nil {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Restore could not be prepared: {why}. Nothing was changed.", "why", err.Error())})
		return
	}
	dbpkg.AuditLog("vayukeep.restore", dbpkg.AuditActor(r), chosen.Name, staged)
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "restart": true,
		"detail": i18n.T(lang, "Restore prepared from {point}. Restarting now; your current database is copied aside first, so this is itself reversible.", "point", chosen.Name),
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
	lang := i18n.Lang(r.Context())

	g, err := a.deleteRestorePoint(body.Name)
	switch {
	case errors.Is(err, errOnlyRestorePoint):
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": onlyRestorePointRefusal(lang)})
	case errors.Is(err, errNoRestorePoint):
		writeAPIError(w, r, http.StatusNotFound, "not-found", i18n.T(lang, "There is no restore point by that name."), "")
	case err != nil:
		writeAPIError(w, r, http.StatusInternalServerError, "vayukeep-error", err.Error(), "")
	default:
		dbpkg.AuditLog("vayukeep.delete", dbpkg.AuditActor(r), g.Name, humanBytes(g.Bytes))
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": true,
			"detail": i18n.T(lang, "{point} deleted ({size} freed).", "point", g.Name, "size", humanBytes(g.Bytes))})
	}
}

var (
	errOnlyRestorePoint = errors.New("the only restore point")
	errNoRestorePoint   = errors.New("no restore point by that name")
)

// onlyRestorePointRefusal is what either page says, in lang, when asked to
// delete the last restore point.
func onlyRestorePointRefusal(lang string) string {
	return i18n.T(lang, "That is your only restore point. Take a new one first if you really want to remove it.")
}

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
	lang := i18n.Lang(r.Context())
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only an administrator can do this."), "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", i18n.T(lang, "Settings cannot be saved yet: their storage is not ready."), "")
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
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Both limits must be at least 1.")})
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
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": false, "detail": i18n.T(lang, "Choose a schedule from the list.")})
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
		"detail": i18n.T(lang, "Saved: backing up {every} while the site changes, keeping at least {points} restore points and anything from the last {days} days.",
			"every", keepEveryLabelIn(lang, a.keepEveryMin(r.Context())), "points", body.Generations, "days", body.Days)})
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
	lang := i18n.Lang(r.Context())
	detail := i18n.T(lang, "Nothing to clean up: every restore point is still within your limits.")
	if removed == 0 && a.vayuKeep.ProvenGeneration() == "" {
		detail = i18n.T(lang, "Nothing removed: old restore points are only deleted once a newer one has passed a test restore, and none has since VayuPress started. Press Test restore now first.")
	}
	if removed > 0 {
		detail = i18n.T(lang, "{n, plural, one {# restore point removed.} other {# restore points removed.}}", "n", removed)
		dbpkg.AuditLog("vayukeep.prune", dbpkg.AuditActor(r), strconv.Itoa(removed), "")
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "reload": removed > 0, "detail": detail})
}
