// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_drive.go — the holder's Drive, a place beside Mail and
// Calendar: their folders and files (internal/vayuos/mail/drive.go), an
// upload, and a sheet for one entry with its rename, move, download and
// Trash.
//
// A file is never served as a page of this site. It is always a download
// (Content-Disposition: attachment, nosniff), except a picture of four
// plain formats shown in the entry's sheet, and that too goes out under a
// CSP that lets it run and load nothing: an uploaded HTML or SVG served
// inline from the console's own origin would be a page anyone could make
// speak with the holder's session.
//
// Drive opens only for the session's own mailbox, as Calendar does; the
// engine refuses an operator's reader whatever this page does.

import (
	"bytes"
	"context"
	"errors"
	htmpl "html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// drivePictures are the types shown in place in a sheet: pictures a browser
// draws and cannot run. SVG is not one: it is a document with scripts.
var drivePictures = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

// driveTransferFor is how long one upload or download may take: a full-size
// file at about 60 KB a second.
const driveTransferFor = 30 * time.Minute

func drivePictureType(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return drivePictures[strings.ToLower(name[i:])]
	}
	return ""
}

// driveKind is the few letters a file's row shows for its kind.
func driveKind(d vmail.DriveEntry) string {
	if d.IsDir {
		return ""
	}
	if i := strings.LastIndexByte(d.Name, '.'); i >= 0 && len(d.Name)-i <= 5 && len(d.Name)-i > 1 {
		return strings.ToUpper(d.Name[i+1:])
	}
	return "FILE"
}

func driveRefusal(err error) string { return strings.TrimPrefix(err.Error(), "vayumail: ") }

func driveHref(dir int64) string {
	if dir == 0 {
		return "/os/vayumail/drive"
	}
	return "/os/vayumail/drive?dir=" + strconv.FormatInt(dir, 10)
}

// driveSide is Drive's sidebar: upload, the two views, and the allowance
// the files share with mail.
func (a *App) driveSide(ctx context.Context, rd vmail.Reader, dir int64, trash bool) string {
	var b strings.Builder
	if !trash {
		b.WriteString(`<label class="btn btn--primary mx-compose-btn">` + saIcon("upload") + `Upload<input class="vp-sr-only" type="file" multiple data-drive-upload data-dir="` + strconv.FormatInt(dir, 10) + `"></label>`)
	}
	item := func(href, icon, label string, on bool) {
		cur := ""
		if on {
			cur = ` aria-current="page"`
		}
		b.WriteString(`<a class="sa-appside__item" href="` + href + `"` + cur + `>` + saIcon(icon) + `<span class="sa-appside__label">` + label + `</span></a>`)
	}
	item("/os/vayumail/drive", "folder", "My files", !trash)
	item("/os/vayumail/drive?view=trash", "trash", "Trash", trash)
	b.WriteString(`<button type="button" class="sa-appside__item" hx-get="/os/vayumail/drive/entry?settings=1" hx-target="#drive-sheet-body" hx-swap="innerHTML">` + saIcon("settings") +
		`<span class="sa-appside__label">Drive settings</span></button>`)
	mbox := a.calendarAddress(rd)
	// Mail and files share the mailbox's one allowance (the engine counts
	// them together), so the figure here is both, with the files' part named.
	used, files, quota := a.vayuMail.MailboxUsage(mbox), a.vayuMail.Accounts().DriveUsed(ctx, mbox), a.vayuMail.MailboxQuota(mbox)
	b.WriteString(`<div class="mx-foot"><div class="mx-storage"><span class="mx-storage__row"><span>Mail and files</span><span>` + esc(humanBytes(used)))
	if quota > 0 {
		b.WriteString(` of ` + esc(humanBytes(quota)))
	}
	b.WriteString(`</span></span><span class="mx-storage__row"><span>Files alone</span><span>` + esc(humanBytes(files)) + `</span></span></div></div>`)
	return b.String()
}

