// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const calBox = "ankush@example.com"

func calEngine(t *testing.T) (*Engine, *AccountStore) {
	t.Helper()
	s := aliasTestStore(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	return &Engine{cfg: cfg, accounts: s, db: s.db}, s
}

// putEvent stores an object as a phone would write it over CalDAV.
func putEvent(t *testing.T, s *AccountStore, name, uid string, lines ...string) {
	t.Helper()
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//test//EN\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n"
	if _, err := s.davPut(context.Background(), calBox, davEvent, name, uid, []byte(body), davCondition{}); err != nil {
		t.Fatal(err)
	}
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func between(t *testing.T, e *Engine, from, to time.Time, home *time.Location) []CalEvent {
	t.Helper()
	evs, err := e.CalendarBetween(ReadAsOwner("ankush"), from, to, home)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func month(y int, m time.Month) (time.Time, time.Time) {
	from := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 1, 0)
}

// TestAWeeklyEventKeepsItsLocalTimeAcrossAClockChange — London moves its
// clocks on 25 October 2026; a 09:00 London meeting stays at 09:00 London,
// so it moves by an hour in UTC and for a holder in Kolkata.
func TestAWeeklyEventKeepsItsLocalTimeAcrossAClockChange(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "standup.ics", "standup@x", "BEGIN:VEVENT", "UID:standup@x", "SUMMARY:Standup",
		"DTSTART;TZID=Europe/London:20261020T090000", "DTEND;TZID=Europe/London:20261020T093000",
		"RRULE:FREQ=WEEKLY;COUNT=3", "END:VEVENT")
	kolkata := zone(t, "Asia/Kolkata")
	evs := between(t, e, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), kolkata)
	if len(evs) != 3 {
		t.Fatalf("%d occurrences, want 3", len(evs))
	}
	london := zone(t, "Europe/London")
	wantUTC := []int{8, 9, 9}
	wantKolkata := []string{"13:30", "14:30", "14:30"}
	for i, ev := range evs {
		if h := ev.Start.In(london).Hour(); h != 9 {
			t.Errorf("occurrence %d is at %d:00 London, want 9:00", i, h)
		}
		if h := ev.Start.UTC().Hour(); h != wantUTC[i] {
			t.Errorf("occurrence %d is at %d:00 UTC, want %d:00", i, h, wantUTC[i])
		}
		if k := ev.Start.In(kolkata).Format("15:04"); k != wantKolkata[i] {
			t.Errorf("occurrence %d is at %s in Kolkata, want %s", i, k, wantKolkata[i])
		}
		if ev.End.Sub(ev.Start) != 30*time.Minute || ev.Zone != "Europe/London" || !ev.Repeats() {
			t.Errorf("occurrence %d: %+v", i, ev)
		}
	}
}

// TestExceptionDatesInOneList — EXDATE may carry several dates in one line.
func TestExceptionDatesInOneList(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "d.ics", "d@x", "BEGIN:VEVENT", "UID:d@x", "SUMMARY:Daily",
		"DTSTART:20261102T100000Z", "DURATION:PT1H", "RRULE:FREQ=DAILY;COUNT=5",
		"EXDATE:20261103T100000Z,20261105T100000Z", "END:VEVENT")
	from, to := month(2026, 11)
	var days []int
	for _, ev := range between(t, e, from, to, time.UTC) {
		days = append(days, ev.Start.Day())
		if ev.End.Sub(ev.Start) != time.Hour {
			t.Errorf("DURATION was not read: %v", ev.End.Sub(ev.Start))
		}
	}
	if len(days) != 3 || days[0] != 2 || days[1] != 4 || days[2] != 6 {
		t.Fatalf("days %v, want [2 4 6]", days)
	}
}

// TestAnOccurrenceEditedOnItsOwn — a RECURRENCE-ID replaces the occurrence
// it names, at its new time, and the old slot is empty.
func TestAnOccurrenceEditedOnItsOwn(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "w.ics", "w@x",
		"BEGIN:VEVENT", "UID:w@x", "SUMMARY:Review", "DTSTART:20261103T150000Z", "DTEND:20261103T160000Z", "RRULE:FREQ=WEEKLY;COUNT=3", "END:VEVENT",
		"BEGIN:VEVENT", "UID:w@x", "SUMMARY:Review (moved)", "RECURRENCE-ID:20261110T150000Z", "DTSTART:20261111T090000Z", "DTEND:20261111T100000Z", "END:VEVENT")
	from, to := month(2026, 11)
	evs := between(t, e, from, to, time.UTC)
	if len(evs) != 3 {
		t.Fatalf("%d occurrences: %+v", len(evs), evs)
	}
	if evs[1].Summary != "Review (moved)" || evs[1].Start.Day() != 11 || evs[1].Instance.Day() != 10 {
		t.Fatalf("the edited occurrence is %+v", evs[1])
	}
	for _, ev := range evs {
		if ev.Start.Day() == 10 {
			t.Fatal("the moved occurrence still shows in its old slot")
		}
	}
}

