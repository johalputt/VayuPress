// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/i18n"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func bookingRouter(a *App) http.Handler {
	r := chi.NewRouter()
	r.Get("/book/confirm", a.handleBookConfirm)
	r.Post("/book/confirm", a.handleBookConfirm)
	r.Get("/book/change", a.handleBookChange)
	r.Post("/book/change", a.handleBookChange)
	r.Get("/book/cancel", a.handleBookChange)
	r.Post("/book/cancel", a.handleBookChange)
	r.Get("/book/{owner}/{slug}", a.handleBookPage)
	r.Post("/book/{owner}/{slug}", a.handleBookPage)
	return r
}

func visit(h http.Handler, method, target, form string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// danaAllDay saves, through the editor, a page dana offers at every hour of
// every day, so there are free times whatever day the test runs.
func danaAllDay(t *testing.T, a *App) {
	t.Helper()
	vals := url.Values{"op": {"save"}, "title": {"Intro call"}, "slug": {"intro"}, "length": {"30"}, "zone": {"UTC"},
		"before": {"0"}, "after": {"0"}, "notice": {"0"}, "reach": {"30"}, "perday": {"0"}, "question": {"What about?"}, "enabled": {"1"}}
	for d := 0; d < 7; d++ {
		vals.Set("hours"+string(rune('0'+d)), "00:00-24:00")
	}
	rec := postForm(a.handleVayuOSBookingAction, "/os/vayumail/calendar/booking/action", vals.Encode(), dana())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
}

var confirmLinkRe = regexp.MustCompile(`/book/confirm\?t=([0-9a-f]+)`)

// TestABookingIsMadeFromItsPublicPage — the page offers times and never the
// calendar's contents; the guest's details hold a time; the link mailed to
// them asks, and its button books it into the holder's calendar.
func TestABookingIsMadeFromItsPublicPage(t *testing.T) {
	a := calendarApp(t)
	if err := a.vayuMail.Accounts().Create(context.Background(), "sam@example.com", "x", "Sam", "mailbox"); err != nil {
		t.Fatal(err)
	}
	danaAllDay(t, a)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	day := tomorrow.Format("2006-01-02")
	calSave(a, dana(), url.Values{"title": {"Secret dentist"}, "sd": {day}, "st": {"09:00"}, "ed": {day}, "et": {"10:00"}, "zone": {"UTC"}})
	h := bookingRouter(a)
	page := visit(h, http.MethodGet, "/book/dana/intro?tz=UTC&m="+tomorrow.Format("2006-01")+"&d="+day, "").Body.String()
	if strings.Contains(page, "Secret") || strings.Contains(page, "dentist") {
		t.Fatal("the public page shows what is in the calendar")
	}
	nine := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, time.UTC)
	if strings.Contains(page, `t=`+itoa64(nine.Unix())+`"`) || strings.Contains(page, `t=`+itoa64(nine.Add(30*time.Minute).Unix())+`"`) {
		t.Fatal("a busy time is offered")
	}
	ten := nine.Add(time.Hour)
	if !strings.Contains(page, `t=`+itoa64(ten.Unix())) || !strings.Contains(page, "Intro call") || !strings.Contains(page, "Dana") {
		t.Fatalf("the page does not offer 10:00: %s", page)
	}
	held := visit(h, http.MethodPost, "/book/dana/intro", url.Values{"t": {itoa64(ten.Unix())}, "len": {"30"}, "tz": {"UTC"},
		"name": {"Sam"}, "email": {"sam@example.com"}, "answer": {"Pricing"}}.Encode()).Body.String()
	if !strings.Contains(held, "Check your mail") {
		t.Fatalf("holding: %s", held)
	}
	msgs, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("sam"), "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("%d messages for the guest", len(msgs))
	}
	raw, _ := a.vayuMail.ReadFolderMessage(vmail.ReadAsOwner("sam"), "Inbox", msgs[0].ID)
	m := confirmLinkRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("no confirm link in %s", raw)
	}
	if ask := visit(h, http.MethodGet, "/book/confirm?t="+m[1], ""); !strings.Contains(ask.Body.String(), "Confirm") {
		t.Fatal("the link's page does not ask")
	}
	from := ten
	if evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), from, from.Add(time.Hour), time.UTC); len(evs) != 0 {
		t.Fatal("opening the link booked the time")
	}
	if done := visit(h, http.MethodPost, "/book/confirm", "t="+m[1]); !strings.Contains(done.Body.String(), "Booked") {
		t.Fatalf("confirming: %s", done.Body.String())
	}
	evs, _ := a.vayuMail.CalendarBetween(vmail.ReadAsOwner("dana"), from, from.Add(time.Hour), time.UTC)
	if len(evs) != 1 || evs[0].Summary != "Intro call with Sam" {
		t.Fatalf("the calendar after confirming: %+v", evs)
	}
	if again := visit(h, http.MethodPost, "/book/confirm", "t="+m[1]); again.Code != http.StatusNotFound {
		t.Errorf("the confirm link worked twice: %d", again.Code)
	}
}

