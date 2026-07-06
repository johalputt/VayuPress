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
