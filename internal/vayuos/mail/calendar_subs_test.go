// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// feed is a calendar another service publishes, with one event.
func feed(summary, start, end string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Other//Other//EN\r\nBEGIN:VEVENT\r\nUID:shared@other\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:" +
		summary + "\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

// fakeFeeds answers each address with what the map holds, counting reads.
func fakeFeeds(e *Engine, by map[string][]byte, reads *int) {
	e.calFetch = func(_ context.Context, addr string) ([]byte, error) {
		*reads++
		if b, ok := by[addr]; ok {
			return b, nil
		}
		return nil, errors.New("the address answered HTTP 404")
	}
}

func TestACalendarsAddressIsReadAsHTTPS(t *testing.T) {
	for in, want := range map[string]string{
		"webcal://calendar.example/private/basic.ics": "https://calendar.example/private/basic.ics",
		"WEBCALS://calendar.example/a.ics":            "https://calendar.example/a.ics",
		" https://calendar.example/a.ics ":            "https://calendar.example/a.ics",
		"http://calendar.example/a.ics":               "http://calendar.example/a.ics",
	} {
		if got, err := calSubURL(in); err != nil || got != want {
			t.Errorf("%q is read as %q (%v)", in, got, err)
		}
	}
	for _, bad := range []string{"", "ftp://calendar.example/a.ics", "file:///etc/passwd", "https://user:secret@calendar.example/a.ics", "calendar.example/a.ics"} {
		if _, err := calSubURL(bad); !errors.Is(err, ErrCalSubAddress) {
			t.Errorf("%q was taken: %v", bad, err)
		}
	}
}

// TestASubscribedCalendarIsShownAndMakesTheHolderBusy — its events are in
// the view, named by the calendar and never taken for the holder's own, and
// a booking page does not offer their time unless the holder says they do
// not make them busy.
func TestASubscribedCalendarIsShownAndMakesTheHolderBusy(t *testing.T) {
	e, s, _ := itipEngine(t)
	reads := 0
	fakeFeeds(e, map[string][]byte{"https://calendar.example/team.ics": feed("Team offsite", "20261110T110000Z", "20261110T113000Z")}, &reads)
	rd := ReadAsOwner("ankush")
	id, err := e.AddCalendarSubscription(rd, "Team", "webcal://calendar.example/team.ics", true, bookingNow)
	if err != nil || reads != 1 {
		t.Fatalf("subscribing: %v (%d reads)", err, reads)
	}
	from := tuesdayAt("00:00")
	evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 1 || evs[0].Summary != "Team offsite" || evs[0].Sub != "Team" || !strings.HasPrefix(evs[0].UID, "sub-") {
		t.Fatalf("the subscribed event: %+v", evs)
	}
	p := introPage(t, s)
	if got := tuesday(t, e, p, bookingNow); strings.Contains(got, "11:00") || strings.Contains(got, "10:30") {
		t.Errorf("a booking page offers a subscribed event's time: %q", got)
	}
	// Said not to make the holder busy, it does not.
	if _, err := s.db.Exec(`UPDATE vayumail_cal_subs SET busy=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := tuesday(t, e, p, bookingNow); !strings.Contains(got, "11:00") {
		t.Errorf("a calendar that does not make the holder busy still does: %q", got)
	}
	// The holder's own UIDs are not reached through it.
	if _, err := s.eventByUID(context.Background(), calBox, evs[0].UID); !errors.Is(err, errNoSuchEvent) {
		t.Errorf("a subscribed event is one of the holder's: %v", err)
	}
	subs, _ := e.CalendarSubscriptions(rd)
	if len(subs) != 1 || subs[0].URL != "https://calendar.example/team.ics" || subs[0].Error != "" || subs[0].Size == 0 {
		t.Errorf("the subscription: %+v", subs)
	}
}

// TestAFeedThatFailsKeepsItsLastGoodCopy — a failed read says why and the
// events stay; what is not a calendar is not kept; a newer copy is shown.
func TestAFeedThatFailsKeepsItsLastGoodCopy(t *testing.T) {
	e, _, _ := itipEngine(t)
	reads := 0
	by := map[string][]byte{"https://calendar.example/a.ics": feed("Standup", "20261110T090000Z", "20261110T091500Z")}
	fakeFeeds(e, by, &reads)
	rd := ReadAsOwner("ankush")
	id, _ := e.AddCalendarSubscription(rd, "A", "https://calendar.example/a.ics", true, bookingNow)
	delete(by, "https://calendar.example/a.ics")
	if err := e.RefreshCalendarSubscription(rd, id, bookingNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	from := tuesdayAt("00:00")
	if evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC); len(evs) != 1 {
		t.Fatalf("a failed read lost the calendar: %+v", evs)
	}
	subs, _ := e.CalendarSubscriptions(rd)
	if subs[0].Error != "the address answered HTTP 404" {
		t.Errorf("a failed read says %q", subs[0].Error)
	}
	by["https://calendar.example/a.ics"] = []byte("<html>Sign in</html>")
	_ = e.RefreshCalendarSubscription(rd, id, bookingNow.Add(4*time.Minute))
	subs, _ = e.CalendarSubscriptions(rd)
	if subs[0].Error != "what the address sent is not a calendar" {
		t.Errorf("a page that is not a calendar says %q", subs[0].Error)
	}
	if evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC); len(evs) != 1 || evs[0].Summary != "Standup" {
		t.Fatalf("a page that is not a calendar replaced it: %+v", evs)
	}
	by["https://calendar.example/a.ics"] = feed("Standup moved", "20261110T100000Z", "20261110T101500Z")
	_ = e.RefreshCalendarSubscription(rd, id, bookingNow.Add(6*time.Minute))
	if evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC); len(evs) != 1 || evs[0].Summary != "Standup moved" {
		t.Fatalf("a newer copy is not shown: %+v", evs)
	}
	// Refresh within a minute of the last read does not read again.
	before := reads
	_ = e.RefreshCalendarSubscription(rd, id, bookingNow.Add(6*time.Minute+30*time.Second))
	if reads != before {
		t.Errorf("a refresh within a minute read the address again")
	}
}

// TestSubscriptionsAreTheHoldersAndBounded — five a mailbox; another
// holder's is neither removed nor refreshed; an administrator has none.
func TestSubscriptionsAreTheHoldersAndBounded(t *testing.T) {
	e, _, _ := itipEngine(t)
	reads := 0
	fakeFeeds(e, map[string][]byte{}, &reads)
	rd := ReadAsOwner("ankush")
	var first int64
	for i := 0; i < maxCalSubs; i++ {
		id, err := e.AddCalendarSubscription(rd, "", "https://calendar.example/x.ics", true, bookingNow)
		if err != nil {
			t.Fatal(err)
		}
		if first == 0 {
			first = id
		}
	}
	if _, err := e.AddCalendarSubscription(rd, "", "https://calendar.example/x.ics", true, bookingNow); !errors.Is(err, ErrCalSubsFull) {
		t.Errorf("a sixth: %v", err)
	}
	dana := ReadAsOwner("dana")
	if err := e.RemoveCalendarSubscription(dana, first); !errors.Is(err, errNoSuchCalSub) {
		t.Errorf("another holder removed one: %v", err)
	}
	if err := e.RefreshCalendarSubscription(dana, first, bookingNow.Add(time.Hour)); !errors.Is(err, errNoSuchCalSub) {
		t.Errorf("another holder refreshed one: %v", err)
	}
	op := ReadAsOperator("ankush", "boss@example.com")
	if _, err := e.CalendarSubscriptions(op); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Errorf("an administrator listed them: %v", err)
	}
	if _, err := e.AddCalendarSubscription(op, "", "https://calendar.example/x.ics", true, bookingNow); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Errorf("an administrator added one: %v", err)
	}
	if err := e.RemoveCalendarSubscription(rd, first); err != nil {
		t.Errorf("the holder's remove: %v", err)
	}
	if subs, _ := e.CalendarSubscriptions(rd); len(subs) != maxCalSubs-1 || subs[0].Name != "Subscribed calendar" {
		t.Errorf("after one is removed: %+v", subs)
	}
}

// TestTheWorkerReadsFeedsThatAreDue — each half an hour, the oldest first,
// five a pass.
func TestTheWorkerReadsFeedsThatAreDue(t *testing.T) {
	e, _, _ := itipEngine(t)
	reads := 0
	fakeFeeds(e, map[string][]byte{}, &reads)
	for _, who := range []string{"ankush", "dana"} {
		for i := 0; i < 4; i++ {
			if _, err := e.AddCalendarSubscription(ReadAsOwner(who), "", "https://calendar.example/x.ics", true, bookingNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	reads = 0
	e.refreshSubscriptions(bookingNow.Add(29 * time.Minute))
	if reads != 0 {
		t.Fatalf("%d feeds read before they were due", reads)
	}
	e.refreshSubscriptions(bookingNow.Add(31 * time.Minute))
	if reads != calSubsPerPass {
		t.Fatalf("a pass read %d feeds", reads)
	}
	e.refreshSubscriptions(bookingNow.Add(31 * time.Minute))
	if reads != 8 {
		t.Fatalf("the second pass left %d of 8 read", reads)
	}
}

// TestAFeedOnTheServersOwnNetworkIsNotRead — through the real fetcher, an
// address on loopback is refused before anything is sent to it.
func TestAFeedOnTheServersOwnNetworkIsNotRead(t *testing.T) {
	e, _, _ := itipEngine(t)
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write(feed("Internal", "20261110T090000Z", "20261110T091500Z"))
	}))
	defer srv.Close()
	rd := ReadAsOwner("ankush")
	if _, err := e.AddCalendarSubscription(rd, "Inside", srv.URL+"/cal.ics", true, bookingNow); err != nil {
		t.Fatal(err)
	}
	subs, _ := e.CalendarSubscriptions(rd)
	if hit || len(subs) != 1 || subs[0].Size != 0 || !strings.Contains(subs[0].Error, "this server's own network") {
		t.Fatalf("a loopback feed: hit=%v %+v", hit, subs)
	}
}
