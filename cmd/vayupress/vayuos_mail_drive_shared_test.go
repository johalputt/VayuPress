// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func ida(t *testing.T, a *App) *users.User {
	t.Helper()
	if err := a.vayuMail.Accounts().Create(context.Background(), "ida@example.com", "x", "Ida", "mailbox"); err != nil {
		t.Fatal(err)
	}
	return &users.User{ID: "vmail:ida@example.com", Email: "ida@example.com", Role: users.RoleAuthor, MailAddress: "ida@example.com"}
}

// TestAFolderSharedWithAnotherMailboxFromTheSheet — shared from the
// folder's sheet, listed under the other mailbox's Shared with me, opened,
// a file downloaded; reached by no third mailbox; stopped from the sheet.
func TestAFolderSharedWithAnotherMailboxFromTheSheet(t *testing.T) {
	a := calendarApp(t)
	e := eli(t, a)
	third := ida(t, a)
	project := driveFolder(t, a, 0, "Project")
	driveUpload(a, dana(), project, "plan.txt", "the plan")
	plan := driveIDs(t, a, "dana", project)["plan.txt"]
	sheetOf := func() string {
		return calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(project, 10), dana(), "").Body.String()
	}
	if !strings.Contains(sheetOf(), "Share with someone here") || !strings.Contains(sheetOf(), `<option value="1">Also add files and folders</option>`) {
		t.Fatalf("the folder's sheet:\n%s", sheetOf())
	}
	refused := driveAct(a, dana(), url.Values{"op": {"grant"}, "id": {strconv.FormatInt(project, 10)}, "who": {"nobody@example.com"}})
	if !strings.Contains(refused.Body.String(), "share with another mailbox on this server, by its address") {
		t.Fatalf("a share with no one here: %s", refused.Body.String())
	}
	shared := driveAct(a, dana(), url.Values{"op": {"grant"}, "id": {strconv.FormatInt(project, 10)}, "who": {"eli"}, "write": {"0"}})
	if !strings.Contains(shared.Body.String(), "Shared. It is under Shared with me in their Drive.") || !strings.Contains(shared.Body.String(), "eli@example.com · can read") {
		t.Fatalf("sharing: %s", shared.Body.String())
	}
	list := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?view=shared", e, "").Body.String()
	g, _ := a.vayuMail.DriveSharedWithMe(vmail.ReadAsOwner("eli"))
	if len(g) != 1 || !strings.Contains(list, "Project") || !strings.Contains(list, "dana@example.com · can read") {
		t.Fatalf("eli's Shared with me:\n%s", list)
	}
	grant := strconv.FormatInt(g[0].ID, 10)
	folder := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?view=shared&grant="+grant, e, "").Body.String()
	href := "/os/vayumail/drive/file?grant=" + grant + "&amp;id=" + strconv.FormatInt(plan, 10)
	if !strings.Contains(folder, href) || !strings.Contains(folder, "You can read and download what is here.") || strings.Contains(folder, "data-drive-upload") || strings.Contains(folder, "New folder") {
		t.Fatalf("the shared folder, to read:\n%s", folder)
	}
	file := strings.ReplaceAll(href, "&amp;", "&")
	if got := calGet(a, a.handleVayuOSDriveFile, file, e, ""); got.Body.String() != "the plan" || got.Header().Get("Content-Disposition") != `attachment; filename="plan.txt"` {
		t.Fatalf("the download: %d %q %v", got.Code, got.Body.String(), got.Header())
	}
	if got := calGet(a, a.handleVayuOSDriveFile, file, third, ""); got.Code != http.StatusNotFound {
		t.Errorf("a third mailbox read through eli's share: %d %q", got.Code, got.Body.String())
	}
	if got := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?view=shared&grant="+grant, third, ""); got.Code != http.StatusSeeOther {
		t.Errorf("a third mailbox opened eli's share: %d", got.Code)
	}
	// To read only: no upload opens.
	if rec, _ := uploadCall(uploadRouter(a), e, http.MethodPost, "/os/vayumail/drive/uploads", url.Values{"grant": {grant}, "dir": {"0"}, "name": {"x.txt"}, "size": {"1"}}.Encode(), nil); rec.Code == http.StatusOK {
		t.Errorf("an upload into a share to read: %d", rec.Code)
	}
	gs, _ := a.vayuMail.DriveGrants(vmail.ReadAsOwner("dana"), project)
	stopped := driveAct(a, dana(), url.Values{"op": {"ungrant"}, "id": {strconv.FormatInt(project, 10)}, "grant": {strconv.FormatInt(gs[0].ID, 10)}})
	if !strings.Contains(stopped.Body.String(), "No longer shared with them.") || strings.Contains(stopped.Body.String(), "eli@example.com · can read") {
		t.Fatalf("stopping it: %s", stopped.Body.String())
	}
	if got := calGet(a, a.handleVayuOSDriveFile, file, e, ""); got.Code != http.StatusNotFound {
		t.Errorf("read after it was stopped: %d", got.Code)
	}
}

