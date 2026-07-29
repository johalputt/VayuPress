// SPDX-License-Identifier: Apache-2.0

package vayushield

// rule.go — the enforcement contract.
//
// Every gate in this package that can refuse, delay or challenge a request has
// four obligations, and until now all four were conventions re-implemented by
// hand at each gate:
//
//  1. Inert in Tor mode where it would be wrong or useless there.
//  2. Never gate a crawler whose identity is confirmed by network facts.
//  3. Self-expiring, or explicitly and deliberately permanent.
//  4. Covered by the operator's one-click amnesty.
//
// A convention re-implemented by hand at eight sites is a convention that gets
// forgotten at the ninth, and the forgetting is silent: a rule that quietly
// enforces in a Tor Space, or that jails a search engine, or whose sentences
// outlive an amnesty, looks exactly like a rule that does none of those things
// until someone is affected by it.
//
// So the obligations are a TYPE. A Rule cannot be constructed without answering
// all four, the answers are asserted by test against the Gate enum, and amnesty
// walks the registry rather than a hand-maintained list. Adding a gate without
// declaring its obligations is a failing test, not a review miss.
//
// This deliberately does not re-plumb the middleware's hot path. The checks the
// gates already perform inline are correct; what was missing was any way to
// state, verify or enumerate them.

import "strings"

// OnionPolicy is what a rule does in a Tor Space (VAYUOS_MODE=tor).
type OnionPolicy uint8

const (
	// onionUnset is the zero value, and it is deliberately NOT a valid answer.
	//
	// The first version of this type made OnionInert the zero value, which meant
	// a Rule literal that simply omitted the field was silently recorded as
	// "never acts in a Tor Space" — the type answering its own question on the
	// author's behalf, which is the exact failure it exists to prevent. A test
	// caught it, and the lesson generalises: a contract whose zero value is a
	// valid answer is not a contract.
	onionUnset OnionPolicy = iota
	// OnionInert — the rule never acts in a Tor Space. The usual reason is that
	// its input is meaningless there: every peer is loopback, so a rule keyed on
	// the client address is measuring the Tor daemon.
	OnionInert
	// OnionActive — the rule acts, and Rationale says why that is still correct
	// when every request arrives from 127.0.0.1.
	OnionActive
)

// CrawlerPolicy is what a rule does to a crawler whose identity is confirmed by
// published IP ranges or forward-confirmed reverse DNS.
type CrawlerPolicy uint8

const (
	// crawlerUnset is the zero value and not a valid answer — see onionUnset.
	crawlerUnset CrawlerPolicy = iota
	// CrawlerExempt — a confirmed crawler is never gated by this rule. This is
	// the SEO obligation: a search engine reads sustained non-200s on a content
	// URL as a crawl error and drops the page, so a rule that refuses one is a
	// rule that de-indexes the site.
	CrawlerExempt
	// CrawlerGated — the rule applies even to a confirmed crawler, and Rationale
	// says why the indexing risk is worth it.
	CrawlerGated
)

// Lifetime is how a rule's effect ends.
type Lifetime uint8

const (
	// lifetimeUnset is the zero value and not a valid answer — see onionUnset.
	lifetimeUnset Lifetime = iota
	// SelfExpiring — the effect ends on its own, without operator action.
	SelfExpiring
	// ExplicitlyPermanent — the effect persists until an operator lifts it. Not
	// forbidden, but it has to be a decision someone made rather than a TTL
	// someone forgot.
	ExplicitlyPermanent
)

// Rule declares one enforcement point's obligations.
type Rule struct {
	Gate      Gate
	Name      string
	Onion     OnionPolicy
	Crawler   CrawlerPolicy
	Lifetime  Lifetime
	Rationale string

	// Amnesty releases whatever durable state this rule holds against sources,
	// and returns how many it freed. nil means the rule holds none — which is
	// itself an answer, and the test below requires it to be consistent with
	// Lifetime rather than merely absent.
	Amnesty func(m *Manager) int
}