// TestAllDayEventsAreDays — a two-day event ends at the second midnight in
// the holder's zone each year, whether or not the clocks change inside it
// (London's do on 25 October 2026, and on 31 October 2027, after it).
func TestAllDayEventsAreDays(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "a.ics", "a@x", "BEGIN:VEVENT", "UID:a@x", "SUMMARY:Trip",
		"DTSTART;VALUE=DATE:20261024", "DTEND;VALUE=DATE:20261026", "RRULE:FREQ=YEARLY;COUNT=2", "END:VEVENT")
	london := zone(t, "Europe/London")
	evs := between(t, e, time.Date(2026, 10, 1, 0, 0, 0, 0, london), time.Date(2027, 11, 1, 0, 0, 0, 0, london), london)
	if len(evs) != 2 {
		t.Fatalf("%+v", evs)
	}
	for i, want := range []string{"2026-10-26 00:00", "2027-10-26 00:00"} {
		if got := evs[i].End.In(london).Format("2006-01-02 15:04"); got != want || !evs[i].AllDay {
			t.Errorf("occurrence %d ends %s, want %s London", i, got, want)
		}
		if evs[i].Zone != "" {
			t.Errorf("an all-day event shows a zone: %q", evs[i].Zone)
		}
	}
}

// TestAFloatingTimeIsTheHoldersOwn — a time with no zone is that time
// wherever the holder is.
func TestAFloatingTimeIsTheHoldersOwn(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "f.ics", "f@x", "BEGIN:VEVENT", "UID:f@x", "SUMMARY:Gym", "DTSTART:20261109T070000", "END:VEVENT")
	tokyo := zone(t, "Asia/Tokyo")
	from, to := month(2026, 11)
	evs := between(t, e, from, to, tokyo)
	if len(evs) != 1 || evs[0].Start.In(tokyo).Hour() != 7 || evs[0].Zone != "" {
		t.Fatalf("%+v", evs)
	}
}

// TestTheStartIsAlwaysTheFirstInstance — a start the rule would not make is
// still an occurrence (RFC 5545 §3.8.5.3).
func TestTheStartIsAlwaysTheFirstInstance(t *testing.T) {
	e, s := calEngine(t)
	// 4 November 2026 is a Wednesday; the rule is for Tuesdays.
	putEvent(t, s, "s.ics", "s@x", "BEGIN:VEVENT", "UID:s@x", "SUMMARY:Class", "DTSTART:20261104T100000Z",
		"RRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20261118T235959Z", "END:VEVENT")
	from, to := month(2026, 11)
	var days []int
	for _, ev := range between(t, e, from, to, time.UTC) {
		days = append(days, ev.Start.Day())
	}
	if len(days) != 3 || days[0] != 4 || days[1] != 10 || days[2] != 17 {
		t.Fatalf("days %v, want [4 10 17]", days)
	}
}

// TestTheLastWorkDayOfTheMonth — RFC 5545's own BYSETPOS example.
func TestTheLastWorkDayOfTheMonth(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "p.ics", "p@x", "BEGIN:VEVENT", "UID:p@x", "SUMMARY:Payroll", "DTSTART:20261030T090000Z",
		"RRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1;COUNT=3", "END:VEVENT")
	var got []string
	for _, ev := range between(t, e, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), time.UTC) {
		got = append(got, ev.Start.Format("01-02"))
	}
	if strings.Join(got, " ") != "10-30 11-30 12-31" {
		t.Fatalf("got %v, want 10-30 11-30 12-31", got)
	}
}

