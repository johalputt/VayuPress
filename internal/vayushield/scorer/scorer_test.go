// SPDX-License-Identifier: Apache-2.0

package scorer

import (
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/behaviour"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
	"github.com/johalputt/vayupress/internal/vayushield/inspect"
)

// realBrowserUA is a current Chrome string, so the heuristic tests below are
// measuring the heuristic inputs rather than a User-Agent penalty.
const realBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func TestStaticBadBotScoresHigh(t *testing.T) {
	sig := botdb.Signature{Name: "python-requests", Classification: botdb.ClassBadBot}
	r := Score(Input{StaticMatch: &sig, Signals: fingerprint.Signals{UserAgent: "python-requests/2.31"}})
	if r.ClientType != botdb.TypeBadBot {
		t.Fatalf("want BadBot got %s", r.ClientType)
	}
	if r.BotScore < 0.9 {
		t.Fatalf("want high score got %.2f", r.BotScore)
	}
}

func TestStaticHeadlessType(t *testing.T) {
	sig := botdb.Signature{Name: "Headless Chrome", Classification: botdb.ClassBadBot}
	r := Score(Input{StaticMatch: &sig})
	if r.ClientType != botdb.TypeHeadless {
		t.Fatalf("want Headless got %s", r.ClientType)
	}
}

func TestGoodBotAllowedType(t *testing.T) {
	sig := botdb.Signature{Name: "Googlebot", Classification: botdb.ClassGoodBot}
	r := Score(Input{StaticMatch: &sig})
	if r.ClientType != botdb.TypeGoodBot {
		t.Fatalf("want GoodBot got %s", r.ClientType)
	}
}

func TestAIAgentType(t *testing.T) {
	sig := botdb.Signature{Name: "ClaudeBot", Classification: botdb.ClassAIAgent}
	r := Score(Input{StaticMatch: &sig})
	if r.ClientType != botdb.TypeAIAgent {
		t.Fatalf("want AIAgent got %s", r.ClientType)
	}
}

func TestRealChromeLeansHuman(t *testing.T) {
	s := fingerprint.Signals{
		UserAgent:              "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		Curves:                 []uint16{0x11ec, 29, 23}, // includes PQ
		HTTP2InitialWindowSize: 6291456,
	}
	r := Score(Input{Signals: s, HasTLS: true})
	if r.ClientType != botdb.TypeHuman {
		t.Fatalf("real Chrome should be Human, got %s (score %.2f, reasons %v)", r.ClientType, r.BotScore, r.Reasons)
	}
	if r.BotScore >= 0.4 {
		t.Fatalf("real Chrome score too high: %.2f", r.BotScore)
	}
}

func TestSpoofedChromeGoTransportIsHeadless(t *testing.T) {
	// Claims Chrome but HTTP/2 window == Go default → spoofed automation.
	s := fingerprint.Signals{
		UserAgent:              "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
		HTTP2InitialWindowSize: 65535,
	}
	r := Score(Input{Signals: s, HasTLS: true})
	if r.ClientType != botdb.TypeHeadless {
		t.Fatalf("spoofed Go transport should be Headless, got %s (%.2f, %v)", r.ClientType, r.BotScore, r.Reasons)
	}
	if r.BotScore < 0.7 {
		t.Fatalf("spoofed transport score too low: %.2f", r.BotScore)
	}
}

func TestNoUserAgentSuspicious(t *testing.T) {
	r := Score(Input{Signals: fingerprint.Signals{}})
	if r.BotScore < 0.6 {
		t.Fatalf("missing UA should be suspicious, got %.2f", r.BotScore)
	}
}

func TestLearnedVerifiedOverridesHeuristic(t *testing.T) {
	learned := &botdb.StoredSignature{Classification: botdb.ClassBadBot, OperatorVerified: true, Confidence: 0.99, BotName: "custom-scraper"}
	r := Score(Input{Signals: fingerprint.Signals{UserAgent: "Mozilla/5.0 Chrome/130 Safari/537.36"}, Learned: learned, HasTLS: true})
	if r.ClientType != botdb.TypeBadBot {
		t.Fatalf("learned verified bad bot should override, got %s", r.ClientType)
	}
	if r.BotName != "custom-scraper" {
		t.Fatalf("bot name not propagated: %q", r.BotName)
	}
}

func TestScoreClamped(t *testing.T) {
	r := Score(Input{Signals: fingerprint.Signals{UserAgent: "python-requests/2.31", HTTP2InitialWindowSize: 65535}})
	if r.BotScore < 0 || r.BotScore > 1 {
		t.Fatalf("score out of range: %.2f", r.BotScore)
	}
}

