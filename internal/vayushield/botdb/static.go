// Package botdb is VayuShield's bot classification database. It has two layers:
//
//   - A static knowledge base compiled into the binary (this file): known good
//     bots (search engines, monitors), bad bots (scrapers, scanners) and AI
//     agents (ChatGPT/Claude/Perplexity browsing on a user's behalf).
//   - A self-learning adaptive layer backed by SQLite (store.go): fingerprints
//     that recur with bot-like signals are learned as candidates, promoted by
//     confidence, reviewed by the operator, and shared with the community.
//
// Classification never blocks good bots or AI agents — they are counted and
// allowed. Only bad bots are challenged or blocked. The static base is the fast
// path; the adaptive layer catches what the static base does not yet know.
package botdb

import "strings"

// Classification is the persisted bot class stored in vayushield_signatures.
type Classification string

const (
	ClassHuman   Classification = "human"
	ClassGoodBot Classification = "good_bot"
	ClassBadBot  Classification = "bad_bot"
	ClassAIAgent Classification = "ai_agent"
	ClassUnknown Classification = "unknown"
)

// ClientType is the runtime client classification surfaced by the scorer/API.
type ClientType string

const (
	TypeHuman    ClientType = "Human"
	TypeGoodBot  ClientType = "GoodBot"
	TypeBadBot   ClientType = "BadBot"
	TypeAIAgent  ClientType = "AIAgent"
	TypeHeadless ClientType = "Headless"
	TypeUnknown  ClientType = "Unknown"
)

// Signature is one static, compiled-in bot definition.
type Signature struct {
	Name           string         // canonical bot name
	Classification Classification // good_bot | bad_bot | ai_agent
	UAPatterns     []string       // lowercase User-Agent substrings (any match)
	IPRangeHint    string         // human-readable expected network hint
}

// goodBots are indexed/allowed and counted separately — never blocked.
var goodBots = []Signature{
	{Name: "Googlebot", Classification: ClassGoodBot, UAPatterns: []string{"googlebot", "google-inspectiontool", "storebot-google", "googleother", "adsbot-google", "apis-google", "feedfetcher-google", "google-read-aloud", "mediapartners-google", "google-safety"}, IPRangeHint: "Google (66.249.0.0/16 et al.)"},
	{Name: "Bingbot", Classification: ClassGoodBot, UAPatterns: []string{"bingbot", "adidxbot", "msnbot", "microsoftpreview"}, IPRangeHint: "Microsoft (40.77.0.0/16 et al.)"},
	{Name: "DuckDuckBot", Classification: ClassGoodBot, UAPatterns: []string{"duckduckbot", "duckduckgo", "duckassistbot"}, IPRangeHint: "DuckDuckGo"},
	{Name: "BraveBot", Classification: ClassGoodBot, UAPatterns: []string{"bravebot"}, IPRangeHint: "Brave"},
	{Name: "YandexBot", Classification: ClassGoodBot, UAPatterns: []string{"yandexbot", "yandeximages", "yandexmobilebot", "yandex.com/bots"}, IPRangeHint: "Yandex (5.45.0.0/16 et al.)"},
	{Name: "Baiduspider", Classification: ClassGoodBot, UAPatterns: []string{"baiduspider"}, IPRangeHint: "Baidu"},
	{Name: "Applebot", Classification: ClassGoodBot, UAPatterns: []string{"applebot"}, IPRangeHint: "Apple (17.0.0.0/8)"},
	{Name: "PetalBot", Classification: ClassGoodBot, UAPatterns: []string{"petalbot", "aspiegelbot"}, IPRangeHint: "Petal/Huawei"},
	{Name: "SeznamBot", Classification: ClassGoodBot, UAPatterns: []string{"seznambot"}, IPRangeHint: "Seznam"},
	{Name: "Naver", Classification: ClassGoodBot, UAPatterns: []string{"yeti/"}, IPRangeHint: "Naver"},
	{Name: "Sogou", Classification: ClassGoodBot, UAPatterns: []string{"sogou"}, IPRangeHint: "Sogou"},
	{Name: "Qwant", Classification: ClassGoodBot, UAPatterns: []string{"qwantify", "qwantbot"}, IPRangeHint: "Qwant"},
	{Name: "Twitterbot", Classification: ClassGoodBot, UAPatterns: []string{"twitterbot"}, IPRangeHint: "X/Twitter"},
	{Name: "LinkedInBot", Classification: ClassGoodBot, UAPatterns: []string{"linkedinbot"}, IPRangeHint: "LinkedIn/Microsoft"},
	{Name: "Slackbot", Classification: ClassGoodBot, UAPatterns: []string{"slackbot", "slack-imgproxy"}, IPRangeHint: "Slack"},
	{Name: "Discordbot", Classification: ClassGoodBot, UAPatterns: []string{"discordbot"}, IPRangeHint: "Discord"},
	{Name: "TelegramBot", Classification: ClassGoodBot, UAPatterns: []string{"telegrambot"}, IPRangeHint: "Telegram"},
	{Name: "WhatsApp", Classification: ClassGoodBot, UAPatterns: []string{"whatsapp"}, IPRangeHint: "Meta/WhatsApp"},
	{Name: "Pinterest", Classification: ClassGoodBot, UAPatterns: []string{"pinterest"}, IPRangeHint: "Pinterest"},
	{Name: "RedditBot", Classification: ClassGoodBot, UAPatterns: []string{"redditbot"}, IPRangeHint: "Reddit"},
	{Name: "FacebookBot", Classification: ClassGoodBot, UAPatterns: []string{"facebookexternalhit", "facebookbot", "meta-externalagent"}, IPRangeHint: "Meta"},
	// Operator tooling — performance testers and uptime monitors. These are NOT
	// indexers; the crawler "go dark" switch must never 403 them (see
	// operatorToolBot), or the operator's own dashboards report the site as down
	// and PageSpeed scoring breaks.
	{Name: "UptimeRobot", Classification: ClassGoodBot, UAPatterns: []string{"uptimerobot"}, IPRangeHint: "UptimeRobot monitors"},
	{Name: "Pingdom", Classification: ClassGoodBot, UAPatterns: []string{"pingdom"}, IPRangeHint: "Pingdom monitors"},
	{Name: "GTmetrix", Classification: ClassGoodBot, UAPatterns: []string{"gtmetrix"}, IPRangeHint: "GTmetrix"},
	{Name: "StatusCake", Classification: ClassGoodBot, UAPatterns: []string{"statuscake"}, IPRangeHint: "StatusCake monitors"},
	{Name: "Site24x7", Classification: ClassGoodBot, UAPatterns: []string{"site24x7"}, IPRangeHint: "Site24x7 monitors"},
	{Name: "PageSpeed", Classification: ClassGoodBot, UAPatterns: []string{"pagespeed", "lighthouse", "chrome-lighthouse", "google page speed"}, IPRangeHint: "Google PageSpeed Insights"},
}

