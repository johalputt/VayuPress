// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strings"
	"testing"
	"time"
)

// remindedPage is introPage reminding a day before.
func remindedPage(t *testing.T, s *AccountStore) BookingPage {
	t.Helper()
	p := introPage(t, s)
	p.Remind = 1440
	if _, err := s.SaveBookingPage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestABookingsGuestAndHolderAreReminded — the guest is mailed once, a day
// before, in their own zone; the holder's event carries the same reminder;
// a move reminds again for the new time.
func TestABookingsGuestAndHolderAreReminded(t *testing.T) {
	e, s, flush := itipEngine(t)
	p := remindedPage(t, s)
	ctx := context.Background()
	start := tuesdayAt("10:00")
	if err := e.HoldBooking(ctx, p, start, 30, BookingGuest{Name: "Kenji Mori", Email: "kenji@guest.example", Zone: "Asia/Tokyo"}, bookingNow, "https://site.example/book/confirm?t="); err != nil {
		t.Fatal(err)
	}
	tok := tokenIn(t, flush()[0].raw)
	b, err := e.ConfirmBooking(ctx, tok, bookingNow.Add(time.Minute), "https://site.example/book/change?t=")
	if err != nil {
		t.Fatal(err)
	}
	flush()
	evs := between(t, e, start, start.Add(time.Hour), time.UTC)
	if len(evs) != 1 || evs[0].Remind == nil || evs[0].Remind.Before != 24*time.Hour || !evs[0].Remind.ByMail {
		t.Fatalf("the holder's event has no day-before reminder: %+v", evs)
	}

	e.sendBookingReminders(start.Add(-24*time.Hour - time.Minute))
	if sent := flush(); len(sent) != 0 {
		t.Fatalf("a reminder before its time: %+v", sent)
	}
	e.sendBookingReminders(start.Add(-24*time.Hour + time.Minute))
	sent := flush()
	if len(sent) != 1 || sent[0].to[0] != "kenji@guest.example" || !strings.Contains(sent[0].raw, "Subject: Reminder: Intro call, Tuesday 10 November, 10:00") ||
		!strings.Contains(sent[0].raw, "Tuesday 10 November 2026, 19:00 (Asia/Tokyo), which is 10:00 on Tuesday for ankush@example.com (Europe/London), 30 minutes.") {
		t.Fatalf("the guest's reminder: %+v", sent)
	}
	e.sendBookingReminders(start.Add(-24*time.Hour + 2*time.Minute))
	if sent := flush(); len(sent) != 0 {
		t.Fatalf("a second reminder: %+v", sent)
	}

	// Moved, the new time is reminded in its turn.
	if _, err := e.MoveBooking(ctx, tokenOfBooking(t, e, b.UID), tuesdayAt("11:00"), bookingNow); err != nil {
		t.Fatal(err)
	}
	flush()
	if evs := between(t, e, start, start.Add(2*time.Hour), time.UTC); len(evs) != 1 || evs[0].Remind == nil || evs[0].Remind.Before != 24*time.Hour {
		t.Errorf("a moved event lost its reminder: %+v", evs)
	}
	e.sendBookingReminders(tuesdayAt("11:00").Add(-24*time.Hour + time.Minute))
	if sent := flush(); len(sent) != 1 || !strings.Contains(sent[0].raw, "Subject: Reminder: Intro call, Tuesday 10 November, 11:00") {
		t.Fatalf("the moved booking's reminder: %+v", sent)
	}
}

// tokenOfBooking is the move-or-cancel token in a booking's event.
func tokenOfBooking(t *testing.T, e *Engine, uid string) string {
	t.Helper()
	o, err := e.accounts.eventByUID(context.Background(), calBox, uid)
	if err != nil {
		t.Fatal(err)
	}
	return tokenIn(t, string(o.Data))
}

// TestWhenAGuestIsNotReminded — a page with none, a reminder whose time
// passed more than ten minutes ago (a booking made late), and a mailbox
// switched off send nothing; a page switched off still reminds its
// bookings.
func TestWhenAGuestIsNotReminded(t *testing.T) {
	e, s, flush := itipEngine(t)
	ctx := context.Background()
	p := remindedPage(t, s)
	confirmedTuesday(t, e, flush, p, "10:00", "kenji@guest.example")
	at := tuesdayAt("10:00").Add(-24 * time.Hour)

	e.sendBookingReminders(at.Add(11 * time.Minute))
	if sent := flush(); len(sent) != 0 {
		t.Errorf("a reminder past its time: %+v", sent)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_bookings SET reminded=0`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(ctx, calBox, false); err != nil {
		t.Fatal(err)
	}
	e.sendBookingReminders(at.Add(time.Minute))
	if sent := flush(); len(sent) != 0 {
		t.Errorf("a mailbox switched off sent a reminder: %+v", sent)
	}
	if err := s.SetActive(ctx, calBox, true); err != nil {
		t.Fatal(err)
	}
	p.Enabled = false
	if _, err := s.SaveBookingPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	e.sendBookingReminders(at.Add(time.Minute))
	if sent := flush(); len(sent) != 1 {
		t.Errorf("a page switched off did not remind its booking: %+v", sent)
	}

	p.Enabled, p.Remind = true, 0
	if _, err := s.SaveBookingPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_bookings SET reminded=0`); err != nil {
		t.Fatal(err)
	}
	e.sendBookingReminders(at.Add(time.Minute))
	if sent := flush(); len(sent) != 0 {
		t.Errorf("a page with no reminder sent one: %+v", sent)
	}
	p.Remind = 30
	if _, err := s.SaveBookingPage(ctx, p); err == nil || !strings.Contains(err.Error(), "an hour or a day before") {
		t.Errorf("a reminder of 30 minutes: %v", err)
	}
}