// TestABookingPageSwitchedOffIsNotThere — and a name nobody has is not.
func TestABookingPageSwitchedOffIsNotThere(t *testing.T) {
	a := calendarApp(t)
	danaAllDay(t, a)
	h := bookingRouter(a)
	if rec := visit(h, http.MethodGet, "/book/dana/nothing", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a page nobody made: %d", rec.Code)
	}
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(context.Background(), "dana@example.com")
	p := pages[0]
	p.Enabled = false
	if _, err := a.vayuMail.Accounts().SaveBookingPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if rec := visit(h, http.MethodGet, "/book/dana/intro", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a page switched off: %d", rec.Code)
	}
}

// TestTheBookingEditorReadsHours — a day's windows as typed, and a typo
// said, not saved.
func TestTheBookingEditorReadsHours(t *testing.T) {
	if ws, ok := parseHours("10:00-13:00, 15:00 – 18:00"); !ok || len(ws) != 2 || ws[1] != [2]int{900, 1080} {
		t.Fatalf("%v %v", ws, ok)
	}
	if _, ok := parseHours("ten to one"); ok {
		t.Error("words were read as hours")
	}
	if hoursText([][2]int{{600, 780}, {900, 1080}}) != "10:00-13:00, 15:00-18:00" {
		t.Error("hours are not shown as typed")
	}
	a := calendarApp(t)
	rec := postForm(a.handleVayuOSBookingAction, "/os/vayumail/calendar/booking/action", url.Values{"op": {"save"}, "title": {"X"}, "slug": {"x"},
		"length": {"30"}, "zone": {"UTC"}, "reach": {"30"}, "hours0": {"ten to one"}}.Encode(), dana())
	if !strings.Contains(rec.Body.String(), "hours are not in the shape 10:00-13:00") {
		t.Fatalf("a typo in the hours: %d %s", rec.Code, rec.Body.String())
	}
	if bookingOwner("dana@example.com", "example.com") != "dana" || bookingOwner("sales@shop.example", "example.com") != "sales@shop.example" {
		t.Error("a link names its mailbox wrongly")
	}
}

// TestABusyBookingPageSaysSoAndWorksOutNothing — past the pages worked out at
// once, a visit is answered 503 and asked to come back.
func TestABusyBookingPageSaysSoAndWorksOutNothing(t *testing.T) {
	a := calendarApp(t)
	danaAllDay(t, a)
	h := bookingRouter(a)
	was := bookWait
	bookWait = 20 * time.Millisecond
	for i := 0; i < cap(bookSlots); i++ {
		bookSlots <- struct{}{}
	}
	t.Cleanup(func() {
		bookWait = was
		for len(bookSlots) > 0 {
			<-bookSlots
		}
	})
	rec := visit(h, http.MethodGet, "/book/dana/intro?tz=UTC", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || strings.Contains(rec.Body.String(), "book__time") {
		t.Fatalf("a busy page answered %d: %s", rec.Code, rec.Body.String())
	}
	<-bookSlots
	if rec := visit(h, http.MethodGet, "/book/dana/intro?tz=UTC", ""); rec.Code != http.StatusOK {
		t.Fatalf("with a slot free: %d", rec.Code)
	}
}

// TestTheBookingEditorIsInThePersonsLanguage — the editor's fields, its
// days and a refused day in French, with none of the English they replaced.
func TestTheBookingEditorIsInThePersonsLanguage(t *testing.T) {
	a := calendarApp(t)
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/calendar/booking/action", strings.NewReader(url.Values{"op": {"save"}, "title": {"X"}, "slug": {"x"},
		"length": {"30"}, "zone": {"UTC"}, "reach": {"30"}, "hours1": {"dix à midi"}}.Encode())), dana())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(i18n.WithLang(req.Context(), "fr"))
	rec := httptest.NewRecorder()
	a.handleVayuOSBookingAction(rec, req)
	page := rec.Body.String()
	for _, want := range []string{"mardi : les horaires n’ont pas la forme", "Durées proposées", "<span>lundi</span>", "Accepte les réservations", "30 min"} {
		if !strings.Contains(page, want) {
			t.Errorf("the French editor has no %q", want)
		}
	}
	for _, english := range []string{"Lengths offered", "<span>Monday</span>", "Taking bookings"} {
		if strings.Contains(page, english) {
			t.Errorf("the French editor says %q", english)
		}
	}
}