// aiAgents represent real readers arriving via an AI assistant browsing on their
// behalf, plus AI indexing crawlers. Tracked separately; never blocked.
var aiAgents = []Signature{
	{Name: "GPTBot", Classification: ClassAIAgent, UAPatterns: []string{"gptbot"}, IPRangeHint: "OpenAI"},
	{Name: "ChatGPT-User", Classification: ClassAIAgent, UAPatterns: []string{"chatgpt-user", "oai-searchbot"}, IPRangeHint: "OpenAI (user-initiated browsing)"},
	{Name: "ClaudeBot", Classification: ClassAIAgent, UAPatterns: []string{"claudebot", "anthropic-ai", "claude-searchbot"}, IPRangeHint: "Anthropic"},
	{Name: "Claude-User", Classification: ClassAIAgent, UAPatterns: []string{"claude-user", "claude-web", "claude-code"}, IPRangeHint: "Anthropic (user-initiated browsing)"},
	{Name: "PerplexityBot", Classification: ClassAIAgent, UAPatterns: []string{"perplexitybot", "perplexity-user"}, IPRangeHint: "Perplexity"},
	{Name: "Google-Extended", Classification: ClassAIAgent, UAPatterns: []string{"google-extended"}, IPRangeHint: "Google Gemini training"},
	{Name: "Bytespider", Classification: ClassAIAgent, UAPatterns: []string{"bytespider"}, IPRangeHint: "ByteDance"},
	{Name: "CCBot", Classification: ClassAIAgent, UAPatterns: []string{"ccbot"}, IPRangeHint: "Common Crawl"},
	{Name: "Amazonbot", Classification: ClassAIAgent, UAPatterns: []string{"amazonbot"}, IPRangeHint: "Amazon"},
	{Name: "Meta-ExternalFetcher", Classification: ClassAIAgent, UAPatterns: []string{"meta-externalfetcher"}, IPRangeHint: "Meta AI"},
	{Name: "Cohere", Classification: ClassAIAgent, UAPatterns: []string{"cohere-ai", "cohere-training-data-crawler"}, IPRangeHint: "Cohere"},
	{Name: "YouBot", Classification: ClassAIAgent, UAPatterns: []string{"youbot"}, IPRangeHint: "You.com"},
	{Name: "MistralAI", Classification: ClassAIAgent, UAPatterns: []string{"mistralai"}, IPRangeHint: "Mistral"},
	{Name: "Diffbot", Classification: ClassAIAgent, UAPatterns: []string{"diffbot"}, IPRangeHint: "Diffbot"},
}

