// Package classifier assigns every VayuAnalytics session to exactly one traffic
// source category, including the category that is unique to VayuAnalytics:
// AI-assisted discovery (visitors arriving from ChatGPT, Claude, Perplexity,
// Copilot, Gemini, …). This lets publishers see how AI discovery compares to
// traditional search — data neither Google Analytics nor Plausible surfaces.
//
// Classification is purely referrer/UTM based and stores no PII. It is a pure
// function of its inputs, so it is fully unit-testable.
package classifier

import (
	"net/url"
	"strings"
)

// Category is the traffic source bucket.
type Category string

const (
	Organic    Category = "organic"     // search engines
	AIAssisted Category = "ai_assisted" // arriving via an AI assistant
	Direct     Category = "direct"      // no referrer / same-site
	Social     Category = "social"      // social platforms
	Corporate  Category = "corporate"   // Teams/Slack/Office CDNs — enterprise shares
	Email      Category = "email"       // webmail / UTM source=email
	Newsletter Category = "newsletter"  // UTM medium=newsletter / ESP
	Referral   Category = "referral"    // any other external site
	Bot        Category = "bot"         // classified as bot by VayuShield
)

// Result is the classification of one visit.
type Result struct {
	Category       Category `json:"source_category"`
	Detail         string   `json:"source_detail"`
	ReferrerDomain string   `json:"referrer_domain"`
	ReferrerPath   string   `json:"referrer_path"`
}

// UTM carries the campaign parameters parsed from the landing URL.
type UTM struct {
	Source   string
	Medium   string
	Campaign string
}

// searchEngines maps a referrer host substring to the engine's display name.
var searchEngines = map[string]string{
	"google.":          "Google",
	"bing.com":         "Bing",
	"duckduckgo.com":   "DuckDuckGo",
	"search.brave.com": "Brave",
	"kagi.com":         "Kagi",
	"yandex.":          "Yandex",
	"ecosia.org":       "Ecosia",
	"search.yahoo.com": "Yahoo",
	"baidu.com":        "Baidu",
	"startpage.com":    "Startpage",
	"qwant.com":        "Qwant",
}

// aiSystems maps an AI referrer host substring to the AI system name.
var aiSystems = map[string]string{
	"chatgpt.com":           "ChatGPT",
	"chat.openai.com":       "ChatGPT",
	"claude.ai":             "Claude",
	"perplexity.ai":         "Perplexity",
	"copilot.microsoft.com": "Copilot",
	"gemini.google.com":     "Gemini",
	"bard.google.com":       "Gemini",
	"you.com":               "You.com",
	"phind.com":             "Phind",
	"poe.com":               "Poe",
}

// socialPlatforms maps a social referrer host substring to the platform name.
var socialPlatforms = map[string]string{
	"facebook.com":         "Facebook",
	"fb.com":               "Facebook",
	"t.co":                 "X/Twitter",
	"twitter.com":          "X/Twitter",
	"x.com":                "X/Twitter",
	"linkedin.com":         "LinkedIn",
	"lnkd.in":              "LinkedIn",
	"reddit.com":           "Reddit",
	"instagram.com":        "Instagram",
	"youtube.com":          "YouTube",
	"pinterest.":           "Pinterest",
	"mastodon":             "Mastodon",
	"bsky.app":             "Bluesky",
	"tiktok.com":           "TikTok",
	"news.ycombinator.com": "Hacker News",
	"t.me":                 "Telegram",
	"whatsapp.com":         "WhatsApp",
}

// webmailHosts maps a webmail referrer host substring to the provider name.
var webmailHosts = map[string]string{
	"mail.google.com":    "Gmail",
	"outlook.live.com":   "Outlook",
	"outlook.office.com": "Outlook",
	"mail.yahoo.com":     "Yahoo Mail",
	"mail.proton.me":     "Proton Mail",
	"webmail":            "Webmail",
}

// corporateHosts maps enterprise CDN/app referrer substrings to a label — the
// signal that a link was shared inside a company (Teams, Slack, Office).
var corporateHosts = map[string]string{
	"teams.microsoft.com":          "Microsoft Teams",
	"teams.cdn":                    "Microsoft Teams",
	"statics.teams.cdn.office.net": "Microsoft Teams",
	"office.net":                   "Microsoft Office",
	"office.com":                   "Microsoft Office",
	"slack-edge.com":               "Slack",
	"slack.com":                    "Slack",
	"app.slack.com":                "Slack",
}

// Classify determines the traffic source for a visit. siteHost is the operator's
// own domain (used to detect same-site/internal referrers). isBot short-circuits
// to the Bot category so bot traffic never mixes with human analytics.
func Classify(referrer, siteHost string, utm UTM, isBot bool) Result {
	res := Result{}
	if isBot {
		res.Category = Bot
		res.Detail = "bot"
	}

	referrer = strings.TrimSpace(referrer)
	var host, path string
	if referrer != "" {
		if u, err := url.Parse(referrer); err == nil {
			host = strings.ToLower(u.Host)
			path = u.Path
		}
	}
	res.ReferrerDomain = host
	res.ReferrerPath = path

	if isBot {
		return res
	}

	// UTM overrides — explicit campaign tagging is the strongest signal.
	medium := strings.ToLower(strings.TrimSpace(utm.Medium))
	source := strings.ToLower(strings.TrimSpace(utm.Source))
	switch {
	case medium == "newsletter" || strings.Contains(source, "newsletter"):
		res.Category, res.Detail = Newsletter, campaignDetail(utm)
		return res
	case medium == "email" || source == "email":
		res.Category, res.Detail = Email, campaignDetail(utm)
		return res
	}

	// No referrer at all → direct (typed URL or app open).
	if host == "" {
		res.Category = Direct
		res.Detail = "typed"
		return res
	}

	// Same-site referrer → internal navigation (treated as direct).
	if siteHost != "" && (host == strings.ToLower(siteHost) || strings.HasSuffix(host, "."+strings.ToLower(siteHost))) {
		res.Category = Direct
		res.Detail = "internal"
		return res
	}

	// Precedence matters: a webmail host like mail.google.com would otherwise
	// match the "google." search rule, so webmail (and corporate, e.g.
	// outlook.office.com vs office.com) are resolved before search.
	if name, ok := matchHost(host, aiSystems); ok {
		res.Category, res.Detail = AIAssisted, name
		return res
	}
	if name, ok := matchHost(host, webmailHosts); ok {
		res.Category, res.Detail = Email, name
		return res
	}
	if name, ok := matchHost(host, corporateHosts); ok {
		res.Category, res.Detail = Corporate, name
		return res
	}
	if name, ok := matchHost(host, socialPlatforms); ok {
		res.Category, res.Detail = Social, name
		return res
	}
	if name, ok := matchHost(host, searchEngines); ok {
		res.Category, res.Detail = Organic, name
		return res
	}

	res.Category = Referral
	res.Detail = host
	return res
}

// matchHost returns the label for the first table key that is a substring of host.
func matchHost(host string, table map[string]string) (string, bool) {
	for k, v := range table {
		if strings.Contains(host, k) {
			return v, true
		}
	}
	return "", false
}

func campaignDetail(utm UTM) string {
	if utm.Campaign != "" {
		return utm.Campaign
	}
	if utm.Source != "" {
		return utm.Source
	}
	return ""
}
