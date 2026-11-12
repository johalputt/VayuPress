// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestDrivesSettingsVersionsAndTrashFromThePage — the settings sheet saves
// the holder's choices; an earlier version downloads as it was while the
// file stays; Trash says how long it keeps things and empties; a link says
// when it was last downloaded.
func TestDrivesSettingsVersionsAndTrashFromThePage(t *testing.T) {
	a := calendarApp(t)
	if sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?settings=1", dana(), "").Body.String(); !strings.Contains(sheet, `<option value="5" selected>5 versions</option>`) ||
		!strings.Contains(sheet, `<option value="30" selected>30 days</option>`) {
		t.Fatalf("the settings sheet: %s", sheet)
	}
	if got := driveAct(a, dana(), url.Values{"op": {"settings"}, "versions": {"3"}, "trash_days": {"7"}}).Body.String(); !strings.Contains(got, "Saved.") || !strings.Contains(got, `<option value="7" selected>7 days</option>`) {
		t.Fatalf("saving: %s", got)
	}
	if got := driveAct(a, dana(), url.Values{"op": {"settings"}, "versions": {"0"}, "trash_days": {"7"}}).Body.String(); !strings.Contains(got, "versions kept are 1 to 20") {
		t.Errorf("a choice out of range: %s", got)
	}
	if ds, _ := a.vayuMail.DriveSettingsOf(vmail.ReadAsOwner("dana")); ds.Versions != 3 || ds.TrashDays != 7 {
		t.Errorf("kept as %+v", ds)
	}

	driveUpload(a, dana(), 0, "plan.txt", "the first plan")
	driveUpload(a, dana(), 0, "plan.txt", "the second plan")
	id := driveIDs(t, a, "dana", 0)["plan.txt"]
	vs, _ := a.vayuMail.DriveVersions(vmail.ReadAsOwner("dana"), id)
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String()
	href := "/os/vayumail/drive/file?id=" + strconv.FormatInt(id, 10) + "&amp;version=" + strconv.FormatInt(vs[0].ID, 10)
	if !strings.Contains(sheet, href) {
		t.Fatalf("the sheet has no download of the version: %s", sheet)
	}
	rec := calGet(a, a.handleVayuOSDriveFile, strings.ReplaceAll(href, "&amp;", "&"), dana(), "")
	if rec.Body.String() != "the first plan" || rec.Header().Get("Content-Disposition") != `attachment; filename="plan.txt"` {
		t.Errorf("the version's download: %q %v", rec.Body.String(), rec.Header())
	}
	if got := calGet(a, a.handleVayuOSDriveFile, "/os/vayumail/drive/file?id="+strconv.FormatInt(id, 10), dana(), "").Body.String(); got != "the second plan" {
		t.Errorf("the file after its version was read: %q", got)
	}

	// A link, downloaded once.
	token, err := a.vayuMail.DriveShareFile(vmail.ReadAsOwner("dana"), id, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, f, err := a.vayuMail.DriveSharedOpen(token, ""); err == nil {
		_ = f.Close()
	}
	if sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String(); !strings.Contains(sheet, "last downloaded") {
		t.Errorf("the link does not say when it was last downloaded: %s", sheet)
	}

	if err := a.vayuMail.DriveTrash(vmail.ReadAsOwner("dana"), id, false); err != nil {
		t.Fatal(err)
	}
	page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive?view=trash", dana(), "").Body.String()
	if !strings.Contains(page, "A file stays here 7 days") || !strings.Contains(page, "Empty Trash") {
		t.Fatalf("the Trash page: %s", page)
	}
	driveAct(a, dana(), url.Values{"op": {"empty"}})
	if left, _ := a.vayuMail.DriveTrashed(vmail.ReadAsOwner("dana")); len(left) != 0 {
		t.Errorf("%d left after Empty Trash", len(left))
	}
}
