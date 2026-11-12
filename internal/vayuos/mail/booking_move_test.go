// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// confirmedTuesday books Tuesday at the given UTC time for who, confirms it
// and returns the link's token from the event's notes.
func confirmedTuesday(t *testing.T, e *Engine, flush func() []sentMail, p BookingPage, at, who string) string {
	t.Helper()
	tok, err := bookTuesday(t, e, flush, p, at, who, bookingNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.ConfirmBooking(context.Background(), tok, bookingNow.Add(time.Minute), "https://site.example/book/change?t=")
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.accounts.eventByUID(context.Background(), calBox, b.UID)
	if err != nil {
		t.Fatal(err)
	}
	flush()
	return tokenIn(t, strings.ReplaceAll(string(o.Data), "\r\n ", ""))
}

func tuesdayAt(hm string) time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-11-10T"+hm+":00Z")
	return t
}

// TestABookingIsMovedByItsLink — the guest's link offers the page's free
// times with the booking's own out of its way; a move changes the event and
// the booking, sends the guest the change and tells the holder; the link
// still names the booking after.
func TestABookingIsMovedByItsLink(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	tok := confirmedTuesday(t, e, flush, p, "10:00", "kenji@guest.example")
	b, page, err := e.BookingByLink(ctx, tok)
	if err != nil || page.ID != p.ID || !b.Start.Equal(tuesdayAt("10:00")) {
		t.Fatalf("the link names %+v %v", b, err)
	}
	// Its own time and the gap around it do not stand in its way.
	from := tuesdayAt("00:00")
	slots, _ := e.MovingSlots(b, page, from, from.AddDate(0, 0, 1), bookingNow)
	if got := clocks(slots); got != "Tue 10:00 Tue 10:30 Tue 11:00 Tue 11:30 Tue 12:00 Tue 12:30" {
		t.Fatalf("a move is offered %q", got)
	}
	// For anyone else the booking still takes its time.
	if got := tuesday(t, e, p, bookingNow); strings.Contains(got, "10:00") || strings.Contains(got, "10:30") {
		t.Fatalf("another guest is offered %q", got)
	}

	moved, err := e.MoveBooking(ctx, tok, tuesdayAt("10:30"), bookingNow)
	if err != nil || !moved.Start.Equal(tuesdayAt("10:30")) || !moved.End.Equal(tuesdayAt("11:00")) {
		t.Fatalf("the move: %+v %v", moved, err)
	}
	evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 1 || !evs[0].Start.Equal(tuesdayAt("10:30")) || !evs[0].End.Equal(tuesdayAt("11:00")) || evs[0].Sequence != 1 ||
		evs[0].Summary != "Intro call with Kenji Mori" || !strings.Contains(evs[0].Description, "Pricing") {
		t.Fatalf("the event after the move: %+v", evs)
	}
	if sent := flush(); len(sent) != 1 || sent[0].to[0] != "kenji@guest.example" || !strings.Contains(sent[0].raw, "METHOD:REQUEST") || !strings.Contains(sent[0].raw, "SEQUENCE:1") {
		t.Fatalf("the guest was not sent the change: %+v", sent)
	}
	var told string
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	for _, m := range msgs {
		if m.Subject == "Booking moved: Intro call with Kenji Mori" {
			raw, _ := e.maildir.ReadRawFolder("example.com", "ankush", "Inbox", m.ID)
			told = string(raw)
		}
	}
	if !strings.Contains(told, "moved Intro call from Tuesday 10 November 2026, 10:00 to Tuesday 10 November 2026, 10:30 (Europe/London).") {
		t.Errorf("the holder was told:\n%s", told)
	}
	if b, _, err := e.BookingByLink(ctx, tok); err != nil || !b.Start.Equal(tuesdayAt("10:30")) || b.Status != "confirmed" {
		t.Errorf("after the move the link names %+v %v", b, err)
	}
	if got := tuesday(t, e, p, bookingNow); got != "Tue 11:30 Tue 12:00 Tue 12:30" {
		t.Errorf("after the move another guest is offered %q", got)
	}
	if _, err := e.CancelBooking(ctx, tok); err != nil {
		t.Errorf("the link no longer cancels: %v", err)
	}
}

