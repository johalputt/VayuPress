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

func calSubsPost(a *App, vals url.Values) string {
	return postForm(a.handleVayuOSCalendarSubs, "/os/vayumail/calendar/subs", vals.Encode(), dana()).Body.String()
}

// TestSubscribedCalendarsFromTheCalendarsSide — the side opens the sheet;
// an address is refused unless it is one; one on the server's own network
// is kept, said to be refused, and shown only by its host; a subscribed
// event's sheet offers no edit.
func TestSubscribedCalendarsFromTheCalendarsSide(t *testing.T) {
	a, db := appWithMailDB(t, nil)
	udb, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udb.Close() })
	a.userStore = users.New(udb)

	if page := calGet(a, a.handleVayuOSCalendar, "/os/vayumail/calendar", dana(), "UTC").Body.String(); !strings.Contains(page, `hx-get="/os/vayumail/calendar/subs"`) {
		t.Fatal("the side does not offer subscribed calendars")
	}
	if sheet := calGet(a, a.handleVayuOSCalendarSubs, "/os/vayumail/calendar/subs", dana(), "").Body.String(); !strings.Contains(sheet, `name="url"`) || !strings.Contains(sheet, `value="add"`) {
		t.Fatalf("the sheet: %s", sheet)
	}
	if got := calSubsPost(a, url.Values{"op": {"add"}, "url": {"file:///etc/passwd"}}); !strings.Contains(got, "Not done: a calendar&#39;s address is an http, https or webcal address.") {
		t.Errorf("a file address: %s", got)
	}
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	got := calSubsPost(a, url.Values{"op": {"add"}, "name": {"Inside"}, "url": {srv.URL + "/private/secret-token/basic.ics"}, "busy": {"1"}})
	if hit || !strings.Contains(got, "Subscribed.") || !strings.Contains(got, "this server&#39;s own network") || strings.Contains(got, "secret-token") {
		t.Fatalf("a loopback address: hit=%v %s", hit, got)
	}
	if !strings.Contains(got, "makes you busy on booking pages") {
		t.Errorf("a calendar subscribed as busy: %s", got)
	}
	if got := calSubsPost(a, url.Values{"op": {"add"}, "name": {"Holidays"}, "url": {srv.URL + "/h.ics"}}); !strings.Contains(got, "does not make you busy") {
		t.Errorf("a calendar subscribed as not busy: %s", got)
	}

	// A copy the engine read, put in place: its event is read only.
	day := time.Now().UTC().AddDate(0, 0, 1)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//O//O//EN\r\nBEGIN:VEVENT\r\nUID:x@other\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Offsite\r\nDTSTART:" +
		day.Format("20060102") + "T090000Z\r\nDTEND:" + day.Format("20060102") + "T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := db.Exec(`UPDATE vayumail_cal_subs SET data=?, error='' WHERE name='Inside'`, []byte(ics)); err != nil {
		t.Fatal(err)
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, time.UTC)
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), start, start.Add(time.Hour), time.UTC)
	if len(evs) != 1 || evs[0].Sub != "Inside" {
		t.Fatalf("the subscribed event: %+v", evs)
	}
	sheet := calGet(a, a.handleVayuOSCalendarEvent, "/os/vayumail/calendar/event?uid="+url.QueryEscape(evs[0].UID)+"&s="+itoa64(start.Unix()), dana(), "UTC").Body.String()
	if !strings.Contains(sheet, "From Inside, a subscribed calendar") || strings.Contains(sheet, "/os/vayumail/calendar/edit") || strings.Contains(sheet, `value="delete"`) {
		t.Errorf("a subscribed event's sheet: %s", sheet)
	}
	if got := calSubsPost(a, url.Values{"op": {"remove"}, "id": {"1"}}); !strings.Contains(got, "Unsubscribed.") || strings.Contains(got, "Inside") {
		t.Errorf("unsubscribing: %s", got)
	}
}
