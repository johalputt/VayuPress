// SPDX-License-Identifier: Apache-2.0

package scorer

import (
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
)

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
