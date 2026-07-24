package payments

// paypal.go — a minimal, dependency-free PayPal REST client for auto-renewing
// subscriptions, plus a small cache for the billing plans PayPal requires.
//
// Like the Stripe client, no PayPal SDK or browser JS is used: the operator's
// REST credentials drive server-to-server calls, and the reader is sent to a
// PayPal-hosted approval page by a top-level redirect (CSP untouched, ADR-0090).
//
// PayPal, unlike Stripe, cannot price a subscription inline — it needs a
// pre-created catalog product and a billing plan. VayuPress creates those lazily
// on first checkout for a (tier, cadence, price) and caches the ids in the
// paypal_plans table (migration 069), so a price change transparently yields a
// new plan and stale prices can never be charged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	payPalLiveBase    = "https://api-m.paypal.com"
	payPalSandboxBase = "https://api-m.sandbox.paypal.com"
	// planProductFingerprint is the reserved cache key for the shared catalog
	// product id (one product backs every plan).
	planProductFingerprint = "__product__"
)

// PayPalClient issues PayPal REST calls with the operator's client credentials.
type PayPalClient struct {
	http     *http.Client
	clientID string
	secret   string
	base     string

	token    string
	tokenExp time.Time
}

// NewPayPalClient binds a client to the operator's REST credentials. sandbox
// selects the PayPal sandbox host (for test credentials).
func NewPayPalClient(hc *http.Client, clientID, secret string, sandbox bool) *PayPalClient {
	if hc == nil {
		hc = guardedDefaultClient()
	}
	base := payPalLiveBase
	if sandbox {
		base = payPalSandboxBase
	}
	return &PayPalClient{http: hc, clientID: strings.TrimSpace(clientID), secret: strings.TrimSpace(secret), base: base}
}

// Configured reports whether both credentials are present.
func (c *PayPalClient) Configured() bool {
	return c != nil && c.clientID != "" && c.secret != ""
}

// accessToken fetches (and caches for the request's lifetime) an OAuth2
// client-credentials access token.
func (c *PayPalClient) accessToken(ctx context.Context) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("paypal: credentials not configured")
	}
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.clientID, c.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("paypal: auth failed: %s", payPalErrMsg(data))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		return "", jerr
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("paypal: empty access token")
	}
	c.token = out.AccessToken
	ttl := out.ExpiresIn
	if ttl <= 0 {
		ttl = 300
	}
	c.tokenExp = time.Now().Add(time.Duration(ttl-30) * time.Second)
	return c.token, nil
}

// Ping verifies the credentials by acquiring an access token.
func (c *PayPalClient) Ping(ctx context.Context) error {
	_, err := c.accessToken(ctx)
	return err
}

// doJSON performs an authenticated JSON request and decodes the response into
// out (when non-nil). It returns the status code and raw body for error shaping.
func (c *PayPalClient) doJSON(ctx context.Context, method, path string, body interface{}, out interface{}) (int, []byte, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	var rdr io.Reader
	if body != nil {
		b, mErr := json.Marshal(body)
		if mErr != nil {
			return 0, nil, mErr
		}
		rdr = bytes.NewReader(b)
	}
	// Fixed PayPal API base; path is a package literal or an id we validate — no
	// user-controlled host.
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil && len(data) > 0 && resp.StatusCode < 300 {
		if jerr := json.Unmarshal(data, out); jerr != nil {
			return resp.StatusCode, data, jerr
		}
	}
	return resp.StatusCode, data, nil
}

// EnsureProduct creates (once) a catalog product to back the billing plans.
func (c *PayPalClient) EnsureProduct(ctx context.Context, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		name = "Membership"
	}
	var out struct {
		ID string `json:"id"`
	}
	code, data, err := c.doJSON(ctx, http.MethodPost, "/v1/catalogs/products", map[string]string{
		"name": name, "type": "SERVICE", "category": "SOFTWARE",
	}, &out)
	if err != nil {
		return "", err
	}
	if code >= 300 || out.ID == "" {
		return "", fmt.Errorf("paypal: create product: %s", payPalErrMsg(data))
	}
	return out.ID, nil
}