func (a *App) handleVayuOSDrive(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Drive")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)
	rd, ok := a.driveOwner(r)
	if !ok {
		page := `<div data-page-kind="status"><div class="page-header"><h1>Drive</h1></div><div class="empty-state">Files belong to a mailbox, and this account has none. An administrator can give you an address under <strong>Members → Team &amp; roles</strong>.</div></div>`
		writeOSHTML(w, r, adminOSLayout(nonce, "Drive", "vayuos", cfg, htmpl.HTML(page)))
		return
	}
	trash := r.URL.Query().Get("view") == "trash"
	dir, _ := strconv.ParseInt(r.URL.Query().Get("dir"), 10, 64)
	var entries []vmail.DriveEntry
	var path []vmail.DriveEntry
	var err error
	if trash {
		dir = 0
		entries, err = a.vayuMail.DriveTrashed(rd)
	} else {
		if entries, err = a.vayuMail.DriveList(rd, dir); errors.Is(err, vmail.ErrDriveNotFound) {
			http.Redirect(w, r, "/os/vayumail/drive", http.StatusSeeOther)
			return
		}
		if dir != 0 {
			path, _ = a.vayuMail.DrivePath(rd, dir)
		}
	}
	side := a.mailSide(r, rd, "", "", nil)
	side.Place, side.PlaceSide = "drive", a.driveSide(r.Context(), rd, dir, trash)
	cfg.MailSide = side

	var b strings.Builder
	b.WriteString(`<div class="drive" data-page-kind="app" data-drive data-dir="` + strconv.FormatInt(dir, 10) + `">`)
	b.WriteString(`<div class="drive-head"><h1 class="drive-crumbs">`)
	switch {
	case trash:
		b.WriteString(`Trash`)
	case len(path) == 0:
		b.WriteString(`My files`)
	default:
		b.WriteString(`<a href="/os/vayumail/drive">My files</a>`)
		for i, p := range path {
			b.WriteString(saIcon("chev-r"))
			if i == len(path)-1 {
				b.WriteString(`<span>` + esc(p.Name) + `</span>`)
			} else {
				b.WriteString(`<a href="` + driveHref(p.ID) + `">` + esc(p.Name) + `</a>`)
			}
		}
	}
	b.WriteString(`</h1>`)
	if !trash {
		b.WriteString(`<button type="button" class="btn btn--sm" hx-get="/os/vayumail/drive/entry?new=folder&amp;dir=` + strconv.FormatInt(dir, 10) + `" hx-target="#drive-sheet-body" hx-swap="innerHTML">` + saIcon("plus") + `New folder</button>`)
	}
	b.WriteString(`</div><div id="drive-status" class="drive-status" role="status" aria-live="polite"></div>`)
	if err != nil {
		b.WriteString(saCallout("danger", "The files could not be read: "+esc(driveRefusal(err))+"."))
	}
	if trash && len(entries) > 0 {
		days := 30
		if ds, err := a.vayuMail.DriveSettingsOf(rd); err == nil {
			days = ds.TrashDays
		}
		b.WriteString(`<p class="cal-note">A file stays here ` + strconv.Itoa(days) + ` days, then it is deleted for good.</p>` +
			`<div class="cal-detail__acts"><button type="button" class="btn btn--sm btn--danger" hx-post="/os/vayumail/drive/action"` + hxVals("op", "empty") +
			` hx-confirm="Delete everything in Trash for good? It cannot be brought back.">Empty Trash</button></div>`)
	}
	if n, _ := a.vayuMail.DriveDamaged(rd); n > 0 {
		b.WriteString(saCallout("danger", strconv.Itoa(n)+" "+pluralWord(n, "file or version", "files or versions")+
			" in this Drive "+pluralWord(n, "no longer matches", "no longer match")+" what was kept: the server's disk changed or damaged "+pluralWord(n, "it", "them")+". Each says so on its sheet."))
	}
	if len(entries) == 0 && err == nil {
		what := "This folder is empty. Upload a file, or drop one here."
		if trash {
			what = "Trash is empty."
		}
		b.WriteString(`<p class="drive-empty">` + what + `</p>`)
	} else {
		b.WriteString(`<ul class="drive-list">`)
		for _, d := range entries {
			id := strconv.FormatInt(d.ID, 10)
			open := ` hx-get="/os/vayumail/drive/entry?id=` + id + `" hx-target="#drive-sheet-body" hx-swap="innerHTML"`
			b.WriteString(`<li class="drive-row">`)
			if d.IsDir && !trash {
				b.WriteString(`<a class="drive-row__name" href="` + driveHref(d.ID) + `">` + saIcon("folder") + `<span>` + esc(d.Name) + `</span></a>`)
			} else {
				kind := saIcon("folder")
				if !d.IsDir {
					kind = `<span class="drive-kind" aria-hidden="true">` + esc(driveKind(d)) + `</span>`
				}
				b.WriteString(`<button type="button" class="drive-row__name"` + open + `>` + kind + `<span>` + esc(d.Name) + `</span></button>`)
			}
			size := ""
			if !d.IsDir {
				size = humanBytes(d.Size)
			}
			b.WriteString(`<span class="drive-row__meta">` + esc(size) + `</span><span class="drive-row__meta">` + esc(d.Modified.In(a.calendarHome(r)).Format("2 Jan 2006")) + `</span>` +
				`<button type="button" class="btn btn--ghost btn--sm" aria-label="More for ` + esc(d.Name) + `"` + open + `>` + saIcon("more") + `</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(string(ui.Sheet("drive-sheet", "File", ui.HTML(`<div id="drive-sheet-body"></div>`))))
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-drive.js?v=` + assetVer("js/admin-os-drive.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Drive", "vayuos", cfg, htmpl.HTML(b.String())))
}

// driveFolders is every live folder of the mailbox as a path, for the move
// list: at most 300, which is more than a list a person reads.
func (a *App) driveFolders(rd vmail.Reader, parent int64, prefix string, depth int, out *[][2]string) {
	if depth > 8 || len(*out) >= 300 {
		return
	}
	list, err := a.vayuMail.DriveList(rd, parent)
	if err != nil {
		return
	}
	for _, d := range list {
		if !d.IsDir {
			continue
		}
		*out = append(*out, [2]string{strconv.FormatInt(d.ID, 10), prefix + d.Name})
		a.driveFolders(rd, d.ID, prefix+d.Name+" / ", depth+1, out)
	}
}

// handleVayuOSDriveEntry is the sheet for one entry, or for a new folder.
func (a *App) handleVayuOSDriveEntry(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Files open only for their holder.", "")
		return
	}
	q := r.URL.Query()
	post := ` hx-post="/os/vayumail/drive/action" hx-target="#drive-sheet-body" hx-swap="innerHTML"`
	if q.Get("new") == "folder" {
		dir, _ := strconv.ParseInt(q.Get("dir"), 10, 64)
		writeOSHTML(w, r, `<form class="cal-form" data-sheet-title="New folder"`+post+`><input type="hidden" name="op" value="mkdir"><input type="hidden" name="dir" value="`+strconv.FormatInt(dir, 10)+`">`+
			`<label class="field"><span class="field-label">Name</span><input type="text" name="name" required maxlength="255" autocomplete="off"></label>`+
			`<div class="cal-form__acts"><button type="button" class="btn" data-sheet-close>Cancel</button><button type="submit" class="btn btn--primary">Make folder</button></div></form>`)
		return
	}
	if q.Get("settings") == "1" {
		writeOSHTML(w, r, a.driveSettingsSheet(rd, ""))
		return
	}
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	path, err := a.vayuMail.DrivePath(rd, id)
	if err != nil || len(path) == 0 {
		writeOSHTML(w, r, saCallout("danger", "This is no longer here."))
		return
	}
	writeOSHTML(w, r, a.driveSheet(rd, path[len(path)-1], a.calendarHome(r), ""))
}

func (a *App) driveSheet(rd vmail.Reader, d vmail.DriveEntry, home *time.Location, msg string) string {
	id := strconv.FormatInt(d.ID, 10)
	post := ` hx-post="/os/vayumail/drive/action" hx-target="#drive-sheet-body" hx-swap="innerHTML"`
	var b strings.Builder
	b.WriteString(`<div class="cal-detail" data-sheet-title="` + esc(d.Name) + `">` + msg)
	if !d.Trashed.IsZero() {
		b.WriteString(`<p class="cal-note">In Trash since ` + esc(d.Trashed.In(home).Format("2 January 2006")) + `.</p><div class="cal-detail__acts">` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "restore", "id", id) + `>Restore</button>` +
			`<button type="button" class="btn btn--sm btn--danger"` + post + hxVals("op", "delete", "id", id) + ` hx-confirm="Delete this for good? It cannot be brought back.">Delete for good</button></div></div>`)
		return b.String()
	}
	if !d.IsDir {
		if drivePictureType(d.Name) != "" && d.Size <= 20<<20 {
			b.WriteString(`<img class="drive-preview" alt="" src="/os/vayumail/drive/file?id=` + id + `&amp;show=1">`)
		}
		meta := humanBytes(d.Size) + " · changed " + d.Modified.In(home).Format("2 January 2006")
		if d.Sealed {
			meta += " · sealed"
		}
		b.WriteString(`<p class="cal-note">` + esc(meta) + `</p>`)
		if d.Damaged {
			b.WriteString(saCallout("danger", "This file's bytes on the server are not what was kept: the disk changed or damaged them, and the file is not handed out as if it were sound. Bring back an earlier version below, or keep it again from your own copy or from a backup."))
		}
		if d.Sum != "" {
			// The content's SHA-256, to compare with a copy kept elsewhere
			// (sha256sum, shasum -a 256, Get-FileHash).
			b.WriteString(`<p class="cal-note">SHA-256 <span class="mono drive-sum">` + esc(d.Sum) + `</span></p>`)
		}
		if !d.Sealed && a.vayuPGP != nil && a.vayuPGP.HolderKeeps(a.calendarAddress(rd)) {
			// A private mailbox's holder was told their files are sealed; the
			// one that is not says so itself.
			b.WriteString(`<p class="cal-note">This file is not sealed yet: it could not be when the mailbox was made private. Making it private again seals it; or download it and delete it here.</p>`)
		}
		b.WriteString(`<div class="cal-detail__acts"><a class="btn btn--sm" href="/os/vayumail/drive/file?id=` + id + `" download>` + saIcon("download") + `Download</a></div>`)
	}
	if !d.IsDir {
		if vs, _ := a.vayuMail.DriveVersions(rd, d.ID); len(vs) > 0 {
			// What the file was before each time it was saved over. Bringing
			// one back swaps it with the file as it is, so that is kept too.
			b.WriteString(`<h3 class="cal-booking__h">Earlier versions</h3><ul class="drive-versions">`)
			for _, v := range vs {
				damaged := ""
				if v.Damaged {
					damaged = " · damaged"
				}
				b.WriteString(`<li><span>` + esc(v.Saved.In(home).Format("2 January 2006, 15:04")) + ` · ` + esc(humanBytes(v.Size)) + damaged + `</span>` +
					`<a class="btn btn--sm" href="/os/vayumail/drive/file?id=` + id + `&amp;version=` + strconv.FormatInt(v.ID, 10) + `" download>Download</a>` +
					`<button type="button" class="btn btn--sm"` + post + hxVals("op", "version", "id", id, "version", strconv.FormatInt(v.ID, 10)) + `>Bring back</button></li>`)
			}
			b.WriteString(`</ul>`)
		}
	}
	if !d.IsDir {
		b.WriteString(a.driveShareSection(rd, d, home, post))
	}
	b.WriteString(`<form class="cal-form"` + post + `><input type="hidden" name="op" value="rename"><input type="hidden" name="id" value="` + id + `">` +
		`<label class="field"><span class="field-label">Name</span><input type="text" name="name" required maxlength="255" value="` + esc(d.Name) + `" autocomplete="off"></label>` +
		`<div class="cal-form__acts"><button type="submit" class="btn btn--sm">Rename</button></div></form>`)
	var folders [][2]string
	a.driveFolders(rd, 0, "", 0, &folders)
	b.WriteString(`<form class="cal-form"` + post + `><input type="hidden" name="op" value="move"><input type="hidden" name="id" value="` + id + `">` +
		`<label class="field"><span class="field-label">Move to</span><select name="to"><option value="0">My files</option>`)
	for _, f := range folders {
		if f[0] == id {
			continue
		}
		sel := ""
		if f[0] == strconv.FormatInt(d.Parent, 10) {
			sel = " selected"
		}
		b.WriteString(`<option value="` + f[0] + `"` + sel + `>` + esc(f[1]) + `</option>`)
	}
	b.WriteString(`</select></label><div class="cal-form__acts"><button type="submit" class="btn btn--sm">Move</button></div></form>`)
	b.WriteString(`<div class="cal-detail__delete"><button type="button" class="btn btn--sm btn--danger"` + post + hxVals("op", "trash", "id", id) + `>Move to Trash</button></div></div>`)
	return b.String()
}

