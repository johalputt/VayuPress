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

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// shareUploadBody bounds one press of an upload link's page, as the plain
// upload in the console is bounded; larger files are the holder's to bring
// in through their own Drive.
const shareUploadBody = 101 << 20

// sharePasswordCard asks for a folder link's password.
func sharePasswordCard(f vmail.SharedFile, said string) string {
	inner := `<div class="login-card"><h1 class="login-title">` + esc(f.Name) + `</h1><p class="login-sub">A folder shared by ` + esc(f.From) + `</p>`
	if said != "" {
		inner += `<div class="login-error" role="alert">` + esc(said) + `</div>`
	}
	return inner + `<form class="login-form" method="POST"><div class="field"><label class="field-label" for="share-password">Password</label>` +
		`<input id="share-password" class="input" type="password" name="password" required autocomplete="off"></div>` +
		`<button class="btn btn--primary" type="submit">Open</button></form></div>`
}

// shareFolderCard is one folder of a folder link.
func shareFolderCard(sf vmail.SharedFolder, password string) string {
	var b strings.Builder
	here := sf.Path[len(sf.Path)-1]
	names := make([]string, len(sf.Path))
	for i, d := range sf.Path {
		names[i] = d.Name
	}
	b.WriteString(`<div class="login-card"><h1 class="login-title">` + esc(here.Name) + `</h1><p class="login-sub">` +
		esc(strings.Join(names, " / ")) + `, shared by ` + esc(sf.Link.From) + `</p><form method="POST">`)
	if password != "" {
		b.WriteString(`<input type="hidden" name="password" value="` + esc(password) + `">`)
	}
	b.WriteString(`<ul class="drive-versions">`)
	if len(sf.Path) > 1 {
		up := sf.Path[len(sf.Path)-2]
		b.WriteString(`<li><span>Back to ` + esc(up.Name) + `</span><button class="btn btn--sm" type="submit" name="dir" value="` + strconv.FormatInt(up.ID, 10) + `">Open</button></li>`)
	}
	for _, d := range sf.Entries {
		id := strconv.FormatInt(d.ID, 10)
		if d.IsDir {
			b.WriteString(`<li><span>` + esc(d.Name) + `/</span><button class="btn btn--sm" type="submit" name="dir" value="` + id + `">Open</button></li>`)
			continue
		}
		b.WriteString(`<li><span>` + esc(d.Name) + ` · ` + esc(humanBytes(d.Size)) + `</span><button class="btn btn--sm" type="submit" name="file" value="` + id + `">Download</button></li>`)
	}
	if len(sf.Entries) == 0 {
		b.WriteString(`<li><span>Nothing here yet.</span></li>`)
	}
	b.WriteString(`</ul></form></div>`)
	return b.String()
}

// shareUploadCard is an upload link's page, with what the last press kept
// or why it did not.
func shareUploadCard(f vmail.SharedFile, kept []string, said string) string {
	inner := `<div class="login-card"><h1 class="login-title">Send files to ` + esc(f.From) + `</h1><p class="login-sub">They go into the folder ` + esc(f.Name) + `. You will not see what else is in it.</p>`
	if len(kept) > 0 {
		inner += `<div class="login-sub" role="status">Received: ` + esc(strings.Join(kept, ", ")) + `.</div>`
	}
	if said != "" {
		inner += `<div class="login-error" role="alert">` + esc(said) + `</div>`
	}
	inner += `<form class="login-form" method="POST" enctype="multipart/form-data">`
	if f.Password {
		// Before the files, so the password is read before any file is.
		inner += `<div class="field"><label class="field-label" for="share-password">Password</label><input id="share-password" class="input" type="password" name="password" required autocomplete="off"></div>`
	}
	return inner + `<div class="field"><label class="field-label" for="share-files">Files, up to 100 MB at a time</label><input id="share-files" class="input" type="file" name="file" multiple required></div>` +
		`<button class="btn btn--primary" type="submit">Send</button></form></div>`
}