// TestLearnedSignatureCannotBlockRealBrowser reproduces the live incident that
// hard-blocked real Chrome and Brave visitors with "Access denied".
//
// The stored row was: classification=bad_bot, operator_verified=1,
// confidence=0.19, false_positive_count=4, user_agent_pattern=chrome. It got
// there honestly — confirmed once in the review queue at 0.99, then decayed 0.2
// per solved challenge (0.99→0.79→0.59→0.39→0.19) as real people proved they were
// human four times. But operator_verified short-circuited scoring regardless, and
// the bad-bot branch manufactured a 0.85 score, so every visitor on that browser
// build got a 403 that no leniency applied to. The database had recorded that it
// was wrong and kept convicting anyway.
func TestLearnedSignatureCannotBlockRealBrowser(t *testing.T) {
	poisoned := &botdb.StoredSignature{
		Classification:   botdb.ClassBadBot,
		OperatorVerified: true,
		Confidence:       0.19,
		FalsePositives:   4,
		UserAgentPattern: "chrome",
	}
	in := Input{
		Learned: poisoned,
		Signals: fingerprint.Signals{
			UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		},
	}
	got := Score(in)
	if got.ClientType == botdb.TypeBadBot {
		t.Errorf("a real browser must not be classified BadBot from this row (score %.2f, reasons %v)",
			got.BotScore, got.Reasons)
	}
	if got.Authoritative {
		t.Error("a disputed, collapsed-confidence row must never be authoritative")
	}
	if got.BotScore >= 0.8 {
		t.Errorf("score %.2f would land in the block band", got.BotScore)
	}

	// Each guard must hold on its own, so removing any one still protects.
	t.Run("browser family alone", func(t *testing.T) {
		l := &botdb.StoredSignature{Classification: botdb.ClassBadBot, OperatorVerified: true,
			Confidence: 0.99, UserAgentPattern: "chromium"}
		if learnedIsUsable(l) {
			t.Error("a bad-bot row for a browser fingerprint must never convict")
		}
	})
	t.Run("false positives alone", func(t *testing.T) {
		l := &botdb.StoredSignature{Classification: botdb.ClassBadBot, OperatorVerified: true,
			Confidence: 0.99, FalsePositives: 1, UserAgentPattern: "http-lib"}
		if learnedIsUsable(l) {
			t.Error("a disputed row must not convict on its own")
		}
	})
	t.Run("collapsed confidence alone", func(t *testing.T) {
		l := &botdb.StoredSignature{Classification: botdb.ClassBadBot, OperatorVerified: true,
			Confidence: 0.19, UserAgentPattern: "http-lib"}
		if learnedIsUsable(l) {
			t.Error("operator_verified must not outrank collapsed confidence")
		}
	})

	// A genuine scraper signature still convicts — the fix must not disarm the DB.
	t.Run("real bad bot still convicts", func(t *testing.T) {
		l := &botdb.StoredSignature{Classification: botdb.ClassBadBot, OperatorVerified: true,
			Confidence: 0.95, UserAgentPattern: "http-lib"}
		if !learnedIsUsable(l) {
			t.Error("a confident, undisputed non-browser bad-bot row must still convict")
		}
	})
	// Good-bot / human verdicts only ever widen access, so they stay usable.
	t.Run("good bot unaffected", func(t *testing.T) {
		l := &botdb.StoredSignature{Classification: botdb.ClassGoodBot, OperatorVerified: true,
			Confidence: 0.1, FalsePositives: 9, UserAgentPattern: "chrome"}
		if !learnedIsUsable(l) {
			t.Error("a good-bot row must remain usable — it only widens access")
		}
	})
}

// TestHeuristicsCannotReachAHardBlock is the bound that makes every heuristic
// input in this engine safe to ship, and it is written against the SUM rather
// than against any one source because the sum is what has gone wrong before.
//
// The behavioural scorer and a header-coherence signal each clamped themselves.
// Both bounds read correctly in their own file. Together they let a client
// accumulate past the hard-block threshold on heuristics alone — two bounded
// things are not a bounded thing. Adding request inspection recreated exactly
// that arithmetic: 0.35 + 0.30 on the 0.25 base is 0.90, past 0.80.
//
// So this test asserts the property, not the current numbers: whatever
// heuristic sources exist, and whatever each of them is bounded at, the most
// they can do together is reach a solvable challenge.
func TestHeuristicsCannotReachAHardBlock(t *testing.T) {
	const unknownStart, powThreshold, blockThreshold = 0.25, 0.4, 0.8

	// Every heuristic source pinned at its own maximum, simultaneously.
	in := Input{
		Signals:        fingerprint.Signals{UserAgent: realBrowserUA},
		BehaviourDelta: behaviour.MaxDelta,
		InspectDelta:   inspect.MaxDelta,
	}
	got := Score(in).BotScore
	if got >= blockThreshold {
		t.Errorf("every heuristic at maximum scores %v, at or past the %v block threshold — a "+
			"client can be hard-blocked on inference alone, with no signature and no operator "+
			"decision behind it", got, blockThreshold)
	}
	if got <= unknownStart {
		t.Errorf("every heuristic at maximum scores %v, no more than the %v an unknown client "+
			"starts at — the inputs are being discarded", got, unknownStart)
	}
	if got < powThreshold {
		t.Errorf("every heuristic at maximum reaches only %v, below the %v challenge threshold — "+
			"the heuristic inputs cannot change any outcome", got, powThreshold)
	}

	// And the clamp must be the thing holding it, not the individual bounds
	// happening to be small. A source that ignored its own cap must still not
	// break the ceiling.
	wild := Input{
		Signals:        fingerprint.Signals{UserAgent: realBrowserUA},
		BehaviourDelta: 10,
		InspectDelta:   10,
	}
	if got := Score(wild).BotScore; got >= blockThreshold {
		t.Errorf("an unbounded heuristic input reached %v — the budget is not being clamped at "+
			"the one place that can see every source", got)
	}
}

// TestAuthoritativeVerdictsIgnoreHeuristics — a compiled-in signature or one an
// operator verified reflects a person's judgement. Heuristics must not move it
// in either direction, or a sketch overrules the human who set it.
func TestAuthoritativeVerdictsIgnoreHeuristics(t *testing.T) {
	human := botdb.StoredSignature{Classification: botdb.ClassHuman, Confidence: 0.9}
	in := Input{
		Signals:        fingerprint.Signals{UserAgent: realBrowserUA},
		Learned:        &human,
		BehaviourDelta: behaviour.MaxDelta,
		InspectDelta:   inspect.MaxDelta,
	}
	if got := Score(in).BotScore; got > 0.25 {
		t.Errorf("a signature an operator verified as human scored %v once heuristics were "+
			"applied — inference is overruling the person who made the call", got)
	}
}