// TestZoneNamesOtherProgramsWrite — a desktop program's zone name, a name
// behind a vendor's path, and a zone known only by its VTIMEZONE.
func TestZoneNamesOtherProgramsWrite(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "win.ics", "win@x", "BEGIN:VEVENT", "UID:win@x", "SUMMARY:Win",
		"DTSTART;TZID=Eastern Standard Time:20261110T090000", "END:VEVENT")
	putEvent(t, s, "path.ics", "path@x", "BEGIN:VEVENT", "UID:path@x", "SUMMARY:Path",
		"DTSTART;TZID=/vendor.example/20050126_1/America/Argentina/Buenos_Aires:20261110T090000", "END:VEVENT")
	putEvent(t, s, "vtz.ics", "vtz@x",
		"BEGIN:VTIMEZONE", "TZID:Office Time", "BEGIN:STANDARD", "DTSTART:19700101T000000", "TZOFFSETFROM:+0530", "TZOFFSETTO:+0530", "END:STANDARD", "END:VTIMEZONE",
		"BEGIN:VEVENT", "UID:vtz@x", "SUMMARY:Vtz", "DTSTART;TZID=Office Time:20261110T090000", "END:VEVENT")
	from, to := month(2026, 11)
	want := map[string]string{"Win": "14:00", "Path": "12:00", "Vtz": "03:30"}
	evs := between(t, e, from, to, time.UTC)
	if len(evs) != 3 {
		t.Fatalf("%d events", len(evs))
	}
	for _, ev := range evs {
		if got := ev.Start.UTC().Format("15:04"); got != want[ev.Summary] {
			t.Errorf("%s at %s UTC, want %s", ev.Summary, got, want[ev.Summary])
		}
	}
}

// TestARuleThatNeverEndsCannotHoldUpAView — an every-minute rule from 1970,
// as any app may write, is walked only so far, and the view still answers
// with the holder's other events.
func TestARuleThatNeverEndsCannotHoldUpAView(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "a-flood.ics", "flood@x", "BEGIN:VEVENT", "UID:flood@x", "SUMMARY:Flood", "DTSTART:19700101T000000Z", "RRULE:FREQ=MINUTELY", "END:VEVENT")
	putEvent(t, s, "b-weekly.ics", "weekly@x", "BEGIN:VEVENT", "UID:weekly@x", "SUMMARY:Weekly", "DTSTART:20261102T100000Z", "RRULE:FREQ=WEEKLY", "END:VEVENT")
	from, to := month(2026, 11)
	start := time.Now()
	evs := between(t, e, from, to, time.UTC)
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("the view took %v", d)
	}
	weekly := 0
	for _, ev := range evs {
		if ev.Summary == "Weekly" {
			weekly++
		}
	}
	if weekly != 5 {
		t.Fatalf("the other event shows %d times, want 5", weekly)
	}
}

// TestAnAdministratorCannotOpenACalendar — not even of a mailbox that is not
// private.
func TestAnAdministratorCannotOpenACalendar(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "x.ics", "x@x", "BEGIN:VEVENT", "UID:x@x", "SUMMARY:Doctor", "DTSTART:20261110T090000Z", "END:VEVENT")
	from, to := month(2026, 11)
	if _, err := e.CalendarBetween(ReadAsOperator("ankush", "boss@example.com"), from, to, time.UTC); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Fatalf("an administrator's read: %v", err)
	}
	if _, err := e.SaveEvent(ReadAsOperator("ankush", "boss@example.com"), EventInput{Summary: "x", Start: from, End: from}, ScopeAll, time.Time{}); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Fatalf("an administrator's write: %v", err)
	}
	if err := e.DeleteEvent(ReadAsOperator("ankush", "boss@example.com"), "x@x", ScopeAll, time.Time{}); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Fatalf("an administrator's delete: %v", err)
	}
}

// TestEditingKeepsWhatTheEditorHasNoFieldFor — a phone's alarm and its own
// property survive an edit in the console.
func TestEditingKeepsWhatTheEditorHasNoFieldFor(t *testing.T) {
	e, s := calEngine(t)
	putEvent(t, s, "k.ics", "k@x", "BEGIN:VEVENT", "UID:k@x", "SUMMARY:Dentist", "DTSTART:20261110T090000Z", "DTEND:20261110T100000Z",
		"X-PHONE-COLOUR:teal", "BEGIN:VALARM", "ACTION:DISPLAY", "TRIGGER:-PT15M", "END:VALARM", "END:VEVENT")
	rd := ReadAsOwner("ankush")
	start := time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)
	if _, err := e.SaveEvent(rd, EventInput{UID: "k@x", Summary: "Dentist (later)", Start: start, End: start.Add(time.Hour)}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	o, _ := s.eventByUID(context.Background(), calBox, "k@x")
	for _, want := range []string{"X-PHONE-COLOUR:teal", "BEGIN:VALARM", "TRIGGER:-PT15M", "SUMMARY:Dentist (later)", "SEQUENCE:1"} {
		if !strings.Contains(string(o.Data), want) {
			t.Errorf("after the edit the event lacks %q:\n%s", want, o.Data)
		}
	}
}

