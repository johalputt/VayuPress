package payments

// btcpay.go — a minimal BTCPay Server (Greenfield API) client for the crypto
// gateway. BTCPay is self-hosted and open-source, so funds and keys stay with
// the operator; its hosted checkout handles coin selection (BTC/XMR/ETH/USDT…),
// QR codes and on-chain confirmation, and it confirms an order back to us with a
// signature-verified settlement webhook. No coin libraries, wallets or nodes run
// inside VayuPress — it only creates invoices and verifies webhooks.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// BTCPayClient talks to a BTCPay Server store over the Greenfield REST API.
type BTCPayClient struct {
	httpc   *http.Client
	baseURL string // scheme+host, no trailing slash
	storeID string
	apiKey  string
}

// NewBTCPayClient builds a client for one store. baseURL is the BTCPay server
// root (e.g. https://btcpay.example.com or a .onion); apiKey is a Greenfield API
// key with invoice create/view permission on the store.
func NewBTCPayClient(httpc *http.Client, baseURL, storeID, apiKey string) *BTCPayClient {
	return &BTCPayClient{
		httpc:   httpc,
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		storeID: strings.TrimSpace(storeID),
		apiKey:  strings.TrimSpace(apiKey),
	}
}

// BTCPayInvoice is the subset of a BTCPay invoice VayuPress needs.
type BTCPayInvoice struct {
	ID           string
	CheckoutLink string
	Status       string
	OrderRef     string // metadata.orderId — our order reference
}

// Settled reports whether the invoice has been paid and confirmed on-chain, so
// the order can be fulfilled. BTCPay's terminal "paid" states are Settled (and
// the legacy Complete/Confirmed); New/Processing/Expired/Invalid are not.
func (inv *BTCPayInvoice) Settled() bool {
	switch strings.ToLower(strings.TrimSpace(inv.Status)) {
	case "settled", "complete", "confirmed":
		return true
	}
	return false
}

// CreateInvoice opens a BTCPay invoice for amountMajor (a major-unit string like
// "9.00") in currency, tagged with our order reference in metadata.orderId, and
// returns it (including the hosted checkoutLink to redirect the payer to).
func (c *BTCPayClient) CreateInvoice(ctx context.Context, amountMajor, currency, orderRef, redirectURL string) (*BTCPayInvoice, error) {
	if c.baseURL == "" || c.storeID == "" || c.apiKey == "" {
		return nil, errors.New("btcpay: not configured")
	}
	payload, err := json.Marshal(map[string]any{
		"amount":   amountMajor,
		"currency": strings.ToUpper(strings.TrimSpace(currency)),
		"metadata": map[string]string{"orderId": orderRef},
		"checkout": map[string]any{"redirectURL": redirectURL, "redirectAutomatically": true},
	})
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL + "/api/v1/stores/" + url.PathEscape(c.storeID) + "/invoices"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// GetInvoice fetches an invoice by id — used to authoritatively confirm status
// and read back the order reference on webhook/return, never trusting the
// browser or the raw webhook body for the money-moving decision.
func (c *BTCPayClient) GetInvoice(ctx context.Context, invoiceID string) (*BTCPayInvoice, error) {
	if c.baseURL == "" || c.storeID == "" || c.apiKey == "" {
		return nil, errors.New("btcpay: not configured")
	}
	if !ValidBTCPayInvoiceID(invoiceID) {
		return nil, errors.New("btcpay: invalid invoice id")
	}
	endpoint := c.baseURL + "/api/v1/stores/" + url.PathEscape(c.storeID) + "/invoices/" + url.PathEscape(invoiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+c.apiKey)
	return c.do(req)
}

// Ping verifies the URL + store + API key by asking BTCPay for the store record.
func (c *BTCPayClient) Ping(ctx context.Context) error {
	if c.baseURL == "" || c.storeID == "" || c.apiKey == "" {
		return errors.New("btcpay: not configured")
	}
	endpoint := c.baseURL + "/api/v1/stores/" + url.PathEscape(c.storeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.apiKey)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("btcpay: store check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// do executes req and decodes an invoice response.
func (c *BTCPayClient) do(req *http.Request) (*BTCPayInvoice, error) {
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("btcpay: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID           string         `json:"id"`
		CheckoutLink string         `json:"checkoutLink"`
		Status       string         `json:"status"`
		Metadata     map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("btcpay: decode response: %w", err)
	}
	ref := ""
	if out.Metadata != nil {
		if v, ok := out.Metadata["orderId"].(string); ok {
			ref = v
		}
	}
	return &BTCPayInvoice{ID: out.ID, CheckoutLink: out.CheckoutLink, Status: out.Status, OrderRef: ref}, nil
}

// btcpayInvoiceIDRe bounds an invoice id to BTCPay's base58-style token so the id
// can be safely interpolated into an API path or matched from a redirect.
var btcpayInvoiceIDRe = regexp.MustCompile(`^[A-Za-z0-9]{6,64}$`)

// ValidBTCPayInvoiceID reports whether id is a well-formed BTCPay invoice id.
func ValidBTCPayInvoiceID(id string) bool {
	return btcpayInvoiceIDRe.MatchString(strings.TrimSpace(id))
}

// VerifyBTCPaySig checks a BTCPay webhook's HMAC-SHA256 signature (the
// "BTCPay-Sig: sha256=<hex>" header) over the exact raw request body, using the
// webhook's shared secret. A constant-time compare avoids timing leaks.
func VerifyBTCPaySig(secret string, body []byte, sigHeader string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	sig := strings.TrimSpace(sigHeader)
	sig = strings.TrimPrefix(sig, "sha256=")
	want, err := hex.DecodeString(sig)
	if err != nil || len(want) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// BTCPayWebhookEvent is the subset of a BTCPay webhook payload we act on.
type BTCPayWebhookEvent struct {
	Type      string `json:"type"`
	InvoiceID string `json:"invoiceId"`
	StoreID   string `json:"storeId"`
}

// ParseBTCPayWebhook decodes a (already signature-verified) webhook body.
func ParseBTCPayWebhook(body []byte) (*BTCPayWebhookEvent, error) {
	var ev BTCPayWebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// IsSettlementEvent reports whether a webhook type means the invoice is paid +
// confirmed (so the order should be fulfilled).
func (ev *BTCPayWebhookEvent) IsSettlementEvent() bool {
	switch ev.Type {
	case "InvoiceSettled", "InvoicePaymentSettled":
		return true
	}
	return false
}
