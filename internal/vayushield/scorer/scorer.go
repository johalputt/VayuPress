// SPDX-License-Identifier: Apache-2.0

// Package scorer combines every VayuShield signal — static bot database match,
// learned adaptive-database match, TLS/JA fingerprint, post-quantum key share,
// HTTP/2 SETTINGS and header order, and User-Agent shape — into a single,
// deterministic composite BotScore (0.0 = definitely human, 1.0 = definitely
// bot) and a ClientType classification (Human, GoodBot, BadBot, AIAgent,
// Headless, Unknown).
//
// The model is intentionally explainable: every point of score carries a reason
// string, so an operator reviewing a block understands exactly which signals
// fired. It is pure and dependency-light (only fingerprint + botdb), so it is
// fully unit-testable without a database or a network.
package scorer

import (
	"math"
	"strings"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
)

// Input bundles everything the scorer needs about one request.
type Input struct {
	Signals     fingerprint.Signals
	Composite   fingerprint.Composite
	StaticMatch *botdb.Signature       // static UA match, or nil
	Learned     *botdb.StoredSignature // adaptive-DB match by fingerprint, or nil
	HasTLS      bool                   // whether TLS ClientHello signals were captured

	// BehaviourDelta is the bounded contribution of the behavioural signals — how
	// the client has ACTED, rather than what it claims to be. Applied only to the
	// heuristic path below: an authoritative verdict (a compiled-in signature, or
	// one the operator verified) already reflects a human's judgement, and moving
	// it on a sketch would let heuristics override the person who set it.
	BehaviourDelta   float64
	BehaviourReasons []string

	// InspectDelta is the bounded contribution of the compiled-in request
	// inspection — a probe for software this binary is not, a traversal shape, an
	// injection-shaped query. Same restriction as BehaviourDelta and for the same
	// reason: it is evidence about an unknown client, not grounds to overrule a
	// signature a person decided on.
	InspectDelta   float64
	InspectReasons []string

	// NetworkDelta is the bounded contribution of third-party network
	// intelligence — currently only "this address belongs to a datacenter".
	//
	// It is the weakest of the three and deliberately so. Datacenter membership
	// is a fact about the network, not about the visitor: a VPN exit, a corporate
	// egress and a scraper all live there, and the first two are people. So it
	// buys a puzzle at most, and it can only arrive through this field — the
	// hostile tier never reaches the scorer at all, because a verdict that strong
	// belongs at a gate where an operator can see it fire, not blended into a
	// number.
	NetworkDelta   float64
	NetworkReasons []string
}

// HeuristicBudget caps the COMBINED contribution of every heuristic input, and
// it is one number rather than one per source on purpose.
//
// This has already gone wrong once. The behavioural scorer and a header-coherence
// signal each clamped themselves, each bound looked correct in its own file, and
// together they let a client accumulate enough to cross the hard-block threshold
// on heuristics alone — two bounded things are not a bounded thing. Adding
// request inspection as a second source recreates that exact arithmetic:
// behaviour's 0.35 plus inspection's 0.3 on the 0.25 base is 0.90, past the 0.8
// block threshold.
//
// So the sum is clamped here, once, at the only place that can see all of them.
// With the shipped defaults it takes an unknown client to at most 0.70: well past
// the 0.4 challenge threshold, and short of a block. Heuristics reach a puzzle;
// only evidence reaches a wall.
//
// Network intelligence became the third source and did not need the number
// raised — which is the point of clamping the sum rather than the parts. Adding a
// signal costs the others some headroom instead of quietly extending the total,
// so no future input can walk the ceiling up one plausible increment at a time.
const HeuristicBudget = 0.45

// Result is the scorer's verdict.
type Result struct {
	BotScore       float64              `json:"bot_score"`
	ClientType     botdb.ClientType     `json:"client_type"`
	Classification botdb.Classification `json:"classification"`
	BotName        string               `json:"bot_name,omitempty"`
	Reasons        []string             `json:"reasons,omitempty"`

	// Authoritative marks a verdict that came from a source a human stands behind:
	// a compiled-in static signature, or a learned signature the OPERATOR verified.
	// It is false for an auto-learned guess and for pure heuristic scoring.
	//
	// This distinction exists because auto-learning can be wrong in a way that
	// compounds: a false-positive block records the visitor's fingerprint as a bad
	// bot, and each repeat raises its confidence until it crosses the threshold
	// where the scorer treats it as identified — at which point real people sharing
	// that fingerprint (every user of a given browser build) are hard-blocked
	// forever. Callers use this to decide how much benefit of the doubt a client is
	// owed, so an unproven GUESS can never become a permanent 403.
	Authoritative bool `json:"authoritative,omitempty"`
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	// Round to 2 dp for stable comparisons/storage.
	return math.Round(f*100) / 100
}

