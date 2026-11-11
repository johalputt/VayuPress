// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// driveUpload sends one file as the page's script does: a multipart body
// with the file in the part named "file".
func driveUpload(a *App, u *users.User, dir int64, name, body string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", name)
	_, _ = part.Write([]byte(body))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/drive/upload?dir="+strconv.FormatInt(dir, 10), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleVayuOSDriveUpload(rec, withUser(req, u))
	return rec
}

func driveAct(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	return postForm(a.handleVayuOSDriveAction, "/os/vayumail/drive/action", vals.Encode(), u)
}

// driveIDs is the holder's entries in a folder, by name.
func driveIDs(t *testing.T, a *App, who string, parent int64) map[string]int64 {
	t.Helper()
	list, err := a.vayuMail.DriveList(vmail.ReadAsOwner(who), parent)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := map[string]int64{}
	for _, d := range list {
		out[d.Name] = d.ID
	}
	return out
}

func eli(t *testing.T, a *App) *users.User {
	t.Helper()
	if err := a.vayuMail.Accounts().Create(context.Background(), "eli@example.com", "x", "Eli", "mailbox"); err != nil {
		t.Fatalf("create eli: %v", err)
	}
	return &users.User{ID: "vmail:eli@example.com", Email: "eli@example.com", Role: users.RoleAuthor, MailAddress: "eli@example.com"}
}

// TestAHolderKeepsAFileInAFolderAndSeesIt — a folder made, a file sent
// into it, and the page showing both with the way back.
func TestAHolderKeepsAFileInAFolderAndSeesIt(t *testing.T) {
	a := calendarApp(t)
	if rec := driveAct(a, dana(), url.Values{"op": {"mkdir"}, "dir": {"0"}, "name": {"Contracts"}}); rec.Header().Get("HX-Redirect") != "/os/vayumail/drive" {
		t.Fatalf("mkdir: %d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	dir := driveIDs(t, a, "dana", 0)["Contracts"]
	if dir == 0 {
		t.Fatal("the folder was not made")
	}
	if rec := driveUpload(a, dana(), dir, "terms.pdf", strings.Repeat("x", 2048)); rec.Code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	root := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", dana(), "").Body.String()
	if !strings.Contains(root, `href="/os/vayumail/drive?dir=`+strconv.FormatInt(dir, 10)+`"`) || !strings.Contains(root, "<span>Contracts</span>") {
		t.Fatalf("My files does not list the folder:\n%s", root)
	}
	if strings.Contains(root, "terms.pdf") {
		t.Error("a file in a folder is listed at the top too")
	}
	if !strings.Contains(root, `aria-current="page">`+saIcon("folder")+`<span>Drive</span>`) {
		t.Error("Drive is not the place marked open beside Mail and Calendar")
	}
	in := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?dir="+strconv.FormatInt(dir, 10), dana(), "").Body.String()
	for _, want := range []string{"<span>terms.pdf</span>", `<span class="drive-kind" aria-hidden="true">PDF</span>`, "2.0 KiB", `<a href="/os/vayumail/drive">My files</a>`, "<span>Contracts</span>", `data-dir="` + strconv.FormatInt(dir, 10) + `"`} {
		if !strings.Contains(in, want) {
			t.Errorf("the folder's page lacks %q", want)
		}
	}
}

// TestAFileIsADownloadNeverAPageOfThisSite — whatever was uploaded, the
// console's origin never serves it as something a browser runs.
func TestAFileIsADownloadNeverAPageOfThisSite(t *testing.T) {
	a := calendarApp(t)
	for name, body := range map[string]string{"page.html": "<script>alert(1)</script>", "art.svg": `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`, "shot.png": "\x89PNG\r\n\x1a\n"} {
		if rec := driveUpload(a, dana(), 0, name, body); rec.Code != http.StatusNoContent {
			t.Fatalf("upload %s: %d", name, rec.Code)
		}
	}
	ids := driveIDs(t, a, "dana", 0)
	get := func(name, extra string) *httptest.ResponseRecorder {
		return calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+strconv.FormatInt(ids[name], 10)+extra, dana(), "")
	}
	for _, c := range []struct{ name, extra, ctype, disp string }{
		{"page.html", "", "application/octet-stream", `attachment; filename="page.html"`},
		{"page.html", "&show=1", "application/octet-stream", `attachment; filename="page.html"`},
		{"art.svg", "&show=1", "application/octet-stream", `attachment; filename="art.svg"`},
		{"shot.png", "", "application/octet-stream", `attachment; filename="shot.png"`},
		{"shot.png", "&show=1", "image/png", `inline; filename="shot.png"`},
	} {
		rec := get(c.name, c.extra)
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Type") != c.ctype || h.Get("Content-Disposition") != c.disp {
			t.Errorf("%s%s: %d %q %q", c.name, c.extra, rec.Code, h.Get("Content-Type"), h.Get("Content-Disposition"))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
			t.Errorf("%s%s goes out without nosniff and the sandbox: %v", c.name, c.extra, h)
		}
	}
	if got := get("page.html", "").Body.String(); got != "<script>alert(1)</script>" {
		t.Errorf("the download is not the file: %q", got)
	}
	// The sheet draws a picture in place and nothing else.
	sheet := func(name string) string {
		return calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(ids[name], 10), dana(), "").Body.String()
	}
	if !strings.Contains(sheet("shot.png"), `<img class="drive-preview"`) {
		t.Error("a PNG has no preview in its sheet")
	}
	if strings.Contains(sheet("art.svg"), "<img") || strings.Contains(sheet("page.html"), "<img") {
		t.Error("an SVG or an HTML file is drawn in the sheet")
	}
}