// driveEnds are the ends a link is offered, in days; 0 is until stopped.
var driveEnds = [][2]string{{"1", "In a day"}, {"7", "In 7 days"}, {"30", "In 30 days"}, {"0", "When I stop it"}}

// driveShareSection is a file's links and the form that makes one. A link's
// address is shown once, when it is made (the action's answer): only its
// hash is kept, so this list cannot show it again.
func (a *App) driveShareSection(rd vmail.Reader, d vmail.DriveEntry, home *time.Location, post string) string {
	id := strconv.FormatInt(d.ID, 10)
	var b strings.Builder
	b.WriteString(`<h3 class="cal-booking__h">Share by link</h3>`)
	if d.Sealed {
		// Said before the link is made: the copy is readable on the server's
		// disk, which the sealed file is not.
		b.WriteString(`<p class="cal-note">This file is sealed to your key. A link needs a copy that is not sealed, kept on the server until the link ends or you stop it.</p>`)
	}
	if links, _ := a.vayuMail.DriveShares(rd, d.ID); len(links) > 0 {
		b.WriteString(`<ul class="drive-versions">`)
		for _, l := range links {
			what := "Made " + l.Created.In(home).Format("2 January")
			if !l.Expires.IsZero() {
				what += " · ends " + l.Expires.In(home).Format("2 January, 15:04")
			}
			if l.Most > 0 {
				what += " · " + strconv.Itoa(l.Downloads) + " of " + strconv.Itoa(l.Most) + " downloads"
			} else {
				what += " · " + strconv.Itoa(l.Downloads) + " " + pluralWord(l.Downloads, "download", "downloads")
			}
			if l.Password {
				what += " · password"
			}
			if len(l.Recent) > 0 {
				what += " · last downloaded " + l.Recent[0].In(home).Format("2 January, 15:04")
			}
			b.WriteString(`<li><span>` + esc(what) + `</span><button type="button" class="btn btn--sm"` + post + hxVals("op", "unshare", "id", id, "share", strconv.FormatInt(l.ID, 10)) + `>Stop</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<form class="cal-form"` + post + `><input type="hidden" name="op" value="share"><input type="hidden" name="id" value="` + id + `">` +
		`<label class="field"><span class="field-label">Ends</span><select name="ends">`)
	for _, e := range driveEnds {
		sel := ""
		if e[0] == "7" {
			sel = " selected"
		}
		b.WriteString(`<option value="` + e[0] + `"` + sel + `>` + e[1] + `</option>`)
	}
	b.WriteString(`</select></label>` +
		`<label class="field"><span class="field-label">Password (optional)</span><input type="text" name="password" minlength="` + strconv.Itoa(vmail.ShareMinPassword) + `" maxlength="256" autocomplete="off"></label>` +
		`<label class="field"><span class="field-label">Most downloads (optional)</span><input type="number" name="most" min="1" max="` + strconv.Itoa(vmail.ShareMaxDownloads) + `"></label>` +
		`<div class="cal-form__acts"><button type="submit" class="btn btn--sm">Make a link</button></div></form>`)
	return b.String()
}

// handleVayuOSDriveAction changes one of the session's own entries.
func (a *App) handleVayuOSDriveAction(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Files open only for their holder.", "")
		return
	}
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	// Where the page goes back to: the folder the entry is in, read before
	// the change, since a deleted entry has no folder to ask for.
	back := int64(0)
	if path, err := a.vayuMail.DrivePath(rd, id); err == nil && len(path) > 0 {
		back = path[len(path)-1].Parent
	}
	var err error
	trashView := false
	switch r.PostFormValue("op") {
	case "mkdir":
		back, _ = strconv.ParseInt(r.PostFormValue("dir"), 10, 64)
		_, err = a.vayuMail.DriveMkdir(rd, back, r.PostFormValue("name"))
	case "rename":
		err = a.vayuMail.DriveRename(rd, id, r.PostFormValue("name"))
	case "move":
		to, _ := strconv.ParseInt(r.PostFormValue("to"), 10, 64)
		if err = a.vayuMail.DriveMove(rd, id, to); err == nil {
			back = to
		}
	case "version":
		version, _ := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
		if err = a.vayuMail.DriveRestoreVersion(rd, id, version); err == nil {
			// The sheet again, on the file as it now is, not the folder: the
			// holder is choosing between versions and may go back.
			if path, perr := a.vayuMail.DrivePath(rd, id); perr == nil && len(path) > 0 {
				writeOSHTML(w, r, a.driveSheet(rd, path[len(path)-1], a.calendarHome(r), saCallout("ok", "That version is the file again. The one it replaced is kept below.")))
				return
			}
		}
	case "share":
		days, _ := strconv.Atoi(r.PostFormValue("ends"))
		most, _ := strconv.Atoi(r.PostFormValue("most"))
		var token string
		if token, err = a.vayuMail.DriveShareFile(rd, id, r.PostFormValue("password"), time.Duration(days)*24*time.Hour, most); err == nil {
			link := a.oauthBaseURL(r) + "/drive/s/" + token
			said := saCallout("ok", `This link is shown once. Copy it now.<input class="input" type="text" readonly aria-label="The link" value="`+esc(link)+`">`)
			if path, perr := a.vayuMail.DrivePath(rd, id); perr == nil && len(path) > 0 {
				writeOSHTML(w, r, a.driveSheet(rd, path[len(path)-1], a.calendarHome(r), said))
				return
			}
		}
	case "unshare":
		share, _ := strconv.ParseInt(r.PostFormValue("share"), 10, 64)
		if err = a.vayuMail.DriveUnshare(rd, share); err == nil {
			if path, perr := a.vayuMail.DrivePath(rd, id); perr == nil && len(path) > 0 {
				writeOSHTML(w, r, a.driveSheet(rd, path[len(path)-1], a.calendarHome(r), saCallout("ok", "That link is stopped.")))
				return
			}
		}
	case "settings":
		versions, _ := strconv.Atoi(r.PostFormValue("versions"))
		days, _ := strconv.Atoi(r.PostFormValue("trash_days"))
		msg := saCallout("ok", "Saved.")
		if serr := a.vayuMail.SetDriveSettings(rd, vmail.DriveSettings{Versions: versions, TrashDays: days}); serr != nil {
			msg = saCallout("danger", esc(driveRefusal(serr))+".")
		}
		writeOSHTML(w, r, a.driveSettingsSheet(rd, msg))
		return
	case "empty":
		_, err = a.vayuMail.DriveEmptyTrash(rd)
		trashView = true
	case "trash":
		err = a.vayuMail.DriveTrash(rd, id, false)
	case "restore":
		err, trashView = a.vayuMail.DriveTrash(rd, id, true), true
	case "delete":
		err, trashView = a.vayuMail.DriveDelete(rd, id), true
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown operation", "")
		return
	}
	if err != nil {
		msg := saCallout("danger", esc(driveRefusal(err))+".")
		if path, perr := a.vayuMail.DrivePath(rd, id); perr == nil && len(path) > 0 {
			writeOSHTML(w, r, a.driveSheet(rd, path[len(path)-1], a.calendarHome(r), msg))
			return
		}
		writeOSHTML(w, r, msg)
		return
	}
	to := driveHref(back)
	if trashView {
		to = "/os/vayumail/drive?view=trash"
	}
	w.Header().Set("HX-Redirect", to)
	writeOSHTML(w, r, "")
}

// handleVayuOSDriveUpload takes one file into a folder. The body is bounded
// before it is read: the engine's own limit is the rule, and this stops a
// body far past it being received at all.
func (a *App) handleVayuOSDriveUpload(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Files open only for their holder.", "")
		return
	}
	// The server's deadlines suit a page, not a hundred megabytes; a signed-in
	// holder's transfer gets driveTransferFor, which still ends a stalled one.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(driveTransferFor))
	_ = rc.SetWriteDeadline(time.Now().Add(driveTransferFor))
	r.Body = http.MaxBytesReader(w, r.Body, 101<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "an upload is a file", "")
		return
	}
	dir, _ := strconv.ParseInt(r.URL.Query().Get("dir"), 10, 64)
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-request", "an upload is a file", "")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		// The part is streamed to the store, never held in memory or in a
		// temporary file of the server's choosing.
		if _, err := a.vayuMail.DrivePut(rd, dir, part.FileName(), part.Header.Get("Content-Type"), part); err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, vmail.ErrDriveFull), errors.Is(err, vmail.ErrDriveTooLarge):
				status = http.StatusRequestEntityTooLarge
			case errors.Is(err, vmail.ErrDriveBusy):
				status = http.StatusTooManyRequests
			}
			writeAPIError(w, r, status, "not-kept", driveRefusal(err), "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
}