// TestASeriesMadeAndEditedFromTheConsole — made, one occurrence moved, the
// rest split from the third, one removed; each as RFC 5545 has it. In New
// York, five hours from UTC that month, so an occurrence named in the wrong
// zone names none.
func TestASeriesMadeAndEditedFromTheConsole(t *testing.T) {
	e, _ := calEngine(t)
	rd := ReadAsOwner("ankush")
	york := zone(t, "America/New_York")
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, york)
	in := EventInput{Summary: "Team", Start: start, End: start.Add(time.Hour), Rule: "FREQ=WEEKLY;COUNT=4"}
	uid, err := e.SaveEvent(rd, in, ScopeAll, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	from, to := month(2026, 11)
	if n := len(between(t, e, from, to, york)); n != 4 {
		t.Fatalf("%d occurrences made, want 4", n)
	}
	// The second, alone, an hour later.
	second := start.AddDate(0, 0, 7)
	moved := in
	moved.UID, moved.Summary, moved.Start, moved.End = uid, "Team (late)", second.Add(time.Hour), second.Add(2*time.Hour)
	if _, err := e.SaveEvent(rd, moved, ScopeOne, second); err != nil {
		t.Fatal(err)
	}
	evs := between(t, e, from, to, york)
	if len(evs) != 4 || evs[1].Summary != "Team (late)" || evs[1].Start.In(york).Hour() != 10 || evs[0].Summary != "Team" || evs[2].Summary != "Team" {
		t.Fatalf("after moving the second: %+v", evs)
	}
	// From the third on, a new title.
	third := start.AddDate(0, 0, 14)
	renamed := in
	renamed.UID, renamed.Summary, renamed.Start, renamed.End, renamed.Rule = uid, "Team sync", third, third.Add(time.Hour), "FREQ=WEEKLY;COUNT=2"
	newUID, err := e.SaveEvent(rd, renamed, ScopeFollowing, third)
	if err != nil || newUID == uid {
		t.Fatalf("split: %q %v", newUID, err)
	}
	var titles []string
	for _, ev := range between(t, e, from, to, york) {
		titles = append(titles, ev.Summary)
	}
	if strings.Join(titles, "|") != "Team|Team (late)|Team sync|Team sync" {
		t.Fatalf("after the split: %v", titles)
	}
	// The fourth removed.
	if err := e.DeleteEvent(rd, newUID, ScopeOne, third.AddDate(0, 0, 7)); err != nil {
		t.Fatal(err)
	}
	if n := len(between(t, e, from, to, york)); n != 3 {
		t.Fatalf("%d occurrences after removing one, want 3", n)
	}
	if err := e.DeleteEvent(rd, uid, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n := len(between(t, e, from, to, york)); n != 1 {
		t.Fatalf("%d occurrences after removing the first series, want 1", n)
	}
}

// TestTheEditorsRefusals — the shapes an event from the form cannot have.
func TestTheEditorsRefusals(t *testing.T) {
	e, _ := calEngine(t)
	rd := ReadAsOwner("ankush")
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for name, in := range map[string]EventInput{
		"no title":         {Start: at, End: at},
		"ends before":      {Summary: "x", Start: at, End: at.Add(-time.Hour)},
		"a day of nothing": {Summary: "x", Start: at, End: at, AllDay: true},
		"every minute":     {Summary: "x", Start: at, End: at, Rule: "FREQ=MINUTELY"},
		"unreadable rule":  {Summary: "x", Start: at, End: at, Rule: "FREQ=SOMETIMES"},
	} {
		if _, err := e.SaveEvent(rd, in, ScopeAll, time.Time{}); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
}

// TestNotesTypedOnSeveralLinesAreSaved — as a browser sends them, CRLF.
func TestNotesTypedOnSeveralLinesAreSaved(t *testing.T) {
	e, _ := calEngine(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), EventInput{Summary: "Plan", Description: "First line\r\nSecond line", Start: at, End: at.Add(time.Hour)}, ScopeAll, time.Time{}); err != nil {
		t.Fatalf("notes on two lines: %v", err)
	}
	evs := between(t, e, at, at.Add(time.Hour), time.UTC)
	if len(evs) != 1 || evs[0].Description != "First line\nSecond line" {
		t.Fatalf("read back %+v", evs)
	}
}
