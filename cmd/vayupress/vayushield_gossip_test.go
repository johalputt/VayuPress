// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/gossip"
)

// TestTheGossipEndpointIsNotInTheAlwaysAdmitLane.
//
// The L0 sovereignty lane admits anything under /__vayushield unconditionally
// and never counts it against the public budget, because the challenge endpoints
// under that prefix MUST stay reachable — a visitor who cannot reach the PoW
// verifier can never stop being challenged.
//
// The gossip endpoint inherited that prefix, and it is a completely different
// animal: an UNAUTHENTICATED caller, on a route that does real work (a 64 KiB
// read and an AEAD open) before it can possibly know the caller is a stranger.
// In the priority lane that is an unbounded, unmetered, unauthenticated compute
// sink on the one lane reserved for keeping the admin plane alive during a
// flood — the exact resource the lane exists to protect.
//
// A peer's push does not need priority. Verdicts are perishable and a dropped
// one costs a little convergence speed and nothing else, so the right place for
// this route is the ordinary public budget with everything else.
func TestTheGossipEndpointIsNotInTheAlwaysAdmitLane(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodPost, gossip.Path, strings.NewReader("x"))
	if a.isSovereignLane(req) {
		t.Error("the peer-gossip endpoint is in the always-admit lane. An unauthenticated " +
			"stranger can then spend unbounded concurrency on AEAD opens inside the very budget " +
			"reserved for keeping the admin plane responsive during an attack.")
	}

	// The endpoints that genuinely need priority must keep it. A visitor who
	// cannot reach the verifier can never stop being challenged.
	for _, p := range []string{"/__vayushield/pow", "/__vayushield/challenge.js"} {
		if !a.isSovereignLane(httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Errorf("%q lost its priority admission — a challenged visitor cannot reach the "+
				"endpoint that would clear them, so the challenge becomes a wall", p)
		}
	}
}

// TestTheShieldBypassOnGossipIsPaidFor.
//
// The endpoint IS bypassed by the shield, and that part is correct for the same
// reason /mcp and /oauth are: a peer node is not a browser and can never solve a
// challenge, so challenging it is an outage rather than a defence. Those
// prefixes carry the caveat "they carry their own auth/rate limits instead" —
// and for this one that has to be true rather than aspirational, because the
// route does real work (a 64 KiB read, an AEAD open) for a caller it has not yet
// authenticated.
//
// This test asserts the bill is paid: the exemption exists AND the package
// enforces a ceiling of its own, at a level a real peer's once-a-second cadence
// never approaches.
func TestTheShieldBypassOnGossipIsPaidFor(t *testing.T) {
	bypassed := false
	for _, pre := range shieldBypassPrefixes {
		if pre != "" && (gossip.Path == pre || strings.HasPrefix(gossip.Path, pre+"/")) {
			bypassed = true
		}
	}
	if !bypassed {
		t.Error("the gossip endpoint is no longer shield-bypassed. A peer node cannot solve a " +
			"browser challenge, so the shield challenging it stops verdicts flowing during " +
			"exactly the attack they exist for")
	}
	if gossip.IngressPerMinute <= 0 {
		t.Fatal("the endpoint is exempt from every shield gate and enforces no limit of its " +
			"own, which makes it an unmetered compute sink for unauthenticated callers")
	}
	// A peer flushes once a second. The ceiling must clear that with room and
	// still be a ceiling.
	if gossip.IngressPerMinute < 60 {
		t.Errorf("the ceiling is %d/min, below a peer's normal 60 — the limit is tighter than "+
			"the protocol it protects", gossip.IngressPerMinute)
	}
	if gossip.IngressPerMinute > 600 {
		t.Errorf("the ceiling is %d/min, ten times a peer's steady state — that is not a limit, "+
			"it is a formality", gossip.IngressPerMinute)
	}
}
