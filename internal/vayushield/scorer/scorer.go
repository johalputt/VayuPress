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
}

// Result is the scorer's verdict.
type Result struct {
	BotScore       float64              `json:"bot_score"`
	ClientType     botdb.ClientType     `json:"client_type"`
	Classification botdb.Classification `json:"classification"`
	BotName        string               `json:"bot_name,omitempty"`
	Reasons        []string             `json:"reasons,omitempty"`
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
				BotName: in.StaticMatch.Name, Reasons: []string{"static good-bot signature: " + in.StaticMatch.Name}}
		case botdb.ClassAIAgent:
			return Result{BotScore: 0.7, ClientType: botdb.TypeAIAgent, Classification: botdb.ClassAIAgent,
				BotName: in.StaticMatch.Name, Reasons: []string{"static AI-agent signature: " + in.StaticMatch.Name}}
		case botdb.ClassBadBot:
			ct := botdb.TypeBadBot
			name := strings.ToLower(in.StaticMatch.Name)
			if strings.Contains(name, "headless") || strings.Contains(name, "puppeteer") || strings.Contains(name, "playwright") || strings.Contains(name, "selenium") || strings.Contains(name, "phantom") {
				ct = botdb.TypeHeadless
			}
			return Result{BotScore: 0.95, ClientType: ct, Classification: botdb.ClassBadBot,
				BotName: in.StaticMatch.Name, Reasons: []string{"static bad-bot signature: " + in.StaticMatch.Name}}
		}
	}

	// 2) Learned adaptive database (operator-verified or high confidence).
	if in.Learned != nil && (in.Learned.OperatorVerified || in.Learned.Confidence >= 0.8) {
		switch in.Learned.Classification {
		case botdb.ClassGoodBot:
			return Result{BotScore: 0.7, ClientType: botdb.TypeGoodBot, Classification: botdb.ClassGoodBot,
				BotName: in.Learned.BotName, Reasons: []string{"learned good-bot signature"}}
		case botdb.ClassAIAgent:
			return Result{BotScore: 0.7, ClientType: botdb.TypeAIAgent, Classification: botdb.ClassAIAgent,
				BotName: in.Learned.BotName, Reasons: []string{"learned AI-agent signature"}}
		case botdb.ClassBadBot:
			return Result{BotScore: clamp01(math.Max(0.85, in.Learned.Confidence)), ClientType: botdb.TypeBadBot,
				Classification: botdb.ClassBadBot, BotName: in.Learned.BotName,
				Reasons: []string{"learned bad-bot signature (confidence " + ftoa(in.Learned.Confidence) + ")"}}
		case botdb.ClassHuman:
			return Result{BotScore: 0.05, ClientType: botdb.TypeHuman, Classification: botdb.ClassHuman,
				Reasons: []string{"learned human signature"}}
		}
	}

	// 3) Heuristic scoring for unknown clients.
	var score float64 = 0.25
	reasons := []string{}
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
	headless := false
	if isBrowserUA && s.HTTP2InitialWindowSize != 0 && s.LooksLikeGoDefaultHTTP2() {
		score += 0.5
		headless = true
		reasons = append(reasons, "HTTP/2 SETTINGS match Go default despite browser User-Agent")
	}

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
