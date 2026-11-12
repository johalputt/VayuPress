// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// bookingNow is a Monday morning; the page's days are in London, which is on
// GMT in November.
var bookingNow = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)

func introPage(t *testing.T, s *AccountStore) BookingPage {
	t.Helper()
	p := BookingPage{Mailbox: calBox, Slug: "intro", Title: "Intro call", Lengths: []int{30}, Zone: "Europe/London",
		Before: 10, After: 10, Reach: 30, PerDay: 2, Question: "What would you like to talk about?", Enabled: true}
	for d := 0; d < 5; d++ {
		p.Hours[d] = [][2]int{{10 * 60, 13 * 60}}
	}
	id, err := s.SaveBookingPage(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	return p
}

func clocks(ts []time.Time) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.UTC().Format("Mon 15:04"))
	}
	return strings.Join(out, " ")
}

func tuesday(t *testing.T, e *Engine, p BookingPage, now time.Time) string {
	t.Helper()
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	slots, err := e.FreeSlots(p, 30, from, from.AddDate(0, 0, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	return clocks(slots)
}

// TestFreeTimesKeepClearOfTheCalendar — an event and the gap on each side of
// a booking are not offered; an all-day event and one marked free are.
func TestFreeTimesKeepClearOfTheCalendar(t *testing.T) {
	e, s, _ := itipEngine(t)
	p := introPage(t, s)
	putEvent(t, s, "busy.ics", "busy@x", "BEGIN:VEVENT", "UID:busy@x", "DTSTAMP:20261101T000000Z", "SUMMARY:Dentist",
		"DTSTART:20261110T110000Z", "DTEND:20261110T113000Z", "END:VEVENT")
	if got := tuesday(t, e, p, bookingNow); got != "Tue 10:00 Tue 12:00 Tue 12:30" {
		t.Fatalf("Tuesday offers %q", got)
	}
	putEvent(t, s, "allday.ics", "allday@x", "BEGIN:VEVENT", "UID:allday@x", "DTSTAMP:20261101T000000Z", "SUMMARY:Holiday",
		"DTSTART;VALUE=DATE:20261111", "DTEND;VALUE=DATE:20261112", "END:VEVENT")
	putEvent(t, s, "free.ics", "free@x", "BEGIN:VEVENT", "UID:free@x", "DTSTAMP:20261101T000000Z", "SUMMARY:Maybe gym", "TRANSP:TRANSPARENT",
		"DTSTART:20261111T100000Z", "DTEND:20261111T130000Z", "END:VEVENT")
	from := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	wed, _ := e.FreeSlots(p, 30, from, from.AddDate(0, 0, 1), bookingNow)
	if len(wed) != 6 {
		t.Fatalf("Wednesday offers %q, want all six", clocks(wed))
	}
}

// TestFreeTimesRespectNoticeReachAndDays — not sooner than the notice, not
// past the reach, nothing on a day with no hours.
func TestFreeTimesRespectNoticeReachAndDays(t *testing.T) {
	e, s, _ := itipEngine(t)
	p := introPage(t, s)
	p.Notice = 26 * 60 // the soonest is Tuesday 10:00
	if got := tuesday(t, e, p, bookingNow); got != "Tue 10:00 Tue 10:30 Tue 11:00 Tue 11:30 Tue 12:00 Tue 12:30" {
		t.Fatalf("with a day's notice Tuesday offers %q", got)
	}
	p.Notice = 27 * 60
	if got := tuesday(t, e, p, bookingNow); strings.Contains(got, "10:00") {
		t.Fatalf("a time inside the notice was offered: %q", got)
	}
	sat := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if slots, _ := e.FreeSlots(p, 30, sat, sat.AddDate(0, 0, 2), bookingNow); len(slots) != 0 {
		t.Fatalf("the weekend offers %q", clocks(slots))
	}
	far := bookingNow.AddDate(0, 0, 40)
	if slots, _ := e.FreeSlots(p, 30, far, far.AddDate(0, 0, 3), bookingNow); len(slots) != 0 {
		t.Fatal("a time past the reach was offered")
	}
}

var linkToken = regexp.MustCompile(`/book/(?:confirm|cancel|change)\?t=([0-9a-f]{64})`)

// tokenIn is the token of the booking link in a mail or an event's notes,
// as the guest has it.
func tokenIn(t *testing.T, text string) string {
	t.Helper()
	m := linkToken.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no booking link in %q", text)
	}
	return m[1]
}

// bookTuesday holds a time and returns the confirm token from the mail the
// guest was sent.
func bookTuesday(t *testing.T, e *Engine, flush func() []sentMail, p BookingPage, at, who string, now time.Time) (string, error) {
	t.Helper()
	start, _ := time.Parse(time.RFC3339, "2026-11-10T"+at+":00Z")
	flush()
	if err := e.HoldBooking(context.Background(), p, start, 30, BookingGuest{Name: "Kenji Mori", Email: who, Answer: "Pricing"}, now, "https://site.example/book/confirm?t="); err != nil {
		return "", err
	}
	sent := flush()
	if len(sent) != 1 {
		t.Fatalf("%d mails for one hold", len(sent))
	}
	return tokenIn(t, sent[0].raw), nil
}

// TestABookingIsHeldConfirmedAndCancelled — held, the time is taken for
// fifteen minutes; confirmed by the guest's link, it is an event with them
// invited and the holder told; cancelled by its own link, it is gone. Each
// link works once.
func TestABookingIsHeldConfirmedAndCancelled(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	start := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	if err := e.HoldBooking(ctx, p, start, 30, BookingGuest{Name: "Kenji Mori", Email: "Kenji <kenji@guest.example>", Answer: "Pricing", Zone: "Asia/Tokyo"}, bookingNow, "https://site.example/book/confirm?t="); err != nil {
		t.Fatal(err)
	}
	sent := flush()
	if len(sent) != 1 || sent[0].to[0] != "kenji@guest.example" || !strings.Contains(sent[0].raw, "Subject: Confirm your booking: Intro call") {
		t.Fatalf("the confirmation mail: %+v", sent)
	}
	// In the zone the guest booked in, with the host's clock beside it.
	if !strings.Contains(sent[0].raw, "Tuesday 10 November 2026, 19:00 (Asia/Tokyo), which is 10:00 on Tuesday for ankush@example.com (Europe/London), 30 minutes.") {
		t.Errorf("the guest's mail does not give the time in their zone:\n%s", sent[0].raw)
	}
	confirm := tokenIn(t, sent[0].raw)
	if got := tuesday(t, e, p, bookingNow); strings.Contains(got, "10:00") || strings.Contains(got, "10:30") {
		t.Fatalf("a held time (and its gap) is still offered: %q", got)
	}
	if got := tuesday(t, e, p, bookingNow.Add(16*time.Minute)); !strings.HasPrefix(got, "Tue 10:00") {
		t.Fatalf("a hold past its fifteen minutes still takes the time: %q", got)
	}
	b, err := e.ConfirmBooking(ctx, confirm, bookingNow.Add(5*time.Minute), "https://site.example/book/cancel?t=")
	if err != nil || b.Status != "confirmed" {
		t.Fatalf("confirm: %+v %v", b, err)
	}
	if _, err := e.ConfirmBooking(ctx, confirm, bookingNow.Add(6*time.Minute), ""); !errors.Is(err, ErrNoSuchBooking) {
		t.Fatalf("the confirm link worked twice: %v", err)
	}
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 1 || evs[0].Summary != "Intro call with Kenji Mori" || len(evs[0].Attendees) != 1 || evs[0].Attendees[0].Email != "kenji@guest.example" ||
		!strings.Contains(evs[0].Description, "/book/cancel?t=") || !strings.Contains(evs[0].Description, "Pricing") {
		t.Fatalf("the event: %+v", evs)
	}
	cancelTok := tokenIn(t, evs[0].Description)
	if sent := flush(); len(sent) != 1 || !strings.Contains(sent[0].raw, "METHOD:REQUEST") {
		t.Fatalf("the guest was not invited: %+v", sent)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	if len(msgs) != 1 || msgs[0].Subject != "New booking: Intro call with Kenji Mori" {
		t.Fatalf("the holder was told %+v", msgs)
	}
	if _, err := e.CancelBooking(ctx, cancelTok); err != nil {
		t.Fatal(err)
	}
	if evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC); len(evs) != 0 {
		t.Fatalf("a cancelled booking is still in the calendar: %+v", evs)
	}
	if sent := flush(); len(sent) != 1 || !strings.Contains(sent[0].raw, "METHOD:CANCEL") {
		t.Fatalf("the guest was not sent the cancellation: %+v", sent)
	}
	if _, err := e.CancelBooking(ctx, cancelTok); !errors.Is(err, ErrNoSuchBooking) {
		t.Fatalf("the cancel link worked twice: %v", err)
	}
	// The holder is told in the page's zone, as of the booking itself.
	msgs, _ = e.maildir.ListFolder("example.com", "ankush", "Inbox")
	var told string
	for _, m := range msgs {
		if m.Subject == "Booking cancelled: Kenji Mori" {
			raw, _ := e.maildir.ReadRawFolder("example.com", "ankush", "Inbox", m.ID)
			told = string(raw)
		}
	}
	if !strings.Contains(told, "cancelled the booking for Tuesday 10 November 2026, 10:00 (Europe/London).") {
		t.Errorf("the holder was told of the cancellation as:\n%s", told)
	}
}

// TestAConfirmLinkPastItsHoldBooksNothing — and a time taken meanwhile is
// refused when the guest confirms.
func TestAConfirmLinkPastItsHoldBooksNothing(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	late, _ := bookTuesday(t, e, flush, p, "10:00", "kenji@guest.example", bookingNow)
	if _, err := e.ConfirmBooking(ctx, late, bookingNow.Add(16*time.Minute), ""); !errors.Is(err, ErrNoSuchBooking) {
		t.Fatalf("a link past its hold: %v", err)
	}
	taken, _ := bookTuesday(t, e, flush, p, "12:00", "ana@guest.example", bookingNow)
	putEvent(t, s, "meanwhile.ics", "mw@x", "BEGIN:VEVENT", "UID:mw@x", "DTSTAMP:20261101T000000Z", "SUMMARY:Board",
		"DTSTART:20261110T120000Z", "DTEND:20261110T130000Z", "END:VEVENT")
	if _, err := e.ConfirmBooking(ctx, taken, bookingNow.Add(time.Minute), ""); !errors.Is(err, ErrSlotTaken) {
		t.Fatalf("a time taken meanwhile: %v", err)
	}
}

// TestABookingPagesMailIsBounded — a page mails an address at most three
// times a day, and holds at most so many at once; a page switched off takes
// none, and a day at its most offers nothing more.
func TestABookingPagesMailIsBounded(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	for i, at := range []string{"10:00", "11:00", "12:00"} {
		now := bookingNow.Add(time.Duration(i) * 20 * time.Minute) // each earlier hold has lapsed
		if _, err := bookTuesday(t, e, flush, p, at, "spam@victim.example", now); err != nil {
			t.Fatalf("hold %d: %v", i, err)
		}
	}
	if _, err := bookTuesday(t, e, flush, p, "12:30", "spam@victim.example", bookingNow.Add(time.Hour)); !errors.Is(err, ErrBookingsWait) {
		t.Fatalf("a fourth mail to one address in a day: %v", err)
	}
	p.Enabled = false
	if _, err := bookTuesday(t, e, flush, p, "12:30", "other@guest.example", bookingNow.Add(time.Hour)); err == nil {
		t.Fatal("a page switched off took a booking")
	}
	p.Enabled = true
	q := p
	q.ID, q.Slug = 0, "consult"
	q.PerDay = 1
	if q.ID, _ = s.SaveBookingPage(context.Background(), q); q.ID == 0 {
		t.Fatal("second page")
	}
	later := bookingNow.Add(2 * time.Hour) // the first page's holds have lapsed
	if _, err := bookTuesday(t, e, flush, q, "10:00", "one@guest.example", later); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if slots, _ := e.FreeSlots(q, 30, from, from.AddDate(0, 0, 1), later); len(slots) != 0 {
		t.Fatalf("a day at its most offers %q", clocks(slots))
	}
}

// TestABookingPageIsChecked — the shapes a page cannot have.
func TestABookingPageIsChecked(t *testing.T) {
	_, s, _ := itipEngine(t)
	ok := introPage(t, s)
	for name, mutate := range map[string]func(*BookingPage){
		"a slug with a slash": func(p *BookingPage) { p.Slug = "a/b" },
		"no title":            func(p *BookingPage) { p.Title = " " },
		"no length":           func(p *BookingPage) { p.Lengths = []int{2} },
		"an unknown zone":     func(p *BookingPage) { p.Zone = "Mars/Olympus" },
		"no hours":            func(p *BookingPage) { p.Hours = [7][][2]int{} },
		"a window backwards":  func(p *BookingPage) { p.Hours[0] = [][2]int{{600, 500}} },
		"a year ahead":        func(p *BookingPage) { p.Reach = 400 },
		"the same name twice": func(p *BookingPage) { p.ID = 0 },
	} {
		p := ok
		p.Hours = ok.Hours
		mutate(&p)
		if _, err := s.SaveBookingPage(context.Background(), p); err == nil {
			t.Errorf("%s was saved", name)
		}
	}
}

// TestADeletedMailboxsBookingPagesGoWithIt — the address given again does
// not offer the last holder's pages.
func TestADeletedMailboxsBookingPagesGoWithIt(t *testing.T) {
	_, s, _ := itipEngine(t)
	introPage(t, s)
	if err := s.Delete(context.Background(), calBox); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BookingPage(context.Background(), calBox, 0, "intro"); err == nil {
		t.Fatal("a deleted mailbox's booking page is still offered")
	}
}

// TestASecondConfirmAtTheSameMomentStops — a link being confirmed is not
// confirmed again beside it, and the booking it is making is left alone.
func TestASecondConfirmAtTheSameMomentStops(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	tok, err := bookTuesday(t, e, flush, p, "10:00", "kenji@guest.example", bookingNow)
	if err != nil {
		t.Fatal(err)
	}
	// The first confirm has claimed the link and not yet finished.
	if _, err := s.db.Exec(`UPDATE vayumail_bookings SET status='checking'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ConfirmBooking(ctx, tok, bookingNow.Add(time.Minute), ""); !errors.Is(err, ErrNoSuchBooking) {
		t.Fatalf("a second confirm beside the first: %v", err)
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM vayumail_bookings`).Scan(&status)
	if status != "checking" {
		t.Fatalf("the second confirm left the booking %q", status)
	}
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC); len(evs) != 0 {
		t.Fatalf("the second confirm made an event: %+v", evs)
	}
}

// TestASuspendedMailboxOffersNoBookingPages — and takes no bookings.
func TestASuspendedMailboxOffersNoBookingPages(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	if !s.BookingOffered(ctx, p) {
		t.Fatal("an active mailbox's page is not offered")
	}
	tok, _ := bookTuesday(t, e, flush, p, "10:00", "kenji@guest.example", bookingNow)
	if err := s.SetActive(ctx, calBox, false); err != nil {
		t.Fatal(err)
	}
	if s.BookingOffered(ctx, p) {
		t.Fatal("a suspended mailbox's page is offered")
	}
	if _, err := bookTuesday(t, e, flush, p, "12:00", "ana@guest.example", bookingNow); err == nil {
		t.Fatal("a suspended mailbox took a booking")
	}
	if _, err := e.ConfirmBooking(ctx, tok, bookingNow.Add(time.Minute), ""); !errors.Is(err, ErrNoSuchBooking) {
		t.Fatalf("a hold was confirmed on a suspended mailbox: %v", err)
	}
}
