// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Time zones in booking, case by case: the holder's weekly hours are clock
// times in the page's zone, whatever that zone's offset does on the day, and
// every time a guest is offered is an instant the holder is free.

// zonePage is a page in zone open hours[0]..hours[1] (minutes after
// midnight) every day of the week, for slots of length minutes.
func zonePage(t *testing.T, s *AccountStore, slug, zone string, open, closeAt, length int) BookingPage {
	t.Helper()
	p := BookingPage{Mailbox: calBox, Slug: slug, Title: "Call", Lengths: []int{length}, Zone: zone, Reach: 30, Enabled: true}
	for d := 0; d < 7; d++ {
		p.Hours[d] = [][2]int{{open, closeAt}}
	}
	id, err := s.SaveBookingPage(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	return p
}

// slotsOn is a page's slots on the given date in its own zone, as clock
// times there and as instants in UTC, asked a week before.
func slotsOn(t *testing.T, e *Engine, p BookingPage, length int, date string) (local, utc string) {
	t.Helper()
	loc := zoneNamed(p.Zone)
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := e.FreeSlots(p, length, day, day.AddDate(0, 0, 1), day.AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	var l, u []string
	for _, s := range slots {
		l = append(l, s.In(loc).Format("15:04 MST"))
		u = append(u, s.UTC().Format("15:04"))
	}
	return strings.Join(l, " "), strings.Join(u, " ")
}

// TestAPagesHoursAreTheHoldersClockOnTheDaysItChanges — 09:00 to 11:00 in
// London is 09:00 to 11:00 on the spring day the clock goes forward and the
// autumn day it goes back, not an hour off: the hours were placed by adding
// real time to midnight, which on those days is not the clock's time.
func TestAPagesHoursAreTheHoldersClockOnTheDaysItChanges(t *testing.T) {
	e, s, _ := itipEngine(t)
	p := zonePage(t, s, "london", "Europe/London", 9*60, 11*60, 60)
	for date, want := range map[string]string{
		"2027-03-27": "09:00 GMT 10:00 GMT", // the day before: GMT
		"2027-03-28": "09:00 BST 10:00 BST", // forward at 01:00
		"2027-03-29": "09:00 BST 10:00 BST",
		"2026-10-25": "09:00 GMT 10:00 GMT", // back at 02:00
	} {
		if got, _ := slotsOn(t, e, p, 60, date); got != want {
			t.Errorf("%s offers %q, want %q", date, got, want)
		}
	}
	// New York changes on other days than London.
	ny := zonePage(t, s, "ny", "America/New_York", 9*60, 10*60, 60)
	for date, want := range map[string]string{"2027-03-14": "13:00", "2027-03-13": "14:00", "2026-11-01": "14:00", "2026-10-31": "13:00"} {
		if _, got := slotsOn(t, e, ny, 60, date); got != want {
			t.Errorf("New York %s offers %q UTC, want %q", date, got, want)
		}
	}
}

// TestHoursAcrossTheChangeAreRealTime — a window that holds the hour that
// does not exist offers one slot fewer; one that holds the hour that comes
// twice offers it twice, as two different instants, never one slot twice.
func TestHoursAcrossTheChangeAreRealTime(t *testing.T) {
	e, s, _ := itipEngine(t)
	p := zonePage(t, s, "night", "Europe/London", 0, 4*60, 60)
	local, utc := slotsOn(t, e, p, 60, "2027-03-28")
	if local != "00:00 GMT 02:00 BST 03:00 BST" || utc != "00:00 01:00 02:00" {
		t.Errorf("the spring night offers %q (%q UTC)", local, utc)
	}
	local, utc = slotsOn(t, e, p, 60, "2026-10-25")
	if local != "00:00 BST 01:00 BST 01:00 GMT 02:00 GMT 03:00 GMT" || utc != "23:00 00:00 01:00 02:00 03:00" {
		t.Errorf("the autumn night offers %q (%q UTC)", local, utc)
	}
}

// TestZonesOffTheHourAndAcrossTheDateLine — Kolkata is +5:30 and Kathmandu
// +5:45; Auckland's Monday morning is Los Angeles's Sunday afternoon, and
// the page counts its most-a-day by the holder's day, not the guest's.
func TestZonesOffTheHourAndAcrossTheDateLine(t *testing.T) {
	e, s, _ := itipEngine(t)
	for zone, want := range map[string]string{"Asia/Kolkata": "03:30 04:00", "Asia/Kathmandu": "03:15 03:45"} {
		p := zonePage(t, s, strings.ToLower(strings.ReplaceAll(zone, "/", "-")), zone, 9*60, 10*60, 30)
		if _, got := slotsOn(t, e, p, 30, "2026-12-01"); got != want {
			t.Errorf("%s 09:00-10:00 offers %q UTC, want %q", zone, got, want)
		}
	}
	nz := zonePage(t, s, "nz", "Pacific/Auckland", 9*60, 11*60, 60)
	nz.PerDay = 1
	if _, err := s.SaveBookingPage(context.Background(), nz); err != nil {
		t.Fatal(err)
	}
	// Monday 7 December 2026, 09:00 in Auckland (NZDT, +13) is Sunday
	// 6 December, 12:00 in Los Angeles (PST, -8): one instant, 20:00 UTC on
	// the Sunday.
	local, utc := slotsOn(t, e, nz, 60, "2026-12-07")
	if local != "09:00 NZDT 10:00 NZDT" || utc != "20:00 21:00" {
		t.Fatalf("Auckland's Monday offers %q (%q UTC)", local, utc)
	}
	la, _ := time.LoadLocation("America/Los_Angeles")
	first := time.Date(2026, 12, 6, 20, 0, 0, 0, time.UTC)
	if got := first.In(la).Format("Monday 15:04"); got != "Sunday 12:00" {
		t.Errorf("the guest in Los Angeles is offered %q", got)
	}
	// Booked, it fills the holder's Monday, and only that day: Sunday and
	// Tuesday in Auckland, which are both partly Monday in Los Angeles, are
	// still offered.
	if _, err := s.db.Exec(`INSERT INTO vayumail_bookings(page_id,mailbox,start,end,name,email,status,created) VALUES(?,?,?,?,?,?,?,?)`,
		nz.ID, calBox, first.Unix(), first.Add(time.Hour).Unix(), "Guest", "g@x.test", "confirmed", first.AddDate(0, 0, -7)); err != nil {
		t.Fatal(err)
	}
	for date, want := range map[string]string{"2026-12-06": "09:00 NZDT 10:00 NZDT", "2026-12-07": "", "2026-12-08": "09:00 NZDT 10:00 NZDT"} {
		if got, _ := slotsOn(t, e, nz, 60, date); got != want {
			t.Errorf("after Monday's booking, Auckland's %s offers %q, want %q", date, got, want)
		}
	}
}

// TestARepeatingEventKeepsItsClockAcrossTheChange — a weekly 09:00 in
// London is 09:00 before and after the clock goes forward, in London, and
// moves in Kolkata, which has no change: the rule repeats the holder's
// clock time, not a fixed instant.
func TestARepeatingEventKeepsItsClockAcrossTheChange(t *testing.T) {
	e, _ := calEngine(t)
	london, kolkata := zoneNamed("Europe/London"), zoneNamed("Asia/Kolkata")
	start := time.Date(2027, 3, 21, 9, 0, 0, 0, london)
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), EventInput{Summary: "Standup", Start: start, End: start.Add(15 * time.Minute), Rule: "FREQ=WEEKLY;COUNT=3"}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var inLondon, inKolkata []string
	for _, ev := range between(t, e, start.AddDate(0, 0, -1), start.AddDate(0, 0, 21), london) {
		inLondon = append(inLondon, ev.Start.In(london).Format("Jan 2 15:04 MST"))
		inKolkata = append(inKolkata, ev.Start.In(kolkata).Format("Jan 2 15:04"))
	}
	if got := strings.Join(inLondon, ", "); got != "Mar 21 09:00 GMT, Mar 28 09:00 BST, Apr 4 09:00 BST" {
		t.Errorf("in London the series is %s", got)
	}
	if got := strings.Join(inKolkata, ", "); got != "Mar 21 14:30, Mar 28 13:30, Apr 4 13:30" {
		t.Errorf("in Kolkata the series is %s", got)
	}
}
