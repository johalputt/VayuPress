package payments

// stripe.go — a minimal, dependency-free Stripe REST client.
//
// VayuPress never embeds the Stripe SDK or browser-side Stripe.js (ADR-0090).
// Instead the operator's own SECRET key drives a few server-to-server REST
// calls, and the reader is sent to a Stripe-HOSTED Checkout page by a top-level
// redirect. A redirect is an ordinary navigation — unrestricted by the strict
// CSP — so nothing about script-src/connect-src/frame-src has to be relaxed.
//
// Flow: CreateCheckoutSession opens a subscription session tagged with the
// VayuPress order reference (client_reference_id + metadata). On return the
// success handler calls GetCheckoutSession to CONFIRM payment server-side before
// fulfilling — the browser is never trusted to assert that money moved.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const stripeAPIBase = "https://api.stripe.com"

// StripeClient issues Stripe REST calls with the operator's secret key.
type StripeClient struct {
	http   *http.Client
	secret string
	base   string // API base; defaults to stripeAPIBase, overridable in tests
}

// NewStripeClient binds a client to the operator's secret key (sk_live_… /
// sk_test_…). A nil http.Client falls back to http.DefaultClient.
func NewStripeClient(hc *http.Client, secretKey string) *StripeClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &StripeClient{http: hc, secret: strings.TrimSpace(secretKey), base: stripeAPIBase}
}

// Configured reports whether a secret key is present.
func (c *StripeClient) Configured() bool { return c != nil && c.secret != "" }

func (c *StripeClient) do(ctx context.Context, method, path string, form url.Values) ([]byte, int, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	base := c.base
	if base == "" {
		base = stripeAPIBase
	}
	// The host is the fixed Stripe API base and every path segment is either a
	// literal or url.PathEscape'd, so there is no user-controlled-host SSRF vector.
	req, err := http.NewRequestWithContext(ctx, method, base+path, body) //nolint:noctx // ctx is passed
	if err != nil {
		return nil, 0, err
	}
	// Stripe authenticates with the secret key as the basic-auth username.
	req.SetBasicAuth(c.secret, "")
	req.Header.Set("Stripe-Version", "2024-06-20")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data, resp.StatusCode, nil
}

// Ping verifies the secret key by fetching the connected account. A nil return
// means the key is valid and live.
func (c *StripeClient) Ping(ctx context.Context) error {
	if !c.Configured() {
		return fmt.Errorf("stripe: no secret key configured")
	}
	data, code, err := c.do(ctx, http.MethodGet, "/v1/account", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("stripe: %s", stripeErrMsg(data))
	}
	return nil
}

// CheckoutParams describes a subscription Checkout Session to create.
type CheckoutParams struct {
	PriceID           string // optional Stripe Price id; empty → inline price_data
	AmountCents       int
	Currency          string
	Interval          string // "month" | "year"
	ProductName       string
	CustomerEmail     string
	ClientReferenceID string // the VayuPress order reference
	SuccessURL        string
	CancelURL         string
	Metadata          map[string]string
	TrialDays         int
}

