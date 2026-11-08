// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

type sentMail struct {
	to  []string
	raw string
}

// itipEngine is calEngine that can sign and send, keeping what it would
// deliver to other servers.
func itipEngine(t *testing.T) (*Engine, *AccountStore, func() []sentMail) {
	t.Helper()
	s := aliasTestStore(t)
	qdb, _ := sql.Open("sqlite3", ":memory:")
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { qdb.Close() })
	var sent []sentMail
	q, err := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, to []string, raw []byte) error {
		sent = append(sent, sentMail{to, string(raw)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.StorageDir = t.TempDir()
	dk, err := LoadOrCreateDKIM(cfg.StorageDir, cfg.DKIMSelector, cfg.Domain)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q, db: s.db, dkim: dk}
	flush := func() []sentMail {
		sent = nil
		if _, _, err := q.ProcessDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return sent
	}
	return e, s, flush
}

func londonTeam(t *testing.T, guests ...string) EventInput {
	t.Helper()
	london, _ := time.LoadLocation("Europe/London")
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, london)
	return EventInput{Summary: "Team", Location: "Studio", Start: start, End: start.Add(time.Hour), Rule: "FREQ=WEEKLY;COUNT=4", Guests: guests}
}

// unfold joins an iCalendar's folded lines, so a long property is one line.
func unfold(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

// TestGuestsAreInvitedFromTheHoldersAddress — one invitation, signed, with
// the event for their programs and the zone it is in.
func TestGuestsAreInvitedFromTheHoldersAddress(t *testing.T) {
	e, _, flush := itipEngine(t)
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), londonTeam(t, "rohan@ledger.example", "Sana <sana@north.example>", "ankush@example.com", "ROHAN@ledger.example"), ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	sent := flush()
	if len(sent) != 1 || strings.Join(sent[0].to, ",") != "rohan@ledger.example,sana@north.example" {
		t.Fatalf("sent %+v", sent)
	}
	raw := unfold(sent[0].raw)
	for _, want := range []string{
		"From: ankush@example.com", "Subject: Invitation: Team @ Tue 10 Nov 2026 09:00 (Europe/London)", "DKIM-Signature:",
		"Content-Type: text/calendar; charset=utf-8; method=REQUEST", "METHOD:REQUEST", "ORGANIZER:mailto:ankush@example.com",
		"mailto:rohan@ledger.example", "PARTSTAT=NEEDS-ACTION", "RSVP=TRUE", "BEGIN:VTIMEZONE", "TZID:Europe/London",
		"BEGIN:DAYLIGHT", "RRULE:FREQ=WEEKLY;COUNT=4", "invites you to this event",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("the invitation lacks %q", want)
		}
	}
}

