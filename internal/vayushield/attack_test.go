// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/gossip"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// attack_test.go — tests written by attacking the shield rather than by
// describing it. Each one below started as "what would I do to this", and each
// found something.

// TestAPeerCannotJailAnOperatorsOwnIPv6Network.
//
// The allow-list override is the control that stops a compromised peer from
// locking an operator out of their whole fleet. It asks the policy whether a
// source is trusted — but the source in a verdict is an ENFORCEMENT KEY, and for
// IPv6 that is always a prefix ("2001:db8::/64"), never a bare address. Policy
// lookups parse an address, so a prefix could not match anything and every IPv6
// verdict sailed straight past the override.
//
// The same applied to IPv4 whenever /24 grouping was enabled.
func TestAPeerCannotJailAnOperatorsOwnIPv6Network(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{
		Enabled: true, AutoBlock: true, AutoBlockJailMinutes: 10, GroupIPv4: true,
		Policy: policy.Config{AllowCIDRs: []string{"2001:db8::1", "198.51.100.7"}},
	})
	if err := m.JoinCluster("edge-1", "install-secret", []string{"http://127.0.0.1:1"}); err != nil {
		t.Fatalf("join: %v", err)
	}

	// A compromised peer names the operator's own networks by enforcement key.
	v6key := m.enforcementKey("2001:db8::1")
	v4key := m.enforcementKey("198.51.100.7")
	if v6key == "2001:db8::1" {
		t.Fatalf("setup: expected a prefix key for IPv6, got %q", v6key)
	}
	m.gossipApply.Apply(gossip.Message{Node: "compromised", Issued: time.Now().Unix(),
		Verdicts: []gossip.Verdict{
			{Kind: gossip.KindJail, Source: v6key},
			{Kind: gossip.KindJail, Source: v4key},
		}}, time.Now())

	if m.blocklist.Blocked(v6key) {
		t.Errorf("a peer jailed %q, the operator's own IPv6 network. The allow-list override "+
			"compares a bare address against an enforcement KEY, which for IPv6 is always a "+
			"prefix — so the one control that stops a compromised node locking an operator out "+
			"of their fleet never fires for IPv6 at all", v6key)
	}
	if m.blocklist.Blocked(v4key) {
		t.Errorf("a peer jailed %q, the operator's own IPv4 network under /24 grouping", v4key)
	}
}

// TestClassifyDoesNotMutateCounters.
//
// Classify is documented as pure so the analytics beacon can call it freely, and
// the beacon does — once per engagement event, on the same request the
// middleware already classified. Anything Classify increments is therefore
// counted at least twice and at a rate set by how chatty the beacon is, not by
// how much of it the shield saw.
//
// An inflated attack counter is not a cosmetic bug: it is the number an operator
// uses to decide whether they are under attack.
func TestClassifyDoesNotMutateCounters(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{Enabled: true})

	req := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	req.RemoteAddr = "198.51.100.9:1234"

	before := m.InspectFindings()
	for i := 0; i < 5; i++ {
		m.Classify(req)
	}
	after := m.InspectFindings()
	if after != before {
		t.Errorf("five Classify calls moved the inspection counters from %v to %v. Classify is "+
			"documented as pure and the analytics beacon calls it once per event on requests the "+
			"middleware already classified, so every finding is counted repeatedly and the "+
			"number an operator reads is inflated by their own traffic volume", before, after)
	}
}

// TestTheMiddlewareStillCountsFindings — the counter has to move somewhere, or
// removing the double count would just remove the count.
func TestTheMiddlewareStillCountsFindings(t *testing.T) {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("test-secret")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "198.51.100.9:1234" },
	})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8})

	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "+
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := m.InspectFindings(); got[0] == 0 {
		t.Error("a scanner probe went through the middleware and no inspection finding was " +
			"counted, so the panel reports the ruleset as having caught nothing")
	}
}

