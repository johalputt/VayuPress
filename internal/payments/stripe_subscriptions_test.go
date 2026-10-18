// SPDX-License-Identifier: Apache-2.0

package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeStripe answers the subscription list for cus_member with one subscription
// of each status, and records every change made to one.
func fakeStripe(t *testing.T) (*StripeClient, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions" {
			if r.URL.Query().Get("customer") != "cus_member" {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"sub_active","status":"active"},{"id":"sub_trial","status":"trialing"},` +
				`{"id":"sub_incomplete","status":"incomplete"},{"id":"sub_over","status":"canceled"}]}`))
			return
		}
		_ = r.ParseForm()
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.PostForm.Encode())
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return &StripeClient{http: srv.Client(), secret: "sk_test_x", base: srv.URL}, &calls
}

// Cancelling a renewal changes exactly the subscriptions that would charge the
// member again: an incomplete one never charged, a cancelled one is over.
func TestCancelAtPeriodEndTouchesOnlyWhatWouldRenew(t *testing.T) {
	c, calls := fakeStripe(t)
	n, err := c.SetCancelAtPeriodEnd(context.Background(), "cus_member", true)
	if err != nil || n != 2 {
		t.Fatalf("SetCancelAtPeriodEnd = %d, %v; want 2 changed", n, err)
	}
	want := []string{
		"POST /v1/subscriptions/sub_active cancel_at_period_end=true",
		"POST /v1/subscriptions/sub_trial cancel_at_period_end=true",
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(*calls, "\n"), strings.Join(want, "\n"))
	}

	*calls = nil
	if _, err := c.SetCancelAtPeriodEnd(context.Background(), "cus_member", false); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.HasSuffix((*calls)[0], "cancel_at_period_end=false") {
		t.Errorf("keeping the plan did not restore the renewal: %v", *calls)
	}
}

// Deleting an account ends the live subscriptions at once, not at period end.
func TestCancelSubscriptionsNowDeletesTheLiveOnes(t *testing.T) {
	c, calls := fakeStripe(t)
	if err := c.CancelSubscriptionsNow(context.Background(), "cus_member"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, "|"); got != "DELETE /v1/subscriptions/sub_active |DELETE /v1/subscriptions/sub_trial " {
		t.Errorf("calls = %q", got)
	}
}

// A customer id from the database goes into a request URL, so anything that is
// not one is refused before a request is made.
func TestAStripeCustomerIDIsCheckedBeforeUse(t *testing.T) {
	c, calls := fakeStripe(t)
	for _, bad := range []string{"", "cus_", "sub_member", "cus_a/../b", "cus_a?x=1", "cus_a b"} {
		if _, err := c.SetCancelAtPeriodEnd(context.Background(), bad, true); err == nil {
			t.Errorf("customer %q was accepted", bad)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a refused id still reached Stripe: %v", *calls)
	}
}

// Stripe's refusal is the member's answer, not a silent success.
func TestAStripeRefusalIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"sub_refused","status":"active"}]}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"No such subscription"}}`))
	}))
	defer srv.Close()
	c := &StripeClient{http: srv.Client(), secret: "sk_test_x", base: srv.URL}
	if _, err := c.SetCancelAtPeriodEnd(context.Background(), "cus_member", true); err == nil || !strings.Contains(err.Error(), "No such subscription") {
		t.Errorf("err = %v, want Stripe's message", err)
	}
	if err := c.CancelSubscriptionsNow(context.Background(), "cus_member"); err == nil {
		t.Error("a refused cancellation reported success")
	}
}