// TestADownloadsNameCannotBreakItsHeader — a quote in a file's name stays
// inside the filename.
func TestADownloadsNameCannotBreakItsHeader(t *testing.T) {
	a := calendarApp(t)
	if _, err := a.vayuMail.DrivePut(vmail.ReadAsOwner("dana"), 0, `a"; filename="evil.html`, "", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	id := driveIDs(t, a, "dana", 0)[`a"; filename="evil.html`]
	got := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+strconv.FormatInt(id, 10), dana(), "").Header().Get("Content-Disposition")
	if got != `attachment; filename="a_; filename=_evil.html"` {
		t.Errorf("Content-Disposition: %s", got)
	}
}

// TestDriveOpensOnlyForItsHolder — not for a session with no mailbox, and
// not for another holder who knows a file's number.
func TestDriveOpensOnlyForItsHolder(t *testing.T) {
	a := calendarApp(t)
	if rec := driveUpload(a, dana(), 0, "plan.txt", "dana's plan"); rec.Code != http.StatusNoContent {
		t.Fatalf("upload: %d", rec.Code)
	}
	id := strconv.FormatInt(driveIDs(t, a, "dana", 0)["plan.txt"], 10)

	boss := rulesAdmin()
	if body := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", boss, "").Body.String(); !strings.Contains(body, "Files belong to a mailbox, and this account has none") || strings.Contains(body, "plan.txt") {
		t.Fatalf("an administrator without a mailbox: %s", body)
	}
	if rec := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+id, boss, ""); rec.Code != http.StatusForbidden {
		t.Errorf("the sheet answered an administrator %d", rec.Code)
	}
	if rec := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+id, boss, ""); rec.Code != http.StatusNotFound {
		t.Errorf("the file answered an administrator %d", rec.Code)
	}
	if rec := driveUpload(a, boss, 0, "x.txt", "x"); rec.Code != http.StatusForbidden {
		t.Errorf("an upload answered an administrator %d", rec.Code)
	}
	if rec := driveAct(a, boss, url.Values{"op": {"trash"}, "id": {id}}); rec.Code != http.StatusForbidden {
		t.Errorf("an action answered an administrator %d", rec.Code)
	}

	other := eli(t, a)
	if rec := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+id, other, ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "dana's plan") {
		t.Errorf("another holder read the file: %d %s", rec.Code, rec.Body.String())
	}
	if body := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+id, other, "").Body.String(); strings.Contains(body, "plan.txt") {
		t.Errorf("another holder's sheet names the file: %s", body)
	}
	for _, op := range []string{"trash", "delete", "rename", "move"} {
		driveAct(a, other, url.Values{"op": {op}, "id": {id}, "name": {"mine.txt"}, "to": {"0"}})
	}
	if rec := driveUpload(a, other, driveIDs(t, a, "dana", 0)["plan.txt"], "x.txt", "x"); rec.Code == http.StatusNoContent {
		t.Error("another holder uploaded under dana's entry")
	}
	if got := driveIDs(t, a, "dana", 0); len(got) != 1 || got["plan.txt"] == 0 {
		t.Errorf("dana's files after another holder's attempts: %v", got)
	}
	if body := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", other, "").Body.String(); strings.Contains(body, "plan.txt") {
		t.Error("another holder's Drive lists dana's file")
	}
}

