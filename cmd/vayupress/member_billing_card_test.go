// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/members"
)

func rowsByLabel(rows []billingRow) map[string]billingRow {
	m := map[string]billingRow{}
	for _, r := range rows {
		m[r.Label] = r
	}
	return m
}

// TestBillingRowsAnswerWhenAmICharged: the subscription always carried
// CurrentPeriodEnd but never showed it, so a paying member could not see when
// their next payment was due — the main question an account page exists to answer.
func TestBillingRowsAnswerWhenAmICharged(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	sub := &members.Subscription{
		Status: "active", Cadence: "monthly", AmountCents: 100, Currency: "USD",
		CurrentPeriodEnd: &end, StartedAt: now.AddDate(0, -3, 0),
	}
	got := rowsByLabel(memberBillingRows(sub, now))

	next, ok := got["Next payment"]
	if !ok {
		t.Fatalf("no next-payment row; got %v", got)
	}
	if !strings.Contains(next.Value, "24 August 2026") {
		t.Errorf("next payment = %q, want the period end date", next.Value)
	}
	if got["Status"].Value != "Active" {
		t.Errorf("status = %q", got["Status"].Value)
	}
	if !strings.Contains(got["Price"].Value, "monthly") {
		t.Errorf("price = %q, want the cadence", got["Price"].Value)
	}
	if _, ok := got["Member since"]; !ok {
		t.Error("expected a member-since row")
	}
}

// TestBillingRowsDoNotPromiseAPaymentAfterCancelling: once a member has
// cancelled, the period end is the day their ACCESS stops — calling it "next
// payment" would tell them they are about to be charged again.
func TestBillingRowsDoNotPromiseAPaymentAfterCancelling(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	sub := &members.Subscription{
		Status: "active", Cadence: "monthly", AmountCents: 100, Currency: "USD",
		CurrentPeriodEnd: &end, CancelAtPeriodEnd: true,
	}
	got := rowsByLabel(memberBillingRows(sub, now))

	if _, bad := got["Next payment"]; bad {
		t.Error("a cancelled subscription must not advertise a next payment")
	}
	access, ok := got["Access until"]
	if !ok {
		t.Fatal("a cancelled subscription should say how long access lasts")
	}
	if !strings.Contains(access.Note, "No further payments") {
		t.Errorf("note = %q, want a reassurance that billing has stopped", access.Note)
	}
	if got["Status"].Value != "Ending" {
		t.Errorf("status = %q, want Ending", got["Status"].Value)
	}
}

// TestBillingRowsSurfaceATrialClearly: a trial that converts silently is how
// people get charged unexpectedly. Both the status and the end date must show.
func TestBillingRowsSurfaceATrialClearly(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	trial := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got := rowsByLabel(memberBillingRows(&members.Subscription{
		Status: "trialing", Cadence: "monthly", AmountCents: 100, Currency: "USD",
		TrialEnd: &trial, CurrentPeriodEnd: &end,
	}, now))

	if got["Status"].Value != "Trial" {
		t.Errorf("status = %q, want Trial", got["Status"].Value)
	}
	if !strings.Contains(got["Status"].Note, "converts automatically") {
		t.Errorf("note = %q, want a warning that it converts", got["Status"].Note)
	}
	if _, ok := got["Trial ends"]; !ok {
		t.Error("a trial must show when it ends")
	}
}

// TestBillingRowsRenderDatesInTheSiteTimezone: a renewal date must read as the
// day the member's own calendar shows. 2026-08-24 21:00 UTC is the 25th in IST.
func TestBillingRowsRenderDatesInTheSiteTimezone(t *testing.T) {
	t.Cleanup(func() { _ = config.SetSiteTimeZone("") })
	if err := config.SetSiteTimeZone("Asia/Kolkata"); err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 24, 21, 0, 0, 0, time.UTC)
	got := rowsByLabel(memberBillingRows(&members.Subscription{
		Status: "active", Cadence: "monthly", AmountCents: 100, Currency: "USD",
		CurrentPeriodEnd: &end,
	}, now))
	if !strings.Contains(got["Next payment"].Value, "25 August 2026") {
		t.Errorf("next payment = %q, want the local date 25 August 2026", got["Next payment"].Value)
	}
}

// TestBillingRowsEmptyWithoutASubscription: a free member has nothing to bill, and
// an empty card is clutter.
func TestBillingRowsEmptyWithoutASubscription(t *testing.T) {
	if rows := memberBillingRows(nil, time.Now()); len(rows) != 0 {
		t.Errorf("got %d rows for no subscription, want none", len(rows))
	}
}

// TestMemberEventLabelsAreReadable: a new event type must still show something
// rather than silently vanishing from the member's history.
func TestMemberEventLabelsAreReadable(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"signup", "Joined"},
		{"cancel", "Cancelled"},
		{"payment", "Payment received"},
		{"some_new_kind", "Some_new_kind"},
		{"", "Account updated"},
	} {
		if got := memberEventLabel(members.Event{Type: tc.in}); got != tc.want {
			t.Errorf("label(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