// CreateCheckoutSession creates a subscription Checkout Session and returns its
// hosted URL and id. When PriceID is set it is used directly; otherwise a
// recurring price_data is built inline from amount/currency/interval so the
// operator needs no pre-created Stripe Price (the "just paste your key" path).
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (checkoutURL, sessionID string, err error) {
	if !c.Configured() {
		return "", "", fmt.Errorf("stripe: no secret key configured")
	}
	f := url.Values{}
	f.Set("mode", "subscription")
	f.Set("success_url", p.SuccessURL)
	f.Set("cancel_url", p.CancelURL)
	if p.CustomerEmail != "" {
		f.Set("customer_email", p.CustomerEmail)
	}
	if p.ClientReferenceID != "" {
		f.Set("client_reference_id", p.ClientReferenceID)
	}
	if strings.TrimSpace(p.PriceID) != "" {
		f.Set("line_items[0][price]", strings.TrimSpace(p.PriceID))
	} else {
		cur := strings.ToLower(strings.TrimSpace(p.Currency))
		if cur == "" {
			cur = "usd"
		}
		interval := "month"
		if p.Interval == "year" {
			interval = "year"
		}
		name := strings.TrimSpace(p.ProductName)
		if name == "" {
			name = "Membership"
		}
		f.Set("line_items[0][price_data][currency]", cur)
		f.Set("line_items[0][price_data][unit_amount]", strconv.Itoa(p.AmountCents))
		f.Set("line_items[0][price_data][recurring][interval]", interval)
		f.Set("line_items[0][price_data][product_data][name]", name)
	}
	f.Set("line_items[0][quantity]", "1")
	if p.TrialDays > 0 {
		f.Set("subscription_data[trial_period_days]", strconv.Itoa(p.TrialDays))
	}
	for k, v := range p.Metadata {
		f.Set("metadata["+k+"]", v)
		// Carry the same tags onto the subscription so later invoice/subscription
		// webhooks can be correlated back to the VayuPress order too.
		f.Set("subscription_data[metadata]["+k+"]", v)
	}
	data, code, err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", f)
	if err != nil {
		return "", "", err
	}
	if code != http.StatusOK {
		return "", "", fmt.Errorf("stripe: %s", stripeErrMsg(data))
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		return "", "", jerr
	}
	if out.URL == "" {
		return "", "", fmt.Errorf("stripe: checkout session returned no url")
	}
	return out.URL, out.ID, nil
}

// CheckoutSession is the subset of a retrieved Checkout Session VayuPress acts on.
type CheckoutSession struct {
	ID                string
	PaymentStatus     string // "paid" | "unpaid" | "no_payment_required"
	Status            string // "complete" | "open" | "expired"
	ClientReferenceID string
	CustomerEmail     string
	CustomerID        string
	SubscriptionID    string
}

// Paid reports whether the session's payment has settled.
func (s *CheckoutSession) Paid() bool {
	return s != nil && (s.PaymentStatus == "paid" || s.PaymentStatus == "no_payment_required")
}

// GetCheckoutSession retrieves a session so the success redirect can CONFIRM
// payment against Stripe directly — the browser's query string is never trusted.
func (c *StripeClient) GetCheckoutSession(ctx context.Context, id string) (*CheckoutSession, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("stripe: no secret key configured")
	}
	if !ValidStripeSessionID(id) {
		return nil, fmt.Errorf("stripe: invalid session id")
	}
	data, code, err := c.do(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("stripe: %s", stripeErrMsg(data))
	}
	var raw struct {
		ID                string `json:"id"`
		PaymentStatus     string `json:"payment_status"`
		Status            string `json:"status"`
		ClientReferenceID string `json:"client_reference_id"`
		CustomerEmail     string `json:"customer_email"`
		CustomerDetails   struct {
			Email string `json:"email"`
		} `json:"customer_details"`
		Customer     string `json:"customer"`
		Subscription string `json:"subscription"`
	}
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		return nil, jerr
	}
	em := raw.CustomerEmail
	if em == "" {
		em = raw.CustomerDetails.Email
	}
	return &CheckoutSession{
		ID: raw.ID, PaymentStatus: raw.PaymentStatus, Status: raw.Status,
		ClientReferenceID: raw.ClientReferenceID, CustomerEmail: em,
		CustomerID: raw.Customer, SubscriptionID: raw.Subscription,
	}, nil
}

// ValidStripeSessionID bounds the session id to Stripe's "cs_"-prefixed token
// charset before it is ever used in a request path.
func ValidStripeSessionID(id string) bool {
	if !strings.HasPrefix(id, "cs_") || len(id) < 10 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// stripeErrMsg extracts a human-readable message from a Stripe error body.
func stripeErrMsg(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "request failed"
	}
	return s
}
