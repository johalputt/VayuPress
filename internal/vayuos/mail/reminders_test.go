// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strings"
	"testing"
	"time"
)

func reminderMail(t *testing.T, e *Engine) []StoredMessage {
	t.Helper()
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	var out []StoredMessage
	for _, m := range msgs {
		if strings.HasPrefix(m.Subject, "Reminder: ") {
			out = append(out, m)
		}
	}
	return out
}

// TestAMailReminderArrivesOnceAtItsTime — not before, then once, and the
// event keeps an alarm to show for the holder's devices as well.
func TestAMailReminderArrivesOnceAtItsTime(t *testing.T) {
	e, s, _ := itipEngine(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	uid, err := e.SaveEvent(ReadAsOwner("ankush"), EventInput{Summary: "Dentist", Location: "High St", Start: start, End: start.Add(time.Hour),
		Remind: &Reminder{Before: 10 * time.Minute, ByMail: true}, SetRemind: true}, ScopeAll, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	e.sendReminders(start.Add(-11 * time.Minute))
	if n := len(reminderMail(t, e)); n != 0 {
		t.Fatalf("%d reminders a minute early", n)
	}
	e.sendReminders(start.Add(-9*time.Minute - 30*time.Second))
	got := reminderMail(t, e)
	if len(got) != 1 || got[0].Subject != "Reminder: Dentist" {
		t.Fatalf("at its time: %+v", got)
	}
	e.sendReminders(start.Add(-9 * time.Minute))
	if n := len(reminderMail(t, e)); n != 1 {
		t.Fatalf("the reminder came %d times", n)
	}
	o, _ := s.eventByUID(context.Background(), calBox, uid)
	data := string(o.Data)
	if !strings.Contains(data, "ACTION:DISPLAY") || !strings.Contains(data, "TRIGGER:-PT600S") || !strings.Contains(data, "ACTION:EMAIL") {
		t.Errorf("the alarms kept: %s", data)
	}
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	evs := between(t, e, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 1 || evs[0].Remind == nil || evs[0].Remind.Before != 10*time.Minute || !evs[0].Remind.ByMail {
		t.Fatalf("read back: %+v", evs)
	}
}

// TestAReminderToShowIsTheDevicesToShow — the server mails only the
// reminder by mail, also on an event that has both, at different times.
func TestAReminderToShowIsTheDevicesToShow(t *testing.T) {
	e, s, _ := itipEngine(t)
	putEvent(t, s, "g.ics", "g@x", "BEGIN:VEVENT", "UID:g@x", "DTSTAMP:20261101T080000Z", "SUMMARY:Gym", "DTSTART:20261112T090000Z",
		"BEGIN:VALARM", "ACTION:DISPLAY", "TRIGGER:-PT30M", "DESCRIPTION:Gym", "END:VALARM",
		"BEGIN:VALARM", "ACTION:EMAIL", "TRIGGER:-PT10M", "SUMMARY:Gym", "DESCRIPTION:Gym", "ATTENDEE:mailto:ankush@example.com", "END:VALARM", "END:VEVENT")
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	e.sendReminders(start.Add(-29 * time.Minute))
	if n := len(reminderMail(t, e)); n != 0 {
		t.Fatalf("the reminder to show was mailed (%d)", n)
	}
	e.sendReminders(start.Add(-9 * time.Minute))
	if n := len(reminderMail(t, e)); n != 1 {
		t.Fatalf("the reminder by mail came %d times", n)
	}
}

// TestAReminderLongMissedIsNotSent — a server back after an hour does not
// send what was due before it went down.
func TestAReminderLongMissedIsNotSent(t *testing.T) {
	e, _, _ := itipEngine(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), EventInput{Summary: "Call", Start: start, End: start.Add(time.Hour),
		Remind: &Reminder{Before: time.Hour, ByMail: true}, SetRemind: true}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.sendReminders(start.Add(-time.Hour + reminderLate + time.Minute))
	if n := len(reminderMail(t, e)); n != 0 {
		t.Fatalf("a reminder %v late was sent", reminderLate+time.Minute)
	}
}

// TestEachOccurrenceIsRemindedOf — a weekly event's second week has its own.
func TestEachOccurrenceIsRemindedOf(t *testing.T) {
	e, _, _ := itipEngine(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	if _, err := e.SaveEvent(ReadAsOwner("ankush"), EventInput{Summary: "Standup", Start: start, End: start.Add(15 * time.Minute), Rule: "FREQ=WEEKLY;COUNT=3",
		Remind: &Reminder{Before: 5 * time.Minute, ByMail: true}, SetRemind: true}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.sendReminders(start.Add(-4 * time.Minute))
	e.sendReminders(start.AddDate(0, 0, 7).Add(-4 * time.Minute))
	if n := len(reminderMail(t, e)); n != 2 {
		t.Fatalf("%d reminders for two weeks", n)
	}
}

// TestAnEditLeavesAPhonesAlarms — an edit that sets no reminder keeps the
// ones an app put on the event; one that sets none takes them off.
func TestAnEditLeavesAPhonesAlarms(t *testing.T) {
	e, s, _ := itipEngine(t)
	putEvent(t, s, "p.ics", "p@x", "BEGIN:VEVENT", "UID:p@x", "DTSTAMP:20261101T080000Z", "SUMMARY:Flight", "DTSTART:20261112T060000Z",
		"BEGIN:VALARM", "ACTION:DISPLAY", "TRIGGER:-PT2H", "END:VALARM", "END:VEVENT")
	rd := ReadAsOwner("ankush")
	at := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	if _, err := e.SaveEvent(rd, EventInput{UID: "p@x", Summary: "Flight to Lisbon", Start: at, End: at}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.eventByUID(context.Background(), calBox, "p@x"); !strings.Contains(string(o.Data), "TRIGGER:-PT2H") {
		t.Fatal("an edit without a reminder dropped the phone's alarm")
	}
	if _, err := e.SaveEvent(rd, EventInput{UID: "p@x", Summary: "Flight to Lisbon", Start: at, End: at, SetRemind: true}, ScopeAll, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.eventByUID(context.Background(), calBox, "p@x"); strings.Contains(string(o.Data), "VALARM") {
		t.Fatal("setting no reminder left an alarm")
	}
}

// TestARemindersPassReadsOnlyEventsOfAnEventsSize — an object past the bound
// is not read every half minute, so its reminder by mail is not sent.
func TestARemindersPassReadsOnlyEventsOfAnEventsSize(t *testing.T) {
	e, s, _ := itipEngine(t)
	putEvent(t, s, "big.ics", "big@x", "BEGIN:VEVENT", "UID:big@x", "DTSTAMP:20261101T080000Z", "SUMMARY:Big", "DTSTART:20261112T090000Z",
		"DESCRIPTION:"+strings.Repeat("x", maxReminderObject), "BEGIN:VALARM", "ACTION:EMAIL", "TRIGGER:-PT10M", "SUMMARY:Big", "DESCRIPTION:Big",
		"ATTENDEE:mailto:ankush@example.com", "END:VALARM", "END:VEVENT")
	e.sendReminders(time.Date(2026, 11, 12, 8, 51, 0, 0, time.UTC))
	if n := len(reminderMail(t, e)); n != 0 {
		t.Fatalf("an object past the bound was read: %d reminders", n)
	}
}