// TestAGuestRemovedIsSentTheCancellation — those kept are sent the change.
func TestAGuestRemovedIsSentTheCancellation(t *testing.T) {
	e, _, flush := itipEngine(t)
	rd := ReadAsOwner("ankush")
	in := londonTeam(t, "rohan@ledger.example", "sana@north.example")
	uid, err := e.SaveEvent(rd, in, ScopeAll, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	flush()
	in.UID, in.Guests = uid, []string{"rohan@ledger.example"}
	if _, err := e.SaveEvent(rd, in, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	sent := flush()
	if len(sent) != 2 {
		t.Fatalf("%d messages: %+v", len(sent), sent)
	}
	for _, m := range sent {
		raw := unfold(m.raw)
		switch strings.Join(m.to, ",") {
		case "rohan@ledger.example":
			if !strings.Contains(raw, "Subject: Updated invitation: Team") || !strings.Contains(raw, "METHOD:REQUEST") || !strings.Contains(raw, "SEQUENCE:1") {
				t.Errorf("the guest kept was sent: %s", raw)
			}
		case "sana@north.example":
			if !strings.Contains(raw, "Subject: Cancelled: Team") || !strings.Contains(raw, "METHOD:CANCEL") || !strings.Contains(raw, "STATUS:CANCELLED") {
				t.Errorf("the guest removed was sent: %s", raw)
			}
		default:
			t.Errorf("sent to %v", m.to)
		}
	}
}

// TestDeletingAnEventTellsEveryGuest — and deleting one occurrence sends the
// series without it.
func TestDeletingAnEventTellsEveryGuest(t *testing.T) {
	e, _, flush := itipEngine(t)
	rd := ReadAsOwner("ankush")
	in := londonTeam(t, "rohan@ledger.example", "sana@north.example")
	uid, _ := e.SaveEvent(rd, in, ScopeAll, time.Time{})
	flush()
	if err := e.DeleteEvent(rd, uid, ScopeOne, in.Start.AddDate(0, 0, 7)); err != nil {
		t.Fatal(err)
	}
	if sent := flush(); len(sent) != 1 || !strings.Contains(unfold(sent[0].raw), "EXDATE;TZID=Europe/London:20261117T090000") {
		t.Fatalf("deleting one occurrence sent %+v", sent)
	}
	if err := e.DeleteEvent(rd, uid, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	sent := flush()
	if len(sent) != 1 || len(sent[0].to) != 2 || !strings.Contains(sent[0].raw, "METHOD:CANCEL") {
		t.Fatalf("deleting the event sent %+v", sent)
	}
}

// TestAnEventSomeoneElseOrganizesSendsNothing — its guests are theirs.
func TestAnEventSomeoneElseOrganizesSendsNothing(t *testing.T) {
	e, s, flush := itipEngine(t)
	putEvent(t, s, "o.ics", "o@x", "BEGIN:VEVENT", "UID:o@x", "SUMMARY:Board", "DTSTART:20261110T090000Z", "DTEND:20261110T100000Z",
		"ORGANIZER:mailto:boss@other.example", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:ankush@example.com", "ATTENDEE:mailto:cfo@other.example", "END:VEVENT")
	rd := ReadAsOwner("ankush")
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	if _, err := e.SaveEvent(rd, EventInput{UID: "o@x", Summary: "Board (my note)", Start: at, End: at.Add(time.Hour)}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if sent := flush(); len(sent) != 0 {
		t.Fatalf("an invited holder's change was sent to %v", sent[0].to)
	}
	if _, err := e.SaveEvent(rd, EventInput{UID: "o@x", Summary: "Board", Start: at, End: at.Add(time.Hour), Guests: []string{"friend@x.example"}}, ScopeAll, time.Time{}); !errors.Is(err, ErrNotOrganizer) {
		t.Fatalf("changing someone else's guests: %v", err)
	}
}

func replyFrom(from, attendee, uid, status string) []byte {
	return []byte("From: " + from + "\r\nTo: ankush@example.com\r\nSubject: Accepted: Team\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nYes.\r\n--b\r\nContent-Type: text/calendar; method=REPLY; charset=utf-8\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//x//EN\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261108T100000Z\r\n" +
		"ATTENDEE;PARTSTAT=" + status + ":mailto:" + attendee + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n--b--\r\n")
}

func partStatOf(t *testing.T, s *AccountStore, uid, guest string) string {
	t.Helper()
	o, err := s.eventByUID(context.Background(), calBox, uid)
	if err != nil {
		t.Fatal(err)
	}
	cal, _ := ical.NewDecoder(strings.NewReader(string(o.Data))).Decode()
	m, _ := parts(cal)
	for _, p := range m.Props.Values(ical.PropAttendee) {
		if mailtoOf(p.Value) == guest {
			return p.Params.Get(ical.ParamParticipationStatus)
		}
	}
	return ""
}

// TestAGuestsAnswerIsRecorded — from the guest, and filed as read; an answer
// for someone else, or about an event the holder does not organize, changes
// nothing and stays unread.
func TestAGuestsAnswerIsRecorded(t *testing.T) {
	e, s, flush := itipEngine(t)
	uid, _ := e.SaveEvent(ReadAsOwner("ankush"), londonTeam(t, "rohan@ledger.example", "sana@north.example"), ScopeAll, time.Time{})
	flush()
	if _, err := e.DeliverInbound("sana@north.example", calBox, replyFrom("Sana <sana@north.example>", "rohan@ledger.example", uid, "DECLINED")); err != nil {
		t.Fatal(err)
	}
	if got := partStatOf(t, s, uid, "rohan@ledger.example"); got != "NEEDS-ACTION" {
		t.Fatalf("an answer from someone else for Rohan set %s", got)
	}
	if _, err := e.DeliverInbound("rohan@ledger.example", calBox, replyFrom("Rohan <rohan@ledger.example>", "rohan@ledger.example", uid, "ACCEPTED")); err != nil {
		t.Fatal(err)
	}
	if got := partStatOf(t, s, uid, "rohan@ledger.example"); got != "ACCEPTED" {
		t.Fatalf("Rohan's answer recorded as %q", got)
	}
	// A guest added later does not undo the answers given.
	in := londonTeam(t, "rohan@ledger.example", "sana@north.example", "lee@third.example")
	in.UID = uid
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), in, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	flush()
	if got := partStatOf(t, s, uid, "rohan@ledger.example"); got != "ACCEPTED" {
		t.Fatalf("adding a guest reset Rohan's answer to %q", got)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	read := 0
	for _, m := range msgs {
		if m.Seen {
			read++
		}
	}
	if len(msgs) != 2 || read != 1 {
		t.Fatalf("%d messages, %d read; want the answer recorded filed read and the other not", len(msgs), read)
	}
	// As a calendar program writes it, DTSTAMP and all: without one the
	// object cannot be written back, and an answer would be refused for that
	// reason and not for whose event it is.
	putEvent(t, s, "o.ics", "o@x", "BEGIN:VEVENT", "UID:o@x", "DTSTAMP:20261101T080000Z", "SUMMARY:Board", "DTSTART:20261110T090000Z",
		"ORGANIZER:mailto:boss@other.example", "ATTENDEE:mailto:cfo@other.example", "END:VEVENT")
	if _, err := e.DeliverInbound("cfo@other.example", calBox, replyFrom("cfo@other.example", "cfo@other.example", "o@x", "ACCEPTED")); err != nil {
		t.Fatal(err)
	}
	if got := partStatOf(t, s, "o@x", "cfo@other.example"); got != "" {
		t.Errorf("an answer about someone else's event was recorded: %q", got)
	}
}

// TestTheZoneGuestsAreSentHasItsClockChanges — London's two changes as yearly
// rules, a zone with none as its one offset, and the southern summer.
func TestTheZoneGuestsAreSentHasItsClockChanges(t *testing.T) {
	enc := func(name string) string {
		loc, _ := time.LoadLocation(name)
		cal := ical.NewCalendar()
		cal.Props.SetText(ical.PropVersion, "2.0")
		cal.Props.SetText(ical.PropProductID, "x")
		cal.Children = append(cal.Children, vtimezone(loc, 2026))
		b, err := encodeCal(cal)
		if err != nil {
			t.Fatal(err)
		}
		return unfold(string(b))
	}
	london := enc("Europe/London")
	for _, want := range []string{
		"BEGIN:DAYLIGHT\r\nDTSTART:20260329T010000\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\nTZNAME:BST\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0100",
		"BEGIN:STANDARD\r\nDTSTART:20261025T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\nTZNAME:GMT\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0000",
	} {
		if !strings.Contains(london, want) {
			t.Errorf("London lacks\n%s\nin\n%s", want, london)
		}
	}
	if k := enc("Asia/Kolkata"); strings.Count(k, "BEGIN:STANDARD") != 1 || strings.Contains(k, "DAYLIGHT") || !strings.Contains(k, "TZOFFSETTO:+0530") {
		t.Errorf("Kolkata:\n%s", k)
	}
	if syd := enc("Australia/Sydney"); !strings.Contains(syd, "BEGIN:DAYLIGHT\r\nDTSTART:20261004T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=1SU") {
		t.Errorf("Sydney's summer:\n%s", syd)
	}
}

// TestTheGuestListIsAddresses — the holder and repeats are dropped; what is
// not an address, or too many, is refused.
func TestTheGuestListIsAddresses(t *testing.T) {
	got, err := cleanGuests([]string{"A <a@x.example>", "a@x.example", " ", "ankush@example.com", "b@y.example"}, calBox)
	if err != nil || strings.Join(got, ",") != "a@x.example,b@y.example" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := cleanGuests([]string{"not an address"}, calBox); err == nil {
		t.Error("a word was taken as a guest")
	}
	many := make([]string, maxGuests+1)
	for i := range many {
		many[i] = "g" + strconv.Itoa(i) + "@x.example"
	}
	if _, err := cleanGuests(many, calBox); err == nil {
		t.Error("more than the most guests were taken")
	}
}

func inviteMail(from, method, uid string, seq int, start string, extra ...string) []byte {
	return []byte("From: " + from + "\r\nTo: ankush@example.com\r\nSubject: Invitation\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nCome.\r\n--b\r\nContent-Type: text/calendar; method=" + method + "; charset=utf-8\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//x//EN\r\nMETHOD:" + method + "\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261108T100000Z\r\n" +
		"SEQUENCE:" + strconv.Itoa(seq) + "\r\nSUMMARY:Board\r\nDTSTART:" + start + "\r\nDURATION:PT1H\r\nORGANIZER:mailto:boss@other.example\r\n" +
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:ankush@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:cfo@other.example\r\n" + strings.Join(extra, "") + "END:VEVENT\r\nEND:VCALENDAR\r\n--b--\r\n")
}

// deliverAndFind delivers a message to the holder and returns its id.
func deliverAndFind(t *testing.T, e *Engine, from string, raw []byte) string {
	t.Helper()
	before, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	if _, err := e.DeliverInbound(from, calBox, raw); err != nil {
		t.Fatal(err)
	}
	after, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	for _, m := range after {
		seen := false
		for _, b := range before {
			seen = seen || b.ID == m.ID
		}
		if !seen {
			return m.ID
		}
	}
	t.Fatal("the message did not arrive")
	return ""
}

func novemberEvents(t *testing.T, e *Engine) []CalEvent {
	t.Helper()
	from, to := month(2026, 11)
	return between(t, e, from, to, time.UTC)
}

// TestAnInvitationWaitsForItsAnswer — shown, not in the calendar; accepted,
// it is, and the organizer is told from the holder's address; declined, it
// is kept and not shown.
func TestAnInvitationWaitsForItsAnswer(t *testing.T) {
	e, s, flush := itipEngine(t)
	rd := ReadAsOwner("ankush")
	id := deliverAndFind(t, e, "boss@other.example", inviteMail("Boss <boss@other.example>", "REQUEST", "board@other", 0, "20261112T090000Z"))
	raw, _ := e.ReadFolderMessage(rd, "Inbox", id)
	inv, ok := e.InvitationIn(rd, raw, time.UTC)
	if !ok || inv.Method != "REQUEST" || inv.Organizer.Email != "boss@other.example" || inv.Stored || inv.Event.Summary != "Board" {
		t.Fatalf("the invitation read as %+v %v", inv, ok)
	}
	if evs := novemberEvents(t, e); len(evs) != 0 {
		t.Fatalf("an unanswered invitation is in the calendar: %+v", evs)
	}
	if err := e.AnswerInvitation(rd, "Inbox", id, "accepted"); err != nil {
		t.Fatal(err)
	}
	if evs := novemberEvents(t, e); len(evs) != 1 || evs[0].Start.Day() != 12 {
		t.Fatalf("after accepting: %+v", evs)
	}
	sent := flush()
	if len(sent) != 1 || sent[0].to[0] != "boss@other.example" {
		t.Fatalf("sent %+v", sent)
	}
	reply := unfold(sent[0].raw)
	for _, want := range []string{"Subject: Accepted: Board", "From: ankush@example.com", "METHOD:REPLY", "UID:board@other", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:ankush@example.com", "method=REPLY"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the answer lacks %q", want)
		}
	}
	if strings.Count(reply, "ATTENDEE") != 1 {
		t.Error("the answer speaks for more than the holder")
	}
	if err := e.AnswerInvitation(rd, "Inbox", id, "declined"); err != nil {
		t.Fatal(err)
	}
	if evs := novemberEvents(t, e); len(evs) != 0 {
		t.Fatalf("a declined event is shown: %+v", evs)
	}
	if inv, _ := e.InvitationIn(rd, raw, time.UTC); !inv.Stored || inv.Answer != "DECLINED" {
		t.Fatalf("after declining: %+v", inv)
	}
	if _, err := s.eventByUID(context.Background(), calBox, "board@other"); err != nil {
		t.Error("a declined event was not kept")
	}
}

// TestALaterVersionOfAnInvitationWins — answering the earlier one after it
// changes only the answer.
func TestALaterVersionOfAnInvitationWins(t *testing.T) {
	e, _, _ := itipEngine(t)
	rd := ReadAsOwner("ankush")
	first := deliverAndFind(t, e, "boss@other.example", inviteMail("boss@other.example", "REQUEST", "b@o", 0, "20261112T090000Z"))
	moved := deliverAndFind(t, e, "boss@other.example", inviteMail("boss@other.example", "REQUEST", "b@o", 1, "20261113T150000Z"))
	if err := e.AnswerInvitation(rd, "Inbox", moved, "tentative"); err != nil {
		t.Fatal(err)
	}
	if err := e.AnswerInvitation(rd, "Inbox", first, "accepted"); err != nil {
		t.Fatal(err)
	}
	evs := novemberEvents(t, e)
	if len(evs) != 1 || evs[0].Start.Day() != 13 || evs[0].Start.Hour() != 15 {
		t.Fatalf("the earlier version replaced the later: %+v", evs)
	}
}

// TestACancellationIsTheOrganizers — from them it takes the event out; from
// anyone else it changes nothing.
func TestACancellationIsTheOrganizers(t *testing.T) {
	e, _, _ := itipEngine(t)
	rd := ReadAsOwner("ankush")
	inv := deliverAndFind(t, e, "boss@other.example", inviteMail("boss@other.example", "REQUEST", "c@o", 0, "20261112T090000Z"))
	if err := e.AnswerInvitation(rd, "Inbox", inv, "accepted"); err != nil {
		t.Fatal(err)
	}
	forged := deliverAndFind(t, e, "mallory@evil.example", inviteMail("mallory@evil.example", "CANCEL", "c@o", 1, "20261112T090000Z"))
	if err := e.RemoveCancelled(rd, "Inbox", forged); err == nil {
		t.Fatal("a cancellation from someone else was taken")
	}
	if evs := novemberEvents(t, e); len(evs) != 1 {
		t.Fatal("a cancellation from someone else removed the event")
	}
	real := deliverAndFind(t, e, "boss@other.example", inviteMail("Boss <boss@other.example>", "CANCEL", "c@o", 1, "20261112T090000Z"))
	if err := e.RemoveCancelled(rd, "Inbox", real); err != nil {
		t.Fatal(err)
	}
	if evs := novemberEvents(t, e); len(evs) != 0 {
		t.Fatalf("after the organizer's cancellation: %+v", evs)
	}
}

// TestAnInvitationIsTheHoldersToAnswer — an administrator's reader sees none
// and answers none; the holder's own invitation to others has no card.
func TestAnInvitationIsTheHoldersToAnswer(t *testing.T) {
	e, _, _ := itipEngine(t)
	id := deliverAndFind(t, e, "boss@other.example", inviteMail("boss@other.example", "REQUEST", "d@o", 0, "20261112T090000Z"))
	op := ReadAsOperator("ankush", "admin@example.com")
	raw, _ := e.ReadFolderMessage(ReadAsOwner("ankush"), "Inbox", id)
	if _, ok := e.InvitationIn(op, raw, time.UTC); ok {
		t.Error("an administrator is shown the holder's invitation")
	}
	if err := e.AnswerInvitation(op, "Inbox", id, "accepted"); !errors.Is(err, ErrCalendarHolderOnly) {
		t.Errorf("an administrator answered: %v", err)
	}
	own := []byte(strings.ReplaceAll(string(inviteMail("ankush@example.com", "REQUEST", "e@o", 0, "20261112T090000Z")), "ORGANIZER:mailto:boss@other.example", "ORGANIZER:mailto:ankush@example.com"))
	if _, ok := e.InvitationIn(ReadAsOwner("ankush"), own, time.UTC); ok {
		t.Error("the holder's own invitation has a card")
	}
}
