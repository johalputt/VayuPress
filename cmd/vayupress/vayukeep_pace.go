// SPDX-License-Identifier: Apache-2.0

package main

// vayukeep_pace.go — backups taken in steps, at the pace the host can spare.
//
// A generation used to be one `VACUUM INTO`: a single statement reading and
// writing the whole database, which nothing could pause. On johal.in's 17 GB
// database that was minutes of the disk's full bandwidth, taken whenever a
// backup fell due, visitors or not. The copy is now SQLite's backup API in
// steps over one pinned read snapshot (internal/sqlitecopy), each step's size
// decided by the host pacer (internal/pace). The Backups page shows the copy
// while it runs, with the pace and the pacer's reason.

import (
	"context"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/pacedio"
	"github.com/johalputt/vayupress/internal/sqlitecopy"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// snapshotPages sizes a snapshot's steps, in database pages (4 KiB each by
// default). The smallest step is what the floor copies once every 30 s while
// the host stays busy, so it has to finish a large database in time that means
// something: 1,024 pages is 4 MiB, 17 GB in under two days of nothing but
// floor steps, which is the worst case, not the expected one. The largest,
// 64 MiB, keeps any one step short enough that the pacer is asked often.
var snapshotPages = pace.JobConfig{Min: 1024, Max: 16384, Step: 1024, Floor: true}

// sealChunks paces sealing a generation and reading one back for its test
// restore, in VayuKeep's 256 KiB chunks: 1 MiB at the slowest, 64 MiB at most
// between asking the pacer again.
var sealChunks = pace.JobConfig{Min: 4, Max: 256, Step: 4, Floor: true}

// updatePacing paces the pre-update backup and the Export download in the same
// units as VayuKeep's copy and seal: pages for the database copy, chunks for
// the archive. A var for the reason newSnapshotJob is one.
var updatePacing = func() update.Pacing {
	return update.Pacing{
		Pages:  func() pacedio.Pacer { return newSnapshotJob() },
		Chunks: func() pacedio.Pacer { return pace.Host().NewJob(sealChunks) },
	}
}

// newSnapshotJob paces one snapshot. A var so a test can take a snapshot at
// full speed: the host pacer reads the machine's real load, and a busy test
// runner is exactly where it would wait.
var newSnapshotJob = func() *pace.Job { return pace.Host().NewJob(snapshotPages) }

// snapshotLiveDB writes a consistent copy of the live database at dbPath to
// dest, paced by the host. The backup command uses it directly; VayuKeep goes
// through keepRun so the Backups page can show the copy.
func snapshotLiveDB(ctx context.Context, dbPath, dest string) error {
	return sqlitecopy.Copy(ctx, dbPath, dest, newSnapshotJob(), nil)
}

// keepManualTimeout bounds a Back up now or a Test restore. Both run off the
// request: its 30 s router deadline cut a slow backup off half way and recorded
// it as failed, and a cut-off drill raised a false alarm about the recovery
// path; a closed tab must not abandon a half-written backup either. The bound
// exists so work that can never finish is eventually reported rather than
// shown as running forever.
const keepManualTimeout = 48 * time.Hour

// keepRun is the work the Backups page shows: the database copy while one is
// being taken (scheduled or asked for), and Back up now, Test restore or a
// download being prepared from the click to its end, then its outcome until the
// next one starts. One slot holds all three buttons' work: each reads the whole
// database or backup, and running two at once would double exactly the load
// that pacing exists to spread.
type keepRun struct {
	mu      sync.Mutex
	copying bool
	job     *pace.Job
	copied  sqlitecopy.Progress
	work    sentence  // what the running button is doing; nil when none runs
	started time.Time // when it was pressed
	paced   *pace.Job // the pacer the button's work last started
	result  *keepResult
}

// show records p as the pacer of the running work, so the page can say at what
// pace sealing, a test restore or a download goes and why, as it does for the
// copy. A pacer that is not the host's (a test's) has no pace to show.
func (k *keepRun) show(p pacedio.Pacer) pacedio.Pacer {
	if j, ok := p.(*pace.Job); ok {
		k.mu.Lock()
		k.paced = j
		k.mu.Unlock()
	}
	return p
}

// keepResult is how the last button's work ended, in the words the page shows.
type keepResult struct {
	OK bool
	// Say is what it came to. The work ran with no reader, so it is said in
	// the language of whoever next opens the page.
	Say        sentence
	Generation string
	Older      int
	OlderBytes int64
	At         time.Time
	// Download is a prepared export waiting to be fetched, and its size. It is
	// removed when the next button's work starts; one left behind by a restart
	// is swept with the other stale temporary files.
	Download      string
	DownloadBytes int64
}

// snapshot is VayuKeep's Snapshot: the paced copy, recorded as it goes.
func (k *keepRun) snapshot(ctx context.Context, dbPath, dest string) error {
	job := newSnapshotJob()
	k.mu.Lock()
	k.copying, k.job, k.copied = true, job, sqlitecopy.Progress{}
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		k.copying, k.job = false, nil
		k.mu.Unlock()
	}()
	return sqlitecopy.Copy(ctx, dbPath, dest, job, func(p sqlitecopy.Progress) {
		k.mu.Lock()
		k.copied = p
		k.mu.Unlock()
	})
}