// handleVayuOSDriveFile sends a file: a download, or with show=1 one of
// drivePictures drawn in place.
func (a *App) handleVayuOSDriveFile(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	var d vmail.DriveEntry
	var f io.ReadSeekCloser
	var err error
	if v := r.URL.Query().Get("version"); v != "" {
		// An earlier version, read without being made the file again.
		version, _ := strconv.ParseInt(v, 10, 64)
		d, f, err = a.vayuMail.DriveVersionOpen(rd, id, version)
	} else {
		d, f, err = a.vayuMail.DriveOpen(rd, id)
	}
	if err != nil {
		if errors.Is(err, vmail.ErrDriveLocked) {
			http.Error(w, "This file is sealed. Unlock your mailbox in Mail to open it.", http.StatusLocked)
			return
		}
		if errors.Is(err, vmail.ErrDriveBusy) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "Two of your sealed files are being opened already. Try again when one is done.", http.StatusTooManyRequests)
			return
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(driveTransferFor))
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	// Whatever the file is, it runs nothing and loads nothing from here.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	ctype, disposition := "application/octet-stream", "attachment"
	if pic := drivePictureType(d.Name); pic != "" && r.URL.Query().Get("show") == "1" {
		ctype, disposition = pic, "inline"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", disposition+`; filename="`+mailSafeFilename(d.Name)+`"`)
	http.ServeContent(w, r, "", time.Time{}, f)
}