// rules is the registry. Every Gate must appear exactly once; ruleFor and the
// tests in rule_test.go enforce that, so a new gate cannot be added without
// stating what it does in Tor mode, what it does to a verified crawler, how its
// effect ends, and what amnesty means for it.
var rules = []Rule{
	{
		Gate: GateBlocklist, Name: "blocklist",
		Onion: OnionActive, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Active in Tor mode: a jail keyed on loopback is nearly always a no-op there, " +
			"but the gate is the cheapest refusal in the pipeline and switching it off would mean " +
			"a Tor Space could not act on a verdict at all. Entries carry a TTL.",
		Amnesty: func(m *Manager) int { return m.blocklist.UnblockAll() },
	},
	{
		Gate: GateReputation, Name: "reputation jail",
		Onion: OnionActive, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Sentences decay toward neutral and a solved challenge pardons instantly, so a " +
			"mis-scored reader heals without operator action.",
		Amnesty: func(m *Manager) int { return m.brain.ReleaseAll() },
	},
	{
		Gate: GateLoadShed, Name: "load shed",
		Onion: OnionActive, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Keyed on total in-flight requests, not on any source, so it is as meaningful " +
			"behind Tor as anywhere. Its effect lasts exactly as long as the saturation does.",
	},
	{
		Gate: GateFairShed, Name: "fair shed",
		Onion: OnionInert, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Keyed on the source address and its network group. In a Tor Space every peer " +
			"is 127.0.0.1, so the sketch would measure the Tor daemon and shed the whole audience " +
			"as one heavy hitter.",
	},
	{
		Gate: GateRateLimit, Name: "rate limit",
		Onion: OnionInert, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Per-source token bucket. Same reason as fair shed: with every peer on loopback " +
			"the whole audience shares one bucket.",
	},
	{
		Gate: GateSurge, Name: "sovereign surge",
		Onion: OnionInert, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Surge answers with a proof-of-work interstitial, and a Tor Space is served over " +
			"plain-http .onion where window.crypto.subtle is undefined — the solver cannot run, so " +
			"NO visitor could ever pass. Enforcing it there is a total lockout, not a defence.",
	},
	{
		Gate: GateChallenge, Name: "challenge ladder",
		Onion: OnionInert, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "Same solver problem as surge. The non-crypto gates still enforce in Tor mode; " +
			"only the ones that require a browser to compute something fail open.",
	},
	{
		Gate: GateBlock, Name: "hard block",
		Onion: OnionActive, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "A confirmed bad actor is refused regardless of transport. The sentence it " +
			"installs expires, and a top-level navigation is softened to a solvable challenge first " +
			"so a single mis-scored browser is never simply shown a wall.",
		Amnesty: func(m *Manager) int { return 0 }, // shares the blocklist and brain, released above
	},
	{
		Gate: GatePolicyDeny, Name: "operator policy",
		Onion: OnionInert, Crawler: CrawlerGated, Lifetime: ExplicitlyPermanent,
		Rationale: "Gates verified crawlers on purpose. Every other rule here yields to a confirmed " +
			"search engine because the shield INFERRED something and could be wrong; this one is a " +
			"fact the operator wrote down. Refusing a crawler will de-index whatever it was reading, " +
			"and that is the operator's call to make about their own network list rather than one " +
			"the shield quietly overrides — the panel says so next to the field. Inert in a Tor " +
			"Space: every peer is 127.0.0.1, so a deny list refuses everyone or nobody, and an allow " +
			"entry as ordinary as 127.0.0.1 would match every visitor and hand each one the " +
			"operator-trusted bypass past every gate below. Permanent because it is configuration, " +
			"not a sentence; the operator lifts it by editing the list.",
		Amnesty: amnestyNotConfiguration,
	},
	{
		Gate: GateGeoDeny, Name: "operator geo policy",
		Onion: OnionInert, Crawler: CrawlerGated, Lifetime: ExplicitlyPermanent,
		Rationale: "Same standing as the address list, and the same indexing consequence, with one " +
			"extra hazard worth stating: a crawler pool is spread across countries, so refusing a " +
			"country can de-index a site through a crawler the operator never associated with it. " +
			"Inert in a Tor Space, where a country lookup returns the SERVER's own location for " +
			"every visitor — a rule built on that would refuse everyone or serve everyone while " +
			"appearing to do geography, which is worse than having no rule at all.",
		Amnesty: amnestyNotConfiguration,
	},
	{
		Gate: GateGeoChallenge, Name: "operator geo challenge",
		Onion: OnionInert, Crawler: CrawlerExempt, Lifetime: SelfExpiring,
		Rationale: "The softer half of the country rules, and the only one that is crawler-EXEMPT: " +
			"a refusal is a decision an operator makes about customers, while a challenge is a " +
			"claim that traffic is mostly automated — and a crawler pool is spread across " +
			"countries, so an operator challenging one has no way to know which of their crawlers " +
			"live there. Recognised crawlers take the SEO fast path before this gate. Self-" +
			"expiring because solving it issues a session, so a reader is asked once. Inert in a " +
			"Tor Space for two independent reasons: a country lookup returns the server's own " +
			"location for every visitor there, and the plain-http onion has no window.crypto." +
			"subtle, so no visitor could solve it even if the geography meant something.",
	},
}