// shareFolderVisit answers a folder link: its page, a folder in it, or one
// of its files.
func (a *App) shareFolderVisit(w http.ResponseWriter, r *http.Request, token string, f vmail.SharedFile) {
	if r.Method != http.MethodPost {
		if f.Password {
			writeGuestPage(w, http.StatusOK, f.Name, sharePasswordCard(f, ""))
			return
		}
		a.shareFolderPage(w, token, "", 0)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = r.ParseForm()
	password := r.PostFormValue("password")
	file, _ := strconv.ParseInt(r.PostFormValue("file"), 10, 64)
	if file == 0 {
		dir, _ := strconv.ParseInt(r.PostFormValue("dir"), 10, 64)
		a.shareFolderPage(w, token, password, dir)
		return
	}
	done, ok := shareSlot(token)
	if !ok {
		shareBusy(w)
		return
	}
	defer done()
	got, body, err := a.vayuMail.DriveSharedFolderOpen(token, password, file)
	if err != nil {
		a.shareFolderRefused(w, token, f, err)
		return
	}
	defer body.Close()
	sendShared(w, got, body)
}

// shareFolderPage is folder dir of a folder link (0 for the shared one).
func (a *App) shareFolderPage(w http.ResponseWriter, token, password string, dir int64) {
	sf, err := a.vayuMail.DriveSharedList(token, password, dir)
	if err != nil {
		f, _ := a.vayuMail.DriveShared(token)
		a.shareFolderRefused(w, token, f, err)
		return
	}
	writeGuestPage(w, http.StatusOK, sf.Path[len(sf.Path)-1].Name, shareFolderCard(sf, password))
}

// shareFolderRefused says why a folder link gave nothing: its password
// asked again, a folder or file no longer in it, or the link gone.
func (a *App) shareFolderRefused(w http.ResponseWriter, token string, f vmail.SharedFile, err error) {
	switch {
	case errors.Is(err, vmail.ErrSharePassword):
		// The page again, unless that was the wrong password that closed it.
		if again, lerr := a.vayuMail.DriveShared(token); lerr == nil {
			writeGuestPage(w, http.StatusForbidden, again.Name, sharePasswordCard(again, "That is not the password for this link."))
			return
		}
	case errors.Is(err, vmail.ErrDriveNotFound):
		writeGuestPage(w, http.StatusNotFound, f.Name, `<div class="login-card"><h1 class="login-title">Not in this folder</h1><p class="login-sub">It may have been moved or deleted. Open the link again to see what is there now.</p></div>`)
		return
	}
	shareGone(w)
}

// shareUploadVisit answers an upload link: its page, or a press that sends
// files. Each file is streamed to the store, never held whole, and each
// counts as one of the link's uses.
func (a *App) shareUploadVisit(w http.ResponseWriter, r *http.Request, token string, f vmail.SharedFile) {
	if r.Method != http.MethodPost {
		writeGuestPage(w, http.StatusOK, f.Name, shareUploadCard(f, nil, ""))
		return
	}
	done, ok := shareSlot(token)
	if !ok {
		shareBusy(w)
		return
	}
	defer done()
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(driveTransferFor))
	r.Body = http.MaxBytesReader(w, r.Body, shareUploadBody)
	mr, err := r.MultipartReader()
	if err != nil {
		writeGuestPage(w, http.StatusBadRequest, f.Name, shareUploadCard(f, nil, "Choose a file to send."))
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
			writeGuestPage(w, http.StatusBadRequest, f.Name, shareUploadCard(f, kept, "The upload did not arrive whole; at most 100 MB at a time."))
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
				a.shareUploadRefused(w, token, f, kept, err)
				return
			}
			kept = append(kept, name)
		}
	}
	if len(kept) == 0 {
		writeGuestPage(w, http.StatusBadRequest, f.Name, shareUploadCard(f, nil, "Choose a file to send."))
		return
	}
	writeGuestPage(w, http.StatusOK, f.Name, shareUploadCard(f, kept, ""))
}

// shareUploadRefused says why an upload link did not keep a file, with
// what it kept before it.
func (a *App) shareUploadRefused(w http.ResponseWriter, token string, f vmail.SharedFile, kept []string, err error) {
	said := ""
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, vmail.ErrSharePassword):
		said, status = "That is not the password for this link.", http.StatusForbidden
	case errors.Is(err, vmail.ErrDriveFull), errors.Is(err, vmail.ErrDriveTooLarge):
		said, status = "There is no room for that file in "+f.From+"'s storage.", http.StatusRequestEntityTooLarge
	case errors.Is(err, vmail.ErrDriveName):
		said = "A file needs a name of at most 255 characters."
	case errors.Is(err, vmail.ErrShareGone):
	default:
		said = "That file was not kept. Try again in a moment."
	}
	// The page again while the link is open; gone, it says so.
	if again, lerr := a.vayuMail.DriveShared(token); lerr == nil && said != "" {
		writeGuestPage(w, status, again.Name, shareUploadCard(again, kept, said))
		return
	}
	shareGone(w)
}
