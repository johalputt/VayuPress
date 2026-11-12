// SPDX-License-Identifier: Apache-2.0

package main

// drive_public.go — a shared file's link, for someone with no account
// (internal/vayuos/mail/drive_share.go).
//
// The page says what the file is and asks; only a press of its button
// hands the file out, so a mail scanner or a link preview opening the
// address uses none of a link's downloads. The file goes out as the
// holder's own downloads do: an attachment that runs and loads nothing,
// never a page of this site.

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A link is open to anyone who has it, with no session to limit them by,
// and a slow reader holds a file open for as long as driveTransferFor. So
// the files being sent at once are bounded twice: shareSlots for the
// install, and sharePerLink for one link, so that a single link passed
// around, or held open on purpose, cannot take every place. Past either a
// press is told the link is busy, before it is counted as a download.
var (
	shareSlots   = make(chan struct{}, 16)
	shareSending = struct {
		sync.Mutex
		n map[string]int
	}{n: map[string]int{}}
)

const sharePerLink = 4

// shareSlot takes a place for one sending of a link's file, or reports
// that there is none. The function it returns gives the place back.
func shareSlot(token string) (func(), bool) {
	shareSending.Lock()
	defer shareSending.Unlock()
	if shareSending.n[token] >= sharePerLink {
		return nil, false
	}
	select {
	case shareSlots <- struct{}{}:
	default:
		return nil, false
	}
	shareSending.n[token]++
	return func() {
		shareSending.Lock()
		defer shareSending.Unlock()
		if shareSending.n[token]--; shareSending.n[token] <= 0 {
			delete(shareSending.n, token)
		}
		<-shareSlots
	}, true
}

func shareGone(w http.ResponseWriter, lang string) {
	writeGuestPage(w, lang, http.StatusNotFound, i18n.T(lang, "Not here"), `<div class="login-card"><h1 class="login-title">`+esc(i18n.T(lang, "This link is not open"))+
		`</h1><p class="login-sub">`+esc(i18n.T(lang, "It may have ended or been stopped. Ask whoever sent it to you."))+`</p></div>`)
}

// sharePasswordField is a link's password field, in lang.
func sharePasswordField(lang string) string {
	return `<div class="field"><label class="field-label" for="share-password">` + esc(i18n.T(lang, "Password")) +
		`</label><input id="share-password" class="input" type="password" name="password" required autocomplete="off"></div>`
}

func shareCard(lang string, f vmail.SharedFile, said string) string {
	inner := `<div class="login-card"><h1 class="login-title">` + esc(f.Name) + `</h1><p class="login-sub">` + esc(i18n.T(lang, "{size}, shared by {who}", "size", humanBytes(f.Size), "who", f.From)) + `</p>`
	if said != "" {
		inner += `<div class="login-error" role="alert">` + esc(said) + `</div>`
	}
	inner += `<form class="login-form" method="POST">`
	if f.Password {
		inner += sharePasswordField(lang)
	}
	return inner + `<button class="btn btn--primary" type="submit">` + esc(i18n.T(lang, "Download")) + `</button></form></div>`
}

// handleDriveShare is GET (the page) and POST (the file) of one link.
func (a *App) handleDriveShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	lang := requestLang(w, r)
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		shareGone(w, lang)
		return
	}
	f, err := a.vayuMail.DriveShared(token)
	if err != nil {
		shareGone(w, lang)
		return
	}
	switch f.Kind {
	case vmail.ShareFolder:
		a.shareFolderVisit(w, r, lang, token, f)
		return
	case vmail.ShareUpload:
		a.shareUploadVisit(w, r, lang, token, f)
		return
	}
	if r.Method != http.MethodPost {
		writeGuestPage(w, lang, http.StatusOK, f.Name, shareCard(lang, f, ""))
		return
	}
	done, ok := shareSlot(token)
	if !ok {
		shareBusy(w, lang)
		return
	}
	defer done()
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = r.ParseForm()
	f, body, err := a.vayuMail.DriveSharedOpen(token, r.PostFormValue("password"))
	if errors.Is(err, vmail.ErrSharePassword) {
		// The page again, unless that was the wrong password that closed it.
		if again, lerr := a.vayuMail.DriveShared(token); lerr == nil {
			writeGuestPage(w, lang, http.StatusForbidden, again.Name, shareCard(lang, again, i18n.T(lang, "That is not the password for this link.")))
			return
		}
	}
	if err != nil {
		shareGone(w, lang)
		return
	}
	defer body.Close()
	sendShared(w, f, body)
}

// shareBusy says a link has no place free for one more sending.
func shareBusy(w http.ResponseWriter, lang string) {
	w.Header().Set("Retry-After", "30")
	writeGuestPage(w, lang, http.StatusServiceUnavailable, i18n.T(lang, "Busy"), `<div class="login-card"><h1 class="login-title">`+esc(i18n.T(lang, "Busy"))+
		`</h1><p class="login-sub">`+esc(i18n.T(lang, "This link is busy. Try again in a moment."))+`</p></div>`)
}

// sendShared sends a file a link handed out.
func sendShared(w http.ResponseWriter, f vmail.SharedFile, body io.Reader) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(driveTransferFor))
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+mailSafeFilename(f.Name)+`"`)
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	_, _ = io.Copy(w, body)
}
