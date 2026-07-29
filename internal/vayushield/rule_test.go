// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"strings"
	"testing"
)

// The point of the Rule registry is that "the next rule forgets an obligation"
// becomes a failing test rather than a review miss. These are the tests that
// make that true — without them the registry is documentation, and documentation
// is exactly what was already being forgotten.

// TestEveryGateDeclaresItsObligations — a gate with no Rule is a gate whose
// behaviour in Tor mode, effect on a verified crawler, lifetime and amnesty
// coverage nobody has stated. Adding one must not be possible silently.
func TestEveryGateDeclaresItsObligations(t *testing.T) {
	byGate := map[Gate]Rule{}
	for _, r := range rules {
		if _, dup := byGate[r.Gate]; dup {
			t.Errorf("gate %q is declared twice — one of the declarations is being ignored", r.Name)
		}
		byGate[r.Gate] = r
	}
	for g := Gate(0); g < gateCount; g++ {
		r, ok := byGate[g]
		if !ok {
			t.Errorf("gate %q (%d) has no declared Rule — nothing states what it does in a Tor "+
				"Space, what it does to a verified crawler, how its effect ends, or whether "+
				"amnesty covers it", GateNames[g], g)
			continue
		}
		if strings.TrimSpace(r.Name) == "" {
			t.Errorf("gate %d has a Rule with no name", g)
		}
		if strings.TrimSpace(r.Rationale) == "" {
			t.Errorf("rule %q states no rationale — the obligations are choices, and a choice "+
				"with no stated reason cannot be reviewed when it is later questioned", r.Name)
		}
		if !r.Complete() {
			t.Errorf("rule %q leaves an obligation unanswered. The zero value of each policy is "+
				"deliberately not a valid answer, so an omitted field is caught here instead of "+
				"silently defaulting to whatever the first constant happens to be", r.Name)
		}
	}
	if len(rules) != int(gateCount) {
		t.Errorf("%d rules for %d gates — the registry and the enum have drifted", len(rules), gateCount)
	}
}

// TestNoRuleGatesAVerifiedCrawlerWithoutSayingWhy — a search engine reads
// sustained non-200s on a content URL as a crawl error and drops the page, so a
// rule that refuses a confirmed crawler is a rule that de-indexes the site. That
// is allowed, but only as an explicit decision with its reasoning attached.
func TestNoRuleGatesAVerifiedCrawlerWithoutSayingWhy(t *testing.T) {
	for _, r := range rules {
		if r.Crawler == CrawlerGated && !strings.Contains(strings.ToLower(r.Rationale), "index") {
			t.Errorf("rule %q gates verified crawlers but its rationale does not address the "+
				"indexing consequence", r.Name)
		}
	}
}

// TestTorInertRulesAreTheOnesKeyedOnASource — in a Tor Space every peer is
// 127.0.0.1. A rule keyed on the client address is therefore measuring the Tor
// daemon and would shed or throttle the entire audience as one heavy hitter.
// Those rules must declare themselves inert; the ones that key on something else
// must not, or a Tor Space silently loses defences it could have had.
func TestTorInertRulesAreTheOnesKeyedOnASource(t *testing.T) {
	wantInert := map[Gate]bool{
		GateFairShed:  true, // sketch keyed on source + network group
		GateRateLimit: true, // per-source token bucket
		GateSurge:     true, // needs a PoW solver the plain-http onion cannot run
		GateChallenge: true, // same solver problem
	}
	for _, r := range rules {
		inert := r.Onion == OnionInert
		if want := wantInert[r.Gate]; inert != want {
			t.Errorf("rule %q: OnionInert=%v, want %v", r.Name, inert, want)
		}
	}
	// And the panel must be able to say which ones those are, rather than leaving
	// an operator to assume the shield behaves identically in both worlds.
	names := GatesInertInTorMode()
	if len(names) != len(wantInert) {
		t.Errorf("GatesInertInTorMode() returned %v, want %d entries", names, len(wantInert))
	}
}

// TestAmnestyWalksTheRegistry is the obligation that was actually broken before
// this file existed. The old ReleaseAllSentences named the brain and the
// blocklist explicitly, so a future rule holding its own durable state would have
// been missed — leaving the operator a one-click control that quietly did not
// cover everything it appeared to.
func TestAmnestyWalksTheRegistry(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{Enabled: true, AutoBlock: true, AutoBlockJailMinutes: 10})

	m.blocklist.Block("203.0.113.1", m.jailTTL())
	m.blocklist.Block("203.0.113.2", m.jailTTL())
	if !m.blocklist.Blocked("203.0.113.1") {
		t.Fatal("setup failed: the source was not jailed")
	}

	if n := m.ReleaseAllSentences(); n < 2 {
		t.Errorf("amnesty released %d sources, want at least the 2 jailed", n)
	}
	if m.blocklist.Blocked("203.0.113.1") {
		t.Error("a jailed source survived amnesty")
	}

	// Every rule whose effect is durable must contribute an Amnesty func, or the
	// control silently under-delivers.
	for _, r := range rules {
		if r.Lifetime == ExplicitlyPermanent && r.Amnesty == nil {
			t.Errorf("rule %q is explicitly permanent and has no amnesty — an operator has no "+
				"way to lift it", r.Name)
		}
	}
}

// TestAmnestySurvivesAnEmptyShield — the operator can press this at any time,
// including on an install that has never jailed anyone.
func TestAmnestySurvivesAnEmptyShield(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{Enabled: true})
	if n := m.ReleaseAllSentences(); n != 0 {
		t.Errorf("amnesty on a shield with no sentences released %d", n)
	}
}

// TestRuleStringNamesTheExceptions — the panel and the boot log render this, and
// the whole reason to render it is that a rule which is inert, or which gates
// crawlers, or which never expires, is the one an operator needs to know about.
func TestRuleStringNamesTheExceptions(t *testing.T) {
	got := Rule{Name: "x", Onion: OnionInert, Crawler: CrawlerGated, Lifetime: ExplicitlyPermanent}.String()
	for _, want := range []string{"inert in Tor mode", "gates verified crawlers", "permanent until lifted"} {
		if !strings.Contains(got, want) {
			t.Errorf("Rule.String() = %q, missing %q", got, want)
		}
	}
	plain := Rule{Name: "y", Onion: OnionActive, Crawler: CrawlerExempt, Lifetime: SelfExpiring}.String()
	if plain != "y" {
		t.Errorf("a rule with no exceptions rendered as %q, want just its name", plain)
	}

	// The zero value must not read as a complete set of answers. This is the
	// assertion that caught the original design: OnionInert was iota 0, so an
	// omitted field silently meant "never acts in a Tor Space".
	if (Rule{Name: "z", Rationale: "r"}).Complete() {
		t.Error("a Rule with every policy left at its zero value reported itself complete — " +
			"the type is answering its own questions")
	}
}
