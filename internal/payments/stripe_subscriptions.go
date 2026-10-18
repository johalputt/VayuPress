// SPDX-License-Identifier: Apache-2.0

package payments

// stripe_subscriptions.go — a member's own control over their Stripe billing:
// stop the renewal at the end of the period (or take that back), and stop it
// at once when they delete their account.
//
// Subscriptions are found from the Stripe customer the checkout linked to the
// member (members.stripe_customer), not from a stored subscription id: checkout
// fulfilment has never recorded one, and the customer's own list is what Stripe
// will actually charge, including a subscription started outside VayuPress.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ValidStripeCustomerID bounds a customer id to Stripe's "cus_" token charset
// before it is ever used in a request.
func ValidStripeCustomerID(id string) bool {
	if !strings.HasPrefix(id, "cus_") || len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// liveSubscriptions returns the ids of the customer's subscriptions that can
// still charge them. Stripe's default list leaves out cancelled ones but keeps
// incomplete ones, which have never charged and are not this member's plan.
func (c *StripeClient) liveSubscriptions(ctx context.Context, customer string) ([]string, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("stripe: no secret key configured")
	}
	if !ValidStripeCustomerID(customer) {
		return nil, fmt.Errorf("stripe: invalid customer id")
	}
	data, code, err := c.do(ctx, http.MethodGet, "/v1/subscriptions?limit=100&customer="+url.QueryEscape(customer), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("stripe: %s", stripeErrMsg(data))
	}
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range list.Data {
		switch s.Status {
		case "active", "trialing", "past_due", "unpaid":
			if s.ID != "" {
				ids = append(ids, s.ID)
			}
		}
	}
	return ids, nil
}

// SetCancelAtPeriodEnd stops (cancel=true) or restores (cancel=false) the
// renewal of every live subscription the customer has. The member keeps what
// they paid for until the period ends either way. It reports how many
// subscriptions it changed; zero means Stripe holds nothing that renews.
func (c *StripeClient) SetCancelAtPeriodEnd(ctx context.Context, customer string, cancel bool) (int, error) {
	ids, err := c.liveSubscriptions(ctx, customer)
	if err != nil {
		return 0, err
	}
	form := url.Values{"cancel_at_period_end": {fmt.Sprint(cancel)}}
	for i, id := range ids {
		data, code, err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id), form)
		if err != nil {
			return i, err
		}
		if code != http.StatusOK {
			return i, fmt.Errorf("stripe: %s", stripeErrMsg(data))
		}
	}
	return len(ids), nil
}

// CancelSubscriptionsNow ends every live subscription the customer has at
// once, so a member who deletes their account is never charged again for an
// account that no longer exists.
func (c *StripeClient) CancelSubscriptionsNow(ctx context.Context, customer string) error {
	ids, err := c.liveSubscriptions(ctx, customer)
	if err != nil {
		return err
	}
	for _, id := range ids {
		data, code, err := c.do(ctx, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(id), nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("stripe: %s", stripeErrMsg(data))
		}
	}
	return nil
}
