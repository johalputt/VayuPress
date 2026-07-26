// SPDX-License-Identifier: Apache-2.0

package verifiedbot

import "strings"

// Class is the crawler family, matching botdb's persisted classification values
// so callers can map a verified bot straight onto the existing analytics buckets.
type Class string

const (
	ClassGoodBot Class = "good_bot" // search engines, monitors, link-preview/unfurl
	ClassAIAgent Class = "ai_agent" // AI crawlers and user-initiated AI fetchers
)

// tier is how a vendor's identity can be verified.
type tier int

const (
	// tierFeed: the vendor publishes machine-readable IP-range JSON. An IP in the
	// feed is cryptographically-strong "verified"; an IP that claims this vendor's
	// UA but is NOT in the feed (and fails any DNS backstop) is a spoof suspect.
	tierFeed tier = iota
	// tierFCrDNS: no public IP feed — identity is confirmed by forward-confirmed
	// reverse DNS (reverse-lookup the IP, require the vendor's PTR suffix, then
	// forward-resolve back to the same IP).
	tierFCrDNS
	// tierUAOnly: a recognised, benign crawler with no published feed and no
	// documented FCrDNS suffix (many smaller search engines, link-preview and
	// uptime bots). It can never be spoof-checked, so it is always treated as
	// SEO-safe "allow on UA" — recognising it exists only to guarantee it is
	// never challenged, never to block anything.
	tierUAOnly
)

// vendorDef is one static, compiled-in verified-crawler definition. The IP data
// itself (for tierFeed) is loaded at runtime from feeds and swapped atomically;
// this struct only holds the immutable identity + where to fetch/verify.
type vendorDef struct {
	name      string
	class     Class
	tier      tier
	tokens    []string // lowercase UA substrings (any match); the robots.txt token
	feeds     []string // IP-range JSON URLs (tierFeed). Multiple = fetch all, union.
	ptrSuffix []string // FCrDNS hostname suffixes (tierFCrDNS, or feed backstop)
}

