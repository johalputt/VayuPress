// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The audit finding, in the attacker's voice:
//
//	Your public rate limits are keyed on a header I write. clientIPForContact
//	returned the LEFTMOST element of X-Forwarded-For with no trusted-proxy
//	check — and your own nginx template uses $proxy_add_x_forwarded_for, which
//	APPENDS the real peer to whatever I sent, so leftmost is always mine. A
//	fresh header per request is a fresh budget per request. Every limiter keyed
//	on this counted to one, forever.
//
//	The part I like better: that string is your audit actor. "public:"+ip and
//	"device:"+ip go into the WORM log unvalidated. I do not merely evade the
//	record — I write it, in someone else's name. Your own AuditActor comment
//	says exactly why that is worse than no log at all; the fix landed there and
//	never came here.
//
// realIPMiddleware has resolved r.RemoteAddr correctly the whole time (it calls
// auth.ClientIP, which honours forwarding headers only from a configured trusted
// proxy). This resolver just did not use it.

// spoofReq is a request from an UNTRUSTED peer carrying a forged forwarding
// header — the shape every request has on a direct-bind install, and the shape
// every request has behind the shipped nginx config once the proxy has appended
// the real peer.
func spoofReq(remote, forwarded string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-device-reset", nil)
	req.RemoteAddr = remote
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	return req
}

func TestTheRateLimitKeyCannotBeChosenByTheCaller(t *testing.T) {
	const peer = "198.51.100.20:44444"

	base := clientIPForContact(spoofReq(peer, ""))
	if base == "" {
		t.Fatal("no key at all for a plain request")
	}

	for _, forged := range []string{
		"10.9.8.7, 198.51.100.9",  // the classic: my address first, the real peer appended
		"203.0.113.1",             // a single value
		"not-an-ip",               // nothing stops me sending this
		"; DROP",                  // nor this
		"  10.0.0.1  ,  10.0.0.2", // whitespace, in case trimming is the only check
	} {
		got := clientIPForContact(spoofReq(peer, forged))
		if got != base {
			t.Errorf("X-Forwarded-For: %q changed the key from %q to %q.\n\n"+
				"Every limiter keyed on this — contact, recovery, and deviceResetByIP, which "+
				"is the only budget in front of twenty sequential Argon2id derivations — is "+
				"defeated by one header. And this string is written into the audit log as the "+
				"actor, so the record names whoever the caller chose.", forged, base, got)
		}
	}
}

// The key must be an ADDRESS. Nothing downstream parses it — not the limiter,
// not the audit log, not the recovery email the victim reads — so if a
// non-address ever survives to here it survives all the way.
func TestTheAuditActorIsAlwaysAnAddress(t *testing.T) {
	for _, remote := range []string{
		"198.51.100.20:44444",
		"[2001:db8::1]:9000",
		"garbage-not-a-host", // a peer address the stack could not split
		"",
	} {
		got := clientIPForContact(spoofReq(remote, "not-an-ip"))
		if got == "not-an-ip" || got == "; DROP" {
			t.Errorf("RemoteAddr %q yielded the caller's own string %q as the audit actor", remote, got)
		}
		if got == "" {
			t.Errorf("RemoteAddr %q yielded an empty key; an empty actor column reads as "+
				"'nobody did this'", remote)
		}
	}
}

// The control. A resolver that returned one constant would pass everything above
// and make every limiter global — one stranger's flood would then lock out the
// whole internet. Distinct peers must key distinctly.
func TestDistinctPeersStillGetDistinctBudgets(t *testing.T) {
	a := clientIPForContact(spoofReq("198.51.100.21:1000", ""))
	b := clientIPForContact(spoofReq("198.51.100.22:1000", ""))
	if a == b {
		t.Fatalf("two different peers both keyed as %q — every rate limit in the product is "+
			"now global, so one flood refuses service to everyone", a)
	}
}

// And the same peer on a different source port is the same person, or a limiter
// counts to one per connection and bounds nothing.
func TestOnePeerKeysTheSameAcrossPorts(t *testing.T) {
	a := clientIPForContact(spoofReq("198.51.100.23:1000", ""))
	b := clientIPForContact(spoofReq("198.51.100.23:2000", ""))
	if a != b {
		t.Errorf("the same address keyed as %q and %q across two source ports; a caller gets a "+
			"fresh budget per connection, which is no budget", a, b)
	}
}

// The second half of the same finding: deviceResetByIP was the ONLY budget on
// /api/v1/members/vayumail-device-reset, and past it VerifyApprovedDevice runs
// Argon2id over every approved credential for the address — up to twenty
// sequential 64 MiB derivations for one unauthenticated request. A caller with
// more than one source address simply pays the per-IP toll twice.
func TestDeviceResetIsBoundedPerMailboxNotOnlyPerSource(t *testing.T) {
	a := credentialFloodApp(t)

	// Thirty requests at ONE mailbox, each from a different source, so the per-IP
	// budget never fires. Only a per-address budget can bound this.
	refused := 0
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-device-reset",
			jsonBody(`{"email":"boss@example.com","app_password":"guess","new_password":"a new password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2." + strconv.Itoa(i+1) + ":9000"
		rec := httptest.NewRecorder()
		a.handleMailDeviceReset(rec, req)
		if rec.Code == http.StatusUnauthorized {
			refused++
		}
	}
	// Every rejection here is a uniform 401 on purpose, so the status cannot
	// distinguish "over budget" from "wrong credential". What CAN be asserted is
	// that the budget exists and fires: with it removed, thirty requests each
	// reach the credential check.
	if !deviceResetByAddress.allow("boss@example.com") {
		return // the address budget is spent, which is the property under test
	}
	t.Errorf("thirty device-reset attempts against one mailbox from thirty different sources "+
		"left its per-address budget untouched (%d refused).\n\n"+
		"Each one that gets past the gate runs Argon2id over up to twenty stored "+
		"credentials. The per-IP budget cannot see a distributed caller; the per-address "+
		"one is what holds.", refused)
}

func jsonBody(s string) *strings.Reader { return strings.NewReader(s) }
