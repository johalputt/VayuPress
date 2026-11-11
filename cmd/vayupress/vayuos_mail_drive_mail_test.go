// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func driveSave(a *App, u *users.User, id string, idx int) *httptest.ResponseRecorder {
	return postForm(a.handleVayuOSDriveSave, "/os/vayumail/drive/save", url.Values{"folder": {"Inbox"}, "id": {id}, "idx": {strconv.Itoa(idx)}}.Encode(), u)
}

// attachmentIndex is the index the reader gives the attachment called name.
func attachmentIndex(t *testing.T, a *App, id, name string) int {
	t.Helper()
	raw, err := a.vayuMail.ReadFolderMessage(vmail.ReadAsOwner("dana"), "Inbox", id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if fn, _, _, ok := vmail.ExtractAttachment(raw, i); ok && fn == name {
			return i
		}
	}
	t.Fatalf("the message has no %s", name)
	return 0
}

// TestAnAttachmentIsSavedToDrive — from the reader's button into the
// holder's own Drive, under its name, never over a file already there, and
// from there only ever a download.
func TestAnAttachmentIsSavedToDrive(t *testing.T) {
	a := calendarApp(t)
	id := withAttachments(t, a)
	req := withUser(httptest.NewRequest(http.MethodGet, "/", nil), dana())
	card, _ := a.vayuReaderCard(a.mailReader(req, ""), "Inbox", id, readerView{Pane: true, Places: a.holderPlaces(req)})
	if n := strings.Count(card, `hx-post="/os/vayumail/drive/save"`); n != 3 || !strings.Contains(card, ">Save to Drive<") {
		t.Fatalf("the reader offers Save to Drive on %d of 3 attachments:\n%s", n, card)
	}
	pdf := attachmentIndex(t, a, id, "doc.pdf")
	for _, want := range []string{"doc.pdf", "doc (2).pdf"} {
		rec := driveSave(a, dana(), id, pdf)
		if !strings.Contains(rec.Body.String(), `href="/os/vayumail/drive">Saved to Drive</a>`) {
			t.Fatalf("saving the PDF: %d %s", rec.Code, rec.Body.String())
		}
		if driveIDs(t, a, "dana", 0)[want] == 0 {
			t.Fatalf("Drive has no %s: %v", want, driveIDs(t, a, "dana", 0))
		}
	}
	ids := driveIDs(t, a, "dana", 0)
	first := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+strconv.FormatInt(ids["doc.pdf"], 10), dana(), "")
	if first.Body.String() != "%PDF-1.4\n%fake\n" {
		t.Errorf("the saved file is not the attachment: %q", first.Body.String())
	}
	if vs, _ := a.vayuMail.DriveVersions(vmail.ReadAsOwner("dana"), ids["doc.pdf"]); len(vs) != 0 {
		t.Error("saving the attachment again wrote over the first")
	}
	// An HTML file that a message calls a picture is, in Drive, a download.
	driveSave(a, dana(), id, attachmentIndex(t, a, id, "trap.png"))
	trap := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+strconv.FormatInt(driveIDs(t, a, "dana", 0)["trap.png"], 10), dana(), "")
	if h := trap.Header(); h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("the saved trap goes out as %v", h)
	}
	if rec := driveSave(a, dana(), id, 99); !strings.Contains(rec.Body.String(), "the message has no such file") {
		t.Errorf("an attachment the message does not have: %s", rec.Body.String())
	}
}

