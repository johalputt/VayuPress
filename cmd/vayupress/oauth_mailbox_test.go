// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/oauth"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailboxOAuthApp is a running mail install (dana's mailbox) with the
// authorization server and the key and user stores.
func mailboxOAuthApp(t *testing.T) (*App, oauth.Client) {
	t.Helper()
	a := calendarApp(t)
	o := oauthTestApp(t)
	a.oauth = o.oauth
	a.apiKeys, a.userStore, _ = keyStoreWithUsers(t)
	client, err := a.oauth.RegisterClient(context.Background(), "Helper", []string{"https://tool.example.org/cb"})
	if err != nil {
		t.Fatal(err)
	}
	return a, client
}

// consentAs posts the consent page's form as the account userID approves.
func consentAs(a *App, client oauth.Client, userID, grant, challenge string) *httptest.ResponseRecorder {
	redirect := "https://tool.example.org/cb"
	form := url.Values{"client_id": {client.ID}, "redirect_uri": {redirect}, "code_challenge": {challenge}, "state": {"s"},
		"consent_token": {oauthConsentToken(userID, time.Now().Add(time.Minute), client.ID, redirect, challenge)}, "decision": {"approve"}, "grant": {grant}}
	r := httptest.NewRequest(http.MethodPost, "/oauth/authorize/consent", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleOAuthConsent(rec, r)
	return rec
}

// TestAHolderConnectsTheirOwnMailboxAndNothingElse — a mailbox's holder,
// who is no administrator, grants a tool their mailbox; the key minted
// holds no site grant and reaches only the connection made for it; asking
// for the site is refused.
func TestAHolderConnectsTheirOwnMailboxAndNothingElse(t *testing.T) {
	a, client := mailboxOAuthApp(t)
	verifier := "a-verifier-long-enough-for-pkce-0123456789abcdef"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	for _, grant := range []string{"full", "author", "readonly"} {
		if rec := consentAs(a, client, "vmail:dana@example.com", grant, challenge); rec.Code == http.StatusSeeOther || !strings.Contains(rec.Body.String(), "Only an administrator connects the site.") {
			t.Errorf("a holder granted the site %s: %d %s", grant, rec.Code, rec.Header().Get("Location"))
		}
	}
	// An account that holds no mailbox has none to grant.
	if rec := consentAs(a, client, "u-no-mailbox", "mailbox", challenge); rec.Code == http.StatusSeeOther || !strings.Contains(rec.Body.String(), "holder connects it.") {
		t.Errorf("an account with no mailbox granted one: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// A session's address with no mailbox behind it: the code is issued for
	// the address, and the exchange makes no connection and no key.
	nobody := consentAs(a, client, "vmail:nobody@example.com", "mailbox", challenge)
	nloc, _ := url.Parse(nobody.Header().Get("Location"))
	if tok := oauthExchange(a, client, nloc.Query().Get("code"), verifier); tok.Code == http.StatusOK {
		t.Errorf("a mailbox that is not here was connected: %s", tok.Body.String())
	}
	if keys, _ := a.apiKeys.ListByOwner(context.Background(), "vmail:nobody@example.com"); len(keys) != 1 || !keys[0].Revoked {
		t.Errorf("a key outlived the refused connection: %+v", keys)
	}
	rec := consentAs(a, client, "vmail:dana@example.com", "mailbox", challenge)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the holder's approval: %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	tok := oauthExchange(a, client, loc.Query().Get("code"), verifier)
	if tok.Code != http.StatusOK || !strings.Contains(tok.Body.String(), "access_token") {
		t.Fatalf("the exchange: %d %s", tok.Code, tok.Body.String())
	}
	cs, _ := a.vayuMail.Connections(vmail.ReadAsOwner("dana"))
	if len(cs) != 1 || strings.Join(cs[0].Scopes, ",") != "read,draft,calendar" || cs[0].Client != "Helper" {
		t.Fatalf("the connection: %+v", cs)
	}
	keys, _ := a.apiKeys.ListByOwner(context.Background(), "vmail:dana@example.com")
	if len(keys) != 1 || keys[0].ID != cs[0].KeyID || !keys[0].Permissions.IsEmpty() {
		t.Fatalf("the key: %+v", keys)
	}
}

func oauthExchange(a *App, client oauth.Client, code, verifier string) *httptest.ResponseRecorder {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client.ID}, "redirect_uri": {"https://tool.example.org/cb"}, "code_verifier": {verifier}}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleOAuthToken(rec, r)
	return rec
}

// TestTheConsentPageOffersWhatTheApproverMayGrant — a holder sees their
// mailbox and not the site.
func TestTheConsentPageOffersWhatTheApproverMayGrant(t *testing.T) {
	client := oauth.Client{ID: "c", Name: "Helper"}
	holder := oauthConsentPage(client, "https://tool.example.org/cb", "ch", "s", "tok", false, "dana@example.com")
	if strings.Contains(holder, `value="full"`) || !strings.Contains(holder, `value="mailbox-read"`) || !strings.Contains(holder, "(dana@example.com)") {
		t.Errorf("a holder's page:\n%s", holder)
	}
	admin := oauthConsentPage(client, "https://tool.example.org/cb", "ch", "s", "tok", true, "")
	if !strings.Contains(admin, `value="full"`) || strings.Contains(admin, `value="mailbox`) {
		t.Errorf("an administrator with no mailbox:\n%s", admin)
	}
}

// TestDisconnectEndsTheConnectionAndItsKey — from the holder's Settings.
func TestDisconnectEndsTheConnectionAndItsKey(t *testing.T) {
	a, client := mailboxOAuthApp(t)
	verifier := "a-verifier-long-enough-for-pkce-0123456789abcdef"
	sum := sha256.Sum256([]byte(verifier))
	rec := consentAs(a, client, "vmail:dana@example.com", "mailbox-read", base64.RawURLEncoding.EncodeToString(sum[:]))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if tok := oauthExchange(a, client, loc.Query().Get("code"), verifier); tok.Code != http.StatusOK {
		t.Fatalf("the exchange: %d %s", tok.Code, tok.Body.String())
	}
	cs, _ := a.vayuMail.Connections(vmail.ReadAsOwner("dana"))
	if len(cs) != 1 {
		t.Fatalf("%d connections", len(cs))
	}
	if block := a.connectedToolsBlock(withUser(httptest.NewRequest(http.MethodGet, "/", nil), dana()), vmail.ReadAsOwner("dana"), ""); !strings.Contains(block, "Helper · may read mail and calendar") {
		t.Fatalf("the Settings block:\n%s", block)
	}
	got := postForm(a.handleVayuOSMailDisconnect, "/os/vayumail/connections/disconnect", url.Values{"key": {cs[0].KeyID}}.Encode(), dana())
	if !strings.Contains(got.Body.String(), "Disconnected.") {
		t.Fatalf("disconnecting: %s", got.Body.String())
	}
	if _, ok := a.vayuMail.ConnectionOf(cs[0].KeyID); ok {
		t.Error("the connection outlived Disconnect")
	}
	if keys, _ := a.apiKeys.ListByOwner(context.Background(), "vmail:dana@example.com"); len(keys) != 1 || !keys[0].Revoked {
		t.Errorf("the key outlived Disconnect: %+v", keys)
	}
}
