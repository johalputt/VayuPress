// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// seriesPage is introPage offering series of up to four.
func seriesPage(t *testing.T, s *AccountStore, perDay int) BookingPage {
	t.Helper()
	p := introPage(t, s)
	p.Repeats, p.PerDay = 4, perDay
	if _, err := s.SaveBookingPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func slotsOnDay(t *testing.T, e *Engine, p BookingPage, date string) string {
	t.Helper()
	from, _ := time.Parse("2006-01-02", date)
	slots, err := e.FreeSlots(p, 30, from, from.AddDate(0, 0, 1), bookingNow)
	if err != nil {
		t.Fatal(err)
	}
	return clocks(slots)
}

// TestASeriesIsHeldConfirmedAndCancelledWhole — held, each of its times is
// busy; confirmed, it is one repeating event and the guest and holder are
// told it repeats; it is not moved, and cancelled it goes whole.
func TestASeriesIsHeldConfirmedAndCancelledWhole(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := seriesPage(t, s, 0)
	ctx := context.Background()
	if err := e.HoldBooking(ctx, p, tuesdayAt("10:00"), 30, BookingGuest{Name: "Kenji", Email: "kenji@guest.example", Every: 1, Count: 3}, bookingNow, "https://site.example/book/confirm?t="); err != nil {
		t.Fatal(err)
	}
	sent := flush()
	if len(sent) != 1 || !strings.Contains(sent[0].raw, "30 minutes, every week, 3 times.") {
		t.Fatalf("the hold's mail: %+v", sent)
	}
	for _, day := range []string{"2026-11-17", "2026-11-24"} {
		if got := slotsOnDay(t, e, p, day); strings.Contains(got, "10:00") {
			t.Errorf("a held series' %s is offered: %q", day, got)
		}
	}
	if got := slotsOnDay(t, e, p, "2026-12-01"); !strings.Contains(got, "10:00") {
		t.Errorf("past the series, its time is not offered: %q", got)
	}
	b, err := e.ConfirmBooking(ctx, tokenIn(t, sent[0].raw), bookingNow.Add(time.Minute), "https://site.example/book/change?t=")
	if err != nil {
		t.Fatal(err)
	}
	evs := between(t, e, tuesdayAt("00:00"), tuesdayAt("00:00").AddDate(0, 0, 28), time.UTC)
	var at []string
	for _, ev := range evs {
		at = append(at, ev.Start.UTC().Format("Jan 2 15:04"))
	}
	if got := strings.Join(at, ", "); got != "Nov 10 10:00, Nov 17 10:00, Nov 24 10:00" || evs[0].Rule != "FREQ=WEEKLY;INTERVAL=1;COUNT=3" {
		t.Fatalf("the series in the calendar: %s (%q)", got, evs[0].Rule)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	raw, _ := e.maildir.ReadRawFolder("example.com", "ankush", "Inbox", msgs[0].ID)
	if !strings.Contains(string(raw), "(Europe/London), every week, 3 times.") {
		t.Errorf("the holder was told:\n%s", raw)
	}
	tok := tokenOfBooking(t, e, b.UID)
	if _, err := e.MoveBooking(ctx, tok, tuesdayAt("11:00"), bookingNow); !errors.Is(err, ErrBookingSeries) {
		t.Errorf("a series moved: %v", err)
	}
	if _, err := e.CancelBooking(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if evs := between(t, e, tuesdayAt("00:00"), tuesdayAt("00:00").AddDate(0, 0, 28), time.UTC); len(evs) != 0 {
		t.Errorf("a cancelled series is still in the calendar: %+v", evs)
	}
}

// TestASeriesIsOnlyWhatThePageOffers — no longer than the page allows,
// weekly or every two weeks, and every one of its times free when held and
// when confirmed.
func TestASeriesIsOnlyWhatThePageOffers(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := seriesPage(t, s, 0)
	ctx := context.Background()
	hold := func(pg BookingPage, every, count int) error {
		flush()
		return e.HoldBooking(ctx, pg, tuesdayAt("10:00"), 30, BookingGuest{Name: "Kenji", Email: "kenji@guest.example", Every: every, Count: count}, bookingNow, "https://site.example/book/confirm?t=")
	}
	if err := hold(p, 1, 5); !errors.Is(err, ErrBookingDetails) {
		t.Errorf("five on a page of four: %v", err)
	}
	if err := hold(p, 3, 2); !errors.Is(err, ErrBookingDetails) {
		t.Errorf("every three weeks: %v", err)
	}
	single := BookingPage{Mailbox: calBox, Slug: "single", Title: "One", Lengths: []int{30}, Zone: "Europe/London", Reach: 30, Hours: p.Hours, Enabled: true}
	id, err := s.SaveBookingPage(ctx, single)
	if err != nil {
		t.Fatal(err)
	}
	single.ID = id
	if err := hold(single, 1, 2); !errors.Is(err, ErrBookingDetails) {
		t.Errorf("a series on a page of single bookings: %v", err)
	}
	putEvent(t, s, "busy.ics", "busy@x", "BEGIN:VEVENT", "UID:busy@x", "DTSTAMP:20261101T000000Z", "DTSTART:20261117T100000Z", "DTEND:20261117T103000Z", "END:VEVENT")
	if err := hold(p, 1, 3); !errors.Is(err, ErrSlotTaken) {
		t.Errorf("a series whose second time is busy: %v", err)
	}
	// Every two weeks steps over the busy week.
	if err := hold(p, 2, 2); err != nil {
		t.Fatalf("every two weeks: %v", err)
	}
	tok := tokenIn(t, flush()[0].raw)
	// Taken meanwhile, a later time stops the confirm.
	putEvent(t, s, "late.ics", "late@x", "BEGIN:VEVENT", "UID:late@x", "DTSTAMP:20261101T000000Z", "DTSTART:20261124T100000Z", "DTEND:20261124T103000Z", "END:VEVENT")
	if _, err := e.ConfirmBooking(ctx, tok, bookingNow.Add(time.Minute), ""); !errors.Is(err, ErrSlotTaken) {
		t.Errorf("a confirm with a later time taken: %v", err)
	}
	p.Repeats = 13
	if _, err := s.SaveBookingPage(ctx, p); err == nil {
		t.Error("a series of 13 was saved")
	}
}

// TestASeriesFillsEachOfItsDays — on a page of one booking a day, each day
// of a series is full.
func TestASeriesFillsEachOfItsDays(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := seriesPage(t, s, 1)
	ctx := context.Background()
	if err := e.HoldBooking(ctx, p, tuesdayAt("10:00"), 30, BookingGuest{Name: "Kenji", Email: "kenji@guest.example", Every: 1, Count: 2}, bookingNow, "https://site.example/book/confirm?t="); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ConfirmBooking(ctx, tokenIn(t, flush()[0].raw), bookingNow.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if got := slotsOnDay(t, e, p, "2026-11-17"); got != "" {
		t.Errorf("the series' second day still offers %q", got)
	}
	if got := slotsOnDay(t, e, p, "2026-11-11"); got == "" {
		t.Error("a day outside the series is full")
	}
}

// TestASeriesIsNeverLongerThanASeriesCanBe — whatever count it is handed,
// a series is at most maxBookingSeries times, and at least one.
func TestASeriesIsNeverLongerThanASeriesCanBe(t *testing.T) {
	p := BookingPage{Zone: "UTC"}
	start := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	for count, want := range map[int]int{100: maxBookingSeries, 13: maxBookingSeries, 12: 12, 0: 1, -5: 1} {
		if got := len(seriesStarts(start, p, 1, count)); got != want {
			t.Errorf("a count of %d made %d", count, got)
		}
	}
}

// TestASeriesKeepsItsClockAcrossTheChange — weekly 09:00 in London is 09:00
// before and after the clock goes forward.
func TestASeriesKeepsItsClockAcrossTheChange(t *testing.T) {
	p := BookingPage{Zone: "Europe/London"}
	var got []string
	for _, at := range seriesStarts(time.Date(2027, 3, 21, 9, 0, 0, 0, time.UTC), p, 1, 3) {
		got = append(got, at.Format("Jan 2 15:04 MST")+"="+at.UTC().Format("15:04"))
	}
	if s := strings.Join(got, ", "); s != "Mar 21 09:00 GMT=09:00, Mar 28 09:00 BST=08:00, Apr 4 09:00 BST=08:00" {
		t.Errorf("the series: %s", s)
	}
}