// badBots are challenged or blocked: scraper frameworks, scanners, generic
// automation clients that identify themselves in the User-Agent.
var badBots = []Signature{
	{Name: "Scrapy", Classification: ClassBadBot, UAPatterns: []string{"scrapy"}},
	{Name: "Headless Chrome", Classification: ClassBadBot, UAPatterns: []string{"headlesschrome", "headless"}},
	{Name: "Puppeteer", Classification: ClassBadBot, UAPatterns: []string{"puppeteer"}},
	{Name: "Playwright", Classification: ClassBadBot, UAPatterns: []string{"playwright"}},
	{Name: "PhantomJS", Classification: ClassBadBot, UAPatterns: []string{"phantomjs"}},
	{Name: "Selenium", Classification: ClassBadBot, UAPatterns: []string{"selenium", "webdriver"}},
	{Name: "Nikto", Classification: ClassBadBot, UAPatterns: []string{"nikto"}},
	{Name: "sqlmap", Classification: ClassBadBot, UAPatterns: []string{"sqlmap"}},
	{Name: "Nuclei", Classification: ClassBadBot, UAPatterns: []string{"nuclei"}},
	{Name: "masscan", Classification: ClassBadBot, UAPatterns: []string{"masscan", "zgrab", "zmap"}},
	{Name: "WPScan", Classification: ClassBadBot, UAPatterns: []string{"wpscan"}},
	{Name: "Nmap", Classification: ClassBadBot, UAPatterns: []string{"nmap scripting engine"}},
	{Name: "Hydra", Classification: ClassBadBot, UAPatterns: []string{"hydra"}},
	{Name: "Go-http-client", Classification: ClassBadBot, UAPatterns: []string{"go-http-client"}},
	{Name: "python-requests", Classification: ClassBadBot, UAPatterns: []string{"python-requests", "python-urllib", "aiohttp"}},
	{Name: "libwww-perl", Classification: ClassBadBot, UAPatterns: []string{"libwww-perl", "lwp::simple"}},
	{Name: "Java", Classification: ClassBadBot, UAPatterns: []string{"java/1.", "apache-httpclient"}},
	{Name: "curl/wget", Classification: ClassBadBot, UAPatterns: []string{"curl/", "wget/"}},
}

// aiReferrerDomains maps a referrer host substring to the AI system name. A
// request whose referrer is one of these is an AI-assisted human visit.
var aiReferrerDomains = map[string]string{
	"chat.openai.com":       "ChatGPT",
	"chatgpt.com":           "ChatGPT",
	"claude.ai":             "Claude",
	"perplexity.ai":         "Perplexity",
	"copilot.microsoft.com": "Copilot",
	"gemini.google.com":     "Gemini",
	"bard.google.com":       "Gemini",
	"you.com":               "You.com",
	"phind.com":             "Phind",
	"poe.com":               "Poe",
}

// StaticDB is the compiled-in, in-memory classifier. It is immutable and safe
// for concurrent reads. Lookups are linear over a small (<100) list, which is
// faster than a map for this size and keeps the memory footprint negligible.
type StaticDB struct {
	all []Signature
}

// NewStaticDB builds the static classifier from the compiled-in signature lists.
func NewStaticDB() *StaticDB {
	all := make([]Signature, 0, len(goodBots)+len(aiAgents)+len(badBots))
	all = append(all, aiAgents...) // AI agents first: ChatGPT-User etc. before generic "bot"
	all = append(all, goodBots...)
	all = append(all, badBots...)
	return &StaticDB{all: all}
}

// MatchUA returns the first static signature whose UA pattern matches ua
// (case-insensitive), and whether one was found.
func (d *StaticDB) MatchUA(ua string) (Signature, bool) {
	l := strings.ToLower(ua)
	if l == "" {
		return Signature{}, false
	}
	for _, sig := range d.all {
		for _, p := range sig.UAPatterns {
			if strings.Contains(l, p) {
				return sig, true
			}
		}
	}
	return Signature{}, false
}

// MatchReferrerAI returns the AI system name if the referrer host indicates an
// AI-assisted visit (e.g. a user following a link from chatgpt.com).
func (d *StaticDB) MatchReferrerAI(referrerHost string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(referrerHost))
	if h == "" {
		return "", false
	}
	for dom, name := range aiReferrerDomains {
		if h == dom || strings.HasSuffix(h, "."+dom) || strings.Contains(h, dom) {
			return name, true
		}
	}
	return "", false
}
