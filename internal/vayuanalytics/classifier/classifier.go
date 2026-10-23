// SPDX-License-Identifier: Apache-2.0

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
	"sort"
	"strconv"
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
	"naver.com":        "Naver",
	"seznam.cz":        "Seznam",
	"sogou.com":        "Sogou",
	"ask.com":          "Ask",
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
	"telegram.org":         "Telegram",
	"youtu.be":             "YouTube",
	"threads.net":          "Threads",
	"tumblr.com":           "Tumblr",
	"quora.com":            "Quora",
	"vk.com":               "VK",
	"lobste.rs":            "Lobsters",
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

	res.Category, res.Detail = hostCategory(host)
	return res
}

// hostTables are the referrer hosts each category is recognised by, in the
// order they are tried. Precedence matters: a webmail host like
// mail.google.com would otherwise match the "google." search rule, so
// webmail (and corporate, e.g. outlook.office.com vs office.com) are resolved
// before search.
var hostTables = []struct {
	cat   Category
	table map[string]string
}{
	{AIAssisted, aiSystems}, {Email, webmailHosts}, {Corporate, corporateHosts},
	{Social, socialPlatforms}, {Organic, searchEngines},
}

// hostCategory is the category of an external referrer host, lowercased, and
// the name of what it matched (the host itself for a referral).
func hostCategory(host string) (Category, string) {
	for _, t := range hostTables {
		if name, ok := matchHost(host, t.table); ok {
			return t.cat, name
		}
	}
	return Referral, host
}

// HostCategory is the category of a referrer stored as its host alone, as
// the event log keeps it: Direct when there is none, else what Classify
// decides for an external host. A report reading the log has no UTM tags or
// site host to weigh, and the log never stores the site's own hosts.
func HostCategory(host string) Category {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return Direct
	}
	c, _ := hostCategory(h)
	return c
}

// HostCategorySQL is HostCategory over the SQL column col, for the reports
// that group or filter by it. The table keys are fixed hostnames with no
// quote in them, so they are written into the expression as they are; a test
// holds the two forms to the same answers.
func HostCategorySQL(col string) string {
	// Trimmed of what strings.TrimSpace trims in a host; SQL's trim alone
	// takes spaces only.
	h := "lower(trim(" + col + ",' '||char(9)||char(10)||char(13)))"
	var b strings.Builder
	b.WriteString("CASE WHEN " + h + "='' THEN '" + string(Direct) + "'")
	for _, t := range hostTables {
		keys := make([]string, 0, len(t.table))
		for k := range t.table {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		terms := make([]string, 0, len(keys))
		for _, k := range keys {
			switch {
			case strings.HasSuffix(k, "."):
				terms = append(terms, "substr("+h+",1,"+strconv.Itoa(len(k))+")='"+k+"'", "instr("+h+",'."+k+"')>0")
			case !strings.Contains(k, "."):
				terms = append(terms, "instr('.'||"+h+"||'.','."+k+".')>0")
			default:
				terms = append(terms, h+"='"+k+"'", "substr("+h+",-"+strconv.Itoa(len(k)+1)+")='."+k+"'")
			}
		}
		b.WriteString(" WHEN " + strings.Join(terms, " OR ") + " THEN '" + string(t.cat) + "'")
	}
	b.WriteString(" ELSE '" + string(Referral) + "' END")
	return b.String()
}

// matchHost returns the label for the first table key that genuinely denotes
// host. Three key styles are honoured:
//
//   - full hosts ("bing.com"): host equals the key or is a deeper subdomain
//     of it (www.bing.com) — never a lookalike like notbing.com;
//   - trailing-dot wildcards ("google."): the label opens some position of
//     the domain (google.de, www.google.com, news.google.co.uk);
//   - bare labels ("mastodon"): a complete DNS label anywhere in the host
//     (mastodon.social) — not a substring of one (notmastodon.com).
//
// The old unanchored strings.Contains was spoofable: any referrer CONTAINING
// "x.com" (box.com, wix.com…) claimed X/Twitter attribution.
func matchHost(host string, table map[string]string) (string, bool) {
	for k, v := range table {
		switch {
		case strings.HasSuffix(k, "."):
			base := strings.TrimSuffix(k, ".")
			if strings.HasPrefix(host, base+".") || strings.Contains(host, "."+base+".") {
				return v, true
			}
		case !strings.Contains(k, "."):
			for _, label := range strings.Split(host, ".") {
				if label == k {
					return v, true
				}
			}
		default:
			if host == k || strings.HasSuffix(host, "."+k) {
				return v, true
			}
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