// handleVayuOSDriveSave keeps one of a message's attachments in the
// holder's Drive: the message is read as the reader reads it (opened, when
// it is sealed or encrypted), and the file is kept under its own name, or
// that name with a number when it is taken. Only for the session's own
// mailbox: an administrator reading someone's mail has no Drive of theirs
// to put it in.
func (a *App) handleVayuOSDriveSave(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Files open only for their holder.", "")
		return
	}
	_ = r.ParseForm()
	folder, id := sanitizeMailFolder(strings.TrimSpace(r.PostFormValue("folder"))), sanitizeMailID(strings.TrimSpace(r.PostFormValue("id")))
	idx, _ := strconv.Atoi(r.PostFormValue("idx"))
	said := func(tone, text string) {
		writeOSHTML(w, r, `<span class="mx-file__open" role="status" data-tone="`+tone+`">`+text+`</span>`)
	}
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if err != nil || id == "" {
		said("danger", "Not saved: the message is not here.")
		return
	}
	name, ctype, data, found := vmail.ExtractAttachment(raw, idx)
	if !found {
		said("danger", "Not saved: the message has no such file.")
		return
	}
	if _, _, err := a.vayuMail.DriveKeepNew(rd, 0, mailSafeFilename(name), ctype, bytes.NewReader(data)); err != nil {
		said("danger", "Not saved: "+esc(driveRefusal(err))+".")
		return
	}
	writeOSHTML(w, r, `<a class="mx-file__open" href="/os/vayumail/drive">Saved to Drive</a>`)
}

