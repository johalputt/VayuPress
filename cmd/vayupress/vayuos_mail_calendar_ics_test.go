// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func calImport(a *App, u *users.User, name, body string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// A field before the file, as a form may send one, is not the file.
	_ = mw.WriteField("note", "BEGIN:VCALENDAR")
	part, _ := mw.CreateFormFile("file", name)
	_, _ = part.Write([]byte(body))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/calendar/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleVayuOSCalendarImport(rec, withUser(req, u))
	return rec
}

// TestACalendarIsExportedAndImportedFromItsPage — the side offers both; the
// export is the holder's events as a .ics download that runs nothing in the
// page; importing it again says nothing was added, and a new file's events
// are in the week.
func TestACalendarIsExportedAndImportedFromItsPage(t *testing.T) {
	a := calendarApp(t)
	calSave(a, dana(), url.Values{"title": {"Pricing review"}, "sd": {"2026-11-10"}, "st": {"09:00"}, "ed": {"2026-11-10"}, "et": {"10:00"}, "zone": {"Europe/London"}})
	page := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar?v=week&d=2026-11-10", dana(), "Europe/London").Body.String()
	for _, want := range []string{`href="/os/vayumail/calendar/export" download`, `hx-post="/os/vayumail/calendar/import"`, `id="cal-import-said"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %q", want)
		}
	}

	rec := calGet(a, a.handleVayuOSCalendarExport, "/os/vayumail/calendar/export", dana(), "")
	file := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/calendar; charset=utf-8" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="dana@example.com.ics"` || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the export: %d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(file, "SUMMARY:Pricing review") || !strings.Contains(file, "X-WR-CALNAME:dana@example.com") {
		t.Fatalf("the file:\n%s", file)
	}
	if got := calImport(a, dana(), "dana.ics", file).Body.String(); got != "Nothing new: 1 already in the calendar, left as they are." {
		t.Errorf("the same file again: %q", got)
	}
	other := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Other//Other//EN\r\nBEGIN:VEVENT\r\nUID:dentist@elsewhere\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Dentist\r\nDTSTART:20261111T090000Z\r\nDTEND:20261111T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if got := calImport(a, dana(), "other.ics", other).Body.String(); got != `Imported 1 event. <a href="/os/vayumail/calendar">Show the calendar</a>.` {
		t.Errorf("a new file: %q", got)
	}
	if week := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar?v=week&d=2026-11-10", dana(), "Europe/London").Body.String(); !strings.Contains(week, "Dentist") {
		t.Error("the imported event is not in the week")
	}
	if got := calImport(a, dana(), "people.csv", "Name,Date\r\n").Body.String(); got != "Not imported: the file is not a calendar (.ics) file." {
		t.Errorf("a spreadsheet: %q", got)
	}
}

// TestACalendarFileIsOnlyItsHolders — an administrator without a mailbox
// of their own neither exports nor imports one.
func TestACalendarFileIsOnlyItsHolders(t *testing.T) {
	a := calendarApp(t)
	calSave(a, dana(), url.Values{"title": {"Private"}, "sd": {"2026-11-10"}, "st": {"09:00"}, "ed": {"2026-11-10"}, "et": {"10:00"}, "zone": {"UTC"}})
	boss := rulesAdmin()
	if rec := calGet(a, a.handleVayuOSCalendarExport, "/os/vayumail/calendar/export?user=dana", boss, ""); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "Private") {
		t.Errorf("an administrator's export: %d %s", rec.Code, rec.Body.String())
	}
	if rec := calImport(a, boss, "x.ics", "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"); rec.Code != http.StatusForbidden {
		t.Errorf("an administrator's import: %d", rec.Code)
	}
}
