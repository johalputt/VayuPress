// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestGoDarkExemptsOperatorTooling is the R8 guard: the crawler "go dark" switch
// must block search/AI indexers but NEVER the operator's own performance testers
// and uptime monitors — blocking those breaks PageSpeed scoring and makes uptime
// dashboards report the live site as down.
func TestGoDarkExemptsOperatorTooling(t *testing.T) {
	blocked := map[string]string{
		"Googlebot": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Bingbot":   "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
		"GPTBot":    "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)",
		"ClaudeBot": "Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
	}
	for name, ua := range blocked {
		if !crawlerUABlocked(ua) {
			t.Errorf("%s must be blocked when going dark", name)
		}
	}

	exempt := map[string]string{
		"PageSpeed/Lighthouse": "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36 Chrome/130 Mobile Safari/537.36 Chrome-Lighthouse",
		"UptimeRobot":          "Mozilla/5.0 (compatible; UptimeRobot/2.0; http://www.uptimerobot.com/)",
		"Pingdom":              "Pingdom.com_bot_version_1.4",
		"GTmetrix":             "Mozilla/5.0 (compatible; GTmetrix)",
		"StatusCake":           "Mozilla/5.0 (compatible; StatusCake)",
	}
	for name, ua := range exempt {
		if crawlerUABlocked(ua) {
			t.Errorf("%s is operator tooling and must NOT be blocked when going dark", name)
		}
	}
}

// TestNewCrawlerTokensRecognised: the expanded token set must classify the
// current fetcher variants so they are never mis-scored as unknown/bad.
func TestNewCrawlerTokensRecognised(t *testing.T) {
	for _, ua := range []string{
		"Mozilla/5.0 (compatible; GoogleOther)",
		"AdsBot-Google (+http://www.google.com/adsbot.html)",
		"FeedFetcher-Google; (+http://www.google.com/feedfetcher.html)",
		"Mozilla/5.0 (compatible; msnbot/2.0b; +http://search.msn.com/msnbot.htm)",
		"Mozilla/5.0 (compatible; Baiduspider/2.0; +http://www.baidu.com/search/spider.html)",
		"Mozilla/5.0 (compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot)",
		"Mozilla/5.0 (compatible; Claude-SearchBot/1.0)",
		"claude-code/1.0",
		"Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)",
		"Perplexity-User/1.0",
	} {
		if !crawlerUABlocked(ua) {
			t.Errorf("expected %q to be a recognised crawler (blockable when going dark)", ua)
		}
	}
}
