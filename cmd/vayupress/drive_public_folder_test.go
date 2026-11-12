// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// shareSend is a press of an upload link's page, with its form as the
// browser sends it: multipart, the password first.
func shareSend(a *App, token, password string, files map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if password != "" {
		_ = mw.WriteField("password", password)
	}
	for name, body := range files {
		part, _ := mw.CreateFormFile("file", name)
		_, _ = part.Write([]byte(body))
	}
	_ = mw.Close()
	router := chi.NewRouter()
	router.Post("/drive/s/{token}", a.handleDriveShare)
	req := httptest.NewRequest(http.MethodPost, "/drive/s/"+token, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func driveFolder(t *testing.T, a *App, parent int64, name string) int64 {
	t.Helper()
	driveAct(a, dana(), url.Values{"op": {"mkdir"}, "dir": {strconv.FormatInt(parent, 10)}, "name": {name}})
	id := driveIDs(t, a, "dana", parent)[name]
	if id == 0 {
		t.Fatalf("no folder %s", name)
	}
	return id
}

// TestAFolderLinksPagesOpenFoldersAndDownloadFiles — made in the folder's
// sheet; its page lists the folder, opens the folders in it and hands out
// their files as attachments; a file outside it is not there.
func TestAFolderLinksPagesOpenFoldersAndDownloadFiles(t *testing.T) {
	a := calendarApp(t)
	project := driveFolder(t, a, 0, "Project")
	drafts := driveFolder(t, a, project, "Drafts")
	driveUpload(a, dana(), project, "<i>notes.txt", "notes")
	driveUpload(a, dana(), drafts, "draft.html", "<script>alert(1)</script>")
	driveUpload(a, dana(), 0, "outside.txt", "not shared")
	outside := driveIDs(t, a, "dana", 0)["outside.txt"]
	draft := driveIDs(t, a, "dana", drafts)["draft.html"]

	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(project, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, `<option value="folder">Look through it and download</option>`) || !strings.Contains(sheet, `<option value="upload">Only upload into it</option>`) {
		t.Fatalf("the folder's sheet offers no folder link:\n%s", sheet)
	}
	token := makeLink(t, a, project, url.Values{"kind": {"folder"}, "ends": {"7"}})

	page := shareVisit(a, http.MethodGet, token, nil).Body.String()
	if !strings.Contains(page, "Drafts/") || !strings.Contains(page, "&lt;i&gt;notes.txt") || strings.Contains(page, "<i>notes") || strings.Contains(page, "outside.txt") {
		t.Fatalf("the folder's page:\n%s", page)
	}
	sub := shareVisit(a, http.MethodPost, token, url.Values{"dir": {strconv.FormatInt(drafts, 10)}})
	if body := sub.Body.String(); sub.Code != http.StatusOK || !strings.Contains(body, "Project / Drafts") || !strings.Contains(body, "draft.html") || !strings.Contains(body, "Back to Project") {
		t.Fatalf("the folder in it: %d\n%s", sub.Code, body)
	}
	got := shareVisit(a, http.MethodPost, token, url.Values{"file": {strconv.FormatInt(draft, 10)}})
	if h := got.Header(); got.Body.String() != "<script>alert(1)</script>" || h.Get("Content-Disposition") != `attachment; filename="draft.html"` || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("a file from it: %d %q %v", got.Code, got.Body.String(), h)
	}
	if out := shareVisit(a, http.MethodPost, token, url.Values{"file": {strconv.FormatInt(outside, 10)}}); out.Code != http.StatusNotFound || strings.Contains(out.Body.String(), "not shared") {
		t.Errorf("a file outside the folder: %d %s", out.Code, out.Body.String())
	}
	if out := shareVisit(a, http.MethodPost, token, url.Values{"dir": {"0"}, "file": {""}}); !strings.Contains(out.Body.String(), "Drafts/") {
		t.Errorf("the shared folder again: %s", out.Body.String())
	}
	links, _ := a.vayuMail.DriveShares(vmail.ReadAsOwner("dana"), project)
	if len(links) != 1 || links[0].Downloads != 1 {
		t.Errorf("the link's count after one file: %+v", links)
	}
}

// TestAFolderLinksPasswordIsAskedFirstAndCarried — the page asks before it
// lists anything; a wrong one is said; the right one opens the folder and
// goes with each press after.
func TestAFolderLinksPasswordIsAskedFirstAndCarried(t *testing.T) {
	a := calendarApp(t)
	dir := driveFolder(t, a, 0, "Private plans")
	driveUpload(a, dana(), dir, "plan.txt", "the plan")
	plan := driveIDs(t, a, "dana", dir)["plan.txt"]
	token := makeLink(t, a, dir, url.Values{"kind": {"folder"}, "ends": {"1"}, "password": {"correct horse"}})
	if page := shareVisit(a, http.MethodGet, token, nil).Body.String(); !strings.Contains(page, `name="password"`) || strings.Contains(page, "plan.txt") {
		t.Fatalf("the first visit:\n%s", page)
	}
	// Opening the address is not a wrong password: a mail scanner or a
	// link preview opening it many times does not close the link.
	for i := 0; i < 12; i++ {
		if page := shareVisit(a, http.MethodGet, token, nil); page.Code != http.StatusOK || strings.Contains(page.Body.String(), "That is not the password") {
			t.Fatalf("visit %d: %d\n%s", i, page.Code, page.Body.String())
		}
	}
	wrong := shareVisit(a, http.MethodPost, token, url.Values{"password": {"battery staple"}})
	if wrong.Code != http.StatusForbidden || !strings.Contains(wrong.Body.String(), "That is not the password") || strings.Contains(wrong.Body.String(), "plan.txt") {
		t.Fatalf("a wrong password: %d %s", wrong.Code, wrong.Body.String())
	}
	right := shareVisit(a, http.MethodPost, token, url.Values{"password": {"correct horse"}}).Body.String()
	if !strings.Contains(right, "plan.txt") || !strings.Contains(right, `<input type="hidden" name="password" value="correct horse">`) {
		t.Fatalf("the right password:\n%s", right)
	}
	if got := shareVisit(a, http.MethodPost, token, url.Values{"password": {"correct horse"}, "file": {strconv.FormatInt(plan, 10)}}); got.Body.String() != "the plan" {
		t.Errorf("a file with the password carried: %d %q", got.Code, got.Body.String())
	}
	if got := shareVisit(a, http.MethodPost, token, url.Values{"file": {strconv.FormatInt(plan, 10)}}); got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), "the plan") {
		t.Errorf("a file without it: %d %q", got.Code, got.Body.String())
	}
}