// TestTrashRestoreAndDeleteFromThePage — out of the list and into Trash,
// back again, then gone with its bytes.
func TestTrashRestoreAndDeleteFromThePage(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "notes.txt", "keep me")
	id := strconv.FormatInt(driveIDs(t, a, "dana", 0)["notes.txt"], 10)
	page := func(q string) string {
		return calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive"+q, dana(), "").Body.String()
	}
	if rec := driveAct(a, dana(), url.Values{"op": {"trash"}, "id": {id}}); rec.Header().Get("HX-Redirect") != "/os/vayumail/drive" {
		t.Fatalf("trash: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(page(""), "notes.txt") || !strings.Contains(page("?view=trash"), "<span>notes.txt</span>") {
		t.Fatal("a trashed file is not in Trash alone")
	}
	if !strings.Contains(page("?view=trash"), "thirty days") {
		t.Error("Trash does not say how long it keeps a file")
	}
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+id, dana(), "").Body.String()
	if !strings.Contains(sheet, "Restore") || !strings.Contains(sheet, "Delete for good") || strings.Contains(sheet, "Download") {
		t.Errorf("a trashed file's sheet:\n%s", sheet)
	}
	if rec := driveAct(a, dana(), url.Values{"op": {"restore"}, "id": {id}}); rec.Header().Get("HX-Redirect") != "/os/vayumail/drive?view=trash" {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(page(""), "<span>notes.txt</span>") {
		t.Fatal("a restored file is not back in its folder")
	}
	driveAct(a, dana(), url.Values{"op": {"trash"}, "id": {id}})
	driveAct(a, dana(), url.Values{"op": {"delete"}, "id": {id}})
	if strings.Contains(page("?view=trash"), "notes.txt") || !strings.Contains(page("?view=trash"), "Trash is empty.") {
		t.Error("a file deleted for good is still listed")
	}
	if n := a.vayuMail.Accounts().DriveUsed(context.Background(), "dana@example.com"); n != 0 {
		t.Errorf("%d bytes still counted after the delete", n)
	}
}

// TestARefusalIsSaidInTheSheet — a name already taken is answered in
// words, in the sheet the holder is looking at, and nothing changes.
func TestARefusalIsSaidInTheSheet(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "a.txt", "a")
	driveUpload(a, dana(), 0, "b.txt", "b")
	ids := driveIDs(t, a, "dana", 0)
	rec := driveAct(a, dana(), url.Values{"op": {"rename"}, "id": {strconv.FormatInt(ids["b.txt"], 10)}, "name": {"a.txt"}})
	body := rec.Body.String()
	if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(body, "already") || !strings.Contains(body, `value="b.txt"`) {
		t.Fatalf("a rename onto a taken name: %q %s", rec.Header().Get("HX-Redirect"), body)
	}
	if got := driveIDs(t, a, "dana", 0); got["a.txt"] != ids["a.txt"] || got["b.txt"] != ids["b.txt"] {
		t.Errorf("the files after a refused rename: %v", got)
	}
}

