// SPDX-License-Identifier: Apache-2.0

package main

// drive_public_folder.go — a folder's link, for someone with no account
// (internal/vayuos/mail/drive_share_folder.go): to look through the folder
// and download from it, or only to upload into it.
//
// The folder's page is one form: each folder is a button that opens it,
// each file a button that downloads it, and a link's password, once given,
// rides along as a field, so nothing of the visit is kept on the server or
// in a cookie. Opening folders hands out nothing and is not counted; each
// file is one of the link's downloads.
//
// The upload page shows nothing of the folder: a file chooser and, after a
// press, the names the files were kept under.

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// shareUploadBody bounds one press of an upload link's page, as the plain
// upload in the console is bounded; larger files are the holder's to bring
// in through their own Drive.
const shareUploadBody = 101 << 20

// sharePasswordCard asks for a folder link's password.
func sharePasswordCard(lang string, f vmail.SharedFile, said string) string {
	inner := `<div class="login-card"><h1 class="login-title">` + esc(f.Name) + `</h1><p class="login-sub">` + esc(i18n.T(lang, "A folder shared by {who}", "who", f.From)) + `</p>`
	if said != "" {
		inner += `<div class="login-error" role="alert">` + esc(said) + `</div>`
	}
	return inner + `<form class="login-form" method="POST">` + sharePasswordField(lang) +
		`<button class="btn btn--primary" type="submit">` + esc(i18n.T(lang, "Open")) + `</button></form></div>`
}

// shareFolderCard is one folder of a folder link.
func shareFolderCard(lang string, sf vmail.SharedFolder, password string) string {
	var b strings.Builder
	here := sf.Path[len(sf.Path)-1]
	names := make([]string, len(sf.Path))
	for i, d := range sf.Path {
		names[i] = d.Name
	}
	b.WriteString(`<div class="login-card"><h1 class="login-title">` + esc(here.Name) + `</h1><p class="login-sub">` +
		esc(i18n.T(lang, "{path}, shared by {who}", "path", strings.Join(names, " / "), "who", sf.Link.From)) + `</p><form method="POST">`)
	if password != "" {
		b.WriteString(`<input type="hidden" name="password" value="` + esc(password) + `">`)
	}
	b.WriteString(`<ul class="drive-versions">`)
	if len(sf.Path) > 1 {
		up := sf.Path[len(sf.Path)-2]
		b.WriteString(`<li><span>` + esc(i18n.T(lang, "Back to {folder}", "folder", up.Name)) + `</span><button class="btn btn--sm" type="submit" name="dir" value="` + strconv.FormatInt(up.ID, 10) + `">` + esc(i18n.T(lang, "Open")) + `</button></li>`)
	}
	for _, d := range sf.Entries {
		id := strconv.FormatInt(d.ID, 10)
		if d.IsDir {
			b.WriteString(`<li><span>` + esc(d.Name) + `/</span><button class="btn btn--sm" type="submit" name="dir" value="` + id + `">` + esc(i18n.T(lang, "Open")) + `</button></li>`)
			continue
		}
		b.WriteString(`<li><span>` + esc(d.Name) + ` · ` + esc(humanBytes(d.Size)) + `</span><button class="btn btn--sm" type="submit" name="file" value="` + id + `">` + esc(i18n.T(lang, "Download")) + `</button></li>`)
	}
	if len(sf.Entries) == 0 {
		b.WriteString(`<li><span>` + esc(i18n.T(lang, "Nothing here yet.")) + `</span></li>`)
	}
	b.WriteString(`</ul></form></div>`)
	return b.String()
}

// shareUploadCard is an upload link's page, with what the last press kept
// or why it did not.
func shareUploadCard(lang string, f vmail.SharedFile, kept []string, said string) string {
	inner := `<div class="login-card"><h1 class="login-title">` + esc(i18n.T(lang, "Send files to {who}", "who", f.From)) + `</h1><p class="login-sub">` +
		esc(i18n.T(lang, "They go into the folder {folder}. You will not see what else is in it.", "folder", f.Name)) + `</p>`
	if len(kept) > 0 {
		inner += `<div class="login-sub" role="status">` + esc(i18n.T(lang, "Received: {names}.", "names", strings.Join(kept, ", "))) + `</div>`
	}
	if said != "" {
		inner += `<div class="login-error" role="alert">` + esc(said) + `</div>`
	}
	inner += `<form class="login-form" method="POST" enctype="multipart/form-data">`
	if f.Password {
		// Before the files, so the password is read before any file is.
		inner += sharePasswordField(lang)
	}
	return inner + `<div class="field"><label class="field-label" for="share-files">` + esc(i18n.T(lang, "Files, up to 100 MB at a time")) + `</label><input id="share-files" class="input" type="file" name="file" multiple required></div>` +
		`<button class="btn btn--primary" type="submit">` + esc(i18n.T(lang, "Send")) + `</button></form></div>`
}

