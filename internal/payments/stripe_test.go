package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidStripeSessionID(t *testing.T) {
	ok := []string{"cs_test_a1B2c3", "cs_live_" + strings.Repeat("x", 40)}
	bad := []string{"", "pi_123", "cs_", "cs_" + strings.Repeat("x", 200), "cs_bad-char", "cs_a b"}
	for _, s := range ok {
		if !ValidStripeSessionID(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	for _, s := range bad {
		if ValidStripeSessionID(s) {
			t.Errorf("expected invalid: %q", s)
		}
	}
}

func TestStripeErrMsg(t *testing.T) {
	if got := stripeErrMsg([]byte(`{"error":{"message":"No such price"}}`)); got != "No such price" {
		t.Errorf("stripeErrMsg = %q", got)
	}
	if got := stripeErrMsg([]byte(`not json`)); got != "not json" {
		t.Errorf("fallback = %q", got)
	}
	if got := stripeErrMsg(nil); got != "request failed" {
		t.Errorf("empty = %q", got)
	}
}

// TestCreateCheckoutSession verifies the form encoding (inline price_data, order
// reference, subscription mode) and response parsing against a stub Stripe API.
func TestCreateCheckoutSession(t *testing.T) {
	var gotForm url.Values
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		gotForm = r.PostForm
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "cs_test_123", "url": "https://checkout.stripe.com/c/pay/cs_test_123"})
	}))
	defer srv.Close()

	c := NewStripeClient(srv.Client(), "sk_test_secret")
	c.base = srv.URL
	url, id, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		AmountCents: 100, Currency: "USD", Interval: "month", ProductName: "1 GB Mailbox",
		CustomerEmail: "reader@example.com", ClientReferenceID: "VP-ORDER-9",
		SuccessURL: "https://x/ok", CancelURL: "https://x/no",
		Metadata: map[string]string{"reference": "VP-ORDER-9", "tier": "starter"},
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if id != "cs_test_123" || !strings.Contains(url, "checkout.stripe.com") {
		t.Fatalf("unexpected result: id=%q url=%q", id, url)
	}
	if gotAuth == "" || !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("expected basic auth with secret key, got %q", gotAuth)
	}
	want := map[string]string{
		"mode":                                   "subscription",
		"success_url":                            "https://x/ok",
		"cancel_url":                             "https://x/no",
		"customer_email":                         "reader@example.com",
		"client_reference_id":                    "VP-ORDER-9",
		"line_items[0][price_data][currency]":    "usd",
		"line_items[0][price_data][unit_amount]": "100",
		"line_items[0][price_data][recurring][interval]": "month",
		"line_items[0][price_data][product_data][name]":  "1 GB Mailbox",
		"line_items[0][quantity]":                        "1",
		"metadata[reference]":                            "VP-ORDER-9",
		"metadata[tier]":                                 "starter",
		"subscription_data[metadata][reference]":         "VP-ORDER-9",
	}
	for k, v := range want {
		if got := gotForm.Get(k); got != v {
			t.Errorf("form[%q] = %q, want %q", k, got, v)
		}
	}
}

// TestCreateCheckoutSessionWithPriceID prefers a pre-created Stripe Price id and
// then omits inline price_data.
func TestCreateCheckoutSessionWithPriceID(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "cs_x", "url": "https://checkout.stripe.com/x"})
	}))
	defer srv.Close()
	c := NewStripeClient(srv.Client(), "sk_test_secret")
	c.base = srv.URL
	if _, _, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		PriceID: "price_123", SuccessURL: "https://x/ok", CancelURL: "https://x/no",
	}); err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("line_items[0][price]") != "price_123" {
		t.Errorf("expected price id, got %q", gotForm.Get("line_items[0][price]"))
	}
	if gotForm.Get("line_items[0][price_data][currency]") != "" {
		t.Error("inline price_data must be omitted when a Price id is set")
	}
}

// TestGetCheckoutSessionPaid parses a settled session and reports Paid().
func TestGetCheckoutSessionPaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/cs_test_paid") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "cs_test_paid", "payment_status": "paid", "status": "complete",
			"client_reference_id": "VP-9", "customer": "cus_1", "subscription": "sub_1",
			"customer_details": map[string]string{"email": "r@x.com"},
		})
	}))
	defer srv.Close()
	c := NewStripeClient(srv.Client(), "sk_test_secret")
	c.base = srv.URL
	sess, err := c.GetCheckoutSession(context.Background(), "cs_test_paid")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Paid() || sess.ClientReferenceID != "VP-9" || sess.CustomerID != "cus_1" || sess.CustomerEmail != "r@x.com" {
		t.Errorf("bad session parse: %+v", sess)
	}
	// An invalid id never leaves the client.
	if _, err := c.GetCheckoutSession(context.Background(), "pi_bad"); err == nil {
		t.Error("expected invalid-session-id error")
	}
}

func TestStripeErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer srv.Close()
	c := NewStripeClient(srv.Client(), "sk_bad")
	c.base = srv.URL
	if err := c.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Errorf("expected Invalid API Key error, got %v", err)
	}
}