// amnestyNotConfiguration is the amnesty answer for a rule the operator wrote
// themselves, and returning zero here is the behaviour rather than a stub.
//
// Amnesty lifts the shield's OWN verdicts — the jail, the reputation sentence,
// everything reached by inference — so that an operator who mis-tuned a
// threshold or ran a load test against their own site can undo the damage in one
// click. It must not touch the allow/deny lists, because those are not damage:
// silently emptying an operator's deny list because they pressed "release
// everyone" would serve exactly the networks they had decided not to serve, and
// they would have no reason to suspect it. A test pins this.
func amnestyNotConfiguration(*Manager) int { return 0 }

// Complete reports whether every obligation has been answered. The zero value of
// each policy is not an answer, so a Rule literal that omits a field fails here
// rather than inheriting a default nobody chose.
func (r Rule) Complete() bool {
	return r.Onion != onionUnset && r.Crawler != crawlerUnset &&
		r.Lifetime != lifetimeUnset && strings.TrimSpace(r.Name) != "" &&
		strings.TrimSpace(r.Rationale) != ""
}

// EnforcementRules returns the declared obligations for every gate, for the
// posture report and for operator-facing documentation. Returning a copy keeps
// a caller from editing the contract by accident.
func EnforcementRules() []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

// GatesInertInTorMode names the rules that deliberately do not enforce in a Tor
// Space, so the panel can say which defences are off there rather than leaving
// an operator to assume the shield behaves identically in both worlds.
func GatesInertInTorMode() []string {
	var out []string
	for _, r := range rules {
		if r.Onion == OnionInert {
			out = append(out, r.Name)
		}
	}
	return out
}

// ReleaseAllSentences lifts every active sentence and returns the number of
// sources released.
//
// It walks the rule registry rather than a hand-written list. That is the whole
// point of the registry: the previous version named the brain and the blocklist
// explicitly, so a future rule holding its own durable state would have been
// missed by amnesty — and the operator would have had a control that quietly
// did not cover everything it appeared to.
//
// Sentences self-expire but escalate to hours, so after a false-positive run —
// a load test, a proxy that made every reader look like one address, a threshold
// set too tight — an operator would otherwise wait out punishments aimed at
// their own readers. Verified sessions and the confirmed-crawler lane were never
// subject to sentences, so this only ever restores access.
func (m *Manager) ReleaseAllSentences() int {
	n := 0
	for _, r := range rules {
		if r.Amnesty != nil {
			n += r.Amnesty(m)
		}
	}
	return n
}

// String renders a rule for the panel and the boot log.
func (r Rule) String() string {
	var b strings.Builder
	b.WriteString(r.Name)
	if r.Onion == OnionInert {
		b.WriteString(" (inert in Tor mode)")
	}
	if r.Crawler == CrawlerGated {
		b.WriteString(" (gates verified crawlers)")
	}
	if r.Lifetime == ExplicitlyPermanent {
		b.WriteString(" (permanent until lifted)")
	}
	return b.String()
}