// shareFolderVisit answers a folder link: its page, a folder in it, or one
// of its files.
func (a *App) shareFolderVisit(w http.ResponseWriter, r *http.Request, lang, token string, f vmail.SharedFile) {
	if r.Method != http.MethodPost {
		if f.Password {
			writeGuestPage(w, r, lang, http.StatusOK, f.Name, sharePasswordCard(lang, f, ""))
			return
		}
		a.shareFolderPage(w, r, lang, token, "", 0)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = r.ParseForm()
	password := r.PostFormValue("password")
	file, _ := strconv.ParseInt(r.PostFormValue("file"), 10, 64)
	if file == 0 {
		dir, _ := strconv.ParseInt(r.PostFormValue("dir"), 10, 64)
		a.shareFolderPage(w, r, lang, token, password, dir)
		return
	}
	done, ok := shareSlot(token)
	if !ok {
		shareBusy(w, r, lang)
		return
	}
	defer done()
	got, body, err := a.vayuMail.DriveSharedFolderOpen(token, password, file)
	if err != nil {
		a.shareFolderRefused(w, r, lang, token, f, err)
		return
	}
	defer body.Close()
	sendShared(w, got, body)
}

// shareFolderPage is folder dir of a folder link (0 for the shared one).
func (a *App) shareFolderPage(w http.ResponseWriter, r *http.Request, lang, token, password string, dir int64) {
	sf, err := a.vayuMail.DriveSharedList(token, password, dir)
	if err != nil {
		f, _ := a.vayuMail.DriveShared(token)
		a.shareFolderRefused(w, r, lang, token, f, err)
		return
	}
	writeGuestPage(w, r, lang, http.StatusOK, sf.Path[len(sf.Path)-1].Name, shareFolderCard(lang, sf, password))
}

// shareFolderRefused says why a folder link gave nothing: its password
// asked again, a folder or file no longer in it, or the link gone.
func (a *App) shareFolderRefused(w http.ResponseWriter, r *http.Request, lang, token string, f vmail.SharedFile, err error) {
	switch {
	case errors.Is(err, vmail.ErrSharePassword):
		// The page again, unless that was the wrong password that closed it.
		if again, lerr := a.vayuMail.DriveShared(token); lerr == nil {
			writeGuestPage(w, r, lang, http.StatusForbidden, again.Name, sharePasswordCard(lang, again, i18n.T(lang, "That is not the password for this link.")))
			return
		}
	case errors.Is(err, vmail.ErrDriveNotFound):
		writeGuestPage(w, r, lang, http.StatusNotFound, f.Name, `<div class="login-card"><h1 class="login-title">`+esc(i18n.T(lang, "Not in this folder"))+
			`</h1><p class="login-sub">`+esc(i18n.T(lang, "It may have been moved or deleted. Open the link again to see what is there now."))+`</p></div>`)
		return
	}
	shareGone(w, r, lang)
}

// shareUploadVisit answers an upload link: its page, or a press that sends
// files. Each file is streamed to the store, never held whole, and each
// counts as one of the link's uses.
func (a *App) shareUploadVisit(w http.ResponseWriter, r *http.Request, lang, token string, f vmail.SharedFile) {
	if r.Method != http.MethodPost {
		writeGuestPage(w, r, lang, http.StatusOK, f.Name, shareUploadCard(lang, f, nil, ""))
		return
	}
	done, ok := shareSlot(token)
	if !ok {
		shareBusy(w, r, lang)
		return
	}
	defer done()
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(driveTransferFor))
	r.Body = http.MaxBytesReader(w, r.Body, shareUploadBody)
	mr, err := r.MultipartReader()
	if err != nil {
		writeGuestPage(w, r, lang, http.StatusBadRequest, f.Name, shareUploadCard(lang, f, nil, i18n.T(lang, "Choose a file to send.")))
		return
	}
	var kept []string
	password := ""
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeGuestPage(w, r, lang, http.StatusBadRequest, f.Name, shareUploadCard(lang, f, kept, i18n.T(lang, "The upload did not arrive whole; at most 100 MB at a time.")))
			return
		}
		switch part.FormName() {
		case "password":
			b, _ := io.ReadAll(io.LimitReader(part, 257))
			password = string(b)
		case "file":
			if part.FileName() == "" {
				continue
			}
			name, err := a.vayuMail.DriveSharedUpload(token, password, part.FileName(), part.Header.Get("Content-Type"), part)
			if err != nil {
				a.shareUploadRefused(w, r, lang, token, f, kept, err)
				return
			}
			kept = append(kept, name)
		}
	}
	if len(kept) == 0 {
		writeGuestPage(w, r, lang, http.StatusBadRequest, f.Name, shareUploadCard(lang, f, nil, i18n.T(lang, "Choose a file to send.")))
		return
	}
	writeGuestPage(w, r, lang, http.StatusOK, f.Name, shareUploadCard(lang, f, kept, ""))
}

// shareUploadRefused says why an upload link did not keep a file, with
// what it kept before it.
func (a *App) shareUploadRefused(w http.ResponseWriter, r *http.Request, lang, token string, f vmail.SharedFile, kept []string, err error) {
	said := ""
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, vmail.ErrSharePassword):
		said, status = i18n.T(lang, "That is not the password for this link."), http.StatusForbidden
	case errors.Is(err, vmail.ErrDriveFull), errors.Is(err, vmail.ErrDriveTooLarge):
		said, status = i18n.T(lang, "There is no room for that file in the storage of {who}.", "who", f.From), http.StatusRequestEntityTooLarge
	case errors.Is(err, vmail.ErrDriveName):
		said = i18n.T(lang, "A file needs a name of at most 255 characters.")
	case errors.Is(err, vmail.ErrShareGone):
	default:
		said = i18n.T(lang, "That file was not kept. Try again in a moment.")
	}
	// The page again while the link is open; gone, it says so.
	if again, lerr := a.vayuMail.DriveShared(token); lerr == nil && said != "" {
		writeGuestPage(w, r, lang, status, again.Name, shareUploadCard(lang, again, kept, said))
		return
	}
	shareGone(w, r, lang)
}
