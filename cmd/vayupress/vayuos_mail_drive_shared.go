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

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// driveGrantSection is who an entry is shared with here, and the form that
// shares it with another mailbox.
func (a *App) driveGrantSection(rd vmail.Reader, d vmail.DriveEntry, post string) string {
	id, lang := strconv.FormatInt(d.ID, 10), rd.Lang()
	var b strings.Builder
	b.WriteString(`<h3 class="cal-booking__h">` + esc(i18n.T(lang, "Share with someone here")) + `</h3>`)
	if a.vayuPGP != nil && a.vayuPGP.HolderKeeps(a.calendarAddress(rd)) {
		// No form: the engine refuses it, and the reason is said first.
		b.WriteString(`<p class="cal-note">` + esc(i18n.T(lang, "Your files are sealed to your key, which no other mailbox has, so they are shared with another mailbox only by sending them.")) + `</p>`)
		return b.String()
	}
	if gs, _ := a.vayuMail.DriveGrants(rd, d.ID); len(gs) > 0 {
		b.WriteString(`<ul class="drive-versions">`)
		for _, g := range gs {
			who := i18n.T(lang, "{address} · can read", "address", g.Grantee)
			if g.Write {
				who = i18n.T(lang, "{address} · can add files", "address", g.Grantee)
			}
			b.WriteString(`<li><span>` + esc(who) + `</span><button type="button" class="btn btn--sm"` + post + hxVals("op", "ungrant", "id", id, "grant", strconv.FormatInt(g.ID, 10)) + `>` + esc(i18n.T(lang, "Stop")) + `</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<form class="cal-form"` + post + `><input type="hidden" name="op" value="grant"><input type="hidden" name="id" value="` + id + `">` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Address on this server")) + `</span><input type="text" name="who" required maxlength="320" autocomplete="off"></label>`)
	if d.IsDir {
		// Adding is all writing means here: nothing is deleted or moved
		// from the other side, and a file saved over keeps what it was.
		b.WriteString(`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "They can")) + `</span><select name="write"><option value="0">` + esc(i18n.T(lang, "Read and download")) + `</option>` +
			`<option value="1">` + esc(i18n.T(lang, "Also add files and folders")) + `</option></select></label>`)
	}
	b.WriteString(`<div class="cal-form__acts"><button type="submit" class="btn btn--sm">` + esc(i18n.T(lang, "Share")) + `</button></div></form>`)
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
	home, lang := a.calendarHome(r), i18n.Lang(r.Context())
	shared := esc(i18n.T(lang, "Shared with me"))
	if grant == 0 {
		b.WriteString(`<div class="drive-head"><h1 class="drive-crumbs">` + shared + `</h1></div>`)
		gs, err := a.vayuMail.DriveSharedWithMe(rd)
		if err != nil {
			b.WriteString(saCallout("danger", esc(i18n.T(lang, "What is shared with you could not be read: {reason}.", "reason", driveRefusal(err)))))
		}
		if len(gs) == 0 && err == nil {
			b.WriteString(`<p class="drive-empty">` + esc(i18n.T(lang, "Nothing is shared with you yet. When someone on this server shares a file or folder with your address, it is here.")) + `</p>`)
			return b.String(), false, true
		}
		b.WriteString(`<ul class="drive-list drive-list--shared">`)
		for _, g := range gs {
			gid := strconv.FormatInt(g.ID, 10)
			b.WriteString(`<li class="drive-row">`)
			if g.Entry.IsDir {
				b.WriteString(`<a class="drive-row__name" href="` + esc(sharedHref(g.ID, 0)) + `">` + saIcon("folder") + `<span>` + esc(g.Entry.Name) + `</span></a>`)
			} else {
				b.WriteString(`<a class="drive-row__name" href="` + esc(sharedFileHref(g.ID, g.Entry.ID)) + `" download><span class="drive-kind" aria-hidden="true">` + esc(driveKind(g.Entry)) + `</span><span>` + esc(g.Entry.Name) + `</span></a>`)
			}
			who := i18n.T(lang, "{address} · can read", "address", g.Owner)
			if g.Write {
				who = i18n.T(lang, "{address} · can add files", "address", g.Owner)
			}
			b.WriteString(`<span class="drive-row__meta" title="` + esc(who) + `">` + esc(who) + `</span><span class="drive-row__meta">` + esc(i18n.Date(lang, g.Created.In(home), i18n.DayMonthYearShort)) + `</span>` +
				`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/vayumail/drive/action"` + hxVals("op", "leave", "grant", gid) +
				` hx-confirm="` + esc(i18n.T(lang, "Take this off your list? Its holder sees it is no longer shared with you.")) + `">` + esc(i18n.T(lang, "Leave")) + `</button></li>`)
		}
		b.WriteString(`</ul>`)
		return b.String(), false, true
	}
	g, path, list, err := a.vayuMail.DriveGrantList(rd, grant, dir)
	if err != nil {
		return "", false, false
	}
	b.WriteString(`<div class="drive-head"><h1 class="drive-crumbs"><a href="` + esc(sharedHref(0, 0)) + `">` + shared + `</a>`)
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
			`" hx-target="#drive-sheet-body" hx-swap="innerHTML">` + saIcon("plus") + esc(i18n.T(lang, "New folder")) + `</button>`)
	}
	b.WriteString(`</div><div id="drive-status" class="drive-status" role="status" aria-live="polite"></div>`)
	note := i18n.T(lang, "Shared by {address}. You can read and download what is here.", "address", g.Owner)
	if g.Write {
		note = i18n.T(lang, "Shared by {address}. You can add files and folders; a file of a name already here is saved over, and its holder keeps what it was.", "address", g.Owner)
	}
	b.WriteString(`<p class="cal-note">` + esc(note) + `</p>`)
	if len(list) == 0 {
		b.WriteString(`<p class="drive-empty">` + esc(i18n.T(lang, "This folder is empty.")) + `</p>`)
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
		b.WriteString(`<span class="drive-row__meta">` + esc(size) + `</span><span class="drive-row__meta">` + esc(i18n.Date(lang, d.Modified.In(home), i18n.DayMonthYearShort)) + `</span></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String(), g.Write, true
}