// startManual claims the one slot for a button's work, described as what the
// page shows while it runs; false while other work holds it.
func (k *keepRun) startManual(work sentence, now time.Time) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.work != nil {
		return false
	}
	if k.result != nil && k.result.Download != "" {
		_ = os.Remove(k.result.Download)
	}
	k.work, k.started, k.paced, k.result = work, now, nil, nil
	return true
}

// download is the prepared export, or "" when none is waiting.
func (k *keepRun) download() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.result == nil {
		return ""
	}
	return k.result.Download
}

func (k *keepRun) finishManual(r keepResult) {
	k.mu.Lock()
	k.work, k.result = nil, &r
	k.mu.Unlock()
}

// keepResultFor words a Back up now outcome and records it in the audit log,
// exactly as the synchronous handler did before it: "saved" is never said of a
// backup that did not pass its test restore.
func (a *App) keepResultFor(res vayukeep.DrillResult, actor string, now time.Time) keepResult {
	switch {
	case res.Err == vayukeep.DrillBusy:
		newest, _ := a.vayuKeep.Newest()
		return keepResult{At: now, Say: func(l string) string {
			return i18n.T(l, "{point} was saved, but a test restore was already running, so it is not tested yet. Press Test restore now in a minute.", "point", newest.Name)
		}}
	case !res.OK && res.Generation == "":
		return keepResult{At: now, Say: func(l string) string { return i18n.T(l, "No backup was made: {why}", "why", res.Err) }}
	case !res.OK:
		dbpkg.AuditLog("vayukeep.backup", actor, res.Generation, "FAILED its test restore")
		return keepResult{Generation: res.Generation, At: now, Say: func(l string) string {
			return i18n.T(l, "{point} was saved but did NOT pass its test restore: {why}. Your older restore points are untouched and nothing will be deleted on its strength.", "point", res.Generation, "why", res.Err)
		}}
	}
	dbpkg.AuditLog("vayukeep.backup", actor, res.Generation, "passed its test restore")
	out := keepResult{OK: true, Generation: res.Generation, At: now, Say: func(l string) string {
		said := i18n.T(l, "{point} saved and tested: it restores, and the database inside checks out clean.", "point", res.Generation)
		if res.Rows > 0 {
			said += " " + i18n.T(l, "{n, plural, one {# post} other {# posts}} read back.", "n", res.Rows)
		}
		return said
	}}
	gens, _ := a.vayuKeep.List()
	for _, g := range gens {
		if g.Name < res.Generation {
			out.Older++
			out.OlderBytes += g.Bytes
		}
	}
	return out
}