// TestAnAttachmentsNameIsNeverAPathInDrive — a sender's name for a file is
// only its last part once it is kept.
func TestAnAttachmentsNameIsNeverAPathInDrive(t *testing.T) {
	a := calendarApp(t)
	raw := "From: x@example.net\r\nTo: dana@example.com\r\nSubject: Up\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
		"--b\r\nContent-Type: text/plain; name=\"up.txt\"\r\nContent-Disposition: attachment; filename=\"..\\\\..\\\\etc\\\\up.txt\"\r\n\r\nclimbing\r\n--b--\r\n"
	id, err := a.vayuMail.DeliverInbound("x@example.net", "dana@example.com", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	rec := driveSave(a, dana(), id, 0)
	got := driveIDs(t, a, "dana", 0)
	if !strings.Contains(rec.Body.String(), "Saved to Drive") || len(got) != 1 || got["up.txt"] == 0 {
		t.Fatalf("kept as %v: %s", got, rec.Body.String())
	}
}

// TestSaveToDriveIsTheHoldersOwn — not offered to an administrator reading
// the mailbox, refused to one, and another holder cannot save dana's mail.
func TestSaveToDriveIsTheHoldersOwn(t *testing.T) {
	a := calendarApp(t)
	id := withAttachments(t, a)
	// An administrator with a Drive of their own, reading dana's mail: the
	// file would go into the wrong Drive, so it is not offered.
	card, _ := a.vayuReaderCard(vmail.ReadAsOperator("dana", "boss@example.com"), "Inbox", id, readerView{Pane: true, Places: holderPlaces{Calendar: true, Drive: true}})
	if !strings.Contains(card, "doc.pdf") || strings.Contains(card, "Save to Drive") {
		t.Fatalf("an administrator reading the mailbox is offered Save to Drive:\n%s", card)
	}
	if rec := driveSave(a, rulesAdmin(), id, 0); rec.Code != http.StatusForbidden {
		t.Errorf("an administrator's save: %d %s", rec.Code, rec.Body.String())
	}
	other := eli(t, a)
	if rec := driveSave(a, other, id, 0); !strings.Contains(rec.Body.String(), "the message is not here") {
		t.Errorf("another holder saving dana's attachment: %d %s", rec.Code, rec.Body.String())
	}
	for _, who := range []string{"dana", "eli"} {
		if got := driveIDs(t, a, who, 0); len(got) != 0 {
			t.Errorf("%s's Drive after the refusals: %v", who, got)
		}
	}
}

// TestTheComposersDriveMenuListsTheHoldersFiles — folders to go into, files
// to attach, one too large for a message shown and not offered; and none of
// it for a session with no mailbox of its own.
func TestTheComposersDriveMenuListsTheHoldersFiles(t *testing.T) {
	t.Setenv("VAYUMAIL_MAX_ATTACH_MB", "1")
	a := calendarApp(t)
	driveAct(a, dana(), url.Values{"op": {"mkdir"}, "dir": {"0"}, "name": {"Contracts"}})
	dir := driveIDs(t, a, "dana", 0)["Contracts"]
	driveUpload(a, dana(), 0, "notes.txt", "notes")
	driveUpload(a, dana(), 0, "film.bin", strings.Repeat("f", 1<<20+1))
	driveUpload(a, dana(), dir, "deal.pdf", "deal")
	ids := driveIDs(t, a, "dana", 0)

	sheet, refusal := a.composeSheet(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose", nil), dana()), false)
	if refusal != "" || !strings.Contains(sheet, `data-c-drive hx-get="/os/vayumail/drive/pick"`) || !strings.Contains(sheet, `aria-label="Attach from Drive"`) {
		t.Fatalf("the composer has no From Drive menu: %q\n%s", refusal, sheet)
	}
	if sheet, _ := a.composeSheet(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose?user=dana", nil), rulesAdmin()), false); strings.Contains(sheet, "data-c-drive") {
		t.Error("an administrator writing as dana is offered a Drive")
	}

	top := calGet(a, a.handleVayuOSDrivePick, "/os/vayumail/drive/pick", dana(), "").Body.String()
	for _, want := range []string{
		`hx-get="/os/vayumail/drive/pick?dir=` + strconv.FormatInt(dir, 10) + `"`,
		`data-c-drive-file="` + strconv.FormatInt(ids["notes.txt"], 10) + `" data-name="notes.txt">`,
		`data-c-drive-file="` + strconv.FormatInt(ids["film.bin"], 10) + `" data-name="film.bin" disabled title="Larger than a message takes">`,
		"notes.txt · 5 B",
	} {
		if !strings.Contains(top, want) {
			t.Errorf("the top of the menu lacks %q:\n%s", want, top)
		}
	}
	if strings.Contains(top, "deal.pdf") || strings.Contains(top, ">Back<") {
		t.Errorf("the top lists a folder's file, or a way back from the top:\n%s", top)
	}
	in := calGet(a, a.handleVayuOSDrivePick, "/os/vayumail/drive/pick?dir="+strconv.FormatInt(dir, 10), dana(), "").Body.String()
	if !strings.Contains(in, `data-name="deal.pdf"`) || !strings.Contains(in, `hx-get="/os/vayumail/drive/pick?dir=0"`) || !strings.Contains(in, "<span>Back</span>") || strings.Contains(in, "notes.txt") {
		t.Errorf("inside the folder:\n%s", in)
	}

	if rec := calGet(a, a.handleVayuOSDrivePick, "/os/vayumail/drive/pick", rulesAdmin(), ""); rec.Code != http.StatusForbidden {
		t.Errorf("the menu answered an administrator %d", rec.Code)
	}
	// Another holder asking for dana's folder gets their own, empty, top.
	theirs := calGet(a, a.handleVayuOSDrivePick, "/os/vayumail/drive/pick?dir="+strconv.FormatInt(dir, 10), eli(t, a), "").Body.String()
	if strings.Contains(theirs, "deal.pdf") || !strings.Contains(theirs, "Nothing is here.") {
		t.Errorf("another holder's menu for dana's folder:\n%s", theirs)
	}
}
