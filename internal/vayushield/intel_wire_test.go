// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
	"github.com/johalputt/vayupress/internal/vayushield/intel"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
	"github.com/johalputt/vayupress/internal/vayushield/scorer"
)

// intel_wire_test.go — what happens to a request once a feed says something
// about its network.
//
// The intel package proves the lookup and the integrity controls; these tests
// prove the ORDERING, which is where a feed can do real damage. Every one of
// them is about precedence: what beats a feed, what a feed beats, and what a
// feed can never do no matter what it contains.

// intelManager builds a shield whose feed answers `kind` for `hostIP` and
// nothing for anything else.
func intelManager(t *testing.T, hostIP string, kind intel.Kind, opts ...func(*Config)) *Manager {
	t.Helper()
	cfg := Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return hostIP + ":4444" },
		IntelFn: func(ip string) (intel.Kind, string) {
			if ip == hostIP {
				return kind, "Test List"
			}
			return 0, ""
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	m := New(cfg)
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8})
	return m
}

// TestAHostileNetworkIsRefused is the baseline the rest of the file qualifies.
func TestAHostileNetworkIsRefused(t *testing.T) {
	m := intelManager(t, "198.51.100.10", intel.KindHostile)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("a listed hostile network must be refused with 403, got %d", rr.Code)
	}
	// The publisher is named. A visitor refused because of somebody else's file
	// can only act on that if they are told whose file it was.
	if !strings.Contains(rr.Body.String(), "Test List") {
		t.Error("the refusal page must name the list that made the claim")
	}
	// And it must not be a puzzle. This gate ignores sessions, so offering work
	// that cannot change the answer would be asking for it dishonestly.
	if rr.Body.Len() > 0 && strings.Contains(rr.Body.String(), "challenge.js") {
		t.Error("a hostile-list refusal must not offer a proof-of-work the visitor cannot benefit from")
	}
}

// TestADatacenterNetworkIsNotRefused is the tier distinction, and it is the one
// most likely to be lost in a refactor: both tiers arrive through the same hook,
// and treating them alike would refuse every VPN user on the internet.
func TestADatacenterNetworkIsNotRefused(t *testing.T) {
	m := intelManager(t, "198.51.100.11", intel.KindDatacenter)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusOK {
		t.Fatalf("a datacenter address must not be refused — it is evidence, not a verdict; got %d", rr.Code)
	}
}

// TestAVerifiedCrawlerOutranksAFeed is the SEO obligation, stated as an
// ordering: a hijacked feed must not be able to de-index the site.
func TestAVerifiedCrawlerOutranksAFeed(t *testing.T) {
	m := intelManager(t, "198.51.100.12", intel.KindHostile, func(c *Config) {
		c.VerifiedBotFn = func(netip.Addr, string) BotFastPath { return BotVerified }
	})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, crawlerReq("/post", googlebotUA))
	if rr.Code != http.StatusOK {
		t.Fatalf("a crawler whose identity is confirmed by network facts must be served even from a "+
			"listed network — otherwise a poisoned feed de-indexes the site; got %d", rr.Code)
	}
}

// TestTheOperatorsAllowListOutranksAFeed — a human wrote this down; the feed
// made a claim. When they disagree the human wins, and this is also the way an
// operator recovers from a bad listing without waiting for a publisher.
func TestTheOperatorsAllowListOutranksAFeed(t *testing.T) {
	m := intelManager(t, "198.51.100.13", intel.KindHostile)
	rules, _ := policy.Compile(policy.Config{AllowCIDRs: []string{"198.51.100.0/24"}})
	m.SetPolicy(rules)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusOK {
		t.Fatalf("an operator's allow entry must beat a third-party list, got %d", rr.Code)
	}
}

// TestASolvedChallengeDoesNotOutrankAFeed — the puzzle proves a browser, and
// "is this a browser" is not the objection a hostile listing raises. If a
// session bought passage here, every listed network would simply solve once.
func TestASolvedChallengeDoesNotOutrankAFeed(t *testing.T) {
	signer := challenge.NewSigner([]byte("s"))
	m := intelManager(t, "198.51.100.14", intel.KindHostile, func(c *Config) { c.Signer = signer })

	req := getBrowser("/post")
	tok, err := signer.IssueSession(m.cfg.SessionTTL, "198.51.100.14")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	req.AddCookie(m.SessionCookie(tok))

	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatal("a solved proof-of-work must not buy passage past a hostile listing")
	}
}

