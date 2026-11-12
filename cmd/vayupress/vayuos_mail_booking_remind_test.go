// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// TestABookingPagesReminderIsChosenInItsEditor — a new page reminds a day
// before unless told otherwise; the choice is saved and shown again.
func TestABookingPagesReminderIsChosenInItsEditor(t *testing.T) {
	a := calendarApp(t)
	if page := calGet(a, a.handleVayuOSBookingPage, "/os/vayumail/calendar/booking", dana(), "").Body.String(); !strings.Contains(page, `<option value="1440" selected>A day before</option>`) {
		t.Fatalf("a new page's reminder: %s", page)
	}
	vals := url.Values{"op": {"save"}, "title": {"Intro call"}, "slug": {"intro"}, "length": {"30"}, "zone": {"UTC"},
		"reach": {"30"}, "hours0": {"10:00-12:00"}, "remind": {"60"}, "enabled": {"1"}}
	postForm(a.handleVayuOSBookingAction, "/os/vayumail/calendar/booking/action", vals.Encode(), dana())
	pages, _ := a.vayuMail.Accounts().BookingPagesFor(context.Background(), "dana@example.com")
	if len(pages) != 1 || pages[0].Remind != 60 {
		t.Fatalf("the saved page: %+v", pages)
	}
	if page := calGet(a, a.handleVayuOSBookingPage, "/os/vayumail/calendar/booking?id="+itoa64(pages[0].ID), dana(), "").Body.String(); !strings.Contains(page, `<option value="60" selected>An hour before</option>`) {
		t.Errorf("the saved page's editor: %s", page)
	}
}
