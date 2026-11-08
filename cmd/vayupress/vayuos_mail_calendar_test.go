// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func calendarApp(t *testing.T) *App {
	t.Helper()
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	return a
}

func dana() *users.User {
	return &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
}

func calGet(a *App, h http.HandlerFunc, target string, u *users.User, tz string) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodGet, target, nil), u)
	if tz != "" {
		req.AddCookie(&http.Cookie{Name: calTZCookie, Value: url.QueryEscape(tz)})
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func calSave(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	vals.Set("op", "save")
	return postForm(a.handleVayuOSCalendarAction, "/os/vayumail/calendar/action", vals.Encode(), u)
}

// TestAHolderMakesARepeatingEventAndSeesItInTheirWeek — from the editor's
// fields to the week, the month and the list, at its own time.
func TestAHolderMakesARepeatingEventAndSeesItInTheirWeek(t *testing.T) {
	a := calendarApp(t)
	rec := calSave(a, dana(), url.Values{"title": {"Pricing review"}, "sd": {"2026-11-10"}, "st": {"09:00"}, "ed": {"2026-11-10"}, "et": {"10:30"},
		"zone": {"Europe/London"}, "location": {"Studio"}, "r_freq": {"WEEKLY"}, "r_int": {"1"}, "r_day": {"TU", "TH"}, "r_end": {"count"}, "r_count": {"4"}})
	if got := rec.Header().Get("HX-Redirect"); got != "/os/vayumail/calendar?v=week&d=2026-11-10" {
		t.Fatalf("save: %d %q %s", rec.Code, got, rec.Body.String())
	}
	week := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar?v=week&d=2026-11-10", dana(), "Europe/London").Body.String()
	if n := strings.Count(week, `class="cal-ev" data-top="540" data-mins="90"`); n != 2 {
		t.Fatalf("%d blocks at 09:00 for an hour and a half, want Tuesday and Thursday:\n%s", n, week)
	}
	if !strings.Contains(week, "9 – 15 November 2026") || !strings.Contains(week, "Pricing review") || !strings.Contains(week, "09:00 · repeats") {
		t.Error("the week does not show the event as it repeats")
	}
	// In Kolkata the same meeting is 14:30 (London is on GMT in November).
	if kol := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar?v=week&d=2026-11-10", dana(), "Asia/Kolkata").Body.String(); !strings.Contains(kol, `data-top="870"`) {
		t.Error("a holder in Kolkata does not see it at 14:30")
	}
	month := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar?v=month&d=2026-11-10", dana(), "Europe/London").Body.String()
	if n := strings.Count(month, `<span class="cal-chip__t">09:00</span> Pricing review`); n != 4 {
		t.Errorf("the month shows it %d times, want 4", n)
	}
	if !strings.Contains(month, `<li><button type="button" class="cal-agenda__ev"`) {
		t.Error("the list a phone shows is missing")
	}
}

// TestTheEventSheetSaysWhenAndHowItRepeats — in the holder's zone, and in
// the event's own when that is another.
func TestTheEventSheetSaysWhenAndHowItRepeats(t *testing.T) {
	a := calendarApp(t)
	calSave(a, dana(), url.Values{"title": {"Standup"}, "sd": {"2026-11-10"}, "st": {"09:00"}, "ed": {"2026-11-10"}, "et": {"09:15"},
		"zone": {"Europe/London"}, "r_freq": {"WEEKLY"}, "r_int": {"2"}, "r_day": {"TU", "TH"}, "r_end": {"until"}, "r_until": {"2026-12-15"}})
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC).Unix()
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), time.Unix(start, 0), time.Unix(start+60, 0), time.UTC)
	if len(evs) != 1 {
		t.Fatalf("%d events", len(evs))
	}
	q := "/os/vayumail/calendar/event?uid=" + url.QueryEscape(evs[0].UID) + "&s=" + itoa64(start) + "&i=" + itoa64(evs[0].Instance.Unix())
	body := calGet(a, a.handleVayuOSCalendarEvent, q, dana(), "Asia/Kolkata").Body.String()
	for _, want := range []string{"Tuesday 10 November 2026 · 14:30–14:45 (09:00–09:15 Europe/London)", "Every 2 weeks on Tuesday and Thursday, until 15 December 2026", "This one only", "This and following events"} {
		if !strings.Contains(body, want) {
			t.Errorf("the sheet lacks %q:\n%s", want, body)
		}
	}
}

