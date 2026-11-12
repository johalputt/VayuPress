// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

const londonZone = "BEGIN:VTIMEZONE\r\nTZID:Europe/London\r\nBEGIN:STANDARD\r\nDTSTART:19701025T020000\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\nEND:STANDARD\r\nBEGIN:DAYLIGHT\r\nDTSTART:19700329T010000\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0100\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\nEND:DAYLIGHT\r\nEND:VTIMEZONE"

// icsFile is a file as another calendar program exports it.
func icsFile(lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Other//Other//EN\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n"
}

func importICS(t *testing.T, e *Engine, rd Reader, file string) CalImport {
	t.Helper()
	res, err := e.CalendarImport(rd, strings.NewReader(file), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func occurrences(t *testing.T, e *Engine, rd Reader, from, to time.Time) string {
	t.Helper()
	evs, err := e.CalendarBetween(rd, from, to, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Summary+" "+ev.Start.UTC().Format("Jan 2 15:04"))
	}
	return strings.Join(out, ", ")
}

// TestACalendarGoesOutAndComesBackTheSame — exported and imported into
// another mailbox, a weekly London event across the clock change, one of
// its occurrences moved on its own, and a task are the same occurrences at
// the same instants; London's zone is written once though two events name
// it.
func TestACalendarGoesOutAndComesBackTheSame(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "standup.ics", "standup@x", londonZone, "BEGIN:VEVENT", "UID:standup@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Standup",
		"DTSTART;TZID=Europe/London:20261019T090000", "DTEND;TZID=Europe/London:20261019T091500", "RRULE:FREQ=WEEKLY;COUNT=3", "END:VEVENT",
		"BEGIN:VEVENT", "UID:standup@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Standup moved", "RECURRENCE-ID;TZID=Europe/London:20261102T090000",
		"DTSTART;TZID=Europe/London:20261102T110000", "DTEND;TZID=Europe/London:20261102T111500", "END:VEVENT")
	putEvent(t, s, "lunch.ics", "lunch@x", londonZone, "BEGIN:VEVENT", "UID:lunch@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Lunch",
		"DTSTART;TZID=Europe/London:20261021T120000", "DTEND;TZID=Europe/London:20261021T130000", "END:VEVENT")
	putEvent(t, s, "task.ics", "task@x", "BEGIN:VTODO", "UID:task@x", "DTSTAMP:20261001T000000Z", "SUMMARY:File the return", "END:VTODO")

	var out bytes.Buffer
	if err := e.CalendarExport(ReadAsOwner("ankush"), "ankush@example.com", &out); err != nil {
		t.Fatal(err)
	}
	file := out.String()
	if !strings.HasPrefix(file, "BEGIN:VCALENDAR\r\nPRODID:"+calProdID+"\r\nVERSION:2.0\r\n") || !strings.HasSuffix(file, "END:VCALENDAR\r\n") {
		t.Fatalf("the file is not one calendar:\n%s", file)
	}
	if n := strings.Count(file, "BEGIN:VCALENDAR"); n != 1 {
		t.Errorf("%d calendars in one file", n)
	}
	if n := strings.Count(file, "TZID:Europe/London\r\n"); n != 1 {
		t.Errorf("London's zone is written %d times", n)
	}
	for _, want := range []string{"UID:standup@x", "RECURRENCE-ID;TZID=Europe/London:20261102T090000", "UID:lunch@x", "BEGIN:VTODO"} {
		if !strings.Contains(file, want) {
			t.Errorf("the file has no %q", want)
		}
	}

	dana := ReadAsOwner("dana")
	if res := importICS(t, e, dana, file); res != (CalImport{Added: 3}) {
		t.Fatalf("the import: %+v", res)
	}
	from, to := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	mine, theirs := occurrences(t, e, ReadAsOwner("ankush"), from, to), occurrences(t, e, dana, from, to)
	if mine != theirs {
		t.Errorf("exported:\n%s\nimported:\n%s", mine, theirs)
	}
	if theirs != "Standup Oct 19 08:00, Lunch Oct 21 11:00, Standup Oct 26 09:00, Standup moved Nov 2 11:00" {
		t.Errorf("imported: %s", theirs)
	}
	// Each UID is one object again, with the zone it names.
	o, err := s.eventByUID(context.Background(), "dana@example.com", "standup@x")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(o.Data); strings.Count(got, "BEGIN:VEVENT") != 2 || !strings.Contains(got, "TZID:Europe/London\r\n") || strings.Contains(got, "lunch@x") {
		t.Errorf("the series' object:\n%s", got)
	}
}

