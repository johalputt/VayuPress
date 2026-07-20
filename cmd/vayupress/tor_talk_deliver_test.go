package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// appWithTalkOnion builds an App in the Tor world with a live relay and a
// settings store, so the inbound onion-delivery endpoint can be exercised. It
// returns the app and our own anonymous address.
func appWithTalkOnion(t *testing.T, federationOn bool) (*App, string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("settings schema: %v", err)
	}
	st := settings.New(db)
	kv := map[string]string{settings.KeyTalkAnonID: "anontesthandle01"}
	if federationOn {
		kv[settings.KeyTalkOnionFederation] = "on"
	}
	if err := st.SetMany(context.Background(), kv); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	e := vtalk.NewEngine(vtalk.Config{
		Enabled: true,
		Verify:  func(context.Context, string, string) bool { return false },
		PubKey:  func(string) (string, string, error) { return "", "", errors.New("no key") },
	})
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start talk: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })

	a := &App{vayuTalk: e, siteSettings: st}

	prevOnion, prevDomain := config.Cfg.OnionMode, config.Cfg.Domain
	config.Cfg.OnionMode = true
	config.Cfg.Domain = "examplexyz234567abcd.onion"
	t.Cleanup(func() { config.Cfg.OnionMode, config.Cfg.Domain = prevOnion, prevDomain })

	self := a.talkAnonAddress(context.Background())
	if self == "" {
		t.Fatal("talkAnonAddress returned empty")
	}
	return a, self
}

func onionDeliver(a *App, body map[string]interface{}) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/talk/onion/deliver", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleTalkOnionDeliver(rec, req)
	return rec
}

// TestOnionDeliverClosedWhenFederationOff proves the endpoint is undiscoverable
// (404) unless the operator has opted into federation.
func TestOnionDeliverClosedWhenFederationOff(t *testing.T) {
	a, self := appWithTalkOnion(t, false)
	ct := base64.StdEncoding.EncodeToString([]byte("cipher"))
	rec := onionDeliver(a, map[string]interface{}{
		"to": self, "from": "anonpeer99999999@peeronion234567xyz.onion", "ciphertext": ct, "mode": "store",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("federation off = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// TestOnionDeliverAcceptsForUs proves a well-formed envelope addressed to our own
// code is accepted and enqueued when federation is on.
func TestOnionDeliverAcceptsForUs(t *testing.T) {
	a, self := appWithTalkOnion(t, true)
	ct := base64.StdEncoding.EncodeToString([]byte("cipher"))
	rec := onionDeliver(a, map[string]interface{}{
		"to": self, "from": "anonpeer99999999@peeronion234567xyz.onion", "ciphertext": ct, "mode": "store",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("deliver = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		ID     string `json:"id"`
		Queued bool   `json:"queued"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ID == "" || !out.Queued {
		t.Fatalf("deliver result: id=%q queued=%v (offline recipient should queue)", out.ID, out.Queued)
	}
}

// TestOnionDeliverRejectsForeignRecipient proves the endpoint is not an open
// relay: an envelope addressed to a code we do not host is refused.
func TestOnionDeliverRejectsForeignRecipient(t *testing.T) {
	a, _ := appWithTalkOnion(t, true)
	ct := base64.StdEncoding.EncodeToString([]byte("cipher"))
	rec := onionDeliver(a, map[string]interface{}{
		"to": "anonsomeoneelse1@othersite234567xy.onion", "from": "anonpeer99999999@peeronion234567xyz.onion", "ciphertext": ct, "mode": "store",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign recipient = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// TestOnionDeliverRejectsBadSender proves a non-onion (clearnet) sender is
// refused, so only onion peers can deliver.
func TestOnionDeliverRejectsBadSender(t *testing.T) {
	a, self := appWithTalkOnion(t, true)
	ct := base64.StdEncoding.EncodeToString([]byte("cipher"))
	rec := onionDeliver(a, map[string]interface{}{
		"to": self, "from": "bob@example.com", "ciphertext": ct, "mode": "store",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("clearnet sender = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}
