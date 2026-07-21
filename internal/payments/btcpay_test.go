package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBTCPayCreateInvoice(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"Ab12Cd34","checkoutLink":"` + "http://btcpay.test/i/Ab12Cd34" + `","status":"New","metadata":{"orderId":"VP-REF-9"}}`))
	}))
	defer srv.Close()

	c := NewBTCPayClient(srv.Client(), srv.URL+"/", "store-1", "greenfield-key")
	inv, err := c.CreateInvoice(context.Background(), "9.00", "usd", "VP-REF-9", "http://site/checkout/crypto/return")
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if inv.ID != "Ab12Cd34" || inv.CheckoutLink != "http://btcpay.test/i/Ab12Cd34" || inv.OrderRef != "VP-REF-9" {
		t.Fatalf("parsed invoice wrong: %+v", inv)
	}
	if gotAuth != "token greenfield-key" {
		t.Errorf("auth header = %q, want token greenfield-key", gotAuth)
	}
	if gotPath != "/api/v1/stores/store-1/invoices" {
		t.Errorf("path = %q", gotPath)
	}
	// The order reference and an uppercased currency must be in the request body.
	if !strings.Contains(gotBody, `"orderId":"VP-REF-9"`) || !strings.Contains(gotBody, `"USD"`) {
		t.Errorf("request body missing orderId/currency: %s", gotBody)
	}
}

func TestBTCPayCreateInvoiceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized"}`))
	}))
	defer srv.Close()
	c := NewBTCPayClient(srv.Client(), srv.URL, "store-1", "bad-key")
	if _, err := c.CreateInvoice(context.Background(), "5.00", "USD", "R", "http://x"); err == nil {
		t.Fatal("expected an error on HTTP 401")
	}
}

func TestVerifyBTCPaySig(t *testing.T) {
	secret := "whsecret"
	body := []byte(`{"type":"InvoiceSettled","invoiceId":"Ab12Cd34"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifyBTCPaySig(secret, body, good) {
		t.Error("valid signature rejected")
	}
	if VerifyBTCPaySig(secret, body, good+"00") {
		t.Error("tampered signature accepted")
	}
	if VerifyBTCPaySig(secret, []byte(`{"type":"x"}`), good) {
		t.Error("signature accepted for a different body")
	}
	if VerifyBTCPaySig("", body, good) {
		t.Error("empty secret must never verify")
	}
	if VerifyBTCPaySig(secret, body, "") {
		t.Error("empty signature must never verify")
	}
}

func TestBTCPaySettledAndInvoiceID(t *testing.T) {
	for _, s := range []string{"Settled", "settled", "Complete", "Confirmed"} {
		if (&BTCPayInvoice{Status: s}).Settled() != true {
			t.Errorf("%q should be settled", s)
		}
	}
	for _, s := range []string{"New", "Processing", "Expired", "Invalid", ""} {
		if (&BTCPayInvoice{Status: s}).Settled() {
			t.Errorf("%q should NOT be settled", s)
		}
	}
	if !ValidBTCPayInvoiceID("Ab12Cd34Ef") || ValidBTCPayInvoiceID("bad/id") || ValidBTCPayInvoiceID("") {
		t.Error("ValidBTCPayInvoiceID gate wrong")
	}
}

func TestParseBTCPayWebhook(t *testing.T) {
	ev, err := ParseBTCPayWebhook([]byte(`{"type":"InvoiceSettled","invoiceId":"Ab12Cd34","storeId":"s1"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ev.IsSettlementEvent() || ev.InvoiceID != "Ab12Cd34" {
		t.Errorf("event parsed wrong: %+v", ev)
	}
	other, _ := ParseBTCPayWebhook([]byte(`{"type":"InvoiceCreated","invoiceId":"x"}`))
	if other.IsSettlementEvent() {
		t.Error("InvoiceCreated must not be a settlement event")
	}
	// Sanity: the webhook JSON shape round-trips.
	b, _ := json.Marshal(ev)
	if !strings.Contains(string(b), "InvoiceSettled") {
		t.Error("marshal lost the type")
	}
}
