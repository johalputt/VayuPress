// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/url"
	"strings"
	"testing"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

const danaInvite = "From: Boss <boss@other.example>\r\nTo: dana@example.com\r\nSubject: Invitation: Board\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n" +
	"--b\r\nContent-Type: text/plain\r\n\r\nCome.\r\n--b\r\nContent-Type: text/calendar; method=REQUEST; charset=utf-8\r\n\r\n" +
	"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//x//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:board@other\r\nDTSTAMP:20261108T100000Z\r\n" +
	"SUMMARY:Board <b>meeting</b>\r\nDTSTART:20261112T090000Z\r\nDURATION:PT1H\r\nLOCATION:Room 4\r\nORGANIZER;CN=The Boss:mailto:boss@other.example\r\n" +
	"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:dana@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n--b--\r\n"

// TestAnInvitationIsAnsweredFromItsCard — the card says what and when, and
// what else is then; Accept puts it in the calendar; an administrator who
// opens the mailbox sees the message and no card.
func TestAnInvitationIsAnsweredFromItsCard(t *testing.T) {
	a := calendarApp(t)
	calSave(a, dana(), url.Values{"title": {"Dentist"}, "sd": {"2026-11-12"}, "st": {"09:30"}, "ed": {"2026-11-12"}, "et": {"10:00"}, "zone": {"UTC"}})
	if _, err := a.vayuMail.DeliverInbound("boss@other.example", "dana@example.com", []byte(danaInvite)); err != nil {
		t.Fatal(err)
	}
	own := vmail.ReadAsOwner("dana")
	msgs, _ := a.vayuMail.ListFolder(own, "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	id := msgs[0].ID
	card, _ := a.vayuReaderCard(own, "Inbox", id, readerView{Pane: true, Home: time.UTC, Places: holderPlaces{Calendar: true}})
	for _, want := range []string{"Invitation from The Boss", "Board &lt;b&gt;meeting&lt;/b&gt;", "Thursday 12 November 2026 · 09:00–10:00", "Room 4", "At the same time: Dentist", ">Accept</button>"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card lacks %q", want)
		}
	}
	if strings.Contains(card, "<b>meeting</b>") {
		t.Error("the invitation's title was written unescaped")
	}
	if op, _ := a.vayuReaderCard(vmail.ReadAsOperator("dana", "boss@example.com"), "Inbox", id, readerView{Pane: true, Places: holderPlaces{Calendar: true}}); strings.Contains(op, "mx-invite") {
		t.Error("an administrator is shown the holder's invitation")
	}
	rec := postForm(a.handleVayuOSCalendarAnswer, "/os/vayumail/calendar/answer", url.Values{"folder": {"Inbox"}, "id": {id}, "answer": {"ACCEPTED"}}.Encode(), dana())
	if body := rec.Body.String(); !strings.Contains(body, `aria-pressed="true"`) || !strings.Contains(body, "You are going") {
		t.Fatalf("the card after accepting: %s", body)
	}
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	evs, _ := a.vayuMail.CalendarBetween(own, from, from.AddDate(0, 0, 1), time.UTC)
	if len(evs) != 2 {
		t.Fatalf("the calendar after accepting: %+v", evs)
	}
}