// TestAnUploadLinksPageTakesFilesAndShowsNothing — made in the sheet with
// an end (refused without); the page shows nothing of the folder; files
// sent are kept under names of their own and said back.
func TestAnUploadLinksPageTakesFilesAndShowsNothing(t *testing.T) {
	a := calendarApp(t)
	dir := driveFolder(t, a, 0, "Received")
	driveUpload(a, dana(), dir, "plan.txt", "the holder's plan")
	refused := driveAct(a, dana(), url.Values{"op": {"share"}, "id": {strconv.FormatInt(dir, 10)}, "kind": {"upload"}, "ends": {"0"}})
	if shareLinkRe.MatchString(refused.Body.String()) || !strings.Contains(refused.Body.String(), "a link to upload into has an end") {
		t.Fatalf("an upload link with no end: %s", refused.Body.String())
	}
	token := makeLink(t, a, dir, url.Values{"kind": {"upload"}, "ends": {"1"}, "password": {"correct horse"}})
	page := shareVisit(a, http.MethodGet, token, nil).Body.String()
	if !strings.Contains(page, `enctype="multipart/form-data"`) || !strings.Contains(page, `name="password"`) || strings.Contains(page, "plan.txt") {
		t.Fatalf("the upload page:\n%s", page)
	}
	if wrong := shareSend(a, token, "battery staple", map[string]string{"x.txt": "x"}); wrong.Code != http.StatusForbidden || !strings.Contains(wrong.Body.String(), "That is not the password") {
		t.Fatalf("a wrong password: %d %s", wrong.Code, wrong.Body.String())
	}
	sent := shareSend(a, token, "correct horse", map[string]string{"plan.txt": "a stranger's plan"})
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), "Received: plan (2).txt.") || strings.Contains(sent.Body.String(), "the holder's plan") {
		t.Fatalf("sending a file: %d %s", sent.Code, sent.Body.String())
	}
	ids := driveIDs(t, a, "dana", dir)
	if len(ids) != 2 || ids["plan (2).txt"] == 0 {
		t.Errorf("the folder holds %v", ids)
	}
	if empty := shareSend(a, token, "correct horse", nil); empty.Code != http.StatusBadRequest || !strings.Contains(empty.Body.String(), "Choose a file to send.") {
		t.Errorf("a press with no file: %d %s", empty.Code, empty.Body.String())
	}
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(dir, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, "Upload only · Made ") || !strings.Contains(sheet, "· 1 file received") {
		t.Errorf("the sheet's line for the link:\n%s", sheet)
	}
}

// TestAPrivateMailboxsFolderOffersOnlyUploads — its sheet says why.
func TestAPrivateMailboxsFolderOffersOnlyUploads(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	postForm(a.handleVayuOSDriveAction, "/os/vayumail/drive/action", url.Values{"op": {"mkdir"}, "dir": {"0"}, "name": {"Papers"}}.Encode(), dana)
	dir := driveIDs(t, a, "dana", 0)["Papers"]
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(dir, 10), dana, "").Body.String()
	if strings.Contains(sheet, `<option value="folder">`) || !strings.Contains(sheet, `<option value="upload">`) || !strings.Contains(sheet, "this folder is shared only to upload into") {
		t.Errorf("a private mailbox's folder sheet:\n%s", sheet)
	}
}