// Score computes the composite verdict. Precedence:
//  1. A static bad/good/AI signature match (fast, authoritative for known UAs).
//  2. A high-confidence operator-verified learned signature.
//  3. Heuristic transport/UA scoring for everything unknown.
func Score(in Input) Result {
	// 1) Static, compiled-in classification (known bots by User-Agent).
	if in.StaticMatch != nil {
		switch in.StaticMatch.Classification {
		case botdb.ClassGoodBot:
			return Result{BotScore: 0.7, ClientType: botdb.TypeGoodBot, Classification: botdb.ClassGoodBot,
				BotName: in.StaticMatch.Name, Authoritative: true,
				Reasons: []string{"static good-bot signature: " + in.StaticMatch.Name}}
		case botdb.ClassAIAgent:
			return Result{BotScore: 0.7, ClientType: botdb.TypeAIAgent, Classification: botdb.ClassAIAgent,
				BotName: in.StaticMatch.Name, Authoritative: true,
				Reasons: []string{"static AI-agent signature: " + in.StaticMatch.Name}}
		case botdb.ClassBadBot:
			ct := botdb.TypeBadBot
			name := strings.ToLower(in.StaticMatch.Name)
			if strings.Contains(name, "headless") || strings.Contains(name, "puppeteer") || strings.Contains(name, "playwright") || strings.Contains(name, "selenium") || strings.Contains(name, "phantom") {
				ct = botdb.TypeHeadless
			}
			return Result{BotScore: 0.95, ClientType: ct, Classification: botdb.ClassBadBot,
				BotName: in.StaticMatch.Name, Authoritative: true,
				Reasons: []string{"static bad-bot signature: " + in.StaticMatch.Name}}
		}
	}

	// 2) Learned adaptive database (operator-verified or high confidence).
	if in.Learned != nil && learnedIsUsable(in.Learned) {
		// Only an operator-verified signature is authoritative. A signature that
		// merely auto-promoted itself past the confidence threshold is still a
		// guess, and callers must be able to tell the difference.
		vetted := in.Learned.OperatorVerified
		switch in.Learned.Classification {
		case botdb.ClassGoodBot:
			return Result{BotScore: 0.7, ClientType: botdb.TypeGoodBot, Classification: botdb.ClassGoodBot,
				BotName: in.Learned.BotName, Authoritative: vetted,
				Reasons: []string{"learned good-bot signature"}}
		case botdb.ClassAIAgent:
			return Result{BotScore: 0.7, ClientType: botdb.TypeAIAgent, Classification: botdb.ClassAIAgent,
				BotName: in.Learned.BotName, Authoritative: vetted,
				Reasons: []string{"learned AI-agent signature"}}
		case botdb.ClassBadBot:
			return Result{BotScore: clamp01(math.Max(0.85, in.Learned.Confidence)), ClientType: botdb.TypeBadBot,
				Classification: botdb.ClassBadBot, BotName: in.Learned.BotName, Authoritative: vetted,
				Reasons: []string{"learned bad-bot signature (confidence " + ftoa(in.Learned.Confidence) + ")"}}
		case botdb.ClassHuman:
			return Result{BotScore: 0.05, ClientType: botdb.TypeHuman, Classification: botdb.ClassHuman,
				Reasons: []string{"learned human signature"}}
		}
	}

	// 3) Heuristic scoring for unknown clients.
	score := 0.25
	reasons := []string{}
	// Every heuristic source, summed and then clamped ONCE. See HeuristicBudget.
	if h := in.BehaviourDelta + in.InspectDelta + in.NetworkDelta; h != 0 {
		if h > HeuristicBudget {
			h = HeuristicBudget
		}
		score += h
		reasons = append(reasons, in.BehaviourReasons...)
		reasons = append(reasons, in.InspectReasons...)
		reasons = append(reasons, in.NetworkReasons...)
	}
	s := in.Signals
	fam := uaFamily(s.UserAgent)

	switch {
	case s.UserAgent == "":
		score += 0.45
		reasons = append(reasons, "no User-Agent header")
	case fam == "http-lib":
		score += 0.5
		reasons = append(reasons, "programmatic HTTP client User-Agent")
	case fam == "bot":
		score += 0.4
		reasons = append(reasons, "bot-like User-Agent token")
	}

	isBrowserUA := fam == "chrome" || fam == "firefox" || fam == "safari" || fam == "edge" || fam == "chromium"

	// Transport-vs-UA contradiction: the strongest spoofing tell. A UA that
	// claims a modern browser but whose HTTP/2 SETTINGS match Go's client
	// default is almost certainly an untuned Go scraper wearing a browser mask.
	//
	// DEAD in the standard deployment, and knowingly so. HTTP2InitialWindowSize
	// is only ever populated from a captured ClientHello, and nothing captures one
	// when a reverse proxy terminates TLS — see vayushield.Config.Capture. The
	// code stays because it is correct and costs one comparison; what would be
	// wrong is describing the shield as if this branch were running.
	headless := false
	if isBrowserUA && s.HTTP2InitialWindowSize != 0 && s.LooksLikeGoDefaultHTTP2() {
		score += 0.5
		headless = true
		reasons = append(reasons, "HTTP/2 SETTINGS match Go default despite browser User-Agent")
	}

	// Also dead behind a terminating proxy: HasTLS is set from a capture, or from
	// r.TLS on an in-process handshake this binary never performs.
	if in.HasTLS && isBrowserUA {
		if s.PostQuantum() {
			// A 2026 browser offering X25519MLKEM768 is a strong human signal.
			score -= 0.2
			reasons = append(reasons, "post-quantum key share present (X25519MLKEM768)")
		} else {
			score += 0.15
			reasons = append(reasons, "no post-quantum key share despite modern browser UA")
		}
		if s.LooksLikeBrowserHTTP2() {
			score -= 0.1
			reasons = append(reasons, "browser-like HTTP/2 window size")
		}
	}

	// A plausible browser UA with no contradicting transport evidence leans human.
	if isBrowserUA && !headless && score <= 0.4 {
		score -= 0.1
		reasons = append(reasons, "consistent browser User-Agent")
	}

	score = clamp01(score)

	// Classify by score bands.
	var ct botdb.ClientType
	var class botdb.Classification
	switch {
	case headless:
		ct, class = botdb.TypeHeadless, botdb.ClassBadBot
	case score >= 0.75:
		ct, class = botdb.TypeBadBot, botdb.ClassBadBot
	case score < 0.4:
		ct, class = botdb.TypeHuman, botdb.ClassHuman
	default:
		ct, class = botdb.TypeUnknown, botdb.ClassUnknown
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no distinguishing signals")
	}
	return Result{BotScore: score, ClientType: ct, Classification: class, Reasons: reasons}
}