// TestTheOperatorIsNeverLockedOut — the one exemption, and the same guarantee
// TrustedFn carries at every other gate. An operator whose office egress lands
// on a published list must still be able to open their own site.
func TestTheOperatorIsNeverLockedOut(t *testing.T) {
	m := intelManager(t, "198.51.100.15", intel.KindHostile, func(c *Config) {
		c.TrustedFn = func(*http.Request) bool { return true }
	})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusOK {
		t.Fatalf("an operator with a valid admin session must never be locked out by a feed, got %d", rr.Code)
	}
}

// TestATorSpaceIgnoresFeedsEntirely — every peer there is 127.0.0.1, so a feed
// verdict is about the Tor daemon. The hazard is not that it is useless: a feed
// that ever grew an entry covering loopback would refuse the whole audience from
// a file nobody on this machine controls.
func TestATorSpaceIgnoresFeedsEntirely(t *testing.T) {
	m := intelManager(t, "127.0.0.1", intel.KindHostile, func(c *Config) { c.OnionMode = true })
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusOK {
		t.Fatalf("a Tor Space must not enforce a feed against loopback, got %d", rr.Code)
	}
	if hostile, _ := m.IntelHits(); hostile != 0 {
		t.Errorf("the lookup must not run at all in a Tor Space, got %d hostile hit(s)", hostile)
	}
}

// TestObserveModeCountsAFeedWithoutEnforcingIt — the whole point of observe mode
// is that a control can be evaluated before it is trusted, and a new source of
// denials is exactly the thing an operator wants to watch first.
func TestObserveModeCountsAFeedWithoutEnforcingIt(t *testing.T) {
	m := intelManager(t, "198.51.100.16", intel.KindHostile)
	m.ApplySettings(Settings{Enabled: true, ObserveOnly: true,
		PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusOK {
		t.Fatalf("observe mode must enforce nothing, got %d", rr.Code)
	}
	if got := m.WouldHave()[GateIntelDeny]; got == 0 {
		t.Error("observe mode must record the refusal it did not make")
	}
	if hostile, _ := m.IntelHits(); hostile == 0 {
		t.Error("the match itself is still counted in observe mode — that is what makes it observable")
	}
}

// TestADatacenterMatchCannotReachABlockOnItsOwn is the arithmetic that keeps the
// weakest signal weak.
//
// The scorer clamps every heuristic source against one shared budget, so this
// checks the number that actually matters: a featureless client from a
// datacenter reaches a puzzle at most, and cannot be walked to the 0.8 block
// threshold by the network signal alone.
func TestADatacenterMatchCannotReachABlockOnItsOwn(t *testing.T) {
	browser := fingerprint.Signals{UserAgent: realBrowserUA}
	base := scorer.Score(scorer.Input{Signals: browser})
	withDC := scorer.Score(scorer.Input{
		Signals:        browser,
		NetworkDelta:   intel.DatacenterDelta,
		NetworkReasons: []string{"datacenter or hosting network (Test List)"},
	})
	if withDC.BotScore <= base.BotScore {
		t.Fatalf("a datacenter match must raise the score: base %v, with %v", base.BotScore, withDC.BotScore)
	}
	if withDC.BotScore >= 0.8 {
		t.Fatalf("a datacenter match alone must never reach the block threshold, got %v", withDC.BotScore)
	}
	// And it must be explainable: an operator reviewing a challenge has to be
	// able to see that a third-party list is what moved the number.
	if !strings.Contains(strings.Join(withDC.Reasons, " "), "datacenter") {
		t.Errorf("the network signal must carry its reason, got %v", withDC.Reasons)
	}
}

// TestEveryHeuristicSourceTogetherStaysBelowABlock is the regression that keeps
// the budget honest as sources are added.
//
// Two bounded things are not a bounded thing — that is how behaviour and
// inspection once summed past the block threshold. Network intelligence is the
// third source, so the sum is checked here rather than trusted.
func TestEveryHeuristicSourceTogetherStaysBelowABlock(t *testing.T) {
	r := scorer.Score(scorer.Input{
		Signals:        fingerprint.Signals{UserAgent: realBrowserUA},
		BehaviourDelta: 1, BehaviourReasons: []string{"b"},
		InspectDelta: 1, InspectReasons: []string{"i"},
		NetworkDelta: 1, NetworkReasons: []string{"n"},
	})
	if r.BotScore >= 0.8 {
		t.Fatalf("every heuristic source at maximum must still stay below a hard block, got %v — "+
			"heuristics reach a puzzle, only evidence reaches a wall", r.BotScore)
	}
}