// TestACalendarOpensOnlyForItsHolder — a session with no mailbox of its own
// gets none, whoever it is, and the places beside Mail are the holder's.
func TestACalendarOpensOnlyForItsHolder(t *testing.T) {
	a := calendarApp(t)
	boss := rulesAdmin()
	if body := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar", boss, "").Body.String(); !strings.Contains(body, "A calendar belongs to a mailbox, and this account has none") {
		t.Fatalf("an administrator without a mailbox: %s", body)
	}
	if rec := calGet(a, a.handleVayuOSCalendarEdit, "/os/vayumail/calendar/edit", boss, ""); rec.Code != http.StatusForbidden {
		t.Errorf("the editor answered an administrator %d", rec.Code)
	}
	if rec := calSave(a, boss, url.Values{"title": {"x"}, "sd": {"2026-11-10"}, "st": {"09:00"}, "ed": {"2026-11-10"}, "et": {"10:00"}, "zone": {"UTC"}}); rec.Code != http.StatusForbidden {
		t.Errorf("a save answered an administrator %d", rec.Code)
	}
	if got := mailPlaces(&osMailSide{User: "dana", Own: false}); strings.Contains(got, "Calendar") {
		t.Errorf("someone else's mailbox offers its calendar: %s", got)
	}
	if got := mailPlaces(&osMailSide{User: "dana", Own: true, Place: "calendar"}); !strings.Contains(got, `href="/os/vayumail/calendar" aria-current="page"`) {
		t.Errorf("the holder's places: %s", got)
	}
}

// TestOverlappingEventsShareTheWidth — events drawn over each other take a
// lane each; one alone has the column.
func TestOverlappingEventsShareTheWidth(t *testing.T) {
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	at := func(h, m, mins int) vmail.CalEvent {
		s := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		return vmail.CalEvent{Start: s, End: s.Add(time.Duration(mins) * time.Minute)}
	}
	segs := daySegments([]vmail.CalEvent{at(9, 0, 60), at(9, 30, 60), at(11, 0, 60), at(14, 0, 5), at(14, 10, 5)}, day)
	want := [][2]int{{0, 2}, {1, 2}, {0, 1}, {0, 2}, {1, 2}}
	if len(segs) != len(want) {
		t.Fatalf("%d segments", len(segs))
	}
	for i, s := range segs {
		if s.lane != want[i][0] || s.lanes != want[i][1] {
			t.Errorf("event %d in lane %d of %d, want %d of %d", i, s.lane, s.lanes, want[i][0], want[i][1])
		}
	}
	// An event across midnight shows in each day's column for its part.
	late := vmail.CalEvent{Start: day.Add(23 * time.Hour), End: day.Add(25 * time.Hour)}
	if s := daySegments([]vmail.CalEvent{late}, day.AddDate(0, 0, 1)); len(s) != 1 || s[0].top != 0 || s[0].mins != 60 {
		t.Errorf("the next day's part: %+v", s)
	}
}

// TestTheEditorsRepeatBecomesARule — and a rule it cannot say is kept as it
// is, not narrowed to what it can.
func TestTheEditorsRepeatBecomesARule(t *testing.T) {
	london, _ := time.LoadLocation("Europe/London")
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, london)
	form := func(v url.Values) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_ = r.ParseForm()
		return r
	}
	for _, c := range []struct {
		v    url.Values
		want string
	}{
		{url.Values{}, ""},
		{url.Values{"r_freq": {"WEEKLY"}, "r_int": {"2"}, "r_day": {"TU", "TH", "XX"}, "r_end": {"until"}, "r_until": {"2026-12-15"}}, "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH;UNTIL=20261215T235959Z"},
		{url.Values{"r_freq": {"MONTHLY"}, "r_end": {"count"}, "r_count": {"6"}}, "FREQ=MONTHLY;COUNT=6"},
		{url.Values{"r_freq": {"keep"}, "r_rule": {"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1"}}, "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1"},
	} {
		got, err := ruleFromCalForm(form(c.v), start, false)
		if err != nil || got != c.want {
			t.Errorf("%v: %q %v, want %q", c.v, got, err, c.want)
		}
	}
	for _, v := range []url.Values{{"r_freq": {"HOURLY"}}, {"r_freq": {"DAILY"}, "r_end": {"until"}, "r_until": {"2026-11-01"}}, {"r_freq": {"DAILY"}, "r_end": {"count"}, "r_count": {"0"}}} {
		if _, err := ruleFromCalForm(form(v), start, false); err == nil {
			t.Errorf("%v was taken", v)
		}
	}
	if editableRule("FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1") || !editableRule("FREQ=WEEKLY;BYDAY=TU,TH;COUNT=3") || editableRule("FREQ=MONTHLY;BYDAY=2TU") {
		t.Error("editableRule misjudges what the fields can say")
	}
	ed := calEditor(vmail.CalEvent{UID: "x@y", Summary: "Payroll", Start: start, End: start, Rule: "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", Instance: start}, "", london, "")
	if !strings.Contains(ed, `<option value="keep" selected>As it is: Every month</option>`) || !strings.Contains(ed, `name="r_rule" value="FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1"`) {
		t.Errorf("the editor does not keep a rule it cannot say:\n%s", ed)
	}
}

