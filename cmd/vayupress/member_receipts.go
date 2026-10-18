// SPDX-License-Identifier: Apache-2.0

package main

// member_receipts.go — a member's own payment history on /members/account.
//
// Before this, a member could see their subscription STATE (plan, renewal date)
// and a short list of membership EVENTS, but nothing about the individual
// payments behind them. The only record of a payment was the confirmation email
// sent once at the time — so a member who lost that email had no way to see what
// they had paid, when, or under what reference, and the only person who could
// answer was the operator reading the admin order ledger.
//
// The ledger has always had the data and the index for this query; only the
// query and this view were missing.

import (
	"context"
	"html"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
)

// maxMemberReceipts bounds the list. A member with more than this has a support
// conversation, not a UI problem.
const maxMemberReceipts = 24

// memberReceiptsSection renders the member's payment history, and returns the
// accordion summary line and chip alongside it. All three are empty when the
// member has never had an order, so the row is not rendered at all — an empty
// "Receipts" row invites a click that reveals nothing.
func (a *App) memberReceiptsSection(ctx context.Context, m *members.Member) (body string, n int, last string) {
	if a.payments == nil || a.members == nil || m == nil {
		return "", 0, ""
	}
	orders, err := a.payments.ListByEmail(ctx, dbpkg.Reader(), m.Email, maxMemberReceipts)
	if err != nil || len(orders) == 0 {
		// A failed lookup renders nothing rather than an error card: this is a
		// secondary panel on a page whose primary job (plan, mail, security) must
		// still work when the ledger is unavailable.
		return "", 0, ""
	}

	// Paid-post orders carry only a sentinel in tier_slug, so resolve which post
	// each one bought. Best-effort: a failure downgrades the line to the generic
	// product label rather than dropping the receipt.
	posts, _ := a.members.PurchasedPostByOrder(ctx, dbpkg.Reader(), m.Email, maxMemberReceipts)

	// Resolve tier slugs to their display names. orderProductLabel renders the raw
	// slug ("Membership: paid"), which is right for the admin ledger — an operator
	// thinks in slugs — but wrong on a receipt, where the reader only ever saw the
	// name they bought. Archived tiers are included: an old receipt may reference a
	// plan no longer on sale, and that receipt still has to read correctly.
	tierNames := map[string]string{}
	if ts, terr := a.members.ListTiers(ctx, true); terr == nil {
		for i := range ts {
			if ts[i].Slug != "" && ts[i].Name != "" {
				tierNames[ts[i].Slug] = ts[i].Name
			}
		}
	}

	pending := 0
	var b strings.Builder
	b.WriteString(`<ul class="acct-list">`)
	for i := range orders {
		o := orders[i]
		if o.Status == payments.StatusPending {
			pending++
		}

		// Date the payment, not the order, whenever the payment actually happened:
		// an offline order can sit pending for days and its created_at is not when
		// money moved.
		when := o.CreatedAt
		dateLabel := "Ordered"
		if o.PaidAt != nil && !o.PaidAt.IsZero() {
			when = *o.PaidAt
			dateLabel = "Paid"
		}

		// Everything here is escaped before it is concatenated: orderProductLabel
		// interpolates the raw tier slug, and this string is written into the page
		// as markup.
		what := html.EscapeString(orderProductLabel(o.TierSlug))
		if name, ok := tierNames[o.TierSlug]; ok {
			what = "Membership: " + html.EscapeString(name)
		}
		// Name the post for a per-post purchase.
		if p, ok := posts[strings.TrimSpace(o.Reference)]; ok {
			switch {
			case p.Title != "":
				what = html.EscapeString(p.Title)
			case p.Slug != "":
				what = html.EscapeString(p.Slug)
			}
		}
		if o.Cadence != "" && o.TierSlug != "" && !strings.HasPrefix(o.TierSlug, "__") {
			what += `, ` + html.EscapeString(o.Cadence)
		}

		amount := priceLabel(o.Currency, o.AmountCents)
		date := config.FormatSite(when, "2 January 2006")
		if last == "" && o.Status == payments.StatusPaid {
			last = "Last: " + date + ", " + amount
		}
		b.WriteString(`<li><div class="acct-list__top"><span class="acct-list__where">` + what + `</span>` +
			`<span class="acct-list__amt">` + html.EscapeString(amount) + `</span></div>` +
			`<span class="acct-list__meta">` + html.EscapeString(dateLabel+" "+config.FormatSite(when, "2 Jan 2006")) +
			` · <code>` + html.EscapeString(o.Reference) + `</code> ` + receiptStatusChip(o.Status) + `</span></li>`)
	}
	b.WriteString(`</ul><p class="acct-more__foot">Quote the reference if you ever need to ask about a payment.`)
	if pending > 0 {
		b.WriteString(` ` + countOf(pending, "payment") + ` still being confirmed; nothing more is needed from you unless we've asked.`)
	}
	b.WriteString(`</p>`)
	return b.String(), len(orders), last
}

// countOf renders "1 payment" / "3 payments". The existing plural() returns only
// the suffix, and these summary lines need the number with it.
func countOf(n int, noun string) string {
	return strconv.Itoa(n) + " " + noun + plural(n)
}

// receiptStatusChip states an order's status in the member's own terms.
//
// It does not reuse orderStatusPill: that emits console .status-pill classes,
// and the member page loads signup.css, which has none of them — the pill would
// render as unstyled text.
func receiptStatusChip(status string) string {
	label, ok := "", false
	switch status {
	case payments.StatusPaid:
		label, ok = "Paid", true
	case payments.StatusPending:
		// Short enough to survive a 380px-wide row without being clipped. The hint
		// line under the list carries the reassurance that a longer label tried to.
		label = "Pending"
	case payments.StatusRefunded:
		label = "Refunded"
	case payments.StatusCanceled:
		label = "Cancelled"
	case payments.StatusFailed:
		label = "Failed"
	default:
		// An unrecognised status shows itself rather than vanishing, so a new
		// status kind is visible as a gap to fix instead of silently blank.
		label = status
	}
	if label == "" {
		return ""
	}
	if ok {
		return acctState(label, "ok")
	}
	return acctState(label, "off")
}