// TestAMoveIsToATimeThePageOffers — not the time it has, not a busy one,
// not past a full day; a day the booking is already on counts it once.
func TestAMoveIsToATimeThePageOffers(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	tok := confirmedTuesday(t, e, flush, p, "10:00", "kenji@guest.example")
	putEvent(t, s, "busy.ics", "busy@x", "BEGIN:VEVENT", "UID:busy@x", "DTSTAMP:20261101T000000Z", "SUMMARY:Dentist",
		"DTSTART:20261110T120000Z", "DTEND:20261110T123000Z", "END:VEVENT")
	for _, at := range []time.Time{tuesdayAt("10:00"), tuesdayAt("12:00"), tuesdayAt("10:15"), tuesdayAt("14:00")} {
		if _, err := e.MoveBooking(ctx, tok, at, bookingNow); !errors.Is(err, ErrSlotTaken) {
			t.Errorf("a move to %s: %v", at.Format("15:04"), err)
		}
	}
	// Wednesday is full (two a day); Tuesday holds this one and another, and
	// a move within Tuesday is still a move, not a third.
	// One of Wednesday's is being moved this moment, and still counts.
	for at, status := range map[string]string{"10:00": "confirmed", "11:00": "moving"} {
		start, _ := time.Parse(time.RFC3339, "2026-11-11T"+at+":00Z")
		if _, err := s.db.Exec(`INSERT INTO vayumail_bookings(page_id,mailbox,start,end,name,email,status,created) VALUES(?,?,?,?,?,?,?,?)`,
			p.ID, calBox, start.Unix(), start.Add(30*time.Minute).Unix(), "Guest", "g@x.test", status, bookingNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO vayumail_bookings(page_id,mailbox,start,end,name,email,status,created) VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, calBox, tuesdayAt("11:30").Unix(), tuesdayAt("12:00").Unix(), "Other", "o@x.test", "confirmed", bookingNow); err != nil {
		t.Fatal(err)
	}
	wed, _ := time.Parse(time.RFC3339, "2026-11-11T12:00:00Z")
	if _, err := e.MoveBooking(ctx, tok, wed, bookingNow); !errors.Is(err, ErrSlotTaken) {
		t.Errorf("a move onto a full day: %v", err)
	}
	if _, err := e.MoveBooking(ctx, tok, tuesdayAt("10:30"), bookingNow); err != nil {
		t.Errorf("a move within its own day: %v", err)
	}
}

// TestAMoveIsBounded — five moves, then the guest is asked to cancel and
// book again; a page switched off moves nothing and still cancels; a move
// under way is not moved or cancelled beside it.
func TestAMoveIsBounded(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := introPage(t, s)
	ctx := context.Background()
	tok := confirmedTuesday(t, e, flush, p, "10:00", "kenji@guest.example")
	for i, at := range []string{"10:30", "11:00", "11:30", "12:00", "12:30"} {
		if _, err := e.MoveBooking(ctx, tok, tuesdayAt(at), bookingNow); err != nil {
			t.Fatalf("move %d: %v", i+1, err)
		}
	}
	if _, err := e.MoveBooking(ctx, tok, tuesdayAt("10:00"), bookingNow); !errors.Is(err, ErrBookingMoves) {
		t.Fatalf("a sixth move: %v", err)
	}

	tok2 := confirmedTuesday(t, e, flush, p, "10:00", "ana@guest.example")
	if _, err := s.db.Exec(`UPDATE vayumail_bookings SET status='moving' WHERE email='ana@guest.example'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.MoveBooking(ctx, tok2, tuesdayAt("11:00"), bookingNow); !errors.Is(err, ErrNoSuchBooking) {
		t.Errorf("a second move beside the first: %v", err)
	}
	if _, err := e.CancelBooking(ctx, tok2); !errors.Is(err, ErrNoSuchBooking) {
		t.Errorf("a cancel beside a move: %v", err)
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM vayumail_bookings WHERE email='ana@guest.example'`).Scan(&status)
	if status != "moving" {
		t.Errorf("a refused move left the booking %q", status)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_bookings SET status='confirmed' WHERE email='ana@guest.example'`); err != nil {
		t.Fatal(err)
	}
	p.Enabled = false
	if _, err := s.SaveBookingPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.MoveBooking(ctx, tok2, tuesdayAt("11:00"), bookingNow); !errors.Is(err, ErrNoSuchBooking) {
		t.Errorf("a move on a page switched off: %v", err)
	}
	_ = s.db.QueryRow(`SELECT status FROM vayumail_bookings WHERE email='ana@guest.example'`).Scan(&status)
	if status != "confirmed" {
		t.Errorf("a refused move left the booking %q", status)
	}
	if _, err := e.CancelBooking(ctx, tok2); err != nil {
		t.Errorf("a page switched off no longer cancels: %v", err)
	}
}
