// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_drive_shared.go — files and folders shared between mailboxes
// on this server (internal/vayuos/mail/drive_grant.go): the holder's
// "Share with someone here" on an entry's sheet, and the other mailbox's
// "Shared with me", where a shared folder is opened, its files downloaded,
// and, when it is shared to write, files uploaded and folders made in it.

import (
	"net/http"
	"strconv"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// driveGrantSection is who an entry is shared with here, and the form that
// shares it with another mailbox.
func (a *App) driveGrantSection(rd vmail.Reader, d vmail.DriveEntry, post string) string {
	id := strconv.FormatInt(d.ID, 10)
	var b strings.Builder
	b.WriteString(`<h3 class="cal-booking__h">Share with someone here</h3>`)
	if a.vayuPGP != nil && a.vayuPGP.HolderKeeps(a.calendarAddress(rd)) {
		// No form: the engine refuses it, and the reason is said first.
		b.WriteString(`<p class="cal-note">Your files are sealed to your key, which no other mailbox has, so they are shared with another mailbox only by sending them.</p>`)
		return b.String()
	}
	if gs, _ := a.vayuMail.DriveGrants(rd, d.ID); len(gs) > 0 {
		b.WriteString(`<ul class="drive-versions">`)
		for _, g := range gs {
			may := "can read"
			if g.Write {
				may = "can add files"
			}
			b.WriteString(`<li><span>` + esc(g.Grantee) + ` · ` + may + `</span><button type="button" class="btn btn--sm"` + post + hxVals("op", "ungrant", "id", id, "grant", strconv.FormatInt(g.ID, 10)) + `>Stop</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<form class="cal-form"` + post + `><input type="hidden" name="op" value="grant"><input type="hidden" name="id" value="` + id + `">` +
		`<label class="field"><span class="field-label">Address on this server</span><input type="text" name="who" required maxlength="320" autocomplete="off"></label>`)
	if d.IsDir {
		// Adding is all writing means here: nothing is deleted or moved
		// from the other side, and a file saved over keeps what it was.
		b.WriteString(`<label class="field"><span class="field-label">They can</span><select name="write"><option value="0">Read and download</option>` +
			`<option value="1">Also add files and folders</option></select></label>`)
	}
	b.WriteString(`<div class="cal-form__acts"><button type="submit" class="btn btn--sm">Share</button></div></form>`)
	return b.String()
}

// sharedHref is Shared with me, a share in it, or a folder in a share.
func sharedHref(grant, dir int64) string {
	h := "/os/vayumail/drive?view=shared"
	if grant != 0 {
		h += "&grant=" + strconv.FormatInt(grant, 10)
	}
	if dir != 0 {
		h += "&dir=" + strconv.FormatInt(dir, 10)
	}
	return h
}

func sharedFileHref(grant, id int64) string {
	return "/os/vayumail/drive/file?grant=" + strconv.FormatInt(grant, 10) + "&id=" + strconv.FormatInt(id, 10)
}

// driveSharedBody is the Shared with me view: what is shared with this
// mailbox, or, with grant, a folder of one share. It answers the page's
// body, and whether files may be uploaded into the folder shown.
func (a *App) driveSharedBody(r *http.Request, rd vmail.Reader, grant, dir int64) (string, bool, bool) {
	var b strings.Builder
	home := a.calendarHome(r)
	if grant == 0 {
		b.WriteString(`<div class="drive-head"><h1 class="drive-crumbs">Shared with me</h1></div>`)
		gs, err := a.vayuMail.DriveSharedWithMe(rd)
		if err != nil {
			b.WriteString(saCallout("danger", "What is shared with you could not be read: "+esc(driveRefusal(err))+"."))
		}
		if len(gs) == 0 && err == nil {
			b.WriteString(`<p class="drive-empty">Nothing is shared with you yet. When someone on this server shares a file or folder with your address, it is here.</p>`)
			return b.String(), false, true
		}
		b.WriteString(`<ul class="drive-list">`)
		for _, g := range gs {
			gid := strconv.FormatInt(g.ID, 10)
			b.WriteString(`<li class="drive-row">`)
			if g.Entry.IsDir {
				b.WriteString(`<a class="drive-row__name" href="` + esc(sharedHref(g.ID, 0)) + `">` + saIcon("folder") + `<span>` + esc(g.Entry.Name) + `</span></a>`)
			} else {
				b.WriteString(`<a class="drive-row__name" href="` + esc(sharedFileHref(g.ID, g.Entry.ID)) + `" download><span class="drive-kind" aria-hidden="true">` + esc(driveKind(g.Entry)) + `</span><span>` + esc(g.Entry.Name) + `</span></a>`)
			}
			may := "can read"
			if g.Write {
				may = "can add files"
			}
			b.WriteString(`<span class="drive-row__meta">` + esc(g.Owner+" · "+may) + `</span><span class="drive-row__meta">` + esc(g.Created.In(home).Format("2 Jan 2006")) + `</span>` +
				`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/vayumail/drive/action"` + hxVals("op", "leave", "grant", gid) +
				` hx-confirm="Take this off your list? Its holder sees it is no longer shared with you.">Leave</button></li>`)
		}
		b.WriteString(`</ul>`)
		return b.String(), false, true
	}
	g, path, list, err := a.vayuMail.DriveGrantList(rd, grant, dir)
	if err != nil {
		return "", false, false
	}
	b.WriteString(`<div class="drive-head"><h1 class="drive-crumbs"><a href="` + esc(sharedHref(0, 0)) + `">Shared with me</a>`)
	for i, p := range path {
		b.WriteString(saIcon("chev-r"))
		if i == len(path)-1 {
			b.WriteString(`<span>` + esc(p.Name) + `</span>`)
		} else {
			b.WriteString(`<a href="` + esc(sharedHref(grant, p.ID)) + `">` + esc(p.Name) + `</a>`)
		}
	}
	b.WriteString(`</h1>`)
	at := path[len(path)-1].ID
	if g.Write {
		b.WriteString(`<button type="button" class="btn btn--sm" hx-get="/os/vayumail/drive/entry?new=folder&amp;dir=` + strconv.FormatInt(at, 10) + `&amp;grant=` + strconv.FormatInt(grant, 10) +
			`" hx-target="#drive-sheet-body" hx-swap="innerHTML">` + saIcon("plus") + `New folder</button>`)
	}
	b.WriteString(`</div><div id="drive-status" class="drive-status" role="status" aria-live="polite"></div>`)
	b.WriteString(`<p class="cal-note">Shared by ` + esc(g.Owner) + `. `)
	if g.Write {
		b.WriteString(`You can add files and folders; a file of a name already here is saved over, and its holder keeps what it was.</p>`)
	} else {
		b.WriteString(`You can read and download what is here.</p>`)
	}
	if len(list) == 0 {
		b.WriteString(`<p class="drive-empty">This folder is empty.</p>`)
		return b.String(), g.Write, true
	}
	b.WriteString(`<ul class="drive-list">`)
	for _, d := range list {
		b.WriteString(`<li class="drive-row">`)
		size := ""
		if d.IsDir {
			b.WriteString(`<a class="drive-row__name" href="` + esc(sharedHref(grant, d.ID)) + `">` + saIcon("folder") + `<span>` + esc(d.Name) + `</span></a>`)
		} else {
			size = humanBytes(d.Size)
			b.WriteString(`<a class="drive-row__name" href="` + esc(sharedFileHref(grant, d.ID)) + `" download><span class="drive-kind" aria-hidden="true">` + esc(driveKind(d)) + `</span><span>` + esc(d.Name) + `</span></a>`)
		}
		b.WriteString(`<span class="drive-row__meta">` + esc(size) + `</span><span class="drive-row__meta">` + esc(d.Modified.In(home).Format("2 Jan 2006")) + `</span></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String(), g.Write, true
}