// uaFamily mirrors the fingerprint package's coarse UA bucketing (kept local so
// scorer has no reliance on unexported fingerprint internals).
func uaFamily(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "":
		return "none"
	case strings.Contains(l, "edg/"):
		return "edge"
	case strings.Contains(l, "chrome/") && !strings.Contains(l, "chromium"):
		return "chrome"
	case strings.Contains(l, "chromium"):
		return "chromium"
	case strings.Contains(l, "firefox/"):
		return "firefox"
	case strings.Contains(l, "safari/") && !strings.Contains(l, "chrome"):
		return "safari"
	case strings.Contains(l, "python") || strings.Contains(l, "go-http") || strings.Contains(l, "okhttp") ||
		strings.Contains(l, "java/") || strings.Contains(l, "curl/") || strings.Contains(l, "wget/") ||
		strings.Contains(l, "libwww") || strings.Contains(l, "aiohttp"):
		return "http-lib"
	case strings.Contains(l, "bot") || strings.Contains(l, "crawler") || strings.Contains(l, "spider"):
		return "bot"
	default:
		return "other"
	}
}

func ftoa(f float64) string {
	// compact 2-dp formatting without importing strconv-heavy paths
	i := int(math.Round(f * 100))
	whole := i / 100
	frac := i % 100
	d := []byte{byte('0' + whole%10), '.', byte('0' + frac/10), byte('0' + frac%10)}
	return string(d)
}

// learnedBadBotFloor is the minimum confidence a learned signature must still
// carry before it is allowed to convict. A row whose confidence has fallen below
// this has been contradicted more than it has been confirmed.
const learnedBadBotFloor = 0.6

// learnedIsUsable decides whether a learned signature may short-circuit scoring.
//
// Three guards, each closing a way a stored row could refuse real people:
//
//  1. A BAD-BOT verdict is never taken for a mainstream-browser fingerprint.
//     That fingerprint is shared by everyone running the same browser build, so no
//     stored row — auto-learned, imported, or even operator-confirmed — should be
//     able to turn "this is Chrome" into a hard block. Falling through to the
//     heuristics scores a real browser as human, which is the honest answer.
//  2. A row with recorded false positives is disputed, so it no longer gets to
//     convict on its own.
//  3. A bad-bot verdict needs confidence still above the floor. Confidence decays
//     when a signature is contradicted; once it has collapsed, the database is
//     telling us it was wrong, and continuing to block on it ignores that. This
//     also stops operator_verified acting as a permanent override — a human's
//     one-time Confirm should not outrank the evidence that accumulated since.
//
// Good-bot / AI / human verdicts are unaffected: those only ever widen access.
func learnedIsUsable(l *botdb.StoredSignature) bool {
	usable := l.OperatorVerified || l.Confidence >= 0.8
	if !usable {
		return false
	}
	if l.Classification != botdb.ClassBadBot {
		return true
	}
	if isBrowserFamily(l.UserAgentPattern) {
		return false
	}
	if l.FalsePositives > 0 {
		return false
	}
	return l.Confidence >= learnedBadBotFloor
}

// isBrowserFamily reports whether a stored user_agent_pattern names a mainstream
// browser. The pattern is written by the coarse UA bucketing at learn time.
func isBrowserFamily(pattern string) bool {
	switch strings.ToLower(strings.TrimSpace(pattern)) {
	case "chrome", "chromium", "firefox", "safari", "edge":
		return true
	}
	return false
}
