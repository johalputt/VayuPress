// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/users"
)

func uploadRouter(a *App) http.Handler {
	r := chi.NewRouter()
	r.Post("/os/vayumail/drive/uploads", a.handleVayuOSDriveUploadStart)
	r.Get("/os/vayumail/drive/uploads/{id}", a.handleVayuOSDriveUploadState)
	r.Put("/os/vayumail/drive/uploads/{id}", a.handleVayuOSDriveUploadPiece)
	r.Delete("/os/vayumail/drive/uploads/{id}", a.handleVayuOSDriveUploadStop)
	return r
}

func uploadCall(h http.Handler, u *users.User, method, target, form string, body []byte) (*httptest.ResponseRecorder, uploadAnswer) {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withUser(req, u))
	var got uploadAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	return rec, got
}

// TestAnUploadInPiecesFromThePage — opened, sent in two pieces, a piece out
// of place answered with where the upload stands, and the file then in the
// list; another account reaches none of it.
func TestAnUploadInPiecesFromThePage(t *testing.T) {
	a := calendarApp(t)
	h := uploadRouter(a)
	body := bytes.Repeat([]byte("z"), 2*65536+10)
	rec, up := uploadCall(h, dana(), http.MethodPost, "/os/vayumail/drive/uploads", url.Values{"dir": {"0"}, "name": {"film.bin"}, "size": {itoa64(int64(len(body)))}}.Encode(), nil)
	if rec.Code != http.StatusOK || up.ID == "" || up.Piece != 8<<20 || up.Received != 0 {
		t.Fatalf("opening: %d %s", rec.Code, rec.Body.String())
	}
	at := "/os/vayumail/drive/uploads/" + up.ID
	if rec, st := uploadCall(h, dana(), http.MethodPut, at+"?offset=0", "", body[:2*65536]); rec.Code != http.StatusOK || st.Received != 2*65536 {
		t.Fatalf("the first piece: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = uploadCall(h, dana(), http.MethodPut, at+"?offset=0", "", body[:2*65536])
	var off struct {
		Received int64 `json:"received"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &off)
	if rec.Code != http.StatusConflict || off.Received != 2*65536 {
		t.Errorf("a piece out of place: %d %s", rec.Code, rec.Body.String())
	}
	if rec, st := uploadCall(h, dana(), http.MethodGet, at, "", nil); rec.Code != http.StatusOK || st.Received != 2*65536 || st.Name != "film.bin" {
		t.Errorf("where it stands: %d %s", rec.Code, rec.Body.String())
	}
	other := &users.User{ID: "vmail:sam@example.com", Email: "sam@example.com", Role: users.RoleAuthor, MailAddress: "sam@example.com"}
	if err := a.vayuMail.Accounts().Create(t.Context(), "sam@example.com", "x", "Sam", "mailbox"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := uploadCall(h, other, http.MethodPut, at+"?offset=131072", "", body[2*65536:]); rec.Code != http.StatusNotFound {
		t.Errorf("another account's piece: %d", rec.Code)
	}
	if rec, _ := uploadCall(h, rulesAdmin(), http.MethodGet, at, "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("an administrator: %d", rec.Code)
	}
	rec, st := uploadCall(h, dana(), http.MethodPut, at+"?offset=131072", "", body[2*65536:])
	if rec.Code != http.StatusOK || st.File == 0 {
		t.Fatalf("the last piece: %d %s", rec.Code, rec.Body.String())
	}
	if ids := driveIDs(t, a, "dana", 0); ids["film.bin"] != st.File {
		t.Errorf("the file is not in the list: %v", ids)
	}
	if rec, _ := uploadCall(h, dana(), http.MethodGet, at, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a finished upload still answers: %d", rec.Code)
	}
	// One stopped is gone.
	_, up2 := uploadCall(h, dana(), http.MethodPost, "/os/vayumail/drive/uploads", url.Values{"dir": {"0"}, "name": {"x.bin"}, "size": {"5"}}.Encode(), nil)
	if rec, _ := uploadCall(h, dana(), http.MethodDelete, "/os/vayumail/drive/uploads/"+up2.ID, "", nil); rec.Code != http.StatusNoContent {
		t.Errorf("stopping: %d", rec.Code)
	}
	if rec, _ := uploadCall(h, dana(), http.MethodGet, "/os/vayumail/drive/uploads/"+up2.ID, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a stopped upload still answers: %d", rec.Code)
	}
}
