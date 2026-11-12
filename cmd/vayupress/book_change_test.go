// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

var changeLinkRe = regexp.MustCompile(`/book/change\?t=([0-9a-f]{64})`)

// bookedTomorrow books tomorrow at 10:00 UTC on dana's page for sam, shown
// in zone, and returns the confirm page's body and the booking's own token
// from the event's notes.
func bookedTomorrow(t *testing.T, a *App, h http.Handler, zone string) (string, string, time.Time) {
	t.Helper()
	if err := a.vayuMail.Accounts().Create(context.Background(), "sam@example.com", "x", "Sam", "mailbox"); err != nil {
		t.Fatal(err)
	}
	danaAllDay(t, a)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	ten := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, time.UTC)
	visit(h, http.MethodPost, "/book/dana/intro", url.Values{"t": {itoa64(ten.Unix())}, "len": {"30"}, "tz": {zone},
		"name": {"Sam"}, "email": {"sam@example.com"}}.Encode())
	msgs, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("sam"), "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("%d messages for the guest", len(msgs))
	}
	raw, _ := a.vayuMail.ReadFolderMessage(vmail.ReadAsOwner("sam"), "Inbox", msgs[0].ID)
	m := confirmLinkRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("no confirm link in %s", raw)
	}
	done := visit(h, http.MethodPost, "/book/confirm", "t="+m[1]).Body.String()
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), ten, ten.Add(time.Hour), time.UTC)
	if len(evs) != 1 {
		t.Fatalf("the calendar after confirming: %+v", evs)
	}
	c := changeLinkRe.FindStringSubmatch(evs[0].Description)
	if c == nil {
		t.Fatalf("no change link in the event's notes: %q", evs[0].Description)
	}
	return done, c[1], ten
}

// TestABookingIsMovedFromItsOwnLink — the link shows the booking, the
// page's free times and Cancel; a time chosen asks before it moves; the
// move changes the event; a busy time is refused; the link still cancels.
func TestABookingIsMovedFromItsOwnLink(t *testing.T) {
	a := calendarApp(t)
	h := bookingRouter(a)
	done, tok, ten := bookedTomorrow(t, a, h, "Asia/Tokyo")
	// The Booked page says the time in the zone the guest booked in.
	if want := ten.In(calZone("Asia/Tokyo")).Format("Monday 2 January 2006, 15:04") + " (Asia/Tokyo)"; !strings.Contains(done, want) || !strings.Contains(done, "move or cancel") {
		t.Errorf("the Booked page: %s", done)
	}
	// Opened from the mail, with no zone asked, it is in the guest's own.
	if bare := visit(h, http.MethodGet, "/book/change?t="+tok, "").Body.String(); !strings.Contains(bare, "Your time: Asia/Tokyo") {
		t.Errorf("the link opened as mailed is not in the guest's zone: %s", bare)
	}
	day := ten.Format("2006-01-02")
	calSave(a, dana(), url.Values{"title": {"Secret dentist"}, "sd": {day}, "st": {"12:00"}, "ed": {day}, "et": {"13:00"}, "zone": {"UTC"}})

	page := visit(h, http.MethodGet, "/book/change?t="+tok+"&tz=UTC&m="+ten.Format("2006-01")+"&d="+day, "").Body.String()
	eleven := ten.Add(time.Hour)
	noon := ten.Add(2 * time.Hour)
	for _, want := range []string{"Intro call", ten.Format("Monday 2 January 2006, 15:04") + " (UTC)", `value="cancel"`, "to=" + itoa64(eleven.Unix()), "to=" + itoa64(ten.Unix())} {
		if !strings.Contains(page, want) {
			t.Errorf("the link's page has no %q", want)
		}
	}
	if strings.Contains(page, "Secret") || strings.Contains(page, "to="+itoa64(noon.Unix())) {
		t.Error("the link's page shows the calendar or offers a busy time")
	}
	ask := visit(h, http.MethodGet, "/book/change?t="+tok+"&tz=UTC&to="+itoa64(eleven.Unix()), "").Body.String()
	if !strings.Contains(ask, "Move to "+eleven.Format("Monday 2 January 2006, 15:04")) || !strings.Contains(ask, `value="move"`) {
		t.Fatalf("choosing a time does not ask: %s", ask)
	}
	if evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), ten, ten.Add(30*time.Minute), time.UTC); len(evs) != 1 {
		t.Fatal("opening a time moved the booking")
	}
	moved := visit(h, http.MethodPost, "/book/change", url.Values{"t": {tok}, "op": {"move"}, "to": {itoa64(eleven.Unix())}, "tz": {"UTC"}}.Encode())
	if moved.Code != http.StatusOK || !strings.Contains(moved.Body.String(), "is now "+eleven.Format("Monday 2 January 2006, 15:04")) {
		t.Fatalf("the move: %d %s", moved.Code, moved.Body.String())
	}
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), ten, ten.Add(3*time.Hour), time.UTC)
	var at []string
	for _, ev := range evs {
		at = append(at, ev.Summary+" "+ev.Start.UTC().Format("15:04"))
	}
	if got := strings.Join(at, ", "); got != "Intro call with Sam 11:00, Secret dentist 12:00" {
		t.Errorf("the calendar after the move: %s", got)
	}
	busy := visit(h, http.MethodPost, "/book/change", url.Values{"t": {tok}, "op": {"move"}, "to": {itoa64(noon.Unix())}}.Encode())
	if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "That time is no longer free.") {
		t.Errorf("a move onto a busy time: %d %s", busy.Code, busy.Body.String())
	}
	// A link made before moves were offered opens the same page.
	if old := visit(h, http.MethodGet, "/book/cancel?t="+tok, "").Body.String(); !strings.Contains(old, `value="cancel"`) || !strings.Contains(old, "Or move it") {
		t.Errorf("the old link's page: %s", old)
	}
	if c := visit(h, http.MethodPost, "/book/cancel", url.Values{"t": {tok}, "op": {"cancel"}}.Encode()); !strings.Contains(c.Body.String(), "Cancelled") {
		t.Fatalf("cancelling: %s", c.Body.String())
	}
	if again := visit(h, http.MethodGet, "/book/change?t="+tok, ""); again.Code != http.StatusNotFound {
		t.Errorf("a cancelled booking's link: %d", again.Code)
	}
}

// TestABookingOnAPageSwitchedOffCanOnlyBeCancelled — no times are offered
// and a move is refused.
func TestABookingOnAPageSwitchedOffCanOnlyBeCancelled(t *testing.T) {
	a := calendarApp(t)
	h := bookingRouter(a)
	_, tok, ten := bookedTomorrow(t, a, h, "UTC")
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(context.Background(), "dana@example.com")
	p := pages[0]
	p.Enabled = false
	if _, err := a.vayuMail.Accounts().SaveBookingPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	page := visit(h, http.MethodGet, "/book/change?t="+tok, "").Body.String()
	if !strings.Contains(page, "cannot be moved") || strings.Contains(page, "to=") || !strings.Contains(page, `value="cancel"`) {
		t.Errorf("the link's page on a page switched off: %s", page)
	}
	if moved := visit(h, http.MethodPost, "/book/change", url.Values{"t": {tok}, "op": {"move"}, "to": {itoa64(ten.Add(time.Hour).Unix())}}.Encode()); moved.Code != http.StatusNotFound {
		t.Errorf("a move on a page switched off: %d", moved.Code)
	}
}
