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
	"strconv"
	"strings"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
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
// the archive.
func updatePacing() update.Pacing {
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
// being taken (scheduled or asked for), and Back up now or Test restore from the
// click to its end, then its outcome until the next one starts. One slot holds
// both buttons' work: each reads or writes the whole backup, and running two at
// once would double exactly the load that pacing exists to spread.
type keepRun struct {
	mu      sync.Mutex
	copying bool
	job     *pace.Job
	copied  sqlitecopy.Progress
	work    string    // what the running button is doing; "" when none runs
	started time.Time // when it was pressed
	result  *keepResult
}

// keepResult is how the last Back up now or Test restore ended, in the words
// the page shows.
type keepResult struct {
	OK         bool
	Detail     string
	Generation string
	Older      int
	OlderBytes int64
	At         time.Time
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
func (k *keepRun) startManual(work string, now time.Time) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.work != "" {
		return false
	}
	k.work, k.started, k.result = work, now, nil
	return true
}

func (k *keepRun) finishManual(r keepResult) {
	k.mu.Lock()
	k.work, k.result = "", &r
	k.mu.Unlock()
}

// keepResultFor words a Back up now outcome and records it in the audit log,
// exactly as the synchronous handler did before it: "saved" is never said of a
// backup that did not pass its test restore.
func (a *App) keepResultFor(res vayukeep.DrillResult, actor string, now time.Time) keepResult {
	switch {
	case res.Err == vayukeep.DrillBusy:
		newest, _ := a.vayuKeep.Newest()
		return keepResult{Detail: newest.Name + " was saved, but a test restore was already running, so it is not tested yet. Press Test restore now in a minute.", At: now}
	case !res.OK && res.Generation == "":
		return keepResult{Detail: "No backup was made: " + res.Err, At: now}
	case !res.OK:
		dbpkg.AuditLog("vayukeep.backup", actor, res.Generation, "FAILED its test restore")
		return keepResult{Generation: res.Generation, At: now,
			Detail: res.Generation + " was saved but did NOT pass its test restore: " + res.Err + ". Your older restore points are untouched and nothing will be deleted on its strength."}
	}
	dbpkg.AuditLog("vayukeep.backup", actor, res.Generation, "passed its test restore")
	out := keepResult{OK: true, Generation: res.Generation, At: now,
		Detail: res.Generation + " saved and tested: it restores, and the database inside checks out clean."}
	if res.Rows > 0 {
		out.Detail += " " + strconv.FormatInt(res.Rows, 10) + " post" + plural(int(res.Rows)) + " read back."
	}
	gens, _ := a.vayuKeep.List()
	for _, g := range gens {
		if g.Name < res.Generation {
			out.Older++
			out.OlderBytes += g.Bytes
		}
	}
	return out
}

// keepRunHTML is the Backups page's "Running now" block. While anything runs
// it polls itself; once the last Back up now has ended it shows the outcome and
// stops polling.
func keepRunHTML(k *keepRun) string {
	k.mu.Lock()
	copying, work, started, res := k.copying, k.work, k.started, k.result
	p, job := k.copied, k.job
	k.mu.Unlock()

	const id = `id="vk-run" role="status" aria-live="polite"`
	if !copying && work == "" {
		if res == nil {
			return `<div ` + id + `></div>`
		}
		cls := "settings-callout"
		out := `<div ` + id + ` class="` + cls + `"><strong>` + html.EscapeString(res.Detail) + `</strong>`
		if res.OK && res.Older > 0 {
			out += ` <button type="button" class="btn btn--danger btn--sm" data-vk-clear-older data-vk-older="` + strconv.Itoa(res.Older) +
				`" data-vk-generation="` + html.EscapeString(res.Generation) + `">Remove ` + strconv.Itoa(res.Older) + ` older restore point` +
				plural(res.Older) + ` (` + html.EscapeString(humanBytes(res.OlderBytes)) + `)</button>`
		}
		return out + `</div>`
	}

	var j ui.Job
	switch {
	case copying:
		j = ui.Job{Title: "Copying the database", Percent: -1}
		if p.Total > 0 {
			j.Percent = p.Copied * 100 / p.Total
			j.Progress = strconv.Itoa(j.Percent) + "% · " + humanBytes(int64(p.Copied)*int64(p.PageSize)) + " of " + humanBytes(int64(p.Total)*int64(p.PageSize))
		}
		if job != nil {
			st := job.Status()
			j.Pace, j.Why = st.Verdict.Level.String(), st.Verdict.Reason
		}
	default:
		// Sealing and test-restoring are paced too, but report no length yet.
		j = ui.Job{Title: work, Percent: -1, Progress: "started " + started.UTC().Format("15:04") + " UTC"}
	}
	return `<div ` + id + ` hx-get="/os/vayukeep/run" hx-trigger="every 2s" hx-swap="outerHTML">` +
		`<div class="section-head"><h2 class="section-head__title">Running now</h2></div>` + string(j.HTML()) + `</div>`
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

// keepHeldNotice is the bell's entry for a copy held that long.
func keepHeldNotice(since time.Time, why string, now time.Time) (osNotification, bool) {
	if since.IsZero() || now.Sub(since) < keepHeldHour {
		return osNotification{}, false
	}
	return osNotification{Title: "A backup is waiting for the server", Href: "/os/vayukeep", Count: 1, Kind: "backup", Severity: "warn",
		Detail: "Copying at its slowest since " + since.UTC().Format("15:04") + " UTC: " + why}, true
}

// handleOSVayuKeepRun is the "Running now" block, polled by the Backups page.
// Only the page's poll asks for it, so a poll that finds nothing running is the
// run just ending: the page reloads, because the figures and restore points
// around the block changed with it.
func (a *App) handleOSVayuKeepRun(w http.ResponseWriter, r *http.Request) {
	if !a.keepGuard(w, r) {
		return
	}
	out := keepRunHTML(&a.keepRun)
	if !strings.Contains(out, "hx-trigger") {
		w.Header().Set("HX-Refresh", "true")
	}
	writeOSHTML(w, r, out)
}
