// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

// SECTION 3 — hardening the consent token, stated honestly.
//
// This is NOT a demonstrated exploit. The token is minted only into an
// authenticated administrator's consent page, it never travels in a URL, and a
// cross-origin attacker cannot read it. I could not find a way to obtain one,
// and it is not reported as exploitable.
//
// What it IS: a bearer capability far broader than the decision it represents.
// The payload was "<admin id>|<expiry>", the consent POST deliberately requires
// no session (the browser drops every cookie on that cross-site POST), and the
// client_id, redirect_uri and code_challenge all come from the form. So a value
// minted to approve ONE named connector authorised any registered client, at any
// redirect that client had registered, for ten minutes.
//
// The consent screen exists so an operator approves a specific app. A token that
// outlives that specificity is a gap between what the operator decided and what
// the server will accept — worth closing whether or not anybody can reach it,
// because it costs one hash and removes the question.
//
// Binding covers client_id, redirect_uri and code_challenge. It deliberately
// does NOT cover the grant: the operator picks that on the page, so it does not
// exist when the token is minted.

func TestAConsentTokenDoesNotTransferToAnotherClient(t *testing.T) {
	minted := oauthConsentToken("u-admin", time.Now().Add(consentTokenTTL),
		"vpc_theapproved", "https://approved.example/cb", "chal-1")

	// Same admin, same expiry, replayed against a DIFFERENT registered client.
	owner, ok := oauthConsentApprover(minted, "vpc_attacker", "https://attacker.example/cb", "chal-1")
	if ok {
		t.Errorf("a consent token minted for vpc_theapproved was accepted for vpc_attacker "+
			"(approver=%q).\n\n"+
			"The operator approved one named app on a screen that showed them its "+
			"destination. A token that authorises any client is broader than the "+
			"decision it represents.", owner)
	}
	// And redirect and challenge are bound too.
	if _, ok := oauthConsentApprover(minted, "vpc_theapproved", "https://elsewhere.example/cb", "chal-1"); ok {
		t.Error("the token was accepted for a different redirect_uri")
	}
	if _, ok := oauthConsentApprover(minted, "vpc_theapproved", "https://approved.example/cb", "chal-2"); ok {
		t.Error("the token was accepted for a different PKCE challenge")
	}
}

// THE CONTROL. The flow this exists to serve must still work end to end.
func TestAConsentTokenApprovesTheRequestItWasMintedFor(t *testing.T) {
	minted := oauthConsentToken("u-admin", time.Now().Add(consentTokenTTL),
		"vpc_theapproved", "https://approved.example/cb", "chal-1")

	owner, ok := oauthConsentApprover(minted, "vpc_theapproved", "https://approved.example/cb", "chal-1")
	if !ok {
		t.Fatal("the consent token was refused for the exact request it was minted for.\n\n" +
			"That is the one-click Connect flow: the operator clicks Approve and nothing " +
			"happens.")
	}
	if owner != "u-admin" {
		t.Errorf("approver = %q, want u-admin — the minted key and the audit row are "+
			"attributed to whoever this names", owner)
	}
}

// Expiry still bounds it, and a forged or truncated token is still refused.
func TestAnExpiredOrForgedConsentTokenIsRefused(t *testing.T) {
	expired := oauthConsentToken("u-admin", time.Now().Add(-time.Minute),
		"vpc_c", "https://c.example/cb", "chal")
	if _, ok := oauthConsentApprover(expired, "vpc_c", "https://c.example/cb", "chal"); ok {
		t.Error("an expired consent token was accepted")
	}

	// Unsigned payload in the right shape: unforgeable without the server secret.
	forged := "u-admin|" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + "|deadbeef"
	if _, ok := oauthConsentApprover(forged, "vpc_c", "https://c.example/cb", "chal"); ok {
		t.Error("an unsigned consent token was accepted")
	}
	if _, ok := oauthConsentApprover(auth.SignedToken("u-admin"), "vpc_c", "https://c.example/cb", "chal"); ok {
		t.Error("a signed token missing the expiry and binding fields was accepted")
	}
	if _, ok := oauthConsentApprover("", "vpc_c", "https://c.example/cb", "chal"); ok {
		t.Error("an empty consent token was accepted")
	}
}

// The pre-binding token shape must not be honoured. An upgrade leaves at most a
// ten-minute window of consent pages rendered by the old binary; those approvals
// are refused with the message that already tells the operator to reconnect,
// which is a far better outcome than continuing to accept an unbound token.
func TestThePreBindingTokenShapeIsRefused(t *testing.T) {
	old := auth.SignedToken("u-admin|" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	if _, ok := oauthConsentApprover(old, "vpc_c", "https://c.example/cb", "chal"); ok {
		t.Error("a two-field (pre-binding) consent token was accepted.\n\n" +
			"That is the shape this change exists to retire; honouring it keeps the gap open.")
	}
	if !strings.Contains(old, ".") {
		t.Fatal("fixture wrong: a signed token should carry its signature after a dot")
	}
}

// The separator is load-bearing, so it is asserted rather than described.
//
// The binding is a hash over three attacker-influenced strings. Joining them
// with nothing — or with any character a field may itself contain — lets two
// different authorization requests collapse to one fingerprint, and the binding
// silently stops binding. Concatenating without a separator was tried as a
// mutation and produced exactly that collision.
func TestTwoDifferentRequestsCannotShareABinding(t *testing.T) {
	pairs := [][2][3]string{
		{{"vpc_a", "https://x/cb", "chal"}, {"vpc_ahttps://x/cb", "", "chal"}},
		{{"vpc_a", "https://x/cb", "chal"}, {"vpc_a", "https://x/cbchal", ""}},
		{{"a", "b", "c"}, {"a|b", "", "c"}},
	}
	for _, p := range pairs {
		x := oauthConsentBinding(p[0][0], p[0][1], p[0][2])
		y := oauthConsentBinding(p[1][0], p[1][1], p[1][2])
		if x == y {
			t.Errorf("%v and %v share the binding %s.\n\n"+
				"Two different approvals with one fingerprint means a token minted for "+
				"the first is accepted for the second, which is the whole property this "+
				"change adds.", p[0], p[1], x)
		}
	}
}
