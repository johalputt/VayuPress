package main

// member_billing_card.go — the "Membership & billing" card on the member account
// page.
//
// The subscription record already carried CurrentPeriodEnd, TrialEnd,
// CancelAtPeriodEnd and StartedAt, but none of it was ever shown: a paying member
// could see WHAT plan they were on and nothing about WHEN they would next be
// charged, whether a trial was running out, or whether a cancellation they had
// requested had actually taken. Those are the questions people open an account
// page to answer, and not answering them is what makes a membership feel opaque.
//
// Dates render in the site's display timezone (config.FormatSite), so a renewal
// date reads as the same day the member's own calendar shows.

import (
	"context"
	"html"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/members"
)

// billingRow is one label/value line in the card.
type billingRow struct {
	Label string
	Value string // pre-escaped or markup
	Note  string // optional muted hint under the value
}

// memberBillingRows derives what to show from the subscription. It is separated
// from rendering so the branching — trial, cancelling, renewing, lapsed — is
// testable without HTML.
func memberBillingRows(sub *members.Subscription, now time.Time) []billingRow {
	esc := html.EscapeString
	rows := []billingRow{}
	if sub == nil {
		return rows
	}

	// Status, in the member's terms rather than the billing system's.
	status := "Active"
	note := ""
	switch {
	case sub.CancelAtPeriodEnd && sub.CurrentPeriodEnd != nil:
		status = "Ending"
		note = "Cancelled — you keep access until the date below."
	case sub.CancelAtPeriodEnd:
		status = "Ending"
		note = "Cancelled — access continues to the end of the current period."
	case sub.TrialEnd != nil && sub.TrialEnd.After(now):
		status = "Trial"
		note = "Your trial converts automatically unless you cancel first."
	case sub.Status != "" && !strings.EqualFold(sub.Status, "active"):
		status = strings.ToUpper(sub.Status[:1]) + sub.Status[1:]
	}
	rows = append(rows, billingRow{Label: "Status", Value: esc(status), Note: note})

	// What it costs, and how often.
	if sub.Cadence == members.CadenceComplimentary {
		rows = append(rows, billingRow{Label: "Price", Value: "Complimentary"})
	} else if sub.AmountCents > 0 {
		rows = append(rows, billingRow{
			Label: "Price",
			Value: esc(sub.Currency) + " " + esc(formatMoney(sub.AmountCents)) + " / " + esc(sub.Cadence),
		})
	}

	// The question people actually came for.
	if sub.TrialEnd != nil && sub.TrialEnd.After(now) {
		rows = append(rows, billingRow{
			Label: "Trial ends",
			Value: esc(config.FormatSite(*sub.TrialEnd, "2 January 2006")),
		})
	}
	if sub.CurrentPeriodEnd != nil {
		label := "Next payment"
		hint := ""
		if sub.CancelAtPeriodEnd {
			// Calling a final date "next payment" would be actively misleading.
			label = "Access until"
			hint = "No further payments will be taken."
		} else if sub.Cadence == members.CadenceComplimentary {
			label = "Renews"
			hint = "No payment is taken for a complimentary plan."
		}
		rows = append(rows, billingRow{
			Label: label,
			Value: esc(config.FormatSite(*sub.CurrentPeriodEnd, "2 January 2006")),
			Note:  hint,
		})
	}
	if !sub.StartedAt.IsZero() {
		rows = append(rows, billingRow{
			Label: "Member since",
			Value: esc(config.FormatSite(sub.StartedAt, "2 January 2006")),
		})
	}
	return rows
}

// memberBillingCard renders the card, or "" when there is no subscription to
// describe (a free member has nothing to bill, and an empty card is clutter).
func (a *App) memberBillingCard(ctx context.Context, m *members.Member) string {
	if a.members == nil || m == nil || !m.IsPaid() {
		return ""
	}
	sub, _ := a.members.ActiveSubscription(ctx, m.ID)
	rows := memberBillingRows(sub, time.Now())
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<section class="ma-card">
    <h2>Membership &amp; billing</h2>
    <dl class="ma-bill">`)
	for _, r := range rows {
		b.WriteString(`<div class="ma-bill-row"><dt>` + html.EscapeString(r.Label) + `</dt><dd>` + r.Value)
		if r.Note != "" {
			b.WriteString(`<span class="ma-bill-note">` + html.EscapeString(r.Note) + `</span>`)
		}
		b.WriteString(`</dd></div>`)
	}
	b.WriteString(`</dl>`)

	// Recent membership activity — upgrades, renewals, cancellations. Best-effort:
	// a member who has never had an event simply sees no list.
	if evs, err := a.members.EventsForMember(ctx, m.ID, 5); err == nil && len(evs) > 0 {
		b.WriteString(`<div class="ma-bill-hist"><div class="ma-bill-hist-h">Recent activity</div><ul>`)
		for _, e := range evs {
			when := config.FormatSite(e.CreatedAt, "2 Jan 2006")
			b.WriteString(`<li><span class="ma-bill-when">` + html.EscapeString(when) + `</span> ` +
				html.EscapeString(memberEventLabel(e)) + `</li>`)
		}
		b.WriteString(`</ul></div>`)
	}
	b.WriteString(`
  </section>`)
	return b.String()
}

// memberEventLabel turns a stored event into something a reader understands,
// falling back to the raw type so a new event kind still shows rather than
// silently vanishing.
func memberEventLabel(e members.Event) string {
	switch e.Type {
	case "signup":
		return "Joined"
	case "upgrade":
		if e.Detail != "" {
			return "Upgraded — " + e.Detail
		}
		return "Upgraded"
	case "downgrade":
		if e.Detail != "" {
			return "Plan changed — " + e.Detail
		}
		return "Plan changed"
	case "cancel", "canceled", "cancelled":
		return "Cancelled"
	case "renew", "renewal":
		return "Renewed"
	case "payment":
		return "Payment received"
	default:
		if e.Type == "" {
			return "Account updated"
		}
		return strings.ToUpper(e.Type[:1]) + e.Type[1:]
	}
}