// TestTheRepeatInWords — what the event's sheet says.
func TestTheRepeatInWords(t *testing.T) {
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for rule, want := range map[string]string{
		"FREQ=DAILY":                                    "Every day",
		"FREQ=DAILY;INTERVAL=3;COUNT=5":                 "Every 3 days, 5 times",
		"FREQ=WEEKLY":                                   "Every week on Tuesday",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":              "Every weekday",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH":            "Every 2 weeks on Tuesday and Thursday",
		"FREQ=MONTHLY":                                  "Every month on day 10",
		"FREQ=YEARLY;UNTIL=20301110T235959Z":            "Every year on 10 November, until 10 November 2030",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1": "Every month",
		"FREQ=SOMETIMES":                                "Repeats",
	} {
		if got := repeatWords(rule, start); got != want {
			t.Errorf("%s: %q, want %q", rule, got, want)
		}
	}
}

// TestTheZoneIsTheBrowsersWhenItIsAZone — a cookie naming the server's own
// zone, or no zone, leaves the install's.
func TestTheZoneIsTheBrowsersWhenItIsAZone(t *testing.T) {
	a := calendarApp(t)
	for v, want := range map[string]string{"Asia/Kolkata": "Asia/Kolkata", "Local": "UTC", "../../etc/passwd": "UTC", "": "UTC"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if v != "" {
			r.AddCookie(&http.Cookie{Name: calTZCookie, Value: url.QueryEscape(v)})
		}
		if got := a.calendarHome(r).String(); got != want {
			t.Errorf("cookie %q: %s, want %s", v, got, want)
		}
	}
}

// TestOneOccurrenceIsDeletedFromItsSheet — the rest of the series stays.
func TestOneOccurrenceIsDeletedFromItsSheet(t *testing.T) {
	a := calendarApp(t)
	calSave(a, dana(), url.Values{"title": {"Gym"}, "sd": {"2026-11-09"}, "st": {"07:00"}, "ed": {"2026-11-09"}, "et": {"08:00"},
		"zone": {"UTC"}, "r_freq": {"DAILY"}, "r_end": {"count"}, "r_count": {"3"}})
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), from, from.AddDate(0, 0, 7), time.UTC)
	if len(evs) != 3 {
		t.Fatalf("%d occurrences", len(evs))
	}
	rec := postForm(a.handleVayuOSCalendarAction, "/os/vayumail/calendar/action", url.Values{"op": {"delete"}, "uid": {evs[1].UID},
		"i": {itoa64(evs[1].Instance.Unix())}, "scope": {"one"}, "d": {"2026-11-10"}}.Encode(), dana())
	if rec.Header().Get("HX-Redirect") == "" {
		t.Fatalf("delete: %s", rec.Body.String())
	}
	evs, _ = a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), from, from.AddDate(0, 0, 7), time.UTC)
	if len(evs) != 2 || evs[0].Start.Day() != 9 || evs[1].Start.Day() != 11 {
		t.Fatalf("after deleting the 10th: %+v", evs)
	}
}

// TestASaveTheEngineRefusesComesBackInTheEditor — with the holder's words
// kept and the reason shown.
func TestASaveTheEngineRefusesComesBackInTheEditor(t *testing.T) {
	a := calendarApp(t)
	rec := calSave(a, dana(), url.Values{"title": {"Backwards"}, "sd": {"2026-11-10"}, "st": {"10:00"}, "ed": {"2026-11-10"}, "et": {"09:00"}, "zone": {"UTC"}})
	body := rec.Body.String()
	if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(body, "an event ends after it starts") || !strings.Contains(body, `value="Backwards"`) {
		t.Fatalf("a refused save: %s", body)
	}
	rec = calSave(a, dana(), url.Values{"title": {"Nowhere"}, "sd": {"2026-11-10"}, "st": {"10:00"}, "ed": {"2026-11-10"}, "et": {"11:00"}, "zone": {"Mars/Olympus"}})
	if !strings.Contains(rec.Body.String(), "the time zone is not one the calendar knows") {
		t.Errorf("an unknown zone: %s", rec.Body.String())
	}
}