// TestAnImportAddsAndNeverReplaces — the same file twice adds nothing, and
// an event edited since keeps its edit.
func TestAnImportAddsAndNeverReplaces(t *testing.T) {
	e, s := calEngine(t)
	rd := ReadAsOwner("ankush")
	file := icsFile("BEGIN:VEVENT", "UID:dentist@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Dentist", "DTSTART:20261110T090000Z", "DTEND:20261110T100000Z", "END:VEVENT",
		"BEGIN:VEVENT", "UID:gym@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Gym", "DTSTART:20261111T090000Z", "DTEND:20261111T100000Z", "END:VEVENT")
	if res := importICS(t, e, rd, file); res != (CalImport{Added: 2}) {
		t.Fatalf("first import: %+v", res)
	}
	o, err := s.eventByUID(context.Background(), calBox, "dentist@x")
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(o.Data, []byte("SUMMARY:Dentist"), []byte("SUMMARY:Dentist (moved by phone)"), 1)
	if _, err := s.davPut(context.Background(), calBox, davEvent, o.Name, "dentist@x", edited, davCondition{}); err != nil {
		t.Fatal(err)
	}
	if res := importICS(t, e, rd, file); res != (CalImport{Already: 2}) {
		t.Fatalf("second import: %+v", res)
	}
	from, to := month(2026, 11)
	if got := occurrences(t, e, rd, from, to); got != "Dentist (moved by phone) Nov 10 09:00, Gym Nov 11 09:00" {
		t.Errorf("after the second import: %s", got)
	}
}

// TestAnImportedInvitationInvitesNobody — a file with METHOD:REQUEST (an
// invitation saved as a file) keeps the event, not the method: stored with
// a METHOD, CalDAV would refuse to serve it, and nothing is sent.
func TestAnImportedInvitationInvitesNobody(t *testing.T) {
	e, s, flush := itipEngine(t)
	file := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Other//Other//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:inv@x\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Review\r\n" +
		"ORGANIZER:mailto:ankush@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:someone@elsewhere.test\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if res := importICS(t, e, ReadAsOwner("ankush"), file); res != (CalImport{Added: 1}) {
		t.Fatalf("the import: %+v", res)
	}
	o, err := s.eventByUID(context.Background(), calBox, "inv@x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(o.Data), "METHOD") {
		t.Errorf("the method was kept:\n%s", o.Data)
	}
	if sent := flush(); len(sent) != 0 {
		t.Errorf("an import sent %d messages", len(sent))
	}
}

