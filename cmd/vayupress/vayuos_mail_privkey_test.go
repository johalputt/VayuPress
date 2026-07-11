package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// appWithMailAndPGP extends appWithMailAccounts with a started VayuPGP engine
// (temp storage, in-memory master secret) so the private-key sync endpoint
// exercises the real keystore end-to-end.
func appWithMailAndPGP(t *testing.T) *App {
	t.Helper()
	a := appWithMailAccounts(t)
	cfg := vpgp.DefaultConfig()
	cfg.Enabled = true
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret")
	e := vpgp.NewEngine(&cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start pgp engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	a.vayuPGP = e
	return a
}

// postPrivKey drives the private-key sync handler with a JSON body, as the
// VayuMail-Mobile app posts it.
func postPrivKey(a *App, email, password string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-privkey", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleMemberVayuMailPrivKey(rec, req)
	return rec
}

const privKeyHeader = "-----BEGIN PGP PRIVATE KEY BLOCK-----"

// TestVayuMailPrivKeyValidReturnsArmoredKey verifies a correct mailbox password
// yields the caller's armored private key (minting one on demand when the
// mailbox pre-dates auto-keygen) and that the response is marked no-store.
// Device approval is switched off here: with it required (the default) the
// raw password no longer unlocks the key — covered by the device tests.
func TestVayuMailPrivKeyValidReturnsArmoredKey(t *testing.T) {
	a := appWithMailAndPGP(t)
	if err := a.vayuMail.Accounts().SetRequireDeviceApproval(context.Background(), "dana@example.com", false); err != nil {
		t.Fatalf("disable device approval: %v", err)
	}
	rec := postPrivKey(a, "dana@example.com", "main-mailbox-pass")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var out struct {
		Email   string `json:"email"`
		Armored string `json:"armored_private_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if out.Email != "dana@example.com" {
		t.Errorf("email = %q, want dana@example.com", out.Email)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.Armored), privKeyHeader) {
		t.Fatalf("armored key does not begin with the PGP private-key header:\n%q", out.Armored)
	}
}

// TestVayuMailPrivKeyAppPasswordWorks confirms a device app password (not just
// the mailbox password) unlocks the private key, matching the login path.
func TestVayuMailPrivKeyAppPasswordWorks(t *testing.T) {
	a := appWithMailAndPGP(t)
	ctx := context.Background()
	const email = "dana@example.com"
	const appSecret = "devicesecret1234" // dashless, as the auth bridge hashes it
	hash, err := auth.HashSecretArgon2id(appSecret)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := a.vayuMail.Accounts().CreateAppPassword(ctx, email, "Phone", hash); err != nil {
		t.Fatalf("create app password: %v", err)
	}
	rec := postPrivKey(a, email, appSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("app-password status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), privKeyHeader) {
		t.Error("app-password auth did not return a private key")
	}
}

// TestVayuMailPrivKeyWrongPasswordNoKey verifies a wrong password returns 401
// and leaks no key material.
func TestVayuMailPrivKeyWrongPasswordNoKey(t *testing.T) {
	a := appWithMailAndPGP(t)
	rec := postPrivKey(a, "dana@example.com", "not-the-password")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), privKeyHeader) || strings.Contains(rec.Body.String(), "armored_private_key") {
		t.Errorf("401 response leaked key material: %s", rec.Body.String())
	}
}

// TestVayuMailPrivKeyNoEnumeration verifies an unknown mailbox and a wrong
// password are indistinguishable — same status and same response body — so the
// endpoint cannot be used to discover which addresses exist.
func TestVayuMailPrivKeyNoEnumeration(t *testing.T) {
	a := appWithMailAndPGP(t)
	wrong := postPrivKey(a, "dana@example.com", "not-the-password")
	unknown := postPrivKey(a, "ghost@example.com", "not-the-password")
	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d / %d, want 401 / 401", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("wrong-password and unknown-mailbox responses differ (enumeration leak):\n wrong:   %s\n unknown: %s",
			wrong.Body.String(), unknown.Body.String())
	}
}