// handleVayuOSDrivePick is a folder of the holder's Drive as the composer's
// From Drive menu shows it: its folders to go into, its files to attach.
// admin-os-mail.js fetches a chosen file and adds it to the message as it
// adds one chosen from the computer, so the send path takes one kind of
// attachment and its size limit is one rule.
func (a *App) handleVayuOSDrivePick(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Files open only for their holder.", "")
		return
	}
	dir, _ := strconv.ParseInt(r.URL.Query().Get("dir"), 10, 64)
	entries, err := a.vayuMail.DriveList(rd, dir)
	if err != nil {
		// A folder since deleted or trashed: the top, not an empty menu.
		dir = 0
		entries, _ = a.vayuMail.DriveList(rd, 0)
	}
	into := func(id int64, label string) string {
		return `<li><button type="button" class="mx-compose__opt" hx-get="/os/vayumail/drive/pick?dir=` + strconv.FormatInt(id, 10) + `" hx-target="closest [data-c-drive-list]" hx-swap="outerHTML">` + saIcon("folder") + `<span>` + label + `</span></button></li>`
	}
	var b strings.Builder
	b.WriteString(`<ul class="mx-compose__tpls" data-c-drive-list aria-label="Files in Drive">`)
	if dir != 0 {
		up := int64(0)
		if path, perr := a.vayuMail.DrivePath(rd, dir); perr == nil && len(path) > 0 {
			up = path[len(path)-1].Parent
		}
		b.WriteString(into(up, "Back"))
	}
	limit := int64(composeMaxAttachMB()) << 20
	for _, d := range entries {
		if d.IsDir {
			b.WriteString(into(d.ID, esc(d.Name)))
			continue
		}
		// A file past the message's own limit is shown and not offered:
		// attaching it could only be refused at Send.
		off := ""
		if d.Size > limit {
			off = ` disabled title="Larger than a message takes"`
		}
		b.WriteString(`<li><button type="button" class="mx-compose__opt" data-c-drive-file="` + strconv.FormatInt(d.ID, 10) + `" data-name="` + esc(d.Name) + `"` + off + `>` +
			saIcon("doc") + `<span>` + esc(d.Name) + ` · ` + esc(humanBytes(d.Size)) + `</span></button></li>`)
	}
	if len(entries) == 0 {
		b.WriteString(`<li class="mx-compose__tpl-empty">Nothing is here.</li>`)
	}
	b.WriteString(`</ul>`)
	writeOSHTML(w, r, b.String())
}