// TestAFolderSharedToWriteTakesUploadsAndFolders — the upload in pieces
// lands in the holder's folder; a folder made from the other side too;
// leaving takes it off the list.
func TestAFolderSharedToWriteTakesUploadsAndFolders(t *testing.T) {
	a := calendarApp(t)
	e := eli(t, a)
	team := driveFolder(t, a, 0, "Team")
	driveAct(a, dana(), url.Values{"op": {"grant"}, "id": {strconv.FormatInt(team, 10)}, "who": {"eli@example.com"}, "write": {"1"}})
	g, _ := a.vayuMail.DriveSharedWithMe(vmail.ReadAsOwner("eli"))
	if len(g) != 1 || !g[0].Write {
		t.Fatalf("the share: %+v", g)
	}
	grant := strconv.FormatInt(g[0].ID, 10)
	page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?view=shared&grant="+grant, e, "").Body.String()
	if !strings.Contains(page, `data-drive-upload data-dir="0" data-grant="`+grant+`"`) || !strings.Contains(page, "New folder") {
		t.Fatalf("the shared folder, to write:\n%s", page)
	}
	h := uploadRouter(a)
	rec, up := uploadCall(h, e, http.MethodPost, "/os/vayumail/drive/uploads", url.Values{"grant": {grant}, "dir": {"0"}, "name": {"notes.txt"}, "size": {"5"}}.Encode(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("opening the upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec, st := uploadCall(h, e, http.MethodPut, "/os/vayumail/drive/uploads/"+up.ID+"?offset=0", "", []byte("notes")); rec.Code != http.StatusOK || st.File == 0 {
		t.Fatalf("the piece: %d %s", rec.Code, rec.Body.String())
	}
	if ids := driveIDs(t, a, "dana", team); ids["notes.txt"] == 0 {
		t.Errorf("dana's folder holds %v", ids)
	}
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?new=folder&dir=0&grant="+grant, e, "").Body.String()
	if !strings.Contains(sheet, `name="grant" value="`+grant+`"`) {
		t.Fatalf("the new folder sheet: %s", sheet)
	}
	made := driveAct(a, e, url.Values{"op": {"mkdir"}, "dir": {"0"}, "grant": {grant}, "name": {"From Eli"}})
	if made.Header().Get("HX-Redirect") != "/os/vayumail/drive?view=shared&grant="+grant {
		t.Fatalf("making a folder: %v %s", made.Header(), made.Body.String())
	}
	if ids := driveIDs(t, a, "dana", team); ids["From Eli"] == 0 {
		t.Errorf("dana's folder holds %v", ids)
	}
	if ids := driveIDs(t, a, "eli", 0); len(ids) != 0 {
		t.Errorf("eli's own files hold %v", ids)
	}
	left := driveAct(a, e, url.Values{"op": {"leave"}, "grant": {grant}})
	if left.Header().Get("HX-Redirect") != "/os/vayumail/drive?view=shared" {
		t.Fatalf("leaving: %v", left.Header())
	}
	if gs, _ := a.vayuMail.DriveGrants(vmail.ReadAsOwner("dana"), team); len(gs) != 0 {
		t.Errorf("%d shares left after eli left", len(gs))
	}
}

// TestAPrivateMailboxsSheetSharesWithNoOneHere — said, with no form.
func TestAPrivateMailboxsSheetSharesWithNoOneHere(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	driveUpload(a, dana, 0, "s.txt", "s")
	id := driveIDs(t, a, "dana", 0)["s.txt"]
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana, "").Body.String()
	if !strings.Contains(sheet, "which no other mailbox has") || strings.Contains(sheet, `name="who"`) {
		t.Errorf("a private mailbox's sheet:\n%s", sheet)
	}
}

// TestSharedWithMeIsInThePersonsLanguage — the other mailbox's list and
// the shared folder, in Italian.
func TestSharedWithMeIsInThePersonsLanguage(t *testing.T) {
	a := calendarApp(t)
	e := eli(t, a)
	project := driveFolder(t, a, 0, "Project")
	driveAct(a, dana(), url.Values{"op": {"grant"}, "id": {strconv.FormatInt(project, 10)}, "who": {"eli"}, "write": {"1"}})
	it := func(target string) string {
		req := withUser(httptest.NewRequest(http.MethodGet, target, nil), e)
		req = req.WithContext(i18n.WithLang(req.Context(), "it"))
		rec := httptest.NewRecorder()
		a.handleVayuOSDrive(rec, req)
		return rec.Body.String()
	}
	list := it("/os/vayumail/drive?view=shared")
	// The sidebar is Drive's and is not in this page's sentences.
	if !strings.Contains(list, `<h1 class="drive-crumbs">Condivisi con me</h1>`) || !strings.Contains(list, "dana@example.com · può aggiungere file") || strings.Contains(list, "can add files") {
		t.Errorf("the Italian list is not in Italian:\n%s", list)
	}
	g, _ := a.vayuMail.DriveSharedWithMe(vmail.ReadAsOwner("eli"))
	folder := it("/os/vayumail/drive?view=shared&grant=" + strconv.FormatInt(g[0].ID, 10))
	if !strings.Contains(folder, "Condiviso da dana@example.com. Puoi aggiungere file e cartelle") || !strings.Contains(folder, "Questa cartella è vuota.") {
		t.Errorf("the Italian folder is not in Italian:\n%s", folder)
	}
}

// TestDriveIsInThePersonsLanguage — Drive's page and a file's sheet in
// German: its sidebar, its list, the sheet's facts and the link form.
func TestDriveIsInThePersonsLanguage(t *testing.T) {
	a := calendarApp(t)
	project := driveFolder(t, a, 0, "Projekt")
	driveUpload(a, dana(), project, "plan.txt", "der Plan")
	plan := driveIDs(t, a, "dana", project)["plan.txt"]
	de := func(h http.HandlerFunc, target string) string {
		req := withUser(httptest.NewRequest(http.MethodGet, target, nil), dana())
		req = req.WithContext(i18n.WithLang(req.Context(), "de"))
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Body.String()
	}
	page := de(a.handleVayuOSDrive, "/os/vayumail/drive?dir="+strconv.FormatInt(project, 10))
	for _, want := range []string{">Meine Dateien<", "Drive-Einstellungen", "Neuer Ordner", "Mehr zu plan.txt"} {
		if !strings.Contains(page, want) {
			t.Errorf("the German page has no %q", want)
		}
	}
	sheet := de(a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(plan, 10))
	for _, want := range []string{" · geändert am ", "Per Link teilen", "Link erstellen", "Umbenennen", "In den Papierkorb"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the German sheet has no %q", want)
		}
	}
	for _, english := range []string{"My files", "Share by link", "Make a link", "changed "} {
		if strings.Contains(page+sheet, english) {
			t.Errorf("the German Drive says %q", english)
		}
	}
}
