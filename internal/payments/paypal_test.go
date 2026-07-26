// SPDX-License-Identifier: Apache-2.0

package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidPayPalSubscriptionID(t *testing.T) {
	if !ValidPayPalSubscriptionID("I-BW452GLLEP1G") {
		t.Error("expected valid PayPal sub id")
	}
	for _, bad := range []string{"", "I", "I-bad/char", "I bad", strings.Repeat("I", 65)} {
		if ValidPayPalSubscriptionID(bad) {
			t.Errorf("expected invalid: %q", bad)
		}
	}
}

func TestMajorAmount(t *testing.T) {
	for cents, want := range map[int]string{100: "1.00", 250: "2.50", 0: "0.00", 999: "9.99"} {
		if got := majorAmount(cents); got != want {
			t.Errorf("majorAmount(%d)=%q want %q", cents, got, want)
		}
	}
}

func TestPayPalErrMsg(t *testing.T) {
	if got := payPalErrMsg([]byte(`{"details":[{"description":"Plan not found"}]}`)); got != "Plan not found" {
		t.Errorf("details = %q", got)
	}
	if got := payPalErrMsg([]byte(`{"message":"bad"}`)); got != "bad" {
		t.Errorf("message = %q", got)
	}
	if got := payPalErrMsg(nil); got != "request failed" {
		t.Errorf("empty = %q", got)
	}
}

// newPayPalStub routes the PayPal REST endpoints used by the checkout sequence.
func newPayPalStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			if u, p, _ := r.BasicAuth(); u == "" || p == "" {
				t.Error("token request missing basic auth")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "A-TOKEN", "expires_in": 3600})
		case r.URL.Path == "/v1/catalogs/products":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "PROD-1"})
		case r.URL.Path == "/v1/billing/plans":
			if got := r.Header.Get("Authorization"); got != "Bearer A-TOKEN" {
				t.Errorf("plan auth = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "P-PLAN1", "status": "ACTIVE"})
		case r.URL.Path == "/v1/billing/subscriptions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "I-SUB1", "status": "APPROVAL_PENDING",
				"links": []map[string]string{
					{"rel": "self", "href": "https://api/x"},
					{"rel": "approve", "href": "https://www.paypal.com/approve/I-SUB1"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/billing/subscriptions/"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "I-SUB1", "status": "ACTIVE", "custom_id": "VP-7",
				"subscriber": map[string]string{"email_address": "r@x.com"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestPayPalCheckoutSequence(t *testing.T) {
	srv := newPayPalStub(t)
	defer srv.Close()
	c := NewPayPalClient(srv.Client(), "cid", "sec", true)
	c.base = srv.URL
	ctx := context.Background()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	prod, err := c.EnsureProduct(ctx, "My Site Membership")
	if err != nil || prod != "PROD-1" {
		t.Fatalf("product: %q %v", prod, err)
	}
	plan, err := c.CreatePlan(ctx, prod, "Starter monthly", 100, "USD", "month")
	if err != nil || plan != "P-PLAN1" {
		t.Fatalf("plan: %q %v", plan, err)
	}
	sub, approve, err := c.CreateSubscription(ctx, plan, "r@x.com", "VP-7", "https://x/ret", "https://x/cancel", "My Site")
	if err != nil || sub != "I-SUB1" {
		t.Fatalf("subscription: %q %v", sub, err)
	}
	if approve != "https://www.paypal.com/approve/I-SUB1" {
		t.Errorf("approval url = %q", approve)
	}
}

func TestPayPalGetSubscriptionActive(t *testing.T) {
	srv := newPayPalStub(t)
	defer srv.Close()
	c := NewPayPalClient(srv.Client(), "cid", "sec", true)
	c.base = srv.URL
	s, err := c.GetSubscription(context.Background(), "I-SUB1")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Active() || s.CustomID != "VP-7" || s.Email != "r@x.com" {
		t.Errorf("bad subscription: %+v", s)
	}
	if _, err := c.GetSubscription(context.Background(), "bad/id"); err == nil {
		t.Error("expected invalid id error")
	}
}
