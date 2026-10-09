// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A report that a sent message did not reach someone shows on its Sent
// copy, a line a recipient, with what their server said and its code; the
// copy of a message that was delivered shows none.
func TestTheSentCopySaysWhatItDidNotReach(t *testing.T) {
	a := scheduledApp(t)
	for _, subject := range []string{"Bounced", "Delivered"} {
		if _, err := a.vayuMail.ComposeRich(context.Background(), vmail.ComposeMessage{From: "dana@example.com", To: []string{"erin@example.com"}, Subject: subject, Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	sent, _ := a.vayuMail.ListFolder(rd, "Sent")
	ids := map[string]vmail.StoredMessage{}
	for _, m := range sent {
		ids[m.Subject] = m
	}
	report := "From: MAILER-DAEMON@far.test\r\nTo: dana@example.com\r\nSubject: Undelivered\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: message/delivery-status\r\n\r\nReporting-MTA: dns; mx.far.test\r\n\r\nFinal-Recipient: rfc822; erin@example.com\r\nAction: failed\r\nStatus: 5.2.2\r\nDiagnostic-Code: smtp; 552 mailbox full\r\n\r\n" +
		"--b\r\nContent-Type: text/rfc822-headers\r\n\r\nMessage-ID: <" + ids["Bounced"].MessageID + ">\r\n\r\n--b--\r\n"
	if _, err := a.vayuMail.DeliverInbound("MAILER-DAEMON@far.test", "dana@example.com", []byte(report)); err != nil {
		t.Fatal(err)
	}
	card, _ := a.vayuReaderCard(rd, "Sent", ids["Bounced"].ID, readerView{Pane: true})
	if !strings.Contains(card, `<strong>Not delivered to erin@example.com.</strong> 552 mailbox full <span class="mx-bounces__code">5.2.2</span>`) {
		t.Fatalf("the Sent copy does not say what it did not reach:\n%s", card)
	}
	if card, _ := a.vayuReaderCard(rd, "Sent", ids["Delivered"].ID, readerView{Pane: true}); strings.Contains(card, "mx-bounces") {
		t.Fatalf("a delivered message shows a bounce:\n%s", card)
	}
}