// driveSettingsSheet is the holder's choices for their Drive.
func (a *App) driveSettingsSheet(rd vmail.Reader, msg string) string {
	ds, err := a.vayuMail.DriveSettingsOf(rd)
	if err != nil {
		return saCallout("danger", "The settings could not be read.")
	}
	sel := func(name string, cur int, opts []int, word string) string {
		var b strings.Builder
		b.WriteString(`<select name="` + name + `">`)
		for _, o := range opts {
			on := ""
			if o == cur {
				on = " selected"
			}
			b.WriteString(`<option value="` + strconv.Itoa(o) + `"` + on + `>` + strconv.Itoa(o) + ` ` + word + `</option>`)
		}
		return b.String() + `</select>`
	}
	return `<form class="cal-form" data-sheet-title="Drive settings" hx-post="/os/vayumail/drive/action" hx-target="#drive-sheet-body" hx-swap="innerHTML">` + msg +
		`<input type="hidden" name="op" value="settings">` +
		`<label class="field"><span class="field-label">Earlier versions kept of each file</span>` + sel("versions", ds.Versions, []int{1, 3, 5, 10, 20}, "versions") + `</label>` +
		`<label class="field"><span class="field-label">Trash keeps what you delete for</span>` + sel("trash_days", ds.TrashDays, []int{7, 14, 30, 60, 90}, "days") + `</label>` +
		`<p class="cal-note">Fewer versions take effect at once: the older ones past the number are deleted. Versions and Trash count against the mailbox's storage.</p>` +
		`<div class="cal-form__acts"><button type="submit" class="btn btn--primary">Save</button></div></form>`
}