// TestAFullAllowanceRefusesAnUploadAndTheSidebarCountsFilesWithMail — the
// allowance is the mailbox's one, mail and files together.
func TestAFullAllowanceRefusesAnUploadAndTheSidebarCountsFilesWithMail(t *testing.T) {
	a := calendarApp(t)
	if err := a.vayuMail.Accounts().SetQuota(context.Background(), "dana@example.com", 4096); err != nil {
		t.Fatal(err)
	}
	// Read once before anything is kept: the figure is remembered for a few
	// seconds, and a file kept must show in it at once all the same.
	if page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", dana(), "").Body.String(); !strings.Contains(page, "<span>Mail and files</span><span>0 B of 4.0 KiB</span>") {
		t.Fatalf("an empty mailbox's figure:\n%s", page[strings.Index(page, "mx-storage")-40:])
	}
	if rec := driveUpload(a, dana(), 0, "small.bin", strings.Repeat("s", 3072)); rec.Code != http.StatusNoContent {
		t.Fatalf("a file inside the allowance: %d %s", rec.Code, rec.Body.String())
	}
	if page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", dana(), "").Body.String(); !strings.Contains(page, "<span>Mail and files</span><span>3.0 KiB of 4.0 KiB</span>") {
		t.Fatalf("the figure straight after a file is kept:\n%s", page[strings.Index(page, "mx-storage")-40:])
	}
	rec := driveUpload(a, dana(), 0, "big.bin", strings.Repeat("b", 2048))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "storage allowance") {
		t.Fatalf("a file past the allowance: %d %s", rec.Code, rec.Body.String())
	}
	if got := driveIDs(t, a, "dana", 0); got["big.bin"] != 0 {
		t.Error("the refused file was kept")
	}
	page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", dana(), "").Body.String()
	if !strings.Contains(page, "<span>Mail and files</span><span>3.0 KiB of 4.0 KiB</span>") || !strings.Contains(page, "<span>Files alone</span><span>3.0 KiB</span>") {
		t.Errorf("the sidebar's figure:\n%s", page[strings.Index(page, "mx-storage")-40:])
	}
	// Files that fill the allowance stop mail being filed too: one figure.
	if a.vayuMail.MailboxOverQuota("dana@example.com") {
		t.Fatal("the mailbox reads as full at three quarters")
	}
	if rec := driveUpload(a, dana(), 0, "last.bin", strings.Repeat("l", 1024)); rec.Code != http.StatusNoContent {
		t.Fatalf("the file that exactly fills the allowance: %d", rec.Code)
	}
	if !a.vayuMail.MailboxOverQuota("dana@example.com") {
		t.Error("a mailbox whose files fill its allowance does not read as full")
	}
}

// TestAnEarlierVersionIsBroughtBackFromTheSheet — a file saved over shows
// what it was, and one press makes that the file again.
func TestAnEarlierVersionIsBroughtBackFromTheSheet(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "plan.txt", "the first plan")
	driveUpload(a, dana(), 0, "plan.txt", "the second")
	id := driveIDs(t, a, "dana", 0)["plan.txt"]
	ids := strconv.FormatInt(id, 10)
	vs, _ := a.vayuMail.DriveVersions(vmail.ReadAsOwner("dana"), id)
	if len(vs) != 1 {
		t.Fatalf("%d versions after saving over the file once", len(vs))
	}
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+ids, dana(), "").Body.String()
	if !strings.Contains(sheet, "Earlier versions") || !strings.Contains(sheet, "· 14 B</span>") || !strings.Contains(sheet, `"version":"`+strconv.FormatInt(vs[0].ID, 10)+`"`) {
		t.Fatalf("the sheet does not offer the earlier version:\n%s", sheet)
	}
	rec := driveAct(a, dana(), url.Values{"op": {"version"}, "id": {ids}, "version": {strconv.FormatInt(vs[0].ID, 10)}})
	if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), "That version is the file again") || !strings.Contains(rec.Body.String(), "· 10 B</span>") {
		t.Fatalf("bringing it back: %q %s", rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	if got := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+ids, dana(), "").Body.String(); got != "the first plan" {
		t.Errorf("the file now downloads as %q", got)
	}
	// Another holder's press with the same numbers changes nothing.
	driveAct(a, eli(t, a), url.Values{"op": {"version"}, "id": {ids}, "version": {strconv.FormatInt(vs[0].ID, 10)}})
	if got := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+ids, dana(), "").Body.String(); got != "the first plan" {
		t.Errorf("after another holder's press the file downloads as %q", got)
	}
}