// TestAnOperatorAllowEntryCannotBeForgedByNotation.
//
// The ALLOW verdict is the most powerful thing in the shield: it skips every
// gate including the jail. Anything that lets a client claim an allowed address
// it does not have is a total bypass, so the parse has to be the same one the
// enforcement key uses, with no notation left over.
func TestAnOperatorAllowEntryCannotBeForgedByNotation(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{
		Enabled: true,
		Policy:  policy.Config{AllowCIDRs: []string{"203.0.113.5"}},
	})
	pol := m.Policy()

	// Variants an attacker would try to have read as the allowed address.
	for _, s := range []string{
		"203.0.113.6",
		"0203.0.113.5",     // leading zero: octal in some parsers
		"203.0.113.5.",     // trailing dot
		"203.0.113.05",     // zero-padded final octet
		"3405803781",       // the same address as a 32-bit integer
		"0xCB00710500",     // hex
		"203.0.113.5:8080", // with a port
		" 203.0.113.5",     // leading space
		"203.0.113.5\n",    // trailing newline
		"[203.0.113.5]",    // bracketed
		"203.0.113.5%eth0", // a zone on a v4 literal
	} {
		if got := pol.Source(s); got == policy.VerdictAllow {
			t.Errorf("%q was read as the allowed address 203.0.113.5 — a client presenting this "+
				"notation would skip every gate in the shield including the jail", s)
		}
	}
	// The real thing, and its IPv4-mapped form, still match.
	if pol.Source("203.0.113.5") != policy.VerdictAllow {
		t.Error("the allowed address itself stopped matching")
	}
	if pol.Source("::ffff:203.0.113.5") != policy.VerdictAllow {
		t.Error("the IPv4-mapped form of the allowed address stopped matching, so whether an " +
			"operator's rule applies depends on the listener's socket family")
	}
}

// TestAllowsAnyDoesNotOverReach — the overlap rule protects an operator from a
// remote node, and an overlap test that said yes too readily would let an
// attacker inside an unrelated network claim protection from every jail.
func TestAllowsAnyDoesNotOverReach(t *testing.T) {
	r, bad := policy.Compile(policy.Config{AllowCIDRs: []string{"2001:db8:1::1", "198.51.100.7"}})
	if len(bad) != 0 {
		t.Fatalf("setup: %v", bad)
	}
	for _, k := range []string{
		"2001:db8:1::/64", // contains the allowed address
		"198.51.100.0/24", // contains the allowed address
		"198.51.100.7",    // is the allowed address
	} {
		if !r.AllowsAny(k) {
			t.Errorf("%q does not overlap the allow list, but it contains an allowed address — "+
				"a peer could jail the operator's own network", k)
		}
	}
	for _, k := range []string{
		"2001:db8:2::/64", // a different /64
		"203.0.113.0/24",  // an unrelated network
		"198.51.101.0/24", // adjacent, not overlapping
		"not-an-address/24",
		"",
	} {
		if r.AllowsAny(k) {
			t.Errorf("%q was treated as covered by the allow list — an attacker in an unrelated "+
				"network would then be immune to every fleet-wide jail", k)
		}
	}
	// An empty allow list protects nothing, or a single-node install with no
	// policy would refuse every inbound verdict.
	var empty policy.Rules
	if empty.AllowsAny("2001:db8::/64") {
		t.Error("an empty allow list reported coverage, so an install with no policy would " +
			"silently discard every verdict its peers sent")
	}
}

// TestAnObserveOnlyInstallNeverPushesEnforcementToPeers.
//
// Observe mode's promise is that NOTHING enforces. A node in observe mode that
// still pushed jail verdicts would be enforcing — on its peers, in a mode whose
// entire point is that the operator is measuring rather than acting, and in the
// one place the panel of the observing install would never show it.
func TestAnObserveOnlyInstallNeverPushesEnforcementToPeers(t *testing.T) {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("test-secret")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "198.51.100.9:1234" },
	})
	m.ApplySettings(Settings{
		Enabled: true, ObserveOnly: true, AutoBlock: true, AutoBlockJailMinutes: 10,
		RateLimit: true, RatePerMinute: 1, Burst: 1,
	})
	if err := m.JoinCluster("edge-1", "install-secret", []string{"http://127.0.0.1:1"}); err != nil {
		t.Fatalf("join: %v", err)
	}

	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for i := 0; i < 40; i++ {
		req := httptest.NewRequest(http.MethodGet, "/scan/x", nil)
		req.Header.Set("User-Agent", "curl/8.0")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	if queued := m.gossipPush.Pending(); queued > 0 {
		t.Errorf("an observe-only node queued %d verdicts for its peers. Observe mode promises "+
			"that nothing enforces; pushing jails to other nodes is enforcement, carried out "+
			"where the observing operator's own panel would never show it", queued)
	}
}