// keepRunHTML is the Backups page's "Running now" block, in lang. While
// anything runs it polls itself; once the last Back up now has ended it shows
// the outcome and stops polling.
func keepRunHTML(lang string, k *keepRun) string {
	k.mu.Lock()
	copying, work, started, res := k.copying, k.work, k.started, k.result
	p, job, paced := k.copied, k.job, k.paced
	k.mu.Unlock()

	const id = `id="vk-run" role="status" aria-live="polite"`
	if !copying && work == nil {
		if res == nil {
			return `<section ` + id + `></section>`
		}
		cls := "settings-callout"
		said := ""
		if res.Say != nil {
			said = res.Say(lang)
		}
		out := `<section ` + id + ` class="` + cls + `"><strong>` + html.EscapeString(said) + `</strong>`
		if res.Download != "" {
			out += ` <a class="btn btn--primary btn--sm" href="/os/api/backup/export/file" download>` +
				esc(i18n.T(lang, "Download ({size})", "size", humanBytes(res.DownloadBytes))) + `</a>`
		}
		if res.OK && res.Older > 0 {
			out += ` <button type="button" class="btn btn--danger btn--sm" data-vk-clear-older data-vk-older="` + strconv.Itoa(res.Older) +
				`" data-vk-generation="` + html.EscapeString(res.Generation) + `">` +
				esc(i18n.T(lang, "{n, plural, one {Remove # older restore point} other {Remove # older restore points}} ({size})", "n", res.Older, "size", humanBytes(res.OlderBytes))) + `</button>`
		}
		return out + `</section>`
	}

	var j ui.Job
	switch {
	case copying:
		j = ui.Job{Title: i18n.T(lang, "Copying the database"), Percent: -1}
		if p.Total > 0 {
			j.Percent = p.Copied * 100 / p.Total
			j.Progress = i18n.T(lang, "{percent}% · {done} of {total}", "percent", j.Percent,
				"done", humanBytes(int64(p.Copied)*int64(p.PageSize)), "total", humanBytes(int64(p.Total)*int64(p.PageSize)))
		}
		if job != nil {
			paceIn(lang, &j, job.Status().Verdict)
		}
	default:
		// Sealing, test-restoring and preparing a download report no length
		// yet, but their pace and its reason.
		j = ui.Job{Title: work(lang), Percent: -1, Progress: i18n.T(lang, "started {time} UTC", "time", started.UTC().Format("15:04"))}
		if paced != nil {
			paceIn(lang, &j, paced.Status().Verdict)
		}
	}
	return `<section ` + id + ` hx-get="/os/vayukeep/run" hx-trigger="every 2s" hx-swap="outerHTML">` +
		`<div class="section-head"><h2 class="section-head__title">` + esc(i18n.T(lang, "Running now")) + `</h2></div>` + string(j.HTML()) + `</section>`
}

// paceIn puts the pacer's verdict on j in lang: its level in the page's
// words, and its reason as the pacer gives it, in the terms of what it
// measures (the disk, the processor).
func paceIn(lang string, j *ui.Job, v pace.Verdict) {
	switch v.Level {
	case pace.Ease:
		j.Pace, j.Yielding = i18n.T(lang, "Easing"), true
	case pace.Wait:
		j.Pace, j.Yielding = i18n.T(lang, "Waiting"), true
	default:
		j.Pace = i18n.T(lang, "Running")
	}
	j.Why = v.Reason
}

// held reports since when the running copy has been waiting on the server,
// and why; a zero time when no copy is waiting.
func (k *keepRun) held() (since time.Time, why string) {
	k.mu.Lock()
	job := k.job
	k.mu.Unlock()
	if job == nil {
		return time.Time{}, ""
	}
	st := job.Status()
	return st.WaitingSince, st.Verdict.Reason
}

// keepHeldHour is how long a backup copy may wait on a busy server before the
// bell says so. Its floor keeps it moving at the slowest pace after ten
// minutes, so it will finish; an hour of that means the server has had no
// room for it all that time, which the operator should hear about.
const keepHeldHour = time.Hour

// keepHeldNotice is the bell's entry for a copy held that long, in lang. The
// reason is the pacer's own, in English: it names what the host is short of,
// in the words its measurements use.
func keepHeldNotice(lang string, since time.Time, why string, now time.Time) (osNotification, bool) {
	if since.IsZero() || now.Sub(since) < keepHeldHour {
		return osNotification{}, false
	}
	at := since.UTC().Format("15:04")
	return said(lang, osNotification{Href: "/os/vayukeep", Count: 1, Kind: "backup", Severity: "warn", State: true},
		func(l string) string { return i18n.T(l, "A backup is waiting for the server") },
		func(l string) string {
			return i18n.T(l, "Copying at its slowest since {time} UTC: {why}", "time", at, "why", why)
		}), true
}

// handleOSVayuKeepRun is the "Running now" block, polled by the Backups page.
// Only the page's poll asks for it, so a poll that finds nothing running is the
// run just ending: the page reloads, because the figures and restore points
// around the block changed with it. Administrators only, but not keepGuard: a
// download is prepared whether or not automatic backup is set up.
func (a *App) handleOSVayuKeepRun(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	out := keepRunHTML(i18n.Lang(r.Context()), &a.keepRun)
	if !strings.Contains(out, "hx-trigger") {
		w.Header().Set("HX-Refresh", "true")
	}
	writeOSHTML(w, r, out)
}
