// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestASeriesIsBookedFromThePublicPage — a page that allows a series asks
// how it repeats; the guest's series is one repeating event; its own link
// shows it and offers Cancel, not a move.
func TestASeriesIsBookedFromThePublicPage(t *testing.T) {
	a := calendarApp(t)
	h := bookingRouter(a)
	if err := a.vayuMail.Accounts().Create(context.Background(), "sam@example.com", "x", "Sam", "mailbox"); err != nil {
		t.Fatal(err)
	}
	danaAllDay(t, a)
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(context.Background(), "dana@example.com")
	vals := url.Values{"op": {"save"}, "id": {itoa64(pages[0].ID)}, "title": {"Intro call"}, "slug": {"intro"}, "length": {"30"}, "zone": {"UTC"},
		"before": {"0"}, "after": {"0"}, "notice": {"0"}, "reach": {"60"}, "perday": {"0"}, "repeats": {"4"}, "enabled": {"1"}}
	for d := 0; d < 7; d++ {
		vals.Set("hours"+string(rune('0'+d)), "00:00-24:00")
	}
	postForm(a.handleVayuOSBookingAction, "/os/vayumail/calendar/booking/action", vals.Encode(), dana())
	if ed := calGet(a, a.handleVayuOSBookingPage, "/os/vayumail/calendar/booking?id="+itoa64(pages[0].ID), dana(), "").Body.String(); !strings.Contains(ed, `<option value="4" selected>Up to 4 in a series</option>`) {
		t.Fatalf("the editor's series: %s", ed)
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	ten := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, time.UTC)
	form := visit(h, http.MethodGet, "/book/dana/intro?tz=UTC&t="+itoa64(ten.Unix()), "").Body.String()
	if !strings.Contains(form, `<option value="2">Every two weeks</option>`) || !strings.Contains(form, `<option value="4">4 times</option>`) || strings.Contains(form, `value="5"`) {
		t.Fatalf("the guest's form: %s", form)
	}
	visit(h, http.MethodPost, "/book/dana/intro", url.Values{"t": {itoa64(ten.Unix())}, "len": {"30"}, "tz": {"UTC"},
		"name": {"Sam"}, "email": {"sam@example.com"}, "every": {"1"}, "count": {"3"}}.Encode())
	msgs, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("sam"), "Inbox")
	raw, _ := a.vayuMail.ReadFolderMessage(vmail.ReadAsOwner("sam"), "Inbox", msgs[0].ID)
	m := confirmLinkRe.FindStringSubmatch(string(raw))
	if m == nil || !strings.Contains(string(raw), "every week, 3 times") {
		t.Fatalf("the hold's mail: %s", raw)
	}
	visit(h, http.MethodPost, "/book/confirm", "t="+m[1])
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), ten, ten.AddDate(0, 0, 21), time.UTC)
	if len(evs) != 3 {
		t.Fatalf("the series in the calendar: %+v", evs)
	}
	c := changeLinkRe.FindStringSubmatch(evs[0].Description)
	if c == nil {
		t.Fatal("no change link")
	}
	page := visit(h, http.MethodGet, "/book/change?t="+c[1], "").Body.String()
	if !strings.Contains(page, "every week, 3 times") || !strings.Contains(page, "A series is not moved here") || strings.Contains(page, "to=") || !strings.Contains(page, `value="cancel"`) {
		t.Errorf("a series' own page: %s", page)
	}
}