// CreatePlan creates an ACTIVE billing plan for a product at the given price and
// interval ("month"|"year") and returns its id.
func (c *PayPalClient) CreatePlan(ctx context.Context, productID, name string, amountCents int, currency, interval string) (string, error) {
	unit := "MONTH"
	if interval == "year" {
		unit = "YEAR"
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if cur == "" {
		cur = "USD"
	}
	plan := map[string]interface{}{
		"product_id": productID,
		"name":       name,
		"status":     "ACTIVE",
		"billing_cycles": []map[string]interface{}{{
			"frequency":    map[string]interface{}{"interval_unit": unit, "interval_count": 1},
			"tenure_type":  "REGULAR",
			"sequence":     1,
			"total_cycles": 0, // 0 = renew forever
			"pricing_scheme": map[string]interface{}{
				"fixed_price": map[string]string{"value": majorAmount(amountCents), "currency_code": cur},
			},
		}},
		"payment_preferences": map[string]interface{}{
			"auto_bill_outstanding":     true,
			"setup_fee_failure_action":  "CONTINUE",
			"payment_failure_threshold": 1,
		},
	}
	var out struct {
		ID string `json:"id"`
	}
	code, data, err := c.doJSON(ctx, http.MethodPost, "/v1/billing/plans", plan, &out)
	if err != nil {
		return "", err
	}
	if code >= 300 || out.ID == "" {
		return "", fmt.Errorf("paypal: create plan: %s", payPalErrMsg(data))
	}
	return out.ID, nil
}

// CreateSubscription starts a subscription against a plan and returns its id and
// the approval URL the reader must be redirected to. custom_id carries the
// VayuPress order reference back on the webhook/return.
func (c *PayPalClient) CreateSubscription(ctx context.Context, planID, email, customID, returnURL, cancelURL, brandName string) (subID, approveURL string, err error) {
	sub := map[string]interface{}{
		"plan_id":   planID,
		"custom_id": customID,
		"subscriber": map[string]interface{}{
			"email_address": email,
		},
		"application_context": map[string]interface{}{
			"brand_name":          brandName,
			"user_action":         "SUBSCRIBE_NOW",
			"shipping_preference": "NO_SHIPPING",
			"return_url":          returnURL,
			"cancel_url":          cancelURL,
		},
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Links  []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
	}
	code, data, err := c.doJSON(ctx, http.MethodPost, "/v1/billing/subscriptions", sub, &out)
	if err != nil {
		return "", "", err
	}
	if code >= 300 || out.ID == "" {
		return "", "", fmt.Errorf("paypal: create subscription: %s", payPalErrMsg(data))
	}
	for _, l := range out.Links {
		if strings.EqualFold(l.Rel, "approve") {
			approveURL = l.Href
			break
		}
	}
	if approveURL == "" {
		return "", "", fmt.Errorf("paypal: subscription has no approval link")
	}
	return out.ID, approveURL, nil
}

// PayPalSubscription is the subset of a subscription VayuPress acts on.
type PayPalSubscription struct {
	ID       string
	Status   string // APPROVAL_PENDING | APPROVED | ACTIVE | SUSPENDED | CANCELLED | EXPIRED
	CustomID string
	Email    string
}

// Active reports whether the subscription is live (payment approved/collecting).
func (s *PayPalSubscription) Active() bool {
	return s != nil && (s.Status == "ACTIVE" || s.Status == "APPROVED")
}

// GetSubscription retrieves a subscription so the return handler can confirm it
// server-side before fulfilling.
func (c *PayPalClient) GetSubscription(ctx context.Context, id string) (*PayPalSubscription, error) {
	if !ValidPayPalSubscriptionID(id) {
		return nil, fmt.Errorf("paypal: invalid subscription id")
	}
	var raw struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		CustomID   string `json:"custom_id"`
		Subscriber struct {
			EmailAddress string `json:"email_address"`
		} `json:"subscriber"`
	}
	code, data, err := c.doJSON(ctx, http.MethodGet, "/v1/billing/subscriptions/"+url.PathEscape(id), nil, &raw)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("paypal: get subscription: %s", payPalErrMsg(data))
	}
	return &PayPalSubscription{ID: raw.ID, Status: raw.Status, CustomID: raw.CustomID, Email: raw.Subscriber.EmailAddress}, nil
}

// ValidPayPalSubscriptionID bounds a subscription id (PayPal ids are like
// "I-BW452GLLEP1G") before it is used in a request path.
func ValidPayPalSubscriptionID(id string) bool {
	if len(id) < 3 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func majorAmount(cents int) string { return fmt.Sprintf("%.2f", float64(cents)/100) }

func payPalErrMsg(data []byte) string {
	var e struct {
		Message string `json:"message"`
		Name    string `json:"name"`
		Details []struct {
			Description string `json:"description"`
			Issue       string `json:"issue"`
		} `json:"details"`
	}
	if json.Unmarshal(data, &e) == nil {
		if len(e.Details) > 0 && e.Details[0].Description != "" {
			return e.Details[0].Description
		}
		if e.Message != "" {
			return e.Message
		}
		if e.Name != "" {
			return e.Name
		}
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

// ── PayPal plan cache (paypal_plans table, migration 069) ─────────────────────

// PayPalPlanID returns the cached plan id for a fingerprint, or ok=false.
func (s *Store) PayPalPlanID(ctx context.Context, fingerprint string) (string, bool) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT plan_id FROM paypal_plans WHERE fingerprint=?`, fingerprint).Scan(&id)
	if err != nil || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

// SavePayPalPlan caches a plan id under a fingerprint (idempotent).
func (s *Store) SavePayPalPlan(ctx context.Context, fingerprint, planID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO paypal_plans(fingerprint,plan_id) VALUES(?,?)
		 ON CONFLICT(fingerprint) DO UPDATE SET plan_id=excluded.plan_id`,
		fingerprint, planID)
	return err
}

// PayPalProductID returns the cached shared catalog product id, or ok=false.
func (s *Store) PayPalProductID(ctx context.Context) (string, bool) {
	return s.PayPalPlanID(ctx, planProductFingerprint)
}

// SavePayPalProduct caches the shared catalog product id.
func (s *Store) SavePayPalProduct(ctx context.Context, productID string) error {
	return s.SavePayPalPlan(ctx, planProductFingerprint, productID)
}