// TestWhatAnImportCannotKeepIsCounted — an entry with no UID is kept as its
// own; a journal entry is another kind and is refused; an event past a
// megabyte is refused and the rest kept.
func TestWhatAnImportCannotKeepIsCounted(t *testing.T) {
	e, s := calEngine(t)
	rd := ReadAsOwner("ankush")
	big := "DESCRIPTION:" + strings.Repeat("x", maxDAVObject)
	file := icsFile("BEGIN:VEVENT", "SUMMARY:One", "DTSTART:20261110T090000Z", "DTEND:20261110T100000Z", "END:VEVENT",
		"BEGIN:VEVENT", "SUMMARY:Two", "DTSTART:20261111T090000Z", "DTEND:20261111T100000Z", "END:VEVENT",
		"BEGIN:VJOURNAL", "UID:j@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Diary", "END:VJOURNAL",
		"BEGIN:VEVENT", "UID:big@x", "DTSTAMP:20261001T000000Z", "SUMMARY:Big", big, "DTSTART:20261112T090000Z", "END:VEVENT")
	if res := importICS(t, e, rd, file); res != (CalImport{Added: 2, Refused: 2}) {
		t.Fatalf("the import: %+v", res)
	}
	from, to := month(2026, 11)
	if got := occurrences(t, e, rd, from, to); got != "One Nov 10 09:00, Two Nov 11 09:00" {
		t.Errorf("kept: %s", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT uid) FROM vayumail_dav WHERE mailbox=? AND kind=?`, calBox, davEvent).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d UIDs for two events without one (%v)", n, err)
	}
}

// TestAnImportStopsWhereTheCalendarIsFull — past the calendar's room the
// rest of the file is not read, and the import says so.
func TestAnImportStopsWhereTheCalendarIsFull(t *testing.T) {
	e, s := calEngine(t)
	tx, _ := s.db.Begin()
	for i := 1; i < maxDAVObjects; i++ {
		_, _ = tx.Exec(`INSERT INTO vayumail_dav(mailbox,kind,name,uid,etag,data,modified) VALUES(?,?,?,?,?,?,?)`, calBox, davEvent, "f"+strconv.Itoa(i), "f"+strconv.Itoa(i), "t", []byte("x"), time.Now())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	file := icsFile("BEGIN:VEVENT", "UID:a@x", "DTSTAMP:20261001T000000Z", "DTSTART:20261110T090000Z", "END:VEVENT",
		"BEGIN:VEVENT", "UID:b@x", "DTSTAMP:20261001T000000Z", "DTSTART:20261111T090000Z", "END:VEVENT",
		"BEGIN:VEVENT", "UID:c@x", "DTSTAMP:20261001T000000Z", "DTSTART:20261112T090000Z", "END:VEVENT")
	if res := importICS(t, e, ReadAsOwner("ankush"), file); res != (CalImport{Added: 1, Full: true}) {
		t.Errorf("the import: %+v", res)
	}
}

// TestAnImportsFileIsBoundedAndRead — past 20 MB, or not a calendar, is
// refused before anything is kept; an administrator neither exports nor
// imports a holder's calendar.
func TestAnImportsFileIsBoundedAndRead(t *testing.T) {
	e, _ := calEngine(t)
	rd := ReadAsOwner("ankush")
	now := time.Now()
	if _, err := e.CalendarImport(rd, strings.NewReader(strings.Repeat("x", MaxCalendarImport+1)), now); !errors.Is(err, ErrICSTooLarge) {
		t.Errorf("a file past the bound: %v", err)
	}
	if _, err := e.CalendarImport(rd, strings.NewReader("Name,Date\r\nDentist,2026-11-10\r\n"), now); !errors.Is(err, ErrICSUnreadable) {
		t.Errorf("a spreadsheet: %v", err)
	}
	if _, err := e.CalendarImport(rd, strings.NewReader(""), now); !errors.Is(err, ErrICSUnreadable) {
		t.Errorf("an empty file: %v", err)
	}
	// A file damaged after its first calendar keeps that calendar, and says
	// the rest was not read: cut off, or not iCalendar.
	first := icsFile("BEGIN:VEVENT", "UID:a@x", "DTSTAMP:20261001T000000Z", "DTSTART:20261110T090000Z", "END:VEVENT")
	for name, tail := range map[string]string{"cut off": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:b@x\r\n", "cut off in lower case": "begin:vcalendar\r\nbegin:vevent\r\nuid:b@x\r\n", "nonsense": "this is not a calendar\r\n"} {
		if res, err := e.CalendarImport(ReadAsOwner(strings.ReplaceAll(name, " ", "")), strings.NewReader(first+tail), now); err != nil || res != (CalImport{Added: 1, Damaged: true}) {
			t.Errorf("a file %s: %+v %v", name, res, err)
		}
	}
	if res, _ := e.CalendarImport(ReadAsOwner("whole"), strings.NewReader(first+first), now); res != (CalImport{Added: 1, Already: 1}) {
		t.Errorf("a whole file of two calendars: %+v", res)
	}
	op := ReadAsOperator("ankush", "boss@example.com")
	if _, err := e.CalendarImport(op, strings.NewReader(icsFile("BEGIN:VEVENT", "UID:a@x", "DTSTART:20261110T090000Z", "END:VEVENT")), now); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Errorf("an administrator's import: %v", err)
	}
	if err := e.CalendarExport(op, "x", &bytes.Buffer{}); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Errorf("an administrator's export: %v", err)
	}
}

// TestTheCalendarsNameIsEscaped — the name goes in the file as text: a
// comma escaped, a carriage return or line feed unable to start a property
// of its own.
func TestTheCalendarsNameIsEscaped(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "x.ics", "x@x", "BEGIN:VEVENT", "UID:x@x", "DTSTAMP:20261001T000000Z", "DTSTART:20261110T090000Z", "END:VEVENT")
	var out bytes.Buffer
	if err := e.CalendarExport(ReadAsOwner("ankush"), "Work, home\r\nMETHOD:REQUEST", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "X-WR-CALNAME:Work\\, home  METHOD:REQUEST\r\n") || strings.Contains(out.String(), "\r\nMETHOD") {
		t.Errorf("the head:\n%s", out.String())
	}
}