// TestObserveModeStillPardonsAcrossTheFleet — the observe-mode suppression must
// not swallow the one verdict that only ever restores access. Withholding a
// pardon would let observe mode make a peer's false positive last LONGER than it
// otherwise would, which is the opposite of what the mode is for.
func TestObserveModeStillPardonsAcrossTheFleet(t *testing.T) {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("test-secret")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "198.51.100.9:1234" },
	})
	m.ApplySettings(Settings{Enabled: true, ObserveOnly: true})
	if err := m.JoinCluster("edge-1", "install-secret", []string{"http://127.0.0.1:1"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	m.RewardProof(httptest.NewRequest(http.MethodGet, "/", nil))
	if m.gossipPush.Pending() == 0 {
		t.Error("an observe-only node withheld a pardon from its peers. Observe mode suppresses " +
			"enforcement; a pardon is not enforcement, and holding it back makes a false " +
			"positive elsewhere last longer than it would have")
	}
}

// TestAChallengedCountryNeverDegradesIntoARefusal.
//
// The whole reason this verdict exists is that refusing a country and ignoring
// it were the only two options.
//
// The first version of this test asserted on the FIRST response and was useless:
// a mutation that replaced the challenge with the deny path still passed, because
// the deny path is deliberately generous to a first request — it offers the same
// solvable challenge while the per-source redeem budget lasts. The two only part
// company under sustained traffic, where a refused source exhausts that budget
// and starts getting throttled while a challenged one keeps being offered the
// puzzle. So that is what this measures.
func TestAChallengedCountryNeverDegradesIntoARefusal(t *testing.T) {
	m := New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("test-secret")),
		Now:       time.Now,
		ClientIP:  func(r *http.Request) string { return "198.51.100.9:1234" },
		CountryFn: func(*http.Request, string) string { return "CN" },
	})
	m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		Policy: policy.Config{ChallengeCountries: []string{"CN"}},
	})

	served := 0
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served++ }))

	challenged, other := 0, 0
	var firstOther int
	for i := 0; i < 60; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/asset/app.css", nil)
		req.Header.Set("User-Agent", realBrowserUA)
		h.ServeHTTP(rec, req)
		if rec.Header().Get("X-VayuShield") == "challenge" {
			challenged++
		} else {
			other++
			if firstOther == 0 {
				firstOther = rec.Code
			}
		}
	}
	if served != 0 {
		t.Errorf("%d requests from a challenged country reached the handler unchallenged", served)
	}
	if other != 0 {
		t.Errorf("%d of 60 requests stopped being offered a puzzle (first was %d). A challenged "+
			"country must never degrade into a refusal — that is the deny rule wearing a "+
			"different name, and it is the outcome this verdict exists to avoid",
			other, firstOther)
	}
	if challenged != 60 {
		t.Errorf("only %d of 60 requests got the solvable interstitial", challenged)
	}
	if n := m.WouldHave()[GateGeoChallenge]; n != 0 {
		t.Errorf("the observe counter moved while enforcing (%d)", n)
	}
}

// TestAVerifiedReaderFromAChallengedCountryIsAskedOnce — the check has to be
// worth solving. If a solved session did not carry, every page view would be
// another puzzle and the country rule would be a wall with extra steps.
func TestAVerifiedReaderFromAChallengedCountryIsAskedOnce(t *testing.T) {
	m := New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("test-secret")),
		Now:       time.Now,
		ClientIP:  func(r *http.Request) string { return "198.51.100.9:1234" },
		CountryFn: func(*http.Request, string) string { return "CN" },
	})
	m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		Policy: policy.Config{ChallengeCountries: []string{"CN"}},
	})

	served := false
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }))

	tok, err := m.cfg.Signer.IssueSession(m.cfg.SessionTTL, "198.51.100.9")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.Header.Set("User-Agent", realBrowserUA)
	req.AddCookie(m.SessionCookie(tok))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !served {
		t.Error("a reader who had already solved the check was challenged again on their next " +
			"page view, which makes the country rule a wall with extra steps")
	}
}

// TestAChallengedCountryCannotDeIndexTheSite is the SEO obligation, and it
// matters more for this rule than for any other in the shield: a crawler pool is
// spread across countries, so an operator challenging one has no way to know
// which of their crawlers live there. A non-JS crawler cannot solve a PoW, and
// Google reads sustained non-200s on a content URL as a crawl error.
func TestAChallengedCountryCannotDeIndexTheSite(t *testing.T) {
	m := New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("test-secret")),
		Now:       time.Now,
		ClientIP:  func(r *http.Request) string { return "198.51.100.9:1234" },
		CountryFn: func(*http.Request, string) string { return "CN" },
	})
	m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		Policy: policy.Config{ChallengeCountries: []string{"CN"}},
	})

	served := false
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/article/indexed-page", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	h.ServeHTTP(rec, req)

	if !served || rec.Code == http.StatusServiceUnavailable {
		t.Errorf("a search-engine crawler was challenged by a country rule (served=%v code=%d). "+
			"It cannot run the solver, so this is a sustained non-200 on a content URL — the "+
			"exact mechanism that drops pages from the index", served, rec.Code)
	}
}