// registry is the authoritative list of crawlers VayuShield will fast-path. It
// is deliberately broad: every major search engine and AI system on the market,
// plus link-preview and uptime/PageSpeed bots, so a VayuPress site is never
// accidentally de-indexed or broken for a legitimate crawler. The big vendors
// that publish IP ranges are verified by IP (spoof-proof); the rest are
// recognised by UA and allowed (SEO-safe). AI-training-only robots tokens
// (Google-Extended, Applebot-Extended) are documented but make NO HTTP requests,
// so they are governed in robots.txt, not here.
//
// Order matters only for first-match: more-specific / feed-backed vendors are
// listed before broad ones. Google feed URLs poll both the 2026 names and the
// legacy names (whichever returns valid JSON wins — see feeds.go), because
// Google renamed the files and relocated the path in 2026.
var registry = []vendorDef{
	// ── Search engines with published IP feeds ─────────────────────────────────
	{
		name:  "Googlebot",
		class: ClassGoodBot,
		tier:  tierFeed,
		tokens: []string{
			"googlebot", "googlebot-image", "googlebot-news", "googlebot-video",
			"storebot-google", "google-inspectiontool", "googleother",
			"google-safety", "google-read-aloud", "google-site-verification",
			"feedfetcher-google", "googleweblight", "google-pagerenderer",
			"apis-google", "adsbot-google", "mediapartners-google", "google-agent",
			// PageSpeed Insights / Lighthouse — must always get the real page so
			// the operator's PageSpeed score reflects reality, not a challenge.
			"chrome-lighthouse", "google page speed", "pagespeed",
		},
		feeds: []string{
			"https://developers.google.com/crawling/ipranges/common-crawlers.json",
			"https://developers.google.com/crawling/ipranges/special-crawlers.json",
			"https://developers.google.com/crawling/ipranges/user-triggered-fetchers-google.json",
			// Legacy names (pre-2026 rename) — harmless to try; whichever 200s wins.
			"https://developers.google.com/static/crawling/ipranges/googlebot.json",
		},
		ptrSuffix: []string{".googlebot.com", ".google.com", ".googleusercontent.com"},
	},
	{
		name:      "Bingbot",
		class:     ClassGoodBot,
		tier:      tierFeed,
		tokens:    []string{"bingbot", "adidxbot", "msnbot", "msnbot-media", "bingpreview", "microsoftpreview"},
		feeds:     []string{"https://www.bing.com/toolbox/bingbot.json"},
		ptrSuffix: []string{".search.msn.com"},
	},
	{
		name:      "DuckDuckBot",
		class:     ClassGoodBot,
		tier:      tierFeed,
		tokens:    []string{"duckduckbot", "duckduckgo", "duckassistbot"},
		feeds:     []string{"https://duckduckgo.com/duckduckbot.json"},
		ptrSuffix: []string{".duckduckgo.com"},
	},
	{
		name:      "Applebot",
		class:     ClassGoodBot,
		tier:      tierFeed,
		tokens:    []string{"applebot"},
		feeds:     []string{"https://search.apple.com/applebot.json"},
		ptrSuffix: []string{".applebot.apple.com"},
	},

	// ── Search engines verified by reverse DNS (no public feed) ────────────────
	{
		name:      "YandexBot",
		class:     ClassGoodBot,
		tier:      tierFCrDNS,
		tokens:    []string{"yandexbot", "yandeximages", "yandexvideo", "yandexmedia", "yandexmobilebot", "yandex.com/bots"},
		ptrSuffix: []string{".yandex.ru", ".yandex.net", ".yandex.com"},
	},
	{
		name:      "Baiduspider",
		class:     ClassGoodBot,
		tier:      tierFCrDNS,
		tokens:    []string{"baiduspider"},
		ptrSuffix: []string{".crawl.baidu.com", ".baidu.com", ".baidu.jp"},
	},
	{
		name:      "PetalBot",
		class:     ClassGoodBot,
		tier:      tierFCrDNS,
		tokens:    []string{"petalbot", "aspiegelbot"},
		ptrSuffix: []string{".aspiegel.com", ".petalsearch.com", ".huawei.com"},
	},
	{
		name:      "SeznamBot",
		class:     ClassGoodBot,
		tier:      tierFCrDNS,
		tokens:    []string{"seznambot", "seznam.cz/bot"},
		ptrSuffix: []string{".seznam.cz"},
	},

	// ── AI systems with published IP feeds ─────────────────────────────────────
	{
		name:  "OpenAI",
		class: ClassAIAgent,
		tier:  tierFeed,
		// GPTBot (training), OAI-SearchBot (search index), ChatGPT-User (live fetch).
		tokens: []string{"gptbot", "oai-searchbot", "chatgpt-user"},
		feeds: []string{
			"https://openai.com/gptbot.json",
			"https://openai.com/searchbot.json",
			"https://openai.com/chatgpt-user.json",
		},
	},
	{
		name:  "Anthropic",
		class: ClassAIAgent,
		tier:  tierFeed,
		// ClaudeBot (training), Claude-User (user fetch), Claude-SearchBot (search),
		// claude-code (Claude Code CLI), plus deprecated anthropic-ai / Claude-Web.
		tokens: []string{"claudebot", "claude-user", "claude-searchbot", "claude-web", "anthropic-ai", "claude-code"},
		feeds:  []string{"https://claude.com/crawling/bots.json"},
	},
	{
		name:   "Perplexity",
		class:  ClassAIAgent,
		tier:   tierFeed,
		tokens: []string{"perplexitybot", "perplexity-user"},
		feeds: []string{
			"https://www.perplexity.com/perplexitybot.json",
			"https://www.perplexity.com/perplexity-user.json",
		},
	},

	// ── AI systems verified by reverse DNS ─────────────────────────────────────
	{
		name:      "Amazonbot",
		class:     ClassAIAgent,
		tier:      tierFCrDNS,
		tokens:    []string{"amazonbot"},
		ptrSuffix: []string{".crawl.amazonbot.amazon", "amazonbot.amazon"},
	},

	// ── Recognised AI/search crawlers with no verification method (UA-only) ─────
	// These are allowed on UA so they are never challenged; they have no public
	// feed and no documented PTR suffix to spoof-check against.
	{name: "Bytespider", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"bytespider", "tiktokspider"}},
	{name: "CCBot", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"ccbot"}},
	{name: "Meta-AI", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"meta-externalagent", "meta-externalfetcher", "facebookbot"}},
	{name: "Cohere", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"cohere-ai", "cohere-training-data-crawler"}},
	{name: "YouBot", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"youbot"}},
	{name: "MistralAI", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"mistralai-user", "mistralai"}},
	{name: "Diffbot", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"diffbot"}},
	{name: "Timpibot", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"timpibot"}},
	{name: "ImagesiftBot", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"imagesiftbot"}},
	{name: "Webzio", class: ClassAIAgent, tier: tierUAOnly, tokens: []string{"omgili", "omgilibot", "webzio"}},

	{name: "BraveBot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"bravebot"}},
	{name: "Naver-Yeti", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"yeti", "naver.me/bot"}},
	{name: "Sogou", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"sogou"}},
	{name: "Qwant", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"qwantify", "qwantbot", "qwant"}},
	{name: "Mojeek", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"mojeekbot"}},
	{name: "Kagi", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"kagibot"}},
	{name: "CocCoc", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"coccocbot"}},

	// ── Link-preview / social unfurlers (must work for shared links) ───────────
	{name: "Twitterbot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"twitterbot"}},
	{name: "LinkedInBot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"linkedinbot"}},
	{name: "Slackbot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"slackbot", "slack-imgproxy"}},
	{name: "Discordbot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"discordbot"}},
	{name: "TelegramBot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"telegrambot"}},
	{name: "WhatsApp", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"whatsapp"}},
	{name: "FacebookExternalHit", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"facebookexternalhit"}},
	{name: "Pinterest", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"pinterest", "pinterestbot"}},
	{name: "RedditBot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"redditbot"}},
	{name: "Embedly", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"embedly"}},

	// ── Uptime / performance monitors (must never be blocked) ──────────────────
	{name: "UptimeRobot", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"uptimerobot"}},
	{name: "Pingdom", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"pingdom"}},
	{name: "GTmetrix", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"gtmetrix"}},
	{name: "StatusCake", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"statuscake"}},
	{name: "Site24x7", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"site24x7"}},
	{name: "BetterUptime", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"betteruptime", "better uptime"}},
	{name: "DatadogSynthetics", class: ClassGoodBot, tier: tierUAOnly, tokens: []string{"datadog"}},
}

// matchVendor returns the first registry vendor whose UA token is a substring of
// ua (case-insensitive), and whether one matched.
func matchVendor(ua string) (*vendorDef, bool) {
	if ua == "" {
		return nil, false
	}
	l := strings.ToLower(ua)
	for i := range registry {
		for _, tok := range registry[i].tokens {
			if strings.Contains(l, tok) {
				return &registry[i], true
			}
		}
	}
	return nil, false
}
