// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_drive_upload.go — Drive's uploads in pieces (internal/vayuos/
// mail/drive_upload.go) as the page sends them: open one, send its pieces,
// ask where it stands after a dropped connection, or stop it. Answers are
// JSON for admin-os-drive.js; a refusal carries the server's own words.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// uploadPieceBytes is the piece the page sends, as the engine takes it.
const uploadPieceBytes = 8 << 20

type uploadAnswer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Received int64  `json:"received"`
	File     int64  `json:"file,omitempty"`
	Piece    int    `json:"piece"`
}

func answerUpload(w http.ResponseWriter, r *http.Request, u vmail.DriveUpload) {
	writeJSON(w, r, http.StatusOK, uploadAnswer{ID: u.ID, Name: u.Name, Size: u.Size, Received: u.Received, File: u.File, Piece: uploadPieceBytes})
}

// uploadRefusal is an engine's refusal as an answer: its status, its code
// and the words the page shows. Where an upload stands goes with a refusal
// of a piece, so the page can go on from there.
func uploadRefusal(w http.ResponseWriter, r *http.Request, err error, u vmail.DriveUpload) {
	status, code := http.StatusBadRequest, "not-kept"
	switch {
	case errors.Is(err, vmail.ErrUploadOffset):
		writeJSON(w, r, http.StatusConflict, map[string]any{"error": map[string]string{"code": "offset", "message": driveRefusal(err)}, "received": u.Received})
		return
	case errors.Is(err, vmail.ErrUploadGone), errors.Is(err, vmail.ErrDriveNotFound):
		status, code = http.StatusNotFound, "gone"
	case errors.Is(err, vmail.ErrDriveFull), errors.Is(err, vmail.ErrDriveTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, vmail.ErrUploadBusy), errors.Is(err, vmail.ErrUploadsOpen), errors.Is(err, vmail.ErrDriveBusy):
		status = http.StatusTooManyRequests
	case errors.Is(err, vmail.ErrDriveLocked):
		status = http.StatusLocked
	}
	writeAPIError(w, r, status, code, driveRefusal(err), "")
}

// handleVayuOSDriveUploadStart opens an upload: dir, name, size, type.
func (a *App) handleVayuOSDriveUploadStart(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Files open only for their holder."), "")
		return
	}
	_ = r.ParseForm()
	dir, _ := strconv.ParseInt(r.PostFormValue("dir"), 10, 64)
	size, err := strconv.ParseInt(r.PostFormValue("size"), 10, 64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "an upload says how large its file is", "")
		return
	}
	var u vmail.DriveUpload
	if grant, _ := strconv.ParseInt(r.PostFormValue("grant"), 10, 64); grant != 0 {
		// Into a folder another mailbox here shares with this one to write.
		u, err = a.vayuMail.DriveGrantUploadStart(rd, grant, dir, r.PostFormValue("name"), r.PostFormValue("type"), size, time.Now())
	} else {
		u, err = a.vayuMail.DriveUploadStart(rd, dir, r.PostFormValue("name"), r.PostFormValue("type"), size, time.Now())
	}
	if err != nil {
		uploadRefusal(w, r, err, u)
		return
	}
	answerUpload(w, r, u)
}

// handleVayuOSDriveUploadState is where an upload stands.
func (a *App) handleVayuOSDriveUploadState(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Files open only for their holder."), "")
		return
	}
	u, err := a.vayuMail.DriveUploadState(rd, chi.URLParam(r, "id"), time.Now())
	if err != nil {
		uploadRefusal(w, r, err, u)
		return
	}
	answerUpload(w, r, u)
}

// handleVayuOSDriveUploadPiece keeps the piece at ?offset=.
func (a *App) handleVayuOSDriveUploadPiece(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Files open only for their holder."), "")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "a piece says where it goes", "")
		return
	}
	// A piece is bounded where it is read too: the engine reads one byte
	// past a piece to refuse it, and nothing more.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(driveTransferFor))
	r.Body = http.MaxBytesReader(w, r.Body, uploadPieceBytes+1)
	u, err := a.vayuMail.DriveUploadPiece(rd, chi.URLParam(r, "id"), offset, r.Body, time.Now())
	if err != nil {
		uploadRefusal(w, r, err, u)
		return
	}
	answerUpload(w, r, u)
}

// handleVayuOSDriveUploadStop ends an upload and what came of it.
func (a *App) handleVayuOSDriveUploadStop(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.driveOwner(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Files open only for their holder."), "")
		return
	}
	if err := a.vayuMail.DriveUploadStop(rd, chi.URLParam(r, "id"), time.Now()); err != nil {
		uploadRefusal(w, r, err, vmail.DriveUpload{})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
